package fetcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentReaders tests multiple goroutines reading from the same TempFileStore concurrently.
// This verifies that concurrent ReadAt operations don't corrupt data or deadlock.
func TestConcurrentReaders(t *testing.T) {
	const (
		fileSize    = 32 * 1024 * 1024 // 32 MiB
		numReaders  = 8
		readsPerReader = 100
		chunkSize   = 128 * 1024 // 128 KiB
	)

	// Create synthetic test file with known pattern
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		
		// Simulate slower network by writing in chunks with delays
		written := 0
		for written < len(testData) {
			n := chunkSize
			if written+n > len(testData) {
				n = len(testData) - written
			}
			if _, err := w.Write(testData[written : written+n]); err != nil {
				return
			}
			written += n
			time.Sleep(5 * time.Millisecond) // Simulate network latency
		}
	}))
	defer server.Close()

	// Create TempFileStore
	ctx := context.Background()
	store, err := NewTempFileStore(ctx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Launch concurrent readers
	var wg sync.WaitGroup
	errCh := make(chan error, numReaders*readsPerReader)

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			buf := make([]byte, chunkSize)
			rng := mrand.New(mrand.NewSource(int64(readerID)))

			for j := 0; j < readsPerReader; j++ {
				// Read random aligned range
				offset := int64(rng.Intn(int(fileSize-chunkSize)/chunkSize)) * chunkSize
				readSize := chunkSize
				if offset+int64(readSize) > fileSize {
					readSize = int(fileSize - offset)
				}

				// Read with timeout context
				readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				n, err := store.ReadAt(readCtx, buf[:readSize], offset)
				cancel()

				if err != nil && err != io.EOF {
					errCh <- fmt.Errorf("reader %d read %d at offset %d: %w", readerID, j, offset, err)
					return
				}

				// Verify data matches
				if !bytes.Equal(buf[:n], testData[offset:offset+int64(n)]) {
					errCh <- fmt.Errorf("reader %d read %d: data mismatch at offset %d", readerID, j, offset)
					return
				}
			}
		}(i)
	}

	// Wait for all readers to finish
	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		t.Error(err)
	}
}

// TestReadCloseConcurrent tests the race condition between ReadAt and Close.
// This verifies that calling Close while readers are active doesn't cause
// data corruption or deadlocks.
func TestReadCloseConcurrent(t *testing.T) {
	const (
		fileSize   = 8 * 1024 * 1024 // 8 MiB
		numReaders = 8
		chunkSize  = 64 * 1024 // 64 KiB
	)

	// Create synthetic test file
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		
		// Write data with delays to allow concurrent reads
		written := 0
		for written < len(testData) {
			n := chunkSize
			if written+n > len(testData) {
				n = len(testData) - written
			}
			if _, err := w.Write(testData[written : written+n]); err != nil {
				return
			}
			written += n
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	// Create TempFileStore
	ctx := context.Background()
	store, err := NewTempFileStore(ctx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Launch readers that hammer the store
	var wg sync.WaitGroup
	stopReaders := make(chan struct{})
	var successfulReads atomic.Int64
	var failedReads atomic.Int64

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			buf := make([]byte, chunkSize)
			rng := mrand.New(mrand.NewSource(int64(readerID)))

			for {
				select {
				case <-stopReaders:
					return
				default:
				}

				// Read random range
				offset := int64(rng.Intn(int(fileSize-chunkSize)/chunkSize)) * chunkSize
				readSize := chunkSize
				if offset+int64(readSize) > fileSize {
					readSize = int(fileSize - offset)
				}

				// Read with timeout
				readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				n, err := store.ReadAt(readCtx, buf[:readSize], offset)
				cancel()

				if err != nil {
					if err == context.Canceled || err == context.DeadlineExceeded {
						failedReads.Add(1)
						continue
					}
					// Expected errors after close
					if err.Error() == "temp file closed" {
						failedReads.Add(1)
						return
					}
					t.Logf("Reader %d unexpected error: %v", readerID, err)
					failedReads.Add(1)
					return
				}

				// Verify data if read succeeded
				if n > 0 && offset+int64(n) <= fileSize {
					if !bytes.Equal(buf[:n], testData[offset:offset+int64(n)]) {
						t.Errorf("Reader %d: data corruption at offset %d", readerID, offset)
						return
					}
				}

				successfulReads.Add(1)
			}
		}(i)
	}

	// Let readers run for a bit
	time.Sleep(200 * time.Millisecond)

	// Now close the store while readers are active
	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	closeErr := store.CloseWithContext(closeCtx)
	cancel()

	if closeErr != nil && closeErr != os.ErrNotExist {
		t.Logf("Close returned error (may be expected): %v", closeErr)
	}

	// Stop readers
	close(stopReaders)
	wg.Wait()

	t.Logf("Successful reads: %d, Failed reads: %d", successfulReads.Load(), failedReads.Load())
	
	// We should have at least some successful reads
	if successfulReads.Load() == 0 {
		t.Error("No successful reads occurred before close")
	}
}

