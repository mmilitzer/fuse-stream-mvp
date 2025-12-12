# FUSE Release Non-Blocking Fix

## Problem

Uploads were still hanging randomly and the filesystem was becoming unresponsive, even after previous fixes. Investigation revealed:

1. **All Read() operations were exiting correctly** - verified by matching "Read ENTER" and "Read EXIT" logs
2. **The blocking was happening in a different FUSE callback** - likely Release()
3. **Root cause**: Release() was calling `store.Close()` while holding `fs.mu.Lock`, causing:
   - The Close() operation could block waiting for background goroutines (downloader) to finish
   - Other FUSE callbacks (Getattr, Readdir, Access) that need `fs.mu.RLock` would queue behind the writer
   - This exhausted the FSKit worker pool, causing complete filesystem freeze

## Solution

### 1. Make Release() Non-Blocking (CRITICAL FIX)

**File**: `internal/fs/fs_fuse.go`

**Changes**:
- Release() now detaches the store from the registry WITHOUT holding `fs.mu.Lock` across `Close()`
- Pattern:
  ```go
  fs.mu.Lock()
  sff.storeMu.Lock()
  storeToClose := sff.store
  sff.store = nil
  delete(fs.stagedFiles, stagedID)
  sff.storeMu.Unlock()
  fs.mu.Unlock()
  
  // Close in background with timeout
  go func() {
      ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
      defer cancel()
      _ = storeToClose.CloseWithContext(ctx)
  }()
  ```
- **Key principle**: Drop all locks BEFORE doing any potentially blocking work
- **Result**: Release() returns immediately to FSKit, background goroutine handles cleanup

### 2. Add CloseWithContext() to BackingStore Interface

**File**: `internal/fetcher/store.go`

**Changes**:
- Added `CloseWithContext(ctx context.Context) error` method to BackingStore interface
- Makes Close() cancellable with timeout support

**File**: `internal/fetcher/store_tempfile.go`

**Changes**:
- Implemented `CloseWithContext()` that waits for downloader with timeout
- If context times out, logs warning but continues cleanup (best effort)
- Regular `Close()` now calls `CloseWithContext(context.Background())`

**File**: `internal/fetcher/store_rangelru.go`

**Changes**:
- Implemented `CloseWithContext()` (same as Close since no blocking operations)

### 3. Instrument ALL FUSE Callbacks with Duration Logging

**File**: `internal/fs/watchdog.go`

**Changes**:
- Enhanced `enter()` to log both ENTER and EXIT with duration
- Added per-opcode in-flight counters (map[string]int)
- Logs operation counts when watchdog detects stalls
- Example output:
  ```
  [FUSE] Open ENTER (id=123)
  [FUSE] Open EXIT (id=123, elapsed=45ms)
  ```

**File**: `internal/fs/fs_fuse.go`

**Changes**:
- Added `defer fs.watchdog.enter("Access")()` to Access callback
- All callbacks now instrumented: Open, Read, Release, Getattr, Readdir, Access

### 4. Fix Readdir to Respect fill() Return Value

**File**: `internal/fs/fs_fuse.go`

**Changes**:
- Changed all `fill()` calls to check return value
- If `fill()` returns false (buffer full), immediately return 0
- Prevents noisy churn from repeatedly calling fill() on full buffer

### 5. Add Debug Configuration Options

**File**: `pkg/config/config.go`

**Changes**:
- Added `DebugSkipEviction bool` - skip eviction in Release to isolate bugs
- Added `DebugFSKitSingleThread bool` - use single-threaded FUSE mode for debugging

**File**: `internal/fs/fs_fuse.go`

**Changes**:
- If `DebugSkipEviction` is true, Release() skips eviction entirely
- If `DebugFSKitSingleThread` is true, adds `-s` mount option for single-threaded mode
- Single-threaded mode simplifies debugging by eliminating concurrency

## Technical Details

### Lock Ordering (Never Hold Locks Across I/O)

**Before (BAD)**:
```go
fs.mu.Lock()
store.Close()  // BLOCKS while holding lock!
fs.mu.Unlock()
```

**After (GOOD)**:
```go
fs.mu.Lock()
storeToClose := sff.store
sff.store = nil
fs.mu.Unlock()

// Do I/O without holding any locks
go func() {
    storeToClose.CloseWithContext(ctx)
}()
```

