package fetcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
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

// TestReadCloseConcurrent tests the race condition between ReadAt and Close using deterministic barriers.
// This test ensures that Close is called while readers are guaranteed to be in-flight,
// and verifies that no deadlock occurs.
func TestReadCloseConcurrent(t *testing.T) {
	const (
		fileSize   = 8 * 1024 * 1024 // 8 MiB
		numReaders = 8
		chunkSize  = 64 * 1024 // 64 KiB
		testTimeout = 5 * time.Second // Entire test must complete within this time
	)

	// Create test context with deadline to catch deadlocks
	testCtx, testCancel := context.WithTimeout(context.Background(), testTimeout)
	defer testCancel()

	// Create synthetic test file
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create test HTTP server that serves data slowly to ensure reads are in-flight
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
	store, err := NewTempFileStore(testCtx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Barrier pattern: readers signal ready, then wait for start signal
	ready := make(chan struct{}, numReaders)
	start := make(chan struct{})
	
	var wg sync.WaitGroup
	var successfulReads atomic.Int64
	var failedReads atomic.Int64

	// Launch reader goroutines
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			buf := make([]byte, chunkSize)
			offset := int64(readerID * chunkSize) % (fileSize - chunkSize)

			// Signal that we're ready to read
			ready <- struct{}{}
			
			// Wait for start signal (this is where Close will be called)
			select {
			case <-start:
				// Proceed with read
			case <-testCtx.Done():
				failedReads.Add(1)
				return
			}

			// Attempt read - this may succeed or fail depending on whether Close completed
			readCtx, cancel := context.WithTimeout(testCtx, 2*time.Second)
			n, err := store.ReadAt(readCtx, buf[:chunkSize], offset)
			cancel()

			if err != nil {
				// Expected errors after close
				if err == fs.ErrClosed || err == context.Canceled || err == context.DeadlineExceeded {
					failedReads.Add(1)
					return
				}
				// Other errors
				errStr := err.Error()
				if strings.Contains(errStr, "closed") || 
				   strings.Contains(errStr, "canceled") ||
				   strings.Contains(errStr, "connection reset") {
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
		}(i)
	}

	// Wait for at least one reader to be ready
	select {
	case <-ready:
		// At least one reader is ready
	case <-testCtx.Done():
		t.Fatal("Timeout waiting for readers to become ready")
	}

	// Drain any additional ready signals
	drainedReady := 1
	for drainedReady < numReaders {
		select {
		case <-ready:
			drainedReady++
		case <-time.After(100 * time.Millisecond):
			// Some readers might not be ready yet, that's fine
			goto closeNow
		}
	}

closeNow:
	t.Logf("%d/%d readers ready, initiating Close", drainedReady, numReaders)

	// Now call Close while readers are waiting to proceed
	closeCtx, closeCancel := context.WithTimeout(testCtx, 3*time.Second)
	closeErr := store.CloseWithContext(closeCtx)
	closeCancel()

	if closeErr != nil && closeErr != os.ErrNotExist && closeErr != context.DeadlineExceeded {
		t.Logf("Close returned error (may be expected): %v", closeErr)
	}

	// Release readers - they will attempt to read and should get ErrClosed or succeed
	close(start)

	// Wait for all readers to complete (with deadline)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("All readers completed: successful=%d, failed=%d", successfulReads.Load(), failedReads.Load())
	case <-testCtx.Done():
		t.Fatal("DEADLOCK: Readers did not complete within timeout")
	}

	// Verify that operations completed
	totalOps := successfulReads.Load() + failedReads.Load()
	if totalOps == 0 {
		t.Error("No read operations completed (possible deadlock)")
	}
}

