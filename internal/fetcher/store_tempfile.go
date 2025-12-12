package fetcher

import (
	"context"
	"fmt"
	"io"
	"io/fs"
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
	
	// Shared file handle for concurrent reads
	// The file is opened once and shared across all ReadAt calls.
	// os.File.ReadAt is documented as safe for concurrent use.
	readFile *os.File

	// Close/Read coordination
	// closing is set to 1 when CloseWithContext starts, preventing new reads
	closing atomic.Int32
	// readers tracks active ReadAt calls in flight
	readers sync.WaitGroup

	// Reference counting and lifecycle
	refCount atomic.Int32
	closed   atomic.Bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup // Tracks downloader goroutine
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
// CRITICAL: This method implements the readers gate pattern to prevent ReadAt/Close races.
// 1. Flip the closing flag to prevent new reads from starting
// 2. Wait for all in-flight ReadAt calls to complete
// 3. Only then close the file handle and delete the temp file
func (s *TempFileStore) CloseWithContext(ctx context.Context) error {
	goid := goroutineid.Get()
	log.Printf("[TempFileStore #%d] Close ENTER goid=%d", s.debugID, goid)
	defer func() {
		log.Printf("[TempFileStore #%d] Close EXIT goid=%d", s.debugID, goid)
	}()
	
	// Check if already closed
	if !s.closed.CompareAndSwap(false, true) {
		return nil // Already closed
	}

	// Step 1: Flip closing flag to prevent new reads
	if !s.closing.CompareAndSwap(0, 1) {
		// Already closing (shouldn't happen due to closed check above, but be safe)
		return nil
	}
	log.Printf("[TempFileStore #%d] Closing flag set, blocking new reads goid=%d", s.debugID, goid)

	// Step 2: Cancel downloader
	s.cancel()

	// Step 3: Wait for downloader to finish
	downloaderDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(downloaderDone)
	}()

	select {
	case <-downloaderDone:
		log.Printf("[TempFileStore #%d] Downloader stopped cleanly goid=%d", s.debugID, goid)
	case <-ctx.Done():
		log.Printf("[TempFileStore #%d] Warning: Close timed out waiting for downloader goid=%d", s.debugID, goid)
		// Continue with cleanup even if downloader didn't finish
	}

	// Step 4: Wait for all active ReadAt calls to drain
	// This is the critical section - we must not close the file while reads are in flight
	readersDone := make(chan struct{})
	go func() {
		s.readers.Wait()
		close(readersDone)
	}()

	select {
	case <-readersDone:
		log.Printf("[TempFileStore #%d] All readers drained, safe to close file goid=%d", s.debugID, goid)
	case <-ctx.Done():
		// This is bad - we're being forced to close while readers are active
		log.Printf("[TempFileStore #%d] ERROR: Close timed out waiting for readers to drain! goid=%d", s.debugID, goid)
		return ctx.Err()
	}

	// Step 5: Now safe to close the file - no readers are using it
	if s.readFile != nil {
		if err := s.readFile.Close(); err != nil {
			log.Printf("[TempFileStore #%d] Warning: error closing read file: %v goid=%d", s.debugID, err, goid)
		}
		s.readFile = nil
	}

	// Step 6: Remove temp file (best effort)
	if err := os.Remove(s.tempPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[TempFileStore #%d] Warning: error removing temp file: %v goid=%d", s.debugID, err, goid)
		return err
	}

	return nil
}

// ReadAt reads data at the specified offset (implements BackingStore interface).
// CRITICAL: Implements readers gate pattern to prevent races with Close.
// 1. Check closing flag - bail out immediately if closing
// 2. Register as active reader (increment WaitGroup)
// 3. Wait for data to be downloaded
// 4. Perform read from shared file handle (safe for concurrent use)
// 5. Unregister as active reader (decrement WaitGroup)
func (s *TempFileStore) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	goid := goroutineid.Get()
	log.Printf("[TempFileStore #%d] ReadAt ENTER off=%d len=%d goid=%d", s.debugID, off, len(p), goid)
	defer func() {
		log.Printf("[TempFileStore #%d] ReadAt EXIT off=%d len=%d goid=%d", s.debugID, off, len(p), goid)
	}()
	
	// Step 1: Check if store is closing - bail out immediately
	if s.closing.Load() != 0 {
		return 0, fs.ErrClosed
	}

	// Step 2: Register as active reader
	// This MUST happen before we access the file handle
	s.readers.Add(1)
	defer s.readers.Done()

	// Double-check closing flag after registering
	// This prevents TOCTOU race where Close could start between our check and Add
	if s.closing.Load() != 0 {
		return 0, fs.ErrClosed
	}
	
	if off >= s.size {
		return 0, io.EOF
	}

	// Calculate how much we need
	wantEnd := off + int64(len(p))
	if wantEnd > s.size {
		wantEnd = s.size
		p = p[:s.size-off]
	}

	// Step 3: Wait until desired region is downloaded or an error/ctx cancel occurs
	// Use dual-channel signaling:
	// 1. doneCh is closed when download finishes - guarantees wake-up
	// 2. notifyCh receives periodic progress updates - reduces polling
	for {
		// Check if store is closing - exit promptly
		if s.closing.Load() != 0 {
			return 0, fs.ErrClosed
		}

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

	// Step 4: Read from shared temp file handle - NO LOCKS HELD during I/O
	// Note: os.File.ReadAt is documented as safe for concurrent use
	// The file won't be closed while we're here because:
	// - We registered with readers.Add(1)
	// - CloseWithContext waits for readers.Wait() before closing
	f := s.readFile
	if f == nil {
		return 0, fs.ErrClosed
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
