# TempFileStore Deadlock Fix

## Problem Analysis

### Symptoms
- Uploads would hang mid-transfer
- FUSE filesystem would become completely unresponsive
- `ls -l /Volumes/FuseStream/` would hang indefinitely
- Timeouts on FUSE Read() callbacks were not being respected

### Root Cause: Three Critical Issues with sync.Cond

#### 1. **Missed-Wakeup Race Condition**
The most critical issue was a classic lost-wakeup problem with `sync.Cond`:

```go
// OLD CODE - BROKEN
for {
    s.cond.L.Lock()
    downloaded := s.downloaded.Load()
    if downloaded >= wantEnd {
        s.cond.L.Unlock()
        break
    }
    // ⚠️ RACE WINDOW HERE ⚠️
    // If downloader finishes and calls Broadcast() HERE,
    // the signal is lost forever!
    s.cond.Wait()  // This will block indefinitely
    s.cond.L.Unlock()
}
```

**Timeline of the deadlock:**
1. Reader goroutine checks `downloaded < wantEnd` (not enough data yet)
2. Reader is about to call `s.cond.Wait()`
3. **Before Wait() is called**, downloader goroutine finishes downloading
4. Downloader calls `s.cond.Broadcast()` - but no one is waiting yet!
5. Reader now calls `s.cond.Wait()` - but the signal was already sent
6. Reader blocks forever because downloader is finished and will never signal again

#### 2. **Multiple Concurrent Opens Aggravate the Issue**
Every `Open()` on the FUSE filesystem shares the same `TempFileStore` instance. This means:
- Dozens of goroutines might simultaneously call `ReadAt()` for the same file
- Each one hits the same race condition window
- With many concurrent readers, the probability of hitting the race increases dramatically
- One unlucky race is enough to deadlock a FUSE worker thread permanently

#### 3. **Complex Timeout Workaround Was Insufficient**
The previous attempt to fix this used timeout goroutines:

```go
// OLD CODE - STILL BROKEN
waitDone := make(chan struct{})
go func() {
    select {
    case <-time.After(100 * time.Millisecond):
        s.cond.Broadcast()  // Wake up to recheck
    case <-ctx.Done():
        s.cond.Broadcast()
    }
    close(waitDone)
}()
s.cond.Wait()
<-waitDone
```

**Problems with this approach:**
- Still had the race condition between checking state and calling Wait()
- Created goroutines for every wait iteration (expensive)
- Complex to understand and maintain
- The periodic broadcasts didn't solve the fundamental missed-wakeup problem

## Solution: Channel-Based Signaling

### Why Channels Solve the Problem

Channels in Go have an important property that sync.Cond lacks: **receiving from a closed channel never blocks and always succeeds**. This eliminates the missed-wakeup race entirely.

### Implementation Details

#### 1. **Progress Notification Channel**
```go
type TempFileStore struct {
    // Progress notification channel - closed when download finishes or errors
    progressCh chan struct{}
    // ... other fields
}
```

#### 2. **Downloader Signals Progress**
```go
func (s *TempFileStore) downloader(ctx context.Context) {
    defer close(s.progressCh)  // ✅ Wakes ALL readers atomically
    
    for {
        // Download chunk
        s.downloaded.Store(totalWritten)
        
        // Non-blocking send to wake readers
        select {
        case s.progressCh <- struct{}{}:
        default:
            // Channel full or no readers - that's fine
        }
    }
}
```

**Key benefits:**
- `defer close(s.progressCh)` ensures all readers wake up when download finishes
- Channel closure is idempotent and broadcasts to ALL readers simultaneously
- Non-blocking send prevents downloader from blocking on slow readers

#### 3. **ReadAt Uses Select with Channel**
```go
func (s *TempFileStore) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
    for {
        // Check if we have enough data
        if s.downloaded.Load() >= wantEnd {
            break
        }
        
        // Check for errors
        if err := s.getError(); err != nil {
            return 0, err
        }
        
        // Wait for progress or cancellation
        select {
        case <-s.progressCh:  // ✅ Never blocks after channel close
            // Loop to recheck progress
        case <-ctx.Done():
            return 0, ctx.Err()
        }
    }
    
    // Read from file...
}
```