// TestMultiFileParallelism tests multiple TempFileStore instances operating in parallel.
// This verifies that different stores don't interfere with each other.
func TestMultiFileParallelism(t *testing.T) {
	const (
		numStores     = 4
		fileSize      = 4 * 1024 * 1024 // 4 MiB per file
		readersPerStore = 4
		readsPerReader  = 50
		chunkSize     = 64 * 1024
	)

	// Create test data for each store
	testDataSets := make([][]byte, numStores)
	for i := 0; i < numStores; i++ {
		testDataSets[i] = make([]byte, fileSize)
		if _, err := rand.Read(testDataSets[i]); err != nil {
			t.Fatalf("Failed to generate test data %d: %v", i, err)
		}
	}

	// Create HTTP servers for each store
	servers := make([]*httptest.Server, numStores)
	for i := 0; i < numStores; i++ {
		idx := i // Capture for closure
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
			w.WriteHeader(http.StatusOK)
			
			written := 0
			for written < len(testDataSets[idx]) {
				n := chunkSize
				if written+n > len(testDataSets[idx]) {
					n = len(testDataSets[idx]) - written
				}
				if _, err := w.Write(testDataSets[idx][written : written+n]); err != nil {
					return
				}
				written += n
				time.Sleep(5 * time.Millisecond)
			}
		}))
		defer servers[i].Close()
	}

	// Create stores
	ctx := context.Background()
	stores := make([]*TempFileStore, numStores)
	for i := 0; i < numStores; i++ {
		var err error
		stores[i], err = NewTempFileStore(ctx, servers[i].URL, fileSize, StoreOptions{})
		if err != nil {
			t.Fatalf("Failed to create store %d: %v", i, err)
		}
		defer stores[i].Close()
	}

	// Launch readers for all stores in parallel
	var wg sync.WaitGroup
	errCh := make(chan error, numStores*readersPerStore*readsPerReader)

	for storeIdx := 0; storeIdx < numStores; storeIdx++ {
		for readerIdx := 0; readerIdx < readersPerStore; readerIdx++ {
			wg.Add(1)
			go func(storeID, readerID int) {
				defer wg.Done()

				store := stores[storeID]
				testData := testDataSets[storeID]
				buf := make([]byte, chunkSize)
				rng := mrand.New(mrand.NewSource(int64(storeID*readersPerStore + readerID)))

				for j := 0; j < readsPerReader; j++ {
					// Read random range
					offset := int64(rng.Intn(int(fileSize-chunkSize)/chunkSize)) * chunkSize
					readSize := chunkSize
					if offset+int64(readSize) > fileSize {
						readSize = int(fileSize - offset)
					}

					// Read with timeout
					readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					n, err := store.ReadAt(readCtx, buf[:readSize], offset)
					cancel()

					if err != nil && err != io.EOF {
						errCh <- fmt.Errorf("store %d reader %d read %d: %w", storeID, readerID, j, err)
						return
					}

					// Verify data
					if !bytes.Equal(buf[:n], testData[offset:offset+int64(n)]) {
						errCh <- fmt.Errorf("store %d reader %d read %d: data mismatch at offset %d", storeID, readerID, j, offset)
						return
					}
				}
			}(storeIdx, readerIdx)
		}
	}

	// Wait for all readers
	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		t.Error(err)
	}
}

