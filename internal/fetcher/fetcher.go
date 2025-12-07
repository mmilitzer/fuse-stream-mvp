package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	chunkSize      = 4 * 1024 * 1024  // 4MB chunks
	maxReadahead   = 64 * 1024 * 1024 // 64MB max readahead
	cacheSize      = 8                 // Keep 8 chunks in LRU cache
	maxRetries     = 3
	retryDelay     = 500 * time.Millisecond
	maxConcurrency = 4
)

type chunk struct {
	offset int64
	data   []byte
	err    error
}

type Fetcher struct {
	url           string
	size          int64
	client        *http.Client
	cache         map[int64]*chunk
	cacheMu       sync.RWMutex
	lruOrder      []int64
	semaphore     chan struct{}
	activeReads   map[int64]chan struct{}
	activeReadsMu sync.Mutex
}

func New(ctx context.Context, url string, size int64) (*Fetcher, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid file size: %d", size)
	}

	// Verify the URL supports Range requests
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create HEAD request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
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

	return &Fetcher{
		url:         url,
		size:        size,
		client:      client,
		cache:       make(map[int64]*chunk),
		lruOrder:    make([]int64, 0, cacheSize),
		semaphore:   make(chan struct{}, maxConcurrency),
		activeReads: make(map[int64]chan struct{}),
	}, nil
}

func (f *Fetcher) ReadAt(ctx context.Context, buf []byte, offset int64) (int, error) {
	if offset >= f.size {
		return 0, io.EOF
	}

	bytesRead := 0
	for bytesRead < len(buf) {
		chunkOffset := (offset / chunkSize) * chunkSize
		chunkData, err := f.getChunk(ctx, chunkOffset)
		if err != nil {
			if bytesRead > 0 {
				return bytesRead, nil
			}
			return 0, err
		}

		offsetInChunk := offset - chunkOffset
		n := copy(buf[bytesRead:], chunkData[offsetInChunk:])
		bytesRead += n
		offset += int64(n)

		if offset >= f.size {
			if bytesRead == 0 {
				return 0, io.EOF
			}
			return bytesRead, nil
		}
	}

	// Prefetch next chunks for readahead
	go f.prefetch(ctx, offset)

	return bytesRead, nil
}

func (f *Fetcher) getChunk(ctx context.Context, offset int64) ([]byte, error) {
	// Check cache first
	f.cacheMu.RLock()
	if c, ok := f.cache[offset]; ok {
		f.cacheMu.RUnlock()
		if c.err != nil {
			return nil, c.err
		}
		f.updateLRU(offset)
		return c.data, nil
	}
	f.cacheMu.RUnlock()

	// Check if another goroutine is already fetching this chunk
	f.activeReadsMu.Lock()
	if ch, ok := f.activeReads[offset]; ok {
		f.activeReadsMu.Unlock()
		<-ch // Wait for the other fetch to complete
		return f.getChunk(ctx, offset) // Try again from cache
	}
	
	// Mark this chunk as being fetched
	doneCh := make(chan struct{})
	f.activeReads[offset] = doneCh
	f.activeReadsMu.Unlock()

	// Fetch the chunk
	data, err := f.fetchChunk(ctx, offset)

	// Store in cache
	f.cacheMu.Lock()
	f.cache[offset] = &chunk{offset: offset, data: data, err: err}
	f.updateLRU(offset)
	f.evictOldChunks()
	f.cacheMu.Unlock()

	// Notify waiters and clean up
	f.activeReadsMu.Lock()
	delete(f.activeReads, offset)
	close(doneCh)
	f.activeReadsMu.Unlock()

	return data, err
}

func (f *Fetcher) fetchChunk(ctx context.Context, offset int64) ([]byte, error) {
	// Acquire semaphore to limit concurrency
	select {
	case f.semaphore <- struct{}{}:
		defer func() { <-f.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	endOffset := offset + chunkSize - 1
	if endOffset >= f.size {
		endOffset = f.size - 1
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(retryDelay * time.Duration(attempt)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", f.url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, endOffset))

		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status: %d", resp.StatusCode)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		return data, nil
	}

	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

func (f *Fetcher) prefetch(ctx context.Context, offset int64) {
	// Prefetch next chunks up to maxReadahead
	for i := int64(1); i <= maxReadahead/chunkSize; i++ {
		nextOffset := ((offset / chunkSize) + i) * chunkSize
		if nextOffset >= f.size {
			break
		}

		// Check if already cached
		f.cacheMu.RLock()
		_, cached := f.cache[nextOffset]
		f.cacheMu.RUnlock()
		if cached {
			continue
		}

		// Fetch in background
		go func(off int64) {
			f.getChunk(ctx, off)
		}(nextOffset)
	}
}

func (f *Fetcher) updateLRU(offset int64) {
	// Remove from current position
	for i, o := range f.lruOrder {
		if o == offset {
			f.lruOrder = append(f.lruOrder[:i], f.lruOrder[i+1:]...)
			break
		}
	}
	// Add to end (most recent)
	f.lruOrder = append(f.lruOrder, offset)
}

func (f *Fetcher) evictOldChunks() {
	for len(f.cache) > cacheSize {
		if len(f.lruOrder) == 0 {
			break
		}
		oldest := f.lruOrder[0]
		f.lruOrder = f.lruOrder[1:]
		delete(f.cache, oldest)
	}
}

func (f *Fetcher) Close() error {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	f.cache = make(map[int64]*chunk)
	f.lruOrder = f.lruOrder[:0]
	return nil
}
