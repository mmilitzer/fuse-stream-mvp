package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// testServer wraps httptest.Server with request tracking
type testServer struct {
	*httptest.Server
	requestCount  atomic.Int64
	bytesServed   atomic.Int64
	fileContent   []byte
	supportsRange bool
}

// newTestServer creates a test HTTP server serving synthetic content
func newTestServer(size int, supportsRange bool) *testServer {
	ts := &testServer{
		fileContent:   generateSyntheticFile(size),
		supportsRange: supportsRange,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.requestCount.Add(1)

		// Handle HEAD requests
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(ts.fileContent)))
			if ts.supportsRange {
				w.Header().Set("Accept-Ranges", "bytes")
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		// Handle GET requests
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			// Full file request
			w.Header().Set("Content-Length", strconv.Itoa(len(ts.fileContent)))
			w.WriteHeader(http.StatusOK)
			ts.bytesServed.Add(int64(len(ts.fileContent)))
			w.Write(ts.fileContent)
			return
		}

		if !ts.supportsRange {
			http.Error(w, "Range not supported", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		// Parse Range header (format: "bytes=start-end")
		rangeStr := strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.Split(rangeStr, "-")
		if len(parts) != 2 {
			http.Error(w, "Invalid range", http.StatusBadRequest)
			return
		}

		start, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.Error(w, "Invalid range start", http.StatusBadRequest)
			return
		}

		var end int64
		if parts[1] == "" {
			end = int64(len(ts.fileContent)) - 1
		} else {
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.Error(w, "Invalid range end", http.StatusBadRequest)
				return
			}
		}

		if start < 0 || start >= int64(len(ts.fileContent)) || end < start || end >= int64(len(ts.fileContent)) {
			http.Error(w, "Range out of bounds", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		data := ts.fileContent[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(ts.fileContent)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		ts.bytesServed.Add(int64(len(data)))
		w.Write(data)
	})

	ts.Server = httptest.NewServer(handler)
	return ts
}

func (ts *testServer) stats() (requests int64, bytes int64) {
	return ts.requestCount.Load(), ts.bytesServed.Load()
}

// generateSyntheticFile creates a file with a repeating pattern for easy verification
func generateSyntheticFile(size int) []byte {
	data := make([]byte, size)
	// Fill with a repeating pattern: 0, 1, 2, ..., 255, 0, 1, 2, ...
	for i := 0; i < size; i++ {
		data[i] = byte(i % 256)
	}
	return data
}

// verifyContent checks if the data matches the expected synthetic pattern
func verifyContent(t *testing.T, data []byte, offset int64) {
	t.Helper()
	for i, b := range data {
		expected := byte((int(offset) + i) % 256)
		if b != expected {
			t.Fatalf("Content mismatch at offset %d: got %d, want %d", offset+int64(i), b, expected)
		}
	}
}