// TestStressWithRandomGOMAXPROCS runs concurrent reader test with randomized GOMAXPROCS.
// This helps expose scheduling-dependent race conditions.
func TestStressWithRandomGOMAXPROCS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	const iterations = 10 // Run multiple times with different GOMAXPROCS
	
	for i := 0; i < iterations; i++ {
		// Randomize GOMAXPROCS between 1 and numCPU
		numCPU := runtime.NumCPU()
		gomaxprocs := mrand.Intn(numCPU) + 1
		oldGOMAXPROCS := runtime.GOMAXPROCS(gomaxprocs)
		
		t.Logf("Iteration %d: GOMAXPROCS=%d", i, gomaxprocs)
		
		// Run a mini version of the concurrent readers test
		runMiniConcurrentReadersTest(t)
		
		// Restore GOMAXPROCS
		runtime.GOMAXPROCS(oldGOMAXPROCS)
	}
}

// runMiniConcurrentReadersTest is a smaller version of TestConcurrentReaders for stress testing.
func runMiniConcurrentReadersTest(t *testing.T) {
	const (
		fileSize       = 2 * 1024 * 1024 // 2 MiB
		numReaders     = 4
		readsPerReader = 20
		chunkSize      = 32 * 1024 // 32 KiB
	)

	// Create synthetic test file
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		w.Write(testData)
	}))
	defer server.Close()

	// Create TempFileStore
	ctx := context.Background()
	store, err := NewTempFileStore(ctx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Launch concurrent readers
	var wg sync.WaitGroup
	errCh := make(chan error, numReaders)

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			buf := make([]byte, chunkSize)
			rng := mrand.New(mrand.NewSource(int64(readerID) + time.Now().UnixNano()))

			for j := 0; j < readsPerReader; j++ {
				// Read random range
				maxOffset := int(fileSize - chunkSize)
				if maxOffset < 0 {
					maxOffset = 0
				}
				offset := int64(rng.Intn(maxOffset+1))
				if offset < 0 {
					offset = 0
				}
				readSize := chunkSize
				if offset+int64(readSize) > fileSize {
					readSize = int(fileSize - offset)
				}

				// Read with timeout
				readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				n, err := store.ReadAt(readCtx, buf[:readSize], offset)
				cancel()

				if err != nil && err != io.EOF {
					errCh <- fmt.Errorf("reader %d read %d: %w", readerID, j, err)
					return
				}

				// Verify data
				if n > 0 && !bytes.Equal(buf[:n], testData[offset:offset+int64(n)]) {
					errCh <- fmt.Errorf("reader %d read %d: data mismatch", readerID, j)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

// TestNoDeadlockOnEarlyClose tests that closing a store before download completes doesn't deadlock.
func TestNoDeadlockOnEarlyClose(t *testing.T) {
	const fileSize = 10 * 1024 * 1024 // 10 MiB

	// Create test data
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create slow server that takes forever to send data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		
		// Send very slowly
		for i := 0; i < len(testData); i += 1024 {
			end := i + 1024
			if end > len(testData) {
				end = len(testData)
			}
			w.Write(testData[i:end])
			time.Sleep(100 * time.Millisecond) // Very slow
		}
	}))
	defer server.Close()

	// Create store
	ctx := context.Background()
	store, err := NewTempFileStore(ctx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Let it start downloading
	time.Sleep(100 * time.Millisecond)

	// Close immediately with timeout
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	closeErr := store.CloseWithContext(closeCtx)
	cancel()

	// Should not hang - either succeeds or times out gracefully
	if closeErr != nil && closeErr != os.ErrNotExist {
		t.Logf("Close returned: %v (expected)", closeErr)
	}
}

// BenchmarkConcurrentReads benchmarks concurrent ReadAt performance.
func BenchmarkConcurrentReads(b *testing.B) {
	const (
		fileSize  = 8 * 1024 * 1024 // 8 MiB
		chunkSize = 64 * 1024       // 64 KiB
	)

	// Create test data
	testData := make([]byte, fileSize)
	rand.Read(testData)

	// Create server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		w.Write(testData)
	}))
	defer server.Close()

	// Create store
	ctx := context.Background()
	store, err := NewTempFileStore(ctx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		b.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Wait for download to complete
	buf := make([]byte, 1)
	for {
		_, err := store.ReadAt(ctx, buf, fileSize-1)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, chunkSize)
		rng := mrand.New(mrand.NewSource(time.Now().UnixNano()))
		
		for pb.Next() {
			offset := int64(rng.Intn(int(fileSize-chunkSize)/chunkSize)) * chunkSize
			_, err := store.ReadAt(ctx, buf, offset)
			if err != nil && err != io.EOF {
				b.Fatalf("ReadAt failed: %v", err)
			}
		}
	})
}
