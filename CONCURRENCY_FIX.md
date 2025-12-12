# TempFileStore Concurrency Fix

## Problem Statement

The FUSE filesystem was experiencing random upload hangs and becoming unresponsive indefinitely, even with single-threaded FUSE mode disabled. Analysis indicated that the root cause was a **threading/concurrency bug** in the backing store (`TempFileStore`), likely involving:

1. **Concurrent reads** triggering race conditions
2. **Missed wakeup signals** when using `sync.Cond` for progress notification
3. **Insufficient instrumentation** to track goroutine behavior and identify deadlocks

The symptoms included:
- Uploads stalling mid-transfer
- Filesystem becoming completely unresponsive
- Two "Read ENTER" logs in a row followed by two "Read EXIT" logs before hang
- No clear indication of which operation was blocking

## Root Cause Analysis

### 1. Missed-Wakeup Race with sync.Cond
The original implementation used `sync.Cond` for signaling download progress:
```go
// Downloader
s.cond.Broadcast()  // Wake up waiters

// Reader
s.cond.Wait()  // Wait for progress
```

**Problem:** If the downloader completed *before* a reader called `Wait()`, the `Broadcast()` signal was lost forever, causing the reader to wait indefinitely.

### 2. Non-Blocking Progress Signals
The original code attempted to use an unbuffered channel with non-blocking sends:
```go
select {
case s.progressCh <- struct{}{}:
default:
    // Signal dropped!
}
```

**Problem:** If no reader was waiting on the channel at the exact moment the signal was sent, it was dropped, leading to missed wakeups.

### 3. No Goroutine Tracking
There was no way to identify which goroutine was doing what operation, making it impossible to diagnose concurrency issues from logs.

## Solution

### 1. Dual-Channel Signaling Pattern

Replaced `sync.Cond` with a **dual-channel broadcast pattern**:

```go
// Channels
doneCh   chan struct{}  // Closed when download completes
notifyCh chan struct{}  // Buffered channel for periodic progress updates

// Downloader
s.downloaded.Store(totalWritten)
select {
case s.notifyCh <- struct{}{}:  // Non-blocking send
default:
}
// On completion:
close(s.doneCh)    // Guarantees all readers wake up
close(s.notifyCh)

// Reader
for {
    downloaded := s.downloaded.Load()
    if downloaded >= wantEnd {
        break
    }
    
    select {
    case <-s.doneCh:      // Always wakes up when download finishes
        continue
    case <-s.notifyCh:    // Periodic progress updates
        continue
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

**Why this works:**
- `doneCh` is closed on completion, which **broadcasts to all goroutines** simultaneously
- Readers arriving after closure immediately proceed through the select
- `notifyCh` provides opportunistic progress updates (buffered, so never blocks downloader)
- No missed signals possible

### 2. Goroutine ID Instrumentation

Added a new package `internal/goroutineid` to extract goroutine IDs for logging:

```go
package goroutineid

func Get() uint64 {
    b := make([]byte, 64)
    b = b[:runtime.Stack(b, false)]
    // Parse "goroutine 123 [running]:\n..."
    // Returns 123
}
```

### 3. Debug ID and Comprehensive Logging

Added unique debug IDs to each `TempFileStore` instance and extensive logging:

```go
var storeDebugIDCounter atomic.Uint64

type TempFileStore struct {
    debugID uint64  // Unique ID for this store
    // ...
}

// All operations log ENTER/EXIT with debugID and goroutine ID:
log.Printf("[TempFileStore #%d] ReadAt ENTER off=%d len=%d goid=%d", ...)
defer func() {
    log.Printf("[TempFileStore #%d] ReadAt EXIT off=%d len=%d goid=%d", ...)
}()
```

**Benefits:**
- Can correlate operations across goroutines
- Can identify which goroutine is blocking
- Can detect concurrent operations on the same store
- Makes races visible in logs

### 4. Thread-Safe Design Principles

Applied strict concurrency patterns throughout:

1. **No locks held across I/O**
   ```go
   // Lock briefly to read state
   s.readFileMu.RLock()
   f := s.readFile
   s.readFileMu.RUnlock()
   
   // I/O with no locks held
   n, err := f.ReadAt(p, off)
   ```

2. **Atomic operations for counters**
   ```go
   downloaded := s.downloaded.Load()  // atomic.Int64
   refCount := s.refCount.Add(1)      // atomic.Int32
   closed := s.closed.Swap(true)      // atomic.Bool
   ```

3. **RWMutex for read-heavy access**
   ```go
   errMu      sync.RWMutex  // Protects error state
   readFileMu sync.RWMutex  // Protects file handle
   ```

## Testing

### 1. Comprehensive Concurrency Tests

Created `store_tempfile_concurrency_test.go` with focused tests:

#### **TestConcurrentReaders**
- 8 goroutines reading 100 random ranges from 32 MiB file
- Verifies data integrity under concurrent access
- Tests that `os.File.ReadAt()` is truly concurrent-safe

#### **TestReadCloseConcurrent**
- 8 goroutines hammering ReadAt operations
- Close() called while readers are active
- Verifies no deadlocks or data corruption

#### **TestMultiFileParallelism**
- 4 separate TempFileStore instances
- 4 readers per store (16 total goroutines)
- Verifies stores don't interfere with each other

#### **TestStressWithRandomGOMAXPROCS**
- Runs tests with randomized `GOMAXPROCS` (1 to numCPU)
- Exposes scheduling-dependent race conditions
- 10 iterations with different scheduler configurations

#### **TestNoDeadlockOnEarlyClose**
- Close store before download completes
- Verifies graceful cancellation with timeout

### 2. Stress Test Harness

Created `scripts/stress-test.sh` that runs:
1. 100 iterations with race detector
2. 100 iterations with randomized GOMAXPROCS
3. Stress tests with race detector
4. High concurrency tests with parallel execution

```bash
# Run stress tests
./scripts/stress-test.sh

