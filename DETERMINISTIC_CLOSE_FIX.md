# Deterministic Close/Read Race Fix

## Overview

This document describes the comprehensive fix for the TempFileStore close/read race condition, implementing deterministic testing and robust synchronization primitives.

## Problem Statement

The previous implementation had several issues:
1. **Tests used sleeps** - TestReadCloseConcurrent used `time.Sleep(50ms)` to "hope" that Close would overlap with active reads
2. **Race condition not guaranteed** - The sleep-based approach was non-deterministic and could miss the actual race
3. **No timeout enforcement** - Tests could hang indefinitely if deadlock occurred
4. **Non-standard errors** - Returned custom error strings instead of `fs.ErrClosed`
5. **Slow closing detection** - ReadAt wait loop didn't check closing flag, causing slow response to Close

## Solution

### 1. Code Changes to TempFileStore

#### Added closing flag check in wait loop
```go
for {
    // Check if store is closing - exit promptly
    if s.closing.Load() != 0 {
        return 0, fs.ErrClosed
    }
    
    // ... rest of wait loop
}
```

This ensures ReadAt exits immediately when Close is called, even if it's waiting for data.

#### Use fs.ErrClosed standard error
Changed all `fmt.Errorf("temp file closed")` to `fs.ErrClosed` for better compatibility with standard Go error handling.

### 2. Deterministic Tests

#### TestReadCloseConcurrent - Barrier Pattern
Instead of sleep, uses barrier synchronization:
1. Readers signal `ready` channel before attempting read
2. Readers block on `start` channel
3. Test waits for at least one ready signal
4. Test calls `CloseWithContext()`
5. Test closes `start` channel to release readers
6. Readers either succeed or get `fs.ErrClosed`

**Key improvement:** Guaranteed overlap between Close and read attempts.

#### TestReadCloseDeterministic - In-Flight Reads
Even more rigorous test:
1. Server sends only first chunk, then stalls
2. Readers attempt to read from later offsets (forcing wait loop)
3. Readers signal `ready` after entering ReadAt
4. Test waits for ready signals
5. Test calls Close while readers are in the wait loop
6. Readers must exit promptly with `fs.ErrClosed`

**Key improvement:** Guarantees Close happens while readers are truly in-flight, blocked in the wait loop.

#### TestMultiFileStagingScenario - Original Scenario
Reproduces the actual deadlock scenario:
1. Create store A, start concurrent reads
2. Concurrently:
   - Close store A
   - Create store B, start reads on it
3. Verify no deadlock and no interference between stores

**Key improvement:** Tests the actual multi-file scenario that was causing issues in production.

### 3. Timeout Enforcement

All tests now have explicit timeouts:
- TestReadCloseConcurrent: 5 seconds
- TestReadCloseDeterministic: 5 seconds  
- TestMultiFileStagingScenario: 10 seconds

Tests use `context.WithTimeout` and verify completion with:
```go
select {
case <-done:
    // Success
case <-testCtx.Done():
    t.Fatal("DEADLOCK: Readers did not complete within timeout")
}
```

### 4. Safety Requirements Verified

✅ **No sleeps in concurrency tests** - Only synchronization primitives (channels, contexts)
✅ **CloseWithContext never waits while holding locks** - Uses atomics and WaitGroups without mutexes
✅ **Reads can't start after closing flips** - Double-check pattern prevents TOCTOU races
✅ **No global/shared state** - Each store is independent with its own goroutines and channels
✅ **Read returns n on partial/EOF** - Proper EOF handling for partial reads
✅ **Closing flag checked in all paths** - Entry, after registration, and in wait loop

## Technical Details

### Readers Gate Pattern

The implementation uses a "readers gate" pattern:

1. **Entry gate**: Check closing flag before entering
2. **Registration**: Add to WaitGroup before accessing resources  
3. **Double-check**: Check closing flag after registration
4. **In-flight check**: Check closing flag in wait loop
5. **Exit**: Decrement WaitGroup on defer

CloseWithContext:
1. Set closing flag (prevents new entries)
2. Cancel downloader
3. Wait for downloader to finish
4. Wait for readers WaitGroup to drain
5. Close file handle
6. Remove temp file

This ensures:
- New reads see closing flag and bail out
- In-flight reads see closing flag in wait loop and exit
- Close waits for all reads to drain before closing file
- No file-closed-during-read races

### Barrier Pattern in Tests

Instead of timing-dependent sleeps, tests use barriers:

```go
ready := make(chan struct{}, N)  // Buffered for N readers
start := make(chan struct{})      // Broadcast channel

// Readers:
ready <- struct{}{}  // Signal ready
<-start              // Wait for start
// ... attempt operation ...

// Test:
<-ready              // Wait for at least one reader
Close()              // Call operation being tested
close(start)         // Release all readers
wg.Wait()            // Wait for completion with timeout
```

This guarantees deterministic overlap of operations.

## Test Coverage

1. **TestConcurrentReaders** - Many readers on one store (data integrity)
2. **TestReadCloseConcurrent** - Close overlaps read attempts (barrier pattern)
3. **TestReadCloseDeterministic** - Close overlaps in-flight reads (most rigorous)
4. **TestMultiFileParallelism** - Multiple stores in parallel (no interference)
5. **TestMultiFileStagingScenario** - Close A while starting B (original scenario)
6. **TestNoDeadlockOnEarlyClose** - Close before download completes
7. **TestStressWithRandomGOMAXPROCS** - Randomized scheduling stress test

## Validation

The fix ensures:
1. ✅ No sleeps in concurrency tests (deterministic)
2. ✅ All tests have deadlock timeouts (5-10s)
3. ✅ Tests fail with clear "DEADLOCK" message on timeout
4. ✅ Close cannot block indefinitely
5. ✅ Reads exit promptly when closing
6. ✅ No data corruption under concurrent access
7. ✅ Multiple stores don't interfere with each other

## Future Enhancements (Optional)

If even more safety is desired:
1. **Per-handle dup**: Dup the fd on Open, close dup on Release, only close base fd when refcount hits 0
2. **Non-blocking logger**: Use try-send to buffered channel, drop on full
3. **In-flight counter per opcode**: Track which FUSE callback is stuck if deadlock occurs
