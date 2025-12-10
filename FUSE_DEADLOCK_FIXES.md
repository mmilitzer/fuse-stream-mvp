# FUSE Deadlock Fixes

## Problem Summary

The FUSE filesystem was stalling mid-upload with the following symptom:
- Upload progresses for a bit, then browser stalls
- `ls /Volumes/FuseStream` hangs
- App UI remains responsive
- Last log shows Read operations, then silence

This indicated a deadlock or blocking call inside FUSE callbacks.

## Root Causes Identified

### 1. **Blocking Logger** 
The async logger used a 10ms timeout in the `Write()` function, which could still block briefly in FUSE callbacks.

### 2. **Lock Held During I/O in Open()**
The `Open()` callback held `sf.storeMu` while:
- Calling `fs.client.BuildTempURL()` (network I/O)
- Calling `fetcher.NewBackingStore()` (potential disk I/O)

This could cause the FUSE callback to block indefinitely during slow network operations.

### 3. **Synchronous Sleep Prevention Calls**
`enableSleepPrevention()` and `disableSleepPrevention()` were called synchronously from FUSE callbacks, making IOKit system calls that could block.

### 4. **Excessive Logging in Hot Paths**
The `Read()` callback had 4-5 log statements per read operation, causing unnecessary overhead.

### 5. **No Entry/Exit Tracing**
Without entry/exit timestamps, it was impossible to diagnose which FUSE operation was stalling.

## Fixes Applied

### 1. Truly Non-Blocking Logger (logging/logger.go)

**Before:**
```go
select {
case aw.logger.logChan <- msg:
    // Message queued successfully
case <-time.After(10 * time.Millisecond):
    // Channel full - drop message
}
```

**After:**
```go
select {
case aw.logger.logChan <- msg:
    // Message queued successfully
default:
    // Channel full - drop message immediately
    // CRITICAL: prevents ANY blocking in FUSE callbacks
}
```

