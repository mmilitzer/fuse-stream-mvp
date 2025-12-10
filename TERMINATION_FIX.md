# Termination Fix Documentation

## Problem
The app could not be closed or force quit after the latest commit. This was caused by:

1. **Deadlock in OnBeforeClose**: The Wails `OnBeforeClose` handler called `dialog.ConfirmQuit()` which used `dispatch_sync` on the main thread. Since Wails' `OnBeforeClose` already runs on the main thread, this caused a deadlock.

2. **Improper NSActivity options**: The app was using `NSActivitySuddenTerminationDisabled` and `NSActivityAutomaticTerminationDisabled` flags (implicitly), which prevented proper app termination.

3. **No proper termination delegate**: There was no `NSApplicationDelegate` implementing `applicationShouldTerminate:` which is the correct way to handle termination on macOS.

4. **Synchronous unmount**: The unmount operation was not implemented with timeout and force unmount fallback.

## Solution

### 1. Fixed NSActivity Options (`internal/appnap/appnap_darwin.go`)
- Explicitly documented that we DO NOT use `NSActivitySuddenTerminationDisabled` or `NSActivityAutomaticTerminationDisabled`
- Only use:
  - `NSActivityUserInitiated` - Prevents App Nap
  - `NSActivityLatencyCritical` - Highest priority
  - `NSActivityIdleSystemSleepDisabled` - Prevents idle sleep

### 2. Created macOS App Delegate (`internal/appdelegate/`)
Implemented a proper `NSApplicationDelegate` with:

- **`applicationShouldTerminate:`**: 
  - Checks for active uploads using Go callback
  - Shows confirmation dialog if uploads are active (runs on main thread properly)
  - Returns `NSTerminateCancel` if user clicks Cancel
  - Returns `NSTerminateLater` and starts async unmount if user clicks Quit or no uploads
  - Calls `[NSApp replyToApplicationShouldTerminate:YES]` when unmount completes

- **`applicationShouldTerminateAfterLastWindowClosed:`**: Returns `YES` so app quits when window closes

- **Async unmount with timeout**:
  - 3-second timeout for normal unmount
  - Falls back to `diskutil unmount force` if timeout exceeded
  - Never blocks the main thread

### 3. Improved Filesystem Unmount (`internal/fs/fs_fuse.go`)
Added:
- `StopAsync()`: Returns a channel for async unmount
- `ForceUnmount()`: Uses `diskutil unmount force` and `umount -f` fallback
- Better logging for unmount operations

### 4. Fixed Signal Handling (`internal/signals/`)
Created proper signal handlers:
- **SIGTERM**: Dispatches to main thread and calls `[NSApp terminate:]` (proper macOS flow)
- **SIGINT** (Ctrl+C): Calls context cancel for graceful shutdown
- Never blocks in signal handler
- Never uses `signal(SIGTERM, SIG_IGN)`

### 5. Updated Main Application (`main.go`)
- **Removed `OnBeforeClose` handler**: This was causing the deadlock
- **Installed app delegate**: Handles all termination events properly
- **Setup signal handlers**: Proper SIGTERM/SIGINT handling
- **Removed dialog package**: No longer needed since app delegate shows confirmation

### 6. Updated Daemon (`internal/daemon/daemon.go`)
- Added `UnmountFS()`: Exposes filesystem unmount for app delegate

## Changes Summary

### New Files
- `internal/appdelegate/appdelegate.go` - Base types and interface
- `internal/appdelegate/appdelegate_darwin.go` - macOS NSApplicationDelegate implementation
- `internal/appdelegate/appdelegate_other.go` - No-op for non-macOS platforms
- `internal/signals/signals_darwin.go` - macOS signal handling
- `internal/signals/signals_other.go` - Non-macOS signal handling

### Modified Files
- `main.go` - Removed OnBeforeClose, integrated app delegate and signal handlers
- `internal/appnap/appnap_darwin.go` - Fixed NSActivity options
- `internal/fs/fs_fuse.go` - Added StopAsync() and ForceUnmount()
- `internal/daemon/daemon.go` - Added UnmountFS()
- `ui/app_common.go` - Added GetAppInstance() for app delegate

### Deleted Files
- `internal/dialog/` - No longer needed (app delegate shows confirmation)

## Testing Checklist

### Manual Testing Required
- [ ] **Quit with Cmd+Q**: Should show confirmation if uploads active, quit cleanly if not
- [ ] **Quit menu**: Should work same as Cmd+Q
- [ ] **Close window**: Should trigger quit (applicationShouldTerminateAfterLastWindowClosed)
- [ ] **Force Quit (Cmd+Opt+Esc)**: Should now work properly
- [ ] **SIGTERM**: `kill -TERM <pid>` should trigger proper termination flow
- [ ] **SIGINT**: Ctrl+C in terminal should trigger graceful shutdown
- [ ] **Active uploads**: Confirmation dialog should appear and work correctly
- [ ] **Unmount timeout**: If filesystem hangs, should force unmount after 3 seconds

### Expected Behavior
1. **Normal quit (no uploads)**: 
   - App quits immediately
   - Filesystem unmounts cleanly
   - No deadlocks

2. **Quit with uploads**:
   - Confirmation dialog appears
   - If Cancel: app stays open
   - If Quit Anyway: async unmount starts, app quits when done (max 3s)

3. **Force Quit**:
   - App terminates immediately
   - macOS handles cleanup
   - Stale mount recovered on next launch

4. **Signal handling**:
   - SIGTERM triggers proper NSApp termination flow
   - SIGINT triggers graceful shutdown
   - Never blocks or hangs

## Technical Notes

### Why This Approach?
1. **No dispatch_sync on main thread**: App delegate methods already run on main thread
2. **Proper termination flow**: Using NSApplicationDelegate is the macOS-native way
3. **Async unmount**: Never blocks the main thread or UI
4. **Timeout + force**: Ensures app can always quit, even if filesystem hangs
5. **No termination disable flags**: Allows proper quit, Force Quit, and SIGTERM

### Architecture
```
User Action (Cmd+Q, window close, SIGTERM)
    ↓
NSApplicationDelegate.applicationShouldTerminate:
    ↓
Check active uploads (Go callback)
    ↓
Show confirmation if needed (on main thread, no dispatch_sync)
    ↓
Start async unmount (Go goroutine)
    ↓
Unmount with 3s timeout
    ↓
Force unmount if timeout exceeded
    ↓
Call [NSApp replyToApplicationShouldTerminate:YES]
    ↓
App terminates cleanly
```

## Related Issues
Fixes: App can't be closed anymore (even Force Quit)