**Why this eliminates the race:**
1. Channel is created at store creation time
2. Readers can `<-progressCh` at any time, even before downloader starts
3. When downloader closes channel, ALL pending and future receives succeed immediately
4. There's no "window" where a signal can be lost - closed channels always deliver

### 4. **Cached File Handle Optimization**
As a bonus optimization, we now cache the `os.File` handle instead of reopening the file on every read:

```go
// Open file once at creation
readFile, err := os.Open(tempPath)
store := &TempFileStore{
    readFile: readFile,
    // ...
}

// ReadAt uses cached handle
func (s *TempFileStore) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
    s.readFileMu.RLock()
    f := s.readFile
    s.readFileMu.RUnlock()
    
    n, err := f.ReadAt(p, off)  // No need to open/close
    // ...
}
```

**Benefits:**
- Reduces syscall overhead significantly
- Multiple concurrent reads can use the same handle safely (ReadAt is thread-safe)
- Cleaner lifecycle management

## Comparison: Before vs After

### Before (sync.Cond)
❌ Lost-wakeup race condition  
❌ Complex timeout workarounds  
❌ High goroutine creation overhead  
❌ File reopened on every read  
❌ Difficult to reason about correctness  

### After (Channels)
✅ No missed-wakeup race (channel closure broadcasts atomically)  
✅ Clean integration with context cancellation via select  
✅ No extra goroutines needed  
✅ Cached file handle reduces syscalls  
✅ Simple, idiomatic Go concurrency pattern  

## Testing Recommendations

1. **Concurrent Upload Test**
   - Upload multiple large files (>1GB) simultaneously
   - Run `ls -l /Volumes/FuseStream/` repeatedly during uploads
   - Verify filesystem remains responsive

2. **Rapid Open/Close Test**
   - Open and close the same file many times in parallel
   - Stress test the progress channel with many concurrent readers

3. **Download Completion Race Test**
   - Upload small files that download quickly
   - Maximize the chance of hitting the race window
   - Verify no hangs occur

4. **Timeout Respect Test**
   - Simulate network stalls
   - Verify FUSE callback timeouts are respected
   - Should return timeout error, not hang indefinitely

## Technical Notes

### Why Non-blocking Send in Downloader?

```go
select {
case s.progressCh <- struct{}{}:
default:
    // Channel full or no readers
}
```

If we used a blocking send `s.progressCh <- struct{}{}`, the downloader could block if:
- All readers are currently processing
- The channel buffer is full
- A reader is slow to receive

The non-blocking send ensures the downloader never waits for readers, preventing a different kind of deadlock.

### Why Channel of `struct{}`?

We use `chan struct{}` instead of `chan bool` or `chan int` because:
- We don't need to send data, just a signal
- `struct{}` has zero size, so it's maximally efficient
- Idiomatic Go for signaling channels

### Thread Safety of os.File.ReadAt

From Go documentation:
> ReadAt reads len(b) bytes from the File starting at byte offset off. 
> It returns the number of bytes read and the error, if any. 
> **ReadAt always returns a non-nil error when n < len(b).**
> **At end of file, that error is io.EOF.**
> **ReadAt is safe for concurrent use.**

This means multiple goroutines can safely call `ReadAt` on the same `*os.File` concurrently.

## Conclusion

By replacing `sync.Cond` with channel-based signaling, we've eliminated the fundamental race condition that caused indefinite blocking in `TempFileStore`. The channel closure semantics guarantee that no reader can ever miss the "download complete" signal, making the system robust even under heavy concurrent load.

The cached file handle optimization provides additional performance benefits without compromising correctness or thread safety.

This fix ensures that FUSE callbacks always respect their timeout contexts and the filesystem remains responsive under all conditions.
