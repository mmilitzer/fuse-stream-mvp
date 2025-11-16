package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// rangeLRUChunk represents a cached chunk of data.
type rangeLRUChunk struct {
	offset int64
	data   []byte
	err    error
}

// RangeLRUStore implements BackingStore using HTTP Range requests with in-memory LRU caching.
type RangeLRUStore struct {
	url           string
	size          int64
	client        *http.Client
	chunkSize     int
	maxConcurrent int
	cacheCapacity int

	// Caching and LRU
	cache    map[int64]*rangeLRUChunk
	cacheMu  sync.RWMutex
	lruOrder []int64

	// Concurrency control
	semaphore     chan struct{}
	activeReads   map[int64]chan struct{}
	activeReadsMu sync.Mutex

	// Reference counting
	refCount atomic.Int32
}

// NewRangeLRUStore creates a new RangeLRUStore.
func NewRangeLRUStore(ctx context.Context, url string, size int64, opts StoreOptions) (*RangeLRUStore, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid file size: %d", size)
	}

	// Set defaults
	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 4 * 1024 * 1024 // 4MB
	}
	maxConcurrent := opts.MaxConcurrentRequests
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	cacheCapacity := opts.CacheSize
	if cacheCapacity <= 0 {
		cacheCapacity = 8
	}

	// Verify the URL supports Range requests
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create HEAD request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HEAD request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HEAD request failed: %d", resp.StatusCode)
	}

	acceptRanges := resp.Header.Get("Accept-Ranges")
	if acceptRanges != "bytes" {
		return nil, fmt.Errorf("server does not support Range requests")
	}

	store := &RangeLRUStore{
		url:           url,
		size:          size,
		client:        client,
		chunkSize:     chunkSize,
		maxConcurrent: maxConcurrent,
		cacheCapacity: cacheCapacity,
		cache:         make(map[int64]*rangeLRUChunk),
		lruOrder:      make([]int64, 0, cacheCapacity),
		semaphore:     make(chan struct{}, maxConcurrent),
		activeReads:   make(map[int64]chan struct{}),
	}

	return store, nil
}

// Size returns the file size.
func (s *RangeLRUStore) Size() int64 {
	return s.size
}

// IncRef increments the reference count.
func (s *RangeLRUStore) IncRef() {
	s.refCount.Add(1)
}

// DecRef decrements the reference count.
func (s *RangeLRUStore) DecRef() int32 {
	newCount := s.refCount.Add(-1)
	if newCount == 0 {
		// Optional: clear cache when no more references
		s.cacheMu.Lock()
		s.cache = make(map[int64]*rangeLRUChunk)
		s.lruOrder = s.lruOrder[:0]
		s.cacheMu.Unlock()
	}
	return newCount
}

// RefCount returns the current reference count.
func (s *RangeLRUStore) RefCount() int32 {
	return s.refCount.Load()
}

// Close releases resources.
func (s *RangeLRUStore) Close() error {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cache = nil
	s.lruOrder = nil
	return nil
}

// ReadAt reads data at the specified offset (implements BackingStore interface).
func (s *RangeLRUStore) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}

	totalRead := 0
	remaining := len(p)
	currentOffset := off

	for remaining > 0 {
		if currentOffset >= s.size {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, io.EOF
		}

		chunkOffset := (currentOffset / int64(s.chunkSize)) * int64(s.chunkSize)
		offsetInChunk := int(currentOffset - chunkOffset)
		
		// Get or fetch the chunk
		chunk, err := s.getChunk(ctx, chunkOffset)
		if err != nil {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, err
		}

		// Copy data from chunk
		available := len(chunk.data) - offsetInChunk
		toCopy := remaining
		if toCopy > available {
			toCopy = available
		}

		copy(p[totalRead:], chunk.data[offsetInChunk:offsetInChunk+toCopy])
		totalRead += toCopy
		remaining -= toCopy
		currentOffset += int64(toCopy)
	}

	return totalRead, nil
}

// getChunk retrieves a chunk from cache or fetches it via HTTP.
func (s *RangeLRUStore) getChunk(ctx context.Context, offset int64) (*rangeLRUChunk, error) {
	// Check cache first
	s.cacheMu.RLock()
	if chunk, ok := s.cache[offset]; ok {
		s.cacheMu.RUnlock()
		return chunk, chunk.err
	}
	s.cacheMu.RUnlock()

	// Check if another goroutine is already fetching this chunk
	s.activeReadsMu.Lock()
	if done, exists := s.activeReads[offset]; exists {
		s.activeReadsMu.Unlock()
		// Wait for the other goroutine to finish
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Check cache again
		s.cacheMu.RLock()
		chunk := s.cache[offset]
		s.cacheMu.RUnlock()
		if chunk == nil {
			return nil, fmt.Errorf("chunk fetch failed")
		}
		return chunk, chunk.err
	}

	// Start fetching this chunk
	done := make(chan struct{})
	s.activeReads[offset] = done
	s.activeReadsMu.Unlock()

	// Fetch the chunk
	chunk := s.fetchChunk(ctx, offset)

	// Store in cache
	s.cacheMu.Lock()
	s.cache[offset] = chunk
	s.updateLRU(offset)
	s.cacheMu.Unlock()

	// Signal that fetch is complete
	s.activeReadsMu.Lock()
	delete(s.activeReads, offset)
	close(done)
	s.activeReadsMu.Unlock()

	return chunk, chunk.err
}

// fetchChunk fetches a chunk via HTTP Range request.
func (s *RangeLRUStore) fetchChunk(ctx context.Context, offset int64) *rangeLRUChunk {
	// Acquire semaphore
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		return &rangeLRUChunk{offset: offset, err: ctx.Err()}
	}

	// Determine range
	endOffset := offset + int64(s.chunkSize) - 1
	if endOffset >= s.size {
		endOffset = s.size - 1
	}

	// Retry logic
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return &rangeLRUChunk{offset: offset, err: ctx.Err()}
			}
		}

		data, err := s.doRangeRequest(ctx, offset, endOffset)
		if err == nil {
			return &rangeLRUChunk{offset: offset, data: data}
		}
		lastErr = err
	}

	return &rangeLRUChunk{offset: offset, err: fmt.Errorf("failed after 3 retries: %w", lastErr)}
}

// doRangeRequest performs a single HTTP Range request.
func (s *RangeLRUStore) doRangeRequest(ctx context.Context, start, end int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// updateLRU updates the LRU order and evicts if necessary.
func (s *RangeLRUStore) updateLRU(offset int64) {
	// Remove from current position if exists
	for i, o := range s.lruOrder {
		if o == offset {
			s.lruOrder = append(s.lruOrder[:i], s.lruOrder[i+1:]...)
			break
		}
	}

	// Add to end (most recently used)
	s.lruOrder = append(s.lruOrder, offset)

	// Evict if over capacity
	for len(s.lruOrder) > s.cacheCapacity {
		evictOffset := s.lruOrder[0]
		s.lruOrder = s.lruOrder[1:]
		delete(s.cache, evictOffset)
	}
}