# Or with custom iteration count
ITERATIONS=500 ./scripts/stress-test.sh
```

### 3. Static Analysis

#### **go vet**
Checks for common Go mistakes:
```bash
go vet ./internal/...
```

#### **staticcheck**
Advanced static analysis:
```bash
staticcheck ./internal/...
```

### 4. Race Detector

All tests can be run with Go's race detector:
```bash
go test -race ./internal/...
```

The race detector instruments memory accesses at runtime to detect data races.

## CI Integration

Updated `.github/workflows/ci.yml` with three new jobs:

### 1. `static-analysis` job
- Runs `go vet ./internal/...`
- Runs `staticcheck ./internal/...`
- **Fails the build** if any issues found

### 2. `race-detector` job
- Runs `go test -race -timeout=25m ./internal/...`
- **Fails the build** if race detector reports issues
- Timeout: 30 minutes (race detector is ~10x slower)

### 3. Existing `unit-tests` job
- Still runs regular tests
- No race detector (faster feedback)

**Result:** CI now catches concurrency bugs before they reach production!

## Makefile Targets

Created `Makefile` for easier testing:

```bash
make lint            # Run go vet + staticcheck
make test            # Run all tests
make test-race       # Run tests with race detector
make test-short      # Run short tests only
make ci              # Run CI checks locally (lint + test-race)
make stress-test     # Run stress test harness
make help            # Show all targets
```

## Usage Examples

### Running Tests Locally

```bash
# Quick check (no race detector)
make test-short

# Full test suite
make test

# With race detector (slow but thorough)
make test-race

# Stress testing
make stress-test
```

### Debugging Hangs

If the filesystem hangs, check logs for:

1. **Which store is involved:**
   ```
   [TempFileStore #5] ReadAt ENTER off=1048576 len=131072 goid=42
   ```

2. **Concurrent operations:**
   ```
   [TempFileStore #5] ReadAt ENTER off=1048576 len=131072 goid=42
   [TempFileStore #5] ReadAt ENTER off=2097152 len=131072 goid=43  <- concurrent
   ```

3. **Missing EXIT logs:**
   ```
   [TempFileStore #5] ReadAt ENTER off=1048576 len=131072 goid=42
   # No EXIT log = operation hung
   ```

### Analyzing Race Detector Output

If race detector finds an issue:

```
==================
WARNING: DATA RACE
Read at 0x00c000012345 by goroutine 7:
  github.com/mmilitzer/fuse-stream-mvp/internal/fetcher.(*TempFileStore).ReadAt()
      /workspace/project/fuse-stream-mvp/internal/fetcher/store_tempfile.go:283

Previous write at 0x00c000012345 by goroutine 6:
  github.com/mmilitzer/fuse-stream-mvp/internal/fetcher.(*TempFileStore).downloader()
      /workspace/project/fuse-stream-mvp/internal/fetcher/store_tempfile.go:169
==================
```

1. Note the goroutine IDs (7 and 6)
2. Check the line numbers (283 and 169)
3. Look for unprotected access to shared state
4. Add proper synchronization (mutex, atomic, or channels)

## Performance Considerations

### Lock Contention
- Minimized by holding locks only briefly
- Never hold locks across I/O operations
- Use RWMutex for read-heavy patterns

### Channel Overhead
- `notifyCh` is buffered (100 elements) to avoid blocking downloader
- `doneCh` closed once (broadcast pattern)
- Readers use `select` for opportunistic receive (no busy-waiting)

### Race Detector Overhead
- ~10x slower execution
- ~10x more memory usage
- Only use in testing, not production
- CI runs both regular and race-detector tests

### os.File.ReadAt Concurrency
- Go's `os.File.ReadAt()` is documented as safe for concurrent use
- Each call uses separate syscall with independent offset
- No lock contention in kernel for different offsets
- Verified by concurrent reader tests

## Key Takeaways

1. **Never use sync.Cond for one-shot events** - use channel closure instead
2. **Closed channels broadcast to all waiters** - the only race-free notification pattern
3. **Instrument with goroutine IDs** - makes concurrent bugs visible in logs
4. **Test with race detector** - catches subtle concurrency bugs
5. **Stress test with randomized GOMAXPROCS** - exposes scheduling-dependent races
6. **CI gates with race detector** - prevents regression

## References

- [Go sync.Cond documentation](https://pkg.go.dev/sync#Cond)
- [Go Race Detector](https://go.dev/doc/articles/race_detector)
- [os.File.ReadAt thread-safety](https://pkg.go.dev/os#File.ReadAt)
- [staticcheck documentation](https://staticcheck.io/)