The `default` case ensures **zero blocking** - if the channel is full, the message is immediately dropped (it's already written to stdout, so not lost).

### 2. FUSE Operation Tracing (logging/fuse_trace.go)

Created new tracing helpers:
```go
defer FUSETrace("Read", "fh=%d off=%d len=%d", fh, offset, len(buff))()
```

This logs:
```
[FUSE] Read ENTER fh=123 off=4096 len=131072
[FUSE] Read EXIT  dur=2.3ms
```

Added to all FUSE operations: Init, Destroy, Statfs, Access, Getattr, Readdir, Open, Read, Release.

### 3. Fixed Open() - No Locks During I/O (fs_fuse.go)

**Before:**
```go
sf.storeMu.Lock()
if sf.store == nil {
    tempURL, err := fs.client.BuildTempURL(...)  // NETWORK I/O - BAD!
    store, err := fetcher.NewBackingStore(...)   // DISK I/O - BAD!
    sf.store = store
}
sf.storeMu.Unlock()
```

**After:**
```go
// Check if initialization needed (quick lock)
sf.storeMu.Lock()
needsInit := (sf.store == nil)
sf.storeMu.Unlock()

// If needed, do I/O WITHOUT holding lock
if needsInit {
    tempURL, err := fs.client.BuildTempURL(...)  // Network I/O - no locks held
    store, err := fetcher.NewBackingStore(...)   // Disk I/O - no locks held
    
    // Atomically update store (quick lock)
    sf.storeMu.Lock()
    if sf.store == nil {
        sf.store = store  // We won the race
    } else {
        store.Close()     // Someone else won, discard ours
    }
    sf.storeMu.Unlock()
}
```

This uses a **check-unlock-perform-check-again pattern** to avoid holding locks during I/O.

### 4. Async Sleep Prevention Calls (fs_fuse.go)

**Before:**
```go
if isFirstHandle {
    fs.enableSleepPrevention()  // Blocks on IOKit calls
}
```

**After:**
```go
if isFirstHandle {
    go fs.enableSleepPrevention()  // Async - never blocks FUSE callback
}
```

Same for `disableSleepPrevention()` in `Release()`.

### 5. Reduced Logging in Read() (fs_fuse.go)

**Before:**
```go
log.Printf("Read: %s reading %d bytes at offset %d (fh=%d)", ...)
n, err := store.ReadAt(...)
log.Printf("Read: %s successfully read %d bytes at offset %d", ...)
```

**After:**
```go
defer logging.FUSETrace("Read", "fh=%d off=%d len=%d", fh, ofst, len(buff))()
n, err := store.ReadAt(...)  // Only log errors
```

The tracing already logs entry/exit, so we don't need per-operation logs for success cases.

### 6. Removed Unnecessary Lock in Read() (fs_fuse.go)

**Before:**
```go
fs.fhMu.RLock()
store, exists := fs.fhToStore[fh]
filePath := fs.fhToPath[fh]  // Not needed
fs.fhMu.RUnlock()
```

**After:**
```go
fs.fhMu.RLock()
store, exists := fs.fhToStore[fh]
fs.fhMu.RUnlock()
// Minimize time holding lock
```

## Verification Checklist

- [x] **Truly non-blocking logger**: `select` with `default` case instead of timeout
- [x] **No locks held during I/O**: Open() refactored to check-unlock-perform-check pattern
- [x] **Async sleep prevention**: All sleep calls moved to goroutines
- [x] **Entry/exit tracing**: All FUSE operations have FUSETrace wrappers
- [x] **Reduced logging**: Read() path simplified
- [x] **No dispatch_sync in FUSE paths**: Verified (only in drag code, which is UI-triggered)
- [x] **Logs in ~/Library/Logs**: Already configured correctly
- [x] **Fetcher doesn't hold locks during I/O**: Verified TempFileStore is correct

## Deadlock Prevention Rules

Going forward, all FUSE callbacks MUST follow these rules:

### ✅ DO:
1. Use `defer logging.FUSETrace(...)()` at function start
2. Release all locks **before** any I/O operation (network, disk, system calls)
3. Use atomic operations or brief critical sections
4. Call into UI/sleep/power management **asynchronously** (goroutines)
5. Use non-blocking logger (automatic via `log.Printf`)

### ❌ DON'T:
1. Hold any mutex while doing I/O (network, disk, file operations)
2. Make synchronous calls to main thread (`dispatch_sync`)
3. Call UI frameworks (Wails runtime, NSWorkspace, etc.) directly
4. Use blocking channels or `select` with timeout in callbacks
5. Log excessively in hot paths (Read, Getattr)

## Testing

To verify the fixes work:

1. **Monitor trace logs**: Check for entry/exit timestamps in all FUSE operations
   ```bash
   tail -f ~/Library/Logs/FuseStream/fusestream.log | grep FUSE
   ```

2. **Look for stalls**: If a FUSE operation shows ENTER but no EXIT, that's the deadlock point

3. **Verify no blocking**: All FUSE operations should complete in < 100ms (except Read, which waits for data)

4. **Check sleep logs**: Sleep prevention should be enabled/disabled asynchronously
   ```bash
   grep -i "sleep prevention" ~/Library/Logs/FuseStream/fusestream.log
   ```

## Related Files Modified

- `internal/logging/logger.go` - Made truly non-blocking
- `internal/logging/fuse_trace.go` - **NEW**: Entry/exit tracing
- `internal/fs/fs_fuse.go` - Fixed all blocking issues
  - Open(): No locks during I/O
  - Read(): Simplified logging
  - Release(): Async sleep prevention
  - All callbacks: Added FUSETrace wrappers

## Performance Impact

**Before:**
- Read operations: 4-5 log writes per read (blocking risk)
- Open operations: Could stall indefinitely on slow network
- Potential deadlocks when staging new files during uploads

**After:**
- Read operations: 2 log writes (enter/exit) - non-blocking
- Open operations: Lock-free I/O, no blocking
- No deadlock risk - all I/O done without holding locks

## Future Improvements

1. Consider adding timeout tracking in FUSETrace to auto-log operations > 1s
2. Add metrics for FUSE operation durations
3. Consider using a ring buffer for trace logs (structured logging)
4. Add automatic deadlock detection (goroutine monitoring)