### FSKit Worker Pool Starvation

**How it happens**:
1. Release() acquires `fs.mu.Lock` and calls `store.Close()`
2. Close() blocks waiting for downloader goroutine (could take seconds)
3. Meanwhile, other callbacks (Getattr, Readdir) need `fs.mu.RLock`
4. They queue behind the Release() that holds the write lock
5. FSKit has a bounded worker pool (e.g., 16 threads)
6. All workers become blocked waiting for the lock
7. Filesystem becomes completely unresponsive (hangs)

**How we fixed it**:
1. Release() detaches the store and releases lock IMMEDIATELY
2. Close() happens in background goroutine (doesn't block FSKit worker)
3. Even if Close() takes 10 seconds, it doesn't matter - FSKit worker is free
4. Other callbacks can acquire `fs.mu.RLock` immediately
5. Filesystem remains responsive

### CloseWithContext() Benefits

- **Timeout protection**: Close() can't block indefinitely
- **Context cancellation**: Can be cancelled if needed
- **Best-effort cleanup**: Attempts cleanup even on timeout
- **Logging**: Reports if cleanup took too long

## Debugging Guide

### If Hangs Persist

1. **Enable skip eviction**:
   ```toml
   # In ~/.fuse-stream-mvp/config.toml
   debug_skip_eviction = true
   ```
   - If hangs stop, the bug is definitely in eviction/close path
   - If hangs continue, the bug is elsewhere (Open, Getattr, etc.)

2. **Enable single-threaded mode**:
   ```toml
   # In ~/.fuse-stream-mvp/config.toml
   debug_fskit_single_thread = true
   ```
   - Eliminates concurrency from the equation
   - First callback that blocks will show up clearly in logs
   - If bug disappears, it's a concurrency issue

3. **Check watchdog logs**:
   - Look for stalled operations in `~/Library/Logs/FuseStream/stacks-*.log`
   - Check which operation type has highest in-flight count
   - Example:
     ```
     [watchdog] In-flight operation counts by type:
       - Release: 8
       - Getattr: 12
     ```
   - High Getattr count suggests Getattr is blocking waiting for Release

4. **Check ENTER/EXIT logs**:
   - Every operation logs ENTER and EXIT with ID
   - Find operations that have ENTER but no EXIT
   - Example:
     ```
     [FUSE] Release ENTER (id=456)
     [FUSE] Getattr ENTER (id=457)
     [FUSE] Getattr ENTER (id=458)
     # No EXIT for 456 - Release is stuck!
     ```

## Performance Impact

- **Watchdog overhead**: Minimal (2-second check interval, only logs on stalls)
- **ENTER/EXIT logging**: Small overhead per operation (timestamp + log)
- **Background Close()**: Better throughput (Release doesn't block FSKit worker)
- **No locks across I/O**: Reduced lock contention, better concurrency

## Testing Recommendations

### Manual Testing
1. Upload large file (>1GB) and verify completion
2. Run `ls -l /Volumes/FuseStream/` during upload (should not hang)
3. Upload multiple files concurrently
4. Check logs for "Release ENTER/EXIT" matching

### Debug Mode Testing
1. Test with `debug_skip_eviction = true` - verify no hangs
2. Test with `debug_fskit_single_thread = true` - verify operations complete
3. Test with both flags enabled

### Load Testing
1. Upload 5-10 files simultaneously
2. Repeatedly run `ls -l` during uploads
3. Monitor watchdog logs for any stalls

## Key Principles Applied

1. **Never hold locks across I/O** - All file/network operations happen with locks released
2. **Fire-and-forget cleanup** - Background goroutines handle slow operations
3. **Timeout everything** - CloseWithContext ensures no indefinite blocking
4. **Observable behavior** - ENTER/EXIT logs and watchdog show what's happening
5. **Debug support** - Config flags allow isolating specific code paths

## Expected Behavior After Fix

✅ **Uploads complete reliably** - No random stalls  
✅ **ls always responsive** - Returns immediately even during uploads  
✅ **No FSKit worker pool exhaustion** - Release() doesn't block workers  
✅ **Clear diagnostics** - ENTER/EXIT logs show exact operation timings  
✅ **Automatic stack dumps** - Watchdog catches any remaining issues  

## Co-authored-by
openhands <openhands@all-hands.dev>