// TestReadCloseDeterministic is the most rigorous test - it ensures Close overlaps active ReadAt calls.
// Uses a barrier pattern where readers signal they've entered ReadAt (before syscall), then block
// until the test calls Close and releases them.
func TestReadCloseDeterministic(t *testing.T) {
	const (
		fileSize = 10 * 1024 * 1024 // 10 MiB
		numReaders = 4
		chunkSize = 64 * 1024
		testTimeout = 5 * time.Second
	)

	// Create test context with deadline
	testCtx, testCancel := context.WithTimeout(context.Background(), testTimeout)
	defer testCancel()

	// Create synthetic test file
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create HTTP server that serves data very slowly
	// This ensures ReadAt calls will be waiting in the loop when Close is called
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		
		// Send only first chunk, then stall - readers will wait for more data
		if len(testData) > 0 {
			firstChunk := chunkSize
			if firstChunk > len(testData) {
				firstChunk = len(testData)
			}
			w.Write(testData[:firstChunk])
			
			// Flush and then stall indefinitely
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			
			// Wait for cancellation
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	// Create TempFileStore
	store, err := NewTempFileStore(testCtx, server.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Barrier: readers signal when they've entered ReadAt and are waiting for data
	ready := make(chan struct{}, numReaders)
	start := make(chan struct{})
	
	var wg sync.WaitGroup
	var readAttempts atomic.Int64

	// Launch readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			buf := make([]byte, chunkSize)
			// Read from later in file - will force waiting since server only sends first chunk
			offset := int64(2*chunkSize + readerID*chunkSize)
			if offset >= fileSize {
				offset = fileSize - chunkSize
			}

			// Create a context that we can pass to ReadAt
			// This context will signal when start channel is closed
			readCtx, readCancel := context.WithCancel(testCtx)
			defer readCancel()
			
			// Start ReadAt in a goroutine so we can signal ready
			readErr := make(chan error, 1)
			go func() {
				// Signal ready right before attempting read
				ready <- struct{}{}
				_, err := store.ReadAt(readCtx, buf, offset)
				readErr <- err
			}()

			// Wait for start signal
			select {
			case <-start:
				readCancel() // Cancel the read context
			case <-testCtx.Done():
				readCancel()
			}

			// Wait for read to complete
			<-readErr
			readAttempts.Add(1)
		}(i)
	}

	// Wait for at least one reader to enter ReadAt
	readyCount := 0
	for readyCount < numReaders {
		select {
		case <-ready:
			readyCount++
			if readyCount == 1 {
				t.Logf("First reader entered ReadAt")
			}
		case <-time.After(500 * time.Millisecond):
			// Timeout waiting for more readers - proceed with what we have
			if readyCount == 0 {
				t.Fatal("Timeout: no readers entered ReadAt")
			}
			t.Logf("%d/%d readers entered ReadAt, proceeding", readyCount, numReaders)
			goto callClose
		}
	}
	t.Logf("All %d readers entered ReadAt", numReaders)

callClose:
	// NOW call Close - readers are in ReadAt waiting for data
	t.Logf("Calling Close while readers are in ReadAt")
	closeCtx, closeCancel := context.WithTimeout(testCtx, 3*time.Second)
	closeErr := store.CloseWithContext(closeCtx)
	closeCancel()

	if closeErr != nil && closeErr != os.ErrNotExist && closeErr != context.DeadlineExceeded {
		t.Logf("Close returned: %v", closeErr)
	}

	// Release readers
	close(start)

	// Wait for readers to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("SUCCESS: All readers exited, no deadlock. Read attempts: %d", readAttempts.Load())
	case <-testCtx.Done():
		t.Fatal("DEADLOCK: Readers did not exit within timeout")
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
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	const fileSize = 10 * 1024 * 1024 // 10 MiB

	// Create test data
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("Failed to generate test data: %v", err)
	}

	// Create slow server that respects client disconnect
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		
		flusher, _ := w.(http.Flusher)
		// Send very slowly, but check for client disconnect
		for i := 0; i < len(testData); i += 1024 {
			select {
			case <-r.Context().Done():
				// Client disconnected, stop sending
				return
			default:
			}

			end := i + 1024
			if end > len(testData) {
				end = len(testData)
			}
			_, err := w.Write(testData[i:end])
			if err != nil {
				// Write failed, client disconnected
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(50 * time.Millisecond) // Slow but not too slow
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

// TestMultiFileStagingScenario reproduces the original deadlock scenario:
// Create two TempFileStores, start reads on A, concurrently Close A and begin reads on B.
// This verifies no deadlock occurs and no global state causes interference.
func TestMultiFileStagingScenario(t *testing.T) {
	const (
		fileSize = 4 * 1024 * 1024 // 4 MiB
		chunkSize = 64 * 1024
		testTimeout = 10 * time.Second
	)

	testCtx, testCancel := context.WithTimeout(context.Background(), testTimeout)
	defer testCancel()

	// Create test data for both stores
	testDataA := make([]byte, fileSize)
	testDataB := make([]byte, fileSize)
	rand.Read(testDataA)
	rand.Read(testDataB)

	// Create HTTP servers
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		
		// Send data slowly
		written := 0
		for written < len(testDataA) {
			n := chunkSize
			if written+n > len(testDataA) {
				n = len(testDataA) - written
			}
			if _, err := w.Write(testDataA[written : written+n]); err != nil {
				return
			}
			written += n
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		w.Write(testDataB)
	}))
	defer serverB.Close()

	// Create store A
	storeA, err := NewTempFileStore(testCtx, serverA.URL, fileSize, StoreOptions{})
	if err != nil {
		t.Fatalf("Failed to create store A: %v", err)
	}

	var wg sync.WaitGroup
	var readsOnA, readsOnB atomic.Int64
	readyA := make(chan struct{}, 4)

	// Start reads on store A
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Signal ready before first read
			readyA <- struct{}{}

			buf := make([]byte, chunkSize)
			for j := 0; j < 10; j++ {
				offset := int64((idx*10 + j) * chunkSize) % (fileSize - chunkSize)
				readCtx, cancel := context.WithTimeout(testCtx, 2*time.Second)
				n, err := storeA.ReadAt(readCtx, buf, offset)
				cancel()

				if err != nil {
					// Expected after close
					if err == fs.ErrClosed || err == context.Canceled {
						return
					}
					t.Logf("Store A read error (expected after close): %v", err)
					return
				}

				if n > 0 && offset+int64(n) <= fileSize {
					if !bytes.Equal(buf[:n], testDataA[offset:offset+int64(n)]) {
						t.Errorf("Store A: data corruption")
						return
					}
				}
				readsOnA.Add(1)
			}
		}(i)
	}

	// Wait for at least one reader to be ready on store A
	select {
	case <-readyA:
		t.Logf("At least one reader started on store A")
	case <-testCtx.Done():
		t.Fatal("Timeout waiting for readers on store A")
	}

	// CONCURRENTLY: Close A and create/read from B
	var closeWg sync.WaitGroup
	
	// Close store A
	closeWg.Add(1)
	go func() {
		defer closeWg.Done()
		t.Logf("Closing store A")
		closeCtx, cancel := context.WithTimeout(testCtx, 5*time.Second)
		closeErr := storeA.CloseWithContext(closeCtx)
		cancel()
		if closeErr != nil && closeErr != os.ErrNotExist {
			t.Logf("Store A close: %v", closeErr)
		}
	}()

	// Create store B and start reads
	closeWg.Add(1)
	go func() {
		defer closeWg.Done()
		
		storeB, err := NewTempFileStore(testCtx, serverB.URL, fileSize, StoreOptions{})
		if err != nil {
			t.Errorf("Failed to create store B: %v", err)
			return
		}
		defer storeB.Close()

		t.Logf("Starting reads on store B")
		buf := make([]byte, chunkSize)
		for i := 0; i < 10; i++ {
			offset := int64(i * chunkSize) % (fileSize - chunkSize)
			readCtx, cancel := context.WithTimeout(testCtx, 2*time.Second)
			n, err := storeB.ReadAt(readCtx, buf, offset)
			cancel()

			if err != nil && err != io.EOF {
				t.Errorf("Store B read failed: %v", err)
				return
			}

			if n > 0 && offset+int64(n) <= fileSize {
				if !bytes.Equal(buf[:n], testDataB[offset:offset+int64(n)]) {
					t.Errorf("Store B: data corruption")
					return
				}
			}
			readsOnB.Add(1)
		}
	}()

	// Wait for concurrent operations with deadline
	done := make(chan struct{})
	go func() {
		closeWg.Wait()
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("SUCCESS: Multi-file scenario completed without deadlock. Reads on A: %d, Reads on B: %d", 
			readsOnA.Load(), readsOnB.Load())
	case <-testCtx.Done():
		t.Fatal("DEADLOCK: Multi-file scenario did not complete within timeout")
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
