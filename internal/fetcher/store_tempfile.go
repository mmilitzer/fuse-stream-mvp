package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// TempFileStore implements BackingStore by downloading the entire file sequentially to disk.
type TempFileStore struct {
	url      string
	size     int64
	tempPath string
	client   *http.Client

	// Download state
	downloaded atomic.Int64
	err        error
	errMu      sync.RWMutex
	cond       *sync.Cond

	// Reference counting and lifecycle
	refCount atomic.Int32
	closed   atomic.Bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewTempFileStore creates a new TempFileStore and starts downloading.
func NewTempFileStore(ctx context.Context, url string, size int64, opts StoreOptions) (*TempFileStore, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid file size: %d", size)
	}

	// Get temp directory
	tempDir := opts.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	
	// Get temp file manager and ensure space is available
	manager := GetTempFileManager(tempDir)
	if err := manager.EnsureSpaceAvailable(size); err != nil {
		return nil, fmt.Errorf("ensure disk space: %w", err)
	}

	// Create temp file
	tempFile, err := os.CreateTemp(tempDir, "fsmvp-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close() // We'll reopen for writing in the downloader
	
	// Register with manager
	manager.Register(tempPath, size)

	// Create store
	ctx, cancel := context.WithCancel(ctx)
	store := &TempFileStore{
		url:      url,
		size:     size,
		tempPath: tempPath,
		client:   &http.Client{Timeout: 5 * time.Minute},
		cancel:   cancel,
	}
	store.cond = sync.NewCond(&sync.Mutex{})

	// Start downloader goroutine
	store.wg.Add(1)
	go store.downloader(ctx)

	return store, nil
}

// downloader runs in a goroutine and downloads the file sequentially.
func (s *TempFileStore) downloader(ctx context.Context) {
	defer s.wg.Done()

	// Open temp file for writing
	f, err := os.OpenFile(s.tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		s.setError(fmt.Errorf("open temp file: %w", err))
		return
	}
	defer f.Close()

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
	if err != nil {
		s.setError(fmt.Errorf("create request: %w", err))
		return
	}

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		s.setError(fmt.Errorf("http request: %w", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		s.setError(fmt.Errorf("unexpected status: %d", resp.StatusCode))
		return
	}

	// Download in chunks and update progress
	buf := make([]byte, 256*1024) // 256KB buffer
	totalWritten := int64(0)

	for {
		select {
		case <-ctx.Done():
			s.setError(ctx.Err())
			return
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := f.Write(buf[:n])
			if writeErr != nil {
				s.setError(fmt.Errorf("write to temp file: %w", writeErr))
				return
			}
			totalWritten += int64(written)

			// Update progress and notify waiters
			s.downloaded.Store(totalWritten)
			s.cond.Broadcast()
		}

		if readErr != nil {
			if readErr == io.EOF {
				// Success!
				s.downloaded.Store(s.size)
				s.cond.Broadcast()
				return
			}
			s.setError(fmt.Errorf("read from response: %w", readErr))
			return
		}
	}
}

// setError sets the error state and notifies waiters.
func (s *TempFileStore) setError(err error) {
	s.errMu.Lock()
	s.err = err
	s.errMu.Unlock()
	s.cond.Broadcast()
}

// getError returns the current error state.
func (s *TempFileStore) getError() error {
	s.errMu.RLock()
	defer s.errMu.RUnlock()
	return s.err
}

// Size returns the file size.
func (s *TempFileStore) Size() int64 {
	return s.size
}

// IncRef increments the reference count.
func (s *TempFileStore) IncRef() {
	s.refCount.Add(1)
}

// DecRef decrements the reference count.
func (s *TempFileStore) DecRef() int32 {
	return s.refCount.Add(-1)
}

// RefCount returns the current reference count.
func (s *TempFileStore) RefCount() int32 {
	return s.refCount.Load()
}

// TempPath returns the path to the temporary file.
func (s *TempFileStore) TempPath() string {
	return s.tempPath
}

// Close releases resources and deletes the temp file.
func (s *TempFileStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}

	// Cancel downloader
	s.cancel()
	s.cond.Broadcast()

	// Wait for downloader to finish
	s.wg.Wait()

	// Unregister from manager
	if manager := globalManager; manager != nil {
		manager.Unregister(s.tempPath)
	}

	// Remove temp file
	return os.Remove(s.tempPath)
}

// ReadAt reads data at the specified offset (implements BackingStore interface).
func (s *TempFileStore) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}

	// Calculate how much we need
	wantEnd := off + int64(len(p))
	if wantEnd > s.size {
		wantEnd = s.size
		p = p[:s.size-off]
	}

	// Wait until desired region is downloaded or an error/ctx cancel
	s.cond.L.Lock()
	for {
		downloaded := s.downloaded.Load()
		err := s.getError()

		// If we have enough data, proceed
		if downloaded >= wantEnd {
			s.cond.L.Unlock()
			break
		}

		// If there's an error and we don't have enough data, fail
		if err != nil {
			s.cond.L.Unlock()
			return 0, err
		}

		// Check context
		if ctx.Err() != nil {
			s.cond.L.Unlock()
			return 0, ctx.Err()
		}

		// Wait for more data
		s.cond.Wait()
	}

	// Read from temp file
	f, err := os.Open(s.tempPath)
	if err != nil {
		return 0, fmt.Errorf("open temp file: %w", err)
	}
	defer f.Close()

	n, err := f.ReadAt(p, off)
	
	// Update access time in manager
	if manager := globalManager; manager != nil {
		manager.UpdateAccess(s.tempPath)
	}
	
	if err != nil {
		// If we got some data but hit EOF, that's fine
		if err == io.EOF && n > 0 && off+int64(n) <= s.size {
			return n, nil
		}
		// If we hit EOF at exactly the file size, that's fine
		if err == io.EOF && off+int64(n) == s.size {
			return n, nil
		}
		return n, err
	}

	return n, nil
}

// Ensure TempFileStore implements BackingStore
var _ BackingStore = (*TempFileStore)(nil)
