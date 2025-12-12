package fetcher

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/goroutineid"
)

var (
	// Global debug ID counter for unique TempFileStore identification
	storeDebugIDCounter atomic.Uint64
)

// TempFileStore implements BackingStore by downloading the entire file sequentially to disk.
type TempFileStore struct {
	url      string
	size     int64
	tempPath string
	client   *http.Client
	debugID  uint64 // Unique ID for this store instance

	// Download state
	downloaded atomic.Int64
	err        error
	errMu      sync.RWMutex
	
	// Progress notification - uses broadcast channel pattern
	// doneCh is closed when download completes (success or error)
	doneCh     chan struct{}
	// notifyCh receives periodic progress updates
	notifyCh   chan struct{}
	
	// Cached file handle for reads (reduces syscall overhead)
	readFile   *os.File
	readFileMu sync.RWMutex

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

	// Open file for reading and cache the handle
	readFile, err := os.Open(tempPath)
	if err != nil {
		os.Remove(tempPath)
		return nil, fmt.Errorf("open temp file for reading: %w", err)
	}

	// Assign unique debug ID
	debugID := storeDebugIDCounter.Add(1)

	// Create store
	ctx, cancel := context.WithCancel(ctx)
	store := &TempFileStore{
		url:      url,
		size:     size,
		tempPath: tempPath,
		client:   &http.Client{Timeout: 5 * time.Minute},
		cancel:   cancel,
		debugID:  debugID,
		doneCh:   make(chan struct{}),
		notifyCh: make(chan struct{}, 100), // Buffered to avoid blocking downloader
		readFile: readFile,
	}

	log.Printf("[TempFileStore #%d] Created for %s (size=%d) goid=%d", debugID, url, size, goroutineid.Get())

	// Start downloader goroutine
	store.wg.Add(1)
	go store.downloader(ctx)

	return store, nil
}

// downloader runs in a goroutine and downloads the file sequentially.
// CRITICAL: This method does file I/O and network I/O - it must NOT hold any locks
// across these operations. Only lock briefly to update shared state.
func (s *TempFileStore) downloader(ctx context.Context) {
	goid := goroutineid.Get()
	log.Printf("[TempFileStore #%d] downloader ENTER goid=%d", s.debugID, goid)
	defer func() {
		log.Printf("[TempFileStore #%d] downloader EXIT goid=%d downloaded=%d/%d", s.debugID, goid, s.downloaded.Load(), s.size)
	}()
	
	defer s.wg.Done()
	defer close(s.doneCh)    // Signal all waiters that download is done
	defer close(s.notifyCh)  // Close notification channel

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
			
			// Signal progress on buffered channel - non-blocking send
			// Readers use this to wake up and recheck progress
			select {
			case s.notifyCh <- struct{}{}:
			default:
				// Channel full, that's fine - readers will check again on doneCh
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				// Success!
				s.downloaded.Store(s.size)
				log.Printf("[TempFileStore #%d] Download complete goid=%d", s.debugID, goid)
				return
			}
			s.setError(fmt.Errorf("read from response: %w", readErr))
			return
		}
	}
}

// setError sets the error state.
func (s *TempFileStore) setError(err error) {
	s.errMu.Lock()
	s.err = err
	s.errMu.Unlock()
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
	return s.CloseWithContext(context.Background())
}

// CloseWithContext releases resources and deletes the temp file with timeout support.
// If the context times out, cleanup is attempted but the downloader may be left running.
func (s *TempFileStore) CloseWithContext(ctx context.Context) error {
	goid := goroutineid.Get()
	log.Printf("[TempFileStore #%d] Close ENTER goid=%d", s.debugID, goid)
	defer func() {
		log.Printf("[TempFileStore #%d] Close EXIT goid=%d", s.debugID, goid)
	}()
	
	if s.closed.Swap(true) {
		return nil
	}

	// Cancel downloader
	s.cancel()

	// Wait for downloader to finish with context timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Downloader finished cleanly
		log.Printf("[TempFileStore #%d] Downloader stopped cleanly goid=%d", s.debugID, goid)
	case <-ctx.Done():
		// Context timed out - log warning but continue cleanup
		log.Printf("[TempFileStore #%d] Warning: Close timed out waiting for downloader, forcing cleanup goid=%d", s.debugID, goid)
	}

	// Close cached read file handle
	s.readFileMu.Lock()
	if s.readFile != nil {
		s.readFile.Close()
		s.readFile = nil
	}
	s.readFileMu.Unlock()

	// Remove temp file (best effort)
	return os.Remove(s.tempPath)
}

// ReadAt reads data at the specified offset (implements BackingStore interface).
// CRITICAL: Uses dual-channel signaling to avoid missed-wakeup races.
// - doneCh is closed when download completes (success or error)
// - notifyCh receives periodic progress updates (buffered, non-blocking)
// This ensures readers can't miss the completion signal even if they arrive late.
func (s *TempFileStore) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	goid := goroutineid.Get()
	log.Printf("[TempFileStore #%d] ReadAt ENTER off=%d len=%d goid=%d", s.debugID, off, len(p), goid)
	defer func() {
		log.Printf("[TempFileStore #%d] ReadAt EXIT off=%d len=%d goid=%d", s.debugID, off, len(p), goid)
	}()
	
	if off >= s.size {
		return 0, io.EOF
	}

	// Calculate how much we need
	wantEnd := off + int64(len(p))
	if wantEnd > s.size {
		wantEnd = s.size
		p = p[:s.size-off]
	}

	// Wait until desired region is downloaded or an error/ctx cancel occurs
	// Use dual-channel signaling:
	// 1. doneCh is closed when download finishes - guarantees wake-up
	// 2. notifyCh receives periodic progress updates - reduces polling
	for {
		// Check if we have enough data
		downloaded := s.downloaded.Load()
		if downloaded >= wantEnd {
			break
		}

		// Check for errors
		if err := s.getError(); err != nil {
			return 0, err
		}

		// Check context cancellation
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}

		// Wait for progress signal, completion, or context cancellation
		// Using both doneCh and notifyCh ensures we wake up promptly
		select {
		case <-s.doneCh:
			// Download finished - loop once more to check final state
			continue
		case <-s.notifyCh:
			// Progress update received - loop to recheck
			continue
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}

	// Read from cached temp file handle - NO LOCKS HELD during I/O
	// Note: os.File.ReadAt is safe for concurrent use according to Go docs
	s.readFileMu.RLock()
	f := s.readFile
	s.readFileMu.RUnlock()

	if f == nil {
		return 0, fmt.Errorf("temp file closed")
	}

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
