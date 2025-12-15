# FUSE Deadlock Fix Summary

## Problem Description
The FUSE filesystem was stalling mid-upload with the following symptoms:
- Upload progresses for a bit, then browser stalls
- `ls /Volumes/FuseStream` hangs with no response
- App UI still responsive (main thread OK)
- Last log shows Read operations completing normally, then silence
- Temp file fully downloaded to /tmp, but filesystem becomes unresponsive

## Root Causes Identified

### 1. **Blocking stdout writes in FUSE callbacks**
The standard `log.Printf()` function writes to stdout synchronously, even when using an async file logger. If stdout is redirected or the TTY buffer is full, this CAN block, causing FUSE operations to stall.

**Affected Operations:**
- `Open()`: 8 log.Printf calls
- `Read()`: 2 log.Printf calls  
- `Release()`: 4 log.Printf calls

### 2. **Lock held during file I/O in TempFileStore.ReadAt**
The `ReadAt` method was checking error state while holding `errMu` during the wait loop, creating potential for lock contention.

### 3. **Potential for logging buffer overflow**
The standard async logger has a 1000-message buffer, which could fill up during heavy I/O operations.

## Solutions Implemented

### 1. Non-Blocking FUSE Logger (fuse_logger.go)
Created a dedicated FUSE logger with the following characteristics:

**Key Features:**
- **10,000-message ring buffer** (10x larger than standard logger)
- **Drop-on-full semantics** - NEVER blocks, drops messages instead
- **Async stdout writes** - stdout is written in the background goroutine
- **Async file writes** - all disk I/O happens in background
- **Batched writes** - flushes every 100ms or 100 messages
- **Logs to ~/Library/Logs/FuseStream/fuse.log** (never on FUSE volume)
- **Stall detection** - warns if operation takes > 5 seconds

**API:**
```go
logging.FUSELog(format string, args ...interface{})
logging.FUSETraceNonBlocking(operation, format string, args ...interface{})
```

### 2. Fixed TempFileStore.ReadAt Lock Behavior
**Before:**
```go
s.errMu.RLock()
err := s.err
s.errMu.RUnlock()
```
Called inside the cond wait loop while holding cond.L.

**After:**
- Reads error without holding lock unnecessarily during wait
- Only holds locks briefly to check state
- Never holds locks during file I/O operations

### 3. Replaced log.Printf with logging.FUSELog in FUSE Callbacks

**Affected Functions:**
- `Open()`: All 8 log statements
- `Read()`: All 2 log statements
- `Release()`: All 4 log statements

**Pattern:**
```go
// Before (BLOCKS on stdout)
log.Printf("Open: Backing store needs initialization for %s", sf.FileName)

// After (NEVER blocks)
logging.FUSELog("[Open] Backing store needs initialization for %s", sf.FileName)
```

### 4. Ensured Sleep/AppNap Prevention is Async

**Verified:**
- `enableSleepPrevention()` called with `go` keyword from FUSE callbacks ✓
- `disableSleepPrevention()` called with `go` keyword from FUSE callbacks ✓
- Native code (Objective-C) does NOT use `dispatch_sync` ✓
- `NSProcessInfo` and `IOPMAssertion` APIs are thread-safe ✓

## Files Modified

### New Files:
1. **internal/logging/fuse_logger.go** - Non-blocking FUSE logger implementation

### Modified Files:
1. **internal/logging/fuse_trace.go** - Updated to use non-blocking logger
2. **internal/fetcher/store_tempfile.go** - Fixed ReadAt lock behavior
3. **internal/fs/fs_fuse.go** - Multiple changes:
   - Initialize FUSE logger at filesystem start
   - Close FUSE logger at filesystem stop
   - Replace all log.Printf in FUSE callbacks with logging.FUSELog

## Critical Design Principles Applied

### 1. NEVER Block in FUSE Callbacks
- **No synchronous stdout writes** - stdout can block on TTY
- **No synchronous disk I/O** - disk can stall on slow media
- **No dispatch_sync to main thread** - main thread must be free (Darwin)
- **No holding locks during I/O** - prevents deadlocks

### 2. Drop Messages Rather Than Block
The FUSE logger drops messages when the buffer is full rather than blocking. This is correct behavior because:
- FUSE operations MUST complete in reasonable time
- A stalled FUSE operation hangs the entire filesystem
- Lost log messages are better than a hung filesystem
- Messages are still written to stdout, just asynchronously

### 3. Lock Hierarchy
```
FUSE callback
  ├─> fs.mu (brief read lock)
  ├─> sf.storeMu (brief lock, never held during I/O)
  └─> fhMu (brief lock for handle management)
  
Never hold locks while:
  - Reading from disk
  - Writing to disk  
  - Making network calls
  - Writing to stdout
  - Calling into AppKit/NSProcessInfo
```

### 4. Async Everything
```
FUSE Callback (must return quickly)
  └─> Queue log message (non-blocking)
        └─> Background goroutine
              └─> Batch write to file
              └─> Batch write to stdout
```

## Testing Recommendations

### 1. Verify No Stalls During Upload
```bash
# Terminal 1: Monitor FUSE logs
tail -f ~/Library/Logs/FuseStream/fuse.log

# Terminal 2: Test with large file
cd /Volumes/FuseStream/Staged/[file]
cat large-file.mp4 > /dev/null

# Terminal 3: Verify filesystem responds
while true; do
  ls -l /Volumes/FuseStream
  sleep 1
done
```

### 2. Check for Warnings
Look for these in the FUSE log:
```
[FUSE] WARNING: <operation> took 5s (potential stall/deadlock)
```

### 3. Verify Logger Statistics
On app shutdown, check:
```
[FUSE] Logger closed - written: <count>, dropped: <count> messages
```
- If `dropped` is 0, buffer is sized correctly
- If `dropped` is non-zero but filesystem works, that's OK (better than blocking)
- If `dropped` is high, consider increasing buffer size

### 4. Stress Test
```bash
# Multiple concurrent reads
for i in {1..10}; do
  (cat /Volumes/FuseStream/Staged/*/file.mp4 > /dev/null) &
done

# While doing ls in another terminal
while true; do ls -l /Volumes/FuseStream; done
```

## Performance Characteristics

### Before Fix:
- FUSE operations could block on stdout writes
- Lock contention during ReadAt
- Could hang indefinitely on stalled stdout

### After Fix:
- FUSE operations NEVER block on logging
- Minimal lock hold times
- Guaranteed forward progress
- Small memory overhead: ~1MB for ring buffer

## Rollback Instructions

If issues arise:
1. Revert to commit before these changes
2. Or disable FUSE logger by commenting out `InitFUSELogger()` in `fs_fuse.go`
3. Standard logging will still work (but may block)

## Future Improvements

1. **Configurable buffer size** - allow tuning based on workload
2. **Metrics collection** - track dropped messages, operation latencies
3. **Log rotation** - currently unlimited growth
4. **Compression** - compress old logs to save space
5. **Remote logging** - send critical events to analytics service

## References

- [FUSE Best Practices](https://github.com/libfuse/libfuse/wiki/FuseBestPractices)
- [macOS FUSE Debugging](https://github.com/osxfuse/osxfuse/wiki/Debugging)
- [Go sync.Cond Best Practices](https://golang.org/pkg/sync/#Cond)
