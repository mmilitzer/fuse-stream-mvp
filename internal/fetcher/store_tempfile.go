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

	// Create temp file
	tempDir := opts.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	tempFile, err := os.CreateTemp(tempDir, "fsmvp-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close() // We'll reopen for writing in the downloader

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
// CRITICAL: This method does file I/O and network I/O - it must NOT hold any locks
// across these operations. Only lock briefly to update shared state.
func (s *TempFileStore) downloader(ctx context.Context) {
	defer s.wg.Done()

	// Open temp file for writing - no locks needed, this is private to downloader
	f, err := os.OpenFile(s.tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		s.setError(fmt.Errorf("open temp file: %w", err))
		return
	}
	defer f.Close()

	// Create HTTP request - no locks needed
	req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
	if err != nil {
		s.setError(fmt.Errorf("create request: %w", err))
		return
	}

	// Execute request - no locks needed
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

		// Read from network - no locks held
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// Write to file - no locks held during I/O
			written, writeErr := f.Write(buf[:n])
			if writeErr != nil {
				s.setError(fmt.Errorf("write to temp file: %w", writeErr))
				return
			}
			totalWritten += int64(written)

			// Update progress atomically (no lock needed for atomic operation)
			s.downloaded.Store(totalWritten)
			// Notify waiters - this is a quick operation, safe to call
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

	// Remove temp file
	return os.Remove(s.tempPath)
}

// ReadAt reads data at the specified offset (implements BackingStore interface).
// CRITICAL: This method must NEVER hold a mutex across I/O operations to avoid deadlocks.
// We use a timeout-based wait to prevent indefinite blocking.
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
	// Use timeout-based wait to prevent indefinite blocking (max 10 seconds per iteration)
	const maxWaitPerIteration = 10 * time.Second
	waitDeadline := time.Now().Add(maxWaitPerIteration)
	
	for {
		// Lock only to read state - unlock before any I/O or long waits
		s.cond.L.Lock()
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

		// Check timeout to prevent indefinite waiting
		if time.Now().After(waitDeadline) {
			s.cond.L.Unlock()
			return 0, fmt.Errorf("timeout waiting for data at offset %d (have %d, need %d)", off, downloaded, wantEnd)
		}

		// Wait for more data with a timeout using a goroutine + channel
		// This allows us to wake up periodically to check context and timeout
		waitDone := make(chan struct{})
		go func() {
			time.Sleep(100 * time.Millisecond) // Wake up every 100ms to recheck
			s.cond.Broadcast()
			close(waitDone)
		}()
		
		s.cond.Wait()
		s.cond.L.Unlock()
		
		// Wait for our timeout goroutine to finish
		<-waitDone
	}

	// Read from temp file - NO LOCKS HELD during I/O
	f, err := os.Open(s.tempPath)
	if err != nil {
		return 0, fmt.Errorf("open temp file: %w", err)
	}
	defer f.Close()

	n, err := f.ReadAt(p, off)
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