func TestFetcher_SequentialRead(t *testing.T) {
	const fileSize = 10 * 1024 * 1024 // 10 MB
	ts := newTestServer(fileSize, true)
	defer ts.Close()

	ctx := context.Background()
	f, err := New(ctx, ts.URL, fileSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer f.Close()

	// Read the entire file sequentially in 1MB chunks
	buf := make([]byte, 1024*1024)
	offset := int64(0)
	totalRead := 0

	for offset < fileSize {
		n, err := f.ReadAt(ctx, buf, offset)
		if err != nil && err != io.EOF {
			t.Fatalf("ReadAt(%d) error = %v", offset, err)
		}
		if n == 0 {
			break
		}

		// Verify content
		verifyContent(t, buf[:n], offset)

		totalRead += n
		offset += int64(n)
	}

	if totalRead != fileSize {
		t.Errorf("Total read = %d, want %d", totalRead, fileSize)
	}

	// Check HTTP request efficiency
	requests, bytesServed := ts.stats()
	t.Logf("Sequential read: %d requests, %d bytes served", requests, bytesServed)

	// We expect approximately fileSize/chunkSize requests (10MB / 4MB = 3 chunks)
	// Plus 1 HEAD request = 4 total
	// Allow some prefetch requests, but shouldn't be excessive
	maxExpectedRequests := int64(20) // HEAD + 3 data chunks + some prefetch
	if requests > maxExpectedRequests {
		t.Errorf("Too many requests: got %d, want <= %d", requests, maxExpectedRequests)
	}

	// Total bytes served should be close to file size (allow for prefetch)
	// We read 10MB, with 4MB chunks we get 3 chunks = 12MB of data
	// Plus prefetch could add more, but shouldn't be excessive
	maxExpectedBytes := int64(fileSize * 3) // Allow 3x for caching/prefetch
	if bytesServed > maxExpectedBytes {
		t.Errorf("Too many bytes served: got %d, want <= %d", bytesServed, maxExpectedBytes)
	}
}

func TestFetcher_RandomReads(t *testing.T) {
	const fileSize = 50 * 1024 * 1024 // 50 MB
	ts := newTestServer(fileSize, true)
	defer ts.Close()

	ctx := context.Background()
	f, err := New(ctx, ts.URL, fileSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer f.Close()

	// Perform random reads at various offsets
	testCases := []struct {
		offset int64
		size   int
	}{
		{0, 1024 * 1024},                     // 1MB at start
		{5 * 1024 * 1024, 1024 * 1024},       // 1MB at 5MB
		{10 * 1024 * 1024, 2 * 1024 * 1024},  // 2MB at 10MB
		{30 * 1024 * 1024, 512 * 1024},       // 512KB at 30MB
		{45 * 1024 * 1024, 1024 * 1024},      // 1MB at 45MB
		{1 * 1024 * 1024, 500 * 1024},        // 500KB at 1MB (test cache hit)
	}

	for _, tc := range testCases {
		buf := make([]byte, tc.size)
		n, err := f.ReadAt(ctx, buf, tc.offset)
		if err != nil && err != io.EOF {
			t.Fatalf("ReadAt(%d, %d) error = %v", tc.offset, tc.size, err)
		}

		if n != tc.size && tc.offset+int64(tc.size) < fileSize {
			t.Errorf("ReadAt(%d, %d) read %d bytes, want %d", tc.offset, tc.size, n, tc.size)
		}

		// Verify content
		verifyContent(t, buf[:n], tc.offset)
	}

	requests, bytesServed := ts.stats()
	t.Logf("Random reads: %d requests, %d bytes served", requests, bytesServed)

	// Random reads will cause more requests, but shouldn't be excessive
	// We have 6 read operations, some may share chunks
	maxExpectedRequests := int64(50) // HEAD + chunks + prefetch
	if requests > maxExpectedRequests {
		t.Errorf("Too many requests for random reads: got %d, want <= %d", requests, maxExpectedRequests)
	}
}

func TestFetcher_SmallChunkReads(t *testing.T) {
	const fileSize = 10 * 1024 * 1024 // 10 MB
	ts := newTestServer(fileSize, true)
	defer ts.Close()

	ctx := context.Background()
	f, err := New(ctx, ts.URL, fileSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer f.Close()

	// Simulate FUSE-style small reads (e.g., 128KB at a time)
	const readSize = 128 * 1024
	const totalToRead = 2 * 1024 * 1024 // Read 2MB in small chunks

	offset := int64(0)
	totalRead := 0

	for totalRead < totalToRead {
		buf := make([]byte, readSize)
		n, err := f.ReadAt(ctx, buf, offset)
		if err != nil && err != io.EOF {
			t.Fatalf("ReadAt(%d) error = %v", offset, err)
		}
		if n == 0 {
			break
		}

		// Verify content
		verifyContent(t, buf[:n], offset)

		totalRead += n
		offset += int64(n)
	}

	if totalRead != totalToRead {
		t.Errorf("Total read = %d, want %d", totalRead, totalToRead)
	}

	requests, bytesServed := ts.stats()
	t.Logf("Small chunk reads: %d requests, %d bytes served", requests, bytesServed)

	// The key test: even with small reads, we should fetch in large chunks
	// Reading 2MB should require 1 chunk (4MB) at most, plus HEAD
	// Allow for prefetch as well
	maxExpectedRequests := int64(10) // HEAD + 1-2 chunks + prefetch
	if requests > maxExpectedRequests {
		t.Errorf("Too many requests for small chunk reads: got %d, want <= %d (fetcher should batch)", 
			requests, maxExpectedRequests)
	}

	// Bytes served should be close to chunk size (4MB), not accumulated small reads
	minExpectedBytes := int64(chunkSize) // At least one full chunk
	maxExpectedBytes := int64(chunkSize * 3) // Allow for prefetch
	if bytesServed < minExpectedBytes || bytesServed > maxExpectedBytes {
		t.Errorf("Bytes served out of range: got %d, want between %d and %d", 
			bytesServed, minExpectedBytes, maxExpectedBytes)
	}
}

func TestFetcher_ConcurrentReads(t *testing.T) {
	const fileSize = 20 * 1024 * 1024 // 20 MB
	ts := newTestServer(fileSize, true)
	defer ts.Close()

	ctx := context.Background()
	f, err := New(ctx, ts.URL, fileSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer f.Close()

	// Perform concurrent reads from different offsets
	var wg sync.WaitGroup
	numGoroutines := 5
	readSize := 1024 * 1024 // 1MB

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			
			offset := int64(index * 4 * 1024 * 1024) // 4MB apart
			if offset >= fileSize {
				return
			}
			
			buf := make([]byte, readSize)
			n, err := f.ReadAt(ctx, buf, offset)
			if err != nil && err != io.EOF {
				t.Errorf("Concurrent ReadAt(%d) error = %v", offset, err)
				return
			}
			
			// Verify content
			verifyContent(t, buf[:n], offset)
		}(i)
	}

	wg.Wait()

	requests, bytesServed := ts.stats()
	t.Logf("Concurrent reads: %d requests, %d bytes served", requests, bytesServed)

	// With concurrency limiting, we shouldn't explode requests
	maxExpectedRequests := int64(30) // HEAD + chunks + prefetch
	if requests > maxExpectedRequests {
		t.Errorf("Too many requests for concurrent reads: got %d, want <= %d", requests, maxExpectedRequests)
	}
}

func TestFetcher_EdgeCases(t *testing.T) {
	const fileSize = 5 * 1024 * 1024 // 5 MB
	ts := newTestServer(fileSize, true)
	defer ts.Close()

	ctx := context.Background()
	f, err := New(ctx, ts.URL, fileSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer f.Close()

	t.Run("ReadBeyondEOF", func(t *testing.T) {
		buf := make([]byte, 1024)
		n, err := f.ReadAt(ctx, buf, fileSize)
		if err != io.EOF {
			t.Errorf("ReadAt(EOF) error = %v, want io.EOF", err)
		}
		if n != 0 {
			t.Errorf("ReadAt(EOF) read %d bytes, want 0", n)
		}
	})

	t.Run("ReadCrossingEOF", func(t *testing.T) {
		offset := int64(fileSize - 512)
		buf := make([]byte, 1024) // Try to read past EOF
		n, err := f.ReadAt(ctx, buf, offset)
		if err != nil && err != io.EOF {
			t.Errorf("ReadAt(crossing EOF) error = %v", err)
		}
		if n != 512 {
			t.Errorf("ReadAt(crossing EOF) read %d bytes, want 512", n)
		}
		verifyContent(t, buf[:n], offset)
	})

	t.Run("EmptyRead", func(t *testing.T) {
		buf := make([]byte, 0)
		n, err := f.ReadAt(ctx, buf, 0)
		if err != nil {
			t.Errorf("ReadAt(empty buffer) error = %v", err)
		}
		if n != 0 {
			t.Errorf("ReadAt(empty buffer) read %d bytes, want 0", n)
		}
	})

	t.Run("SingleByteRead", func(t *testing.T) {
		buf := make([]byte, 1)
		n, err := f.ReadAt(ctx, buf, 1000)
		if err != nil {
			t.Errorf("ReadAt(1 byte) error = %v", err)
		}
		if n != 1 {
			t.Errorf("ReadAt(1 byte) read %d bytes, want 1", n)
		}
		verifyContent(t, buf[:n], 1000)
	})
}

func TestFetcher_NoRangeSupport(t *testing.T) {
	const fileSize = 1024 * 1024 // 1 MB
	ts := newTestServer(fileSize, false)
	defer ts.Close()

	ctx := context.Background()
	_, err := New(ctx, ts.URL, fileSize)
	if err == nil {
		t.Error("New() succeeded for server without Range support, want error")
	}
	if err != nil && !strings.Contains(err.Error(), "Range") {
		t.Errorf("New() error = %v, want error mentioning Range support", err)
	}
}

func TestFetcher_InvalidSize(t *testing.T) {
	ctx := context.Background()
	_, err := New(ctx, "http://example.com", 0)
	if err == nil {
		t.Error("New() with size=0 succeeded, want error")
	}

	_, err = New(ctx, "http://example.com", -1)
	if err == nil {
		t.Error("New() with size=-1 succeeded, want error")
	}
}

func TestFetcher_CacheEfficiency(t *testing.T) {
	const fileSize = 20 * 1024 * 1024 // 20 MB
	ts := newTestServer(fileSize, true)
	defer ts.Close()

	ctx := context.Background()
	f, err := New(ctx, ts.URL, fileSize)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer f.Close()

	// Read the same region multiple times
	offset := int64(1024 * 1024) // 1MB
	buf := make([]byte, 512*1024) // 512KB

	// First read
	_, err = f.ReadAt(ctx, buf, offset)
	if err != nil {
		t.Fatalf("First ReadAt error = %v", err)
	}

	requestsAfterFirst, _ := ts.stats()

	// Read the same region again (should hit cache)
	_, err = f.ReadAt(ctx, buf, offset)
	if err != nil {
		t.Fatalf("Second ReadAt error = %v", err)
	}

	requestsAfterSecond, _ := ts.stats()

	// Second read should not generate new HTTP requests (cache hit)
	if requestsAfterSecond > requestsAfterFirst {
		t.Errorf("Cache miss: second read generated %d new requests, want 0 (cache hit)",
			requestsAfterSecond-requestsAfterFirst)
	}

	t.Logf("Cache test: first read = %d requests, second read = %d requests (delta = %d)",
		requestsAfterFirst, requestsAfterSecond, requestsAfterSecond-requestsAfterFirst)
}
