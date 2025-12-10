# FSKit/macFUSE Debugging Guide

This document describes the debugging tests and tools created to diagnose FSKit mounting issues on macOS.

## Problem Description

When trying to run the application on macOS with FSKit backend, mounting fails with:

```
umount: /Users/ec2-user/FuseStream: not currently mounted
2025/12/08 08:51:59 [fs] Failed to mount filesystem at /Users/ec2-user/FuseStream
2025/12/08 08:51:59 [fs] ERROR: Mount failed. This may indicate:
2025/12/08 08:51:59 [fs]   - macFUSE is not installed or is too old (requires macFUSE ≥5)
2025/12/08 08:51:59 [fs]   - macOS version is too old (requires macOS ≥15.4 for FSKit)
2025/12/08 08:51:59 [fs]   - FSKit backend is not available (try 'backend=fskit' option)
2025/12/08 08:51:59 [fs]   - Mountpoint is already in use or inaccessible
```

## Debugging Tools

### 1. Automated Test Suite

**Location**: `internal/fs/fskit_debug_test.go`

This comprehensive test suite includes 7 tests with increasing complexity:

- **Test 1: macFUSE Installation Check** - Verifies macFUSE is installed and checks version
- **Test 2: Basic FUSE Mount** - Tests mounting without FSKit option
- **Test 3: FSKit Backend Mount** - Tests mounting with `backend=fskit` option
- **Test 4: Different Option Formats** - Tests various ways of passing FSKit options
- **Test 5: Mount Recovery** - Tests the mount recovery mechanism
- **Test 6: Mount Command Details** - Examines system configuration
- **Test 7: Full Integration** - Tests the exact mount sequence used in the application

**Run all tests:**
```bash
go test -v -tags fuse -run TestFSKit ./internal/fs
```

**Run specific test:**
```bash
go test -v -tags fuse -run TestFSKit_BasicFUSEMount ./internal/fs
```

### 2. GitHub Actions Workflow

**Location**: `.github/workflows/fskit-debug.yml`

Automatically runs all debug tests on the macOS self-hosted runner with macFUSE installed.

**Trigger:**
- Automatically on push to `feature/m3-macos-fskit` branch
- Manually via workflow_dispatch

**Features:**
- Runs all 7 tests sequentially
- Collects detailed system information
- Uploads test logs as artifacts
- Cleans up stale mounts after tests

**Manual trigger:**
```bash
# Via GitHub UI: Actions -> FSKit Debug Tests -> Run workflow

# Or via gh CLI:
gh workflow run fskit-debug.yml
```

### 3. Standalone Debug Script

**Location**: `scripts/debug-fskit.sh`

A comprehensive bash script that checks the system for FSKit compatibility.

**Run:**
```bash
cd /path/to/fuse-stream-mvp
./scripts/debug-fskit.sh
```

**Checks:**
- macOS version (requires 15.4+ for FSKit)
- macFUSE installation and version
- Kernel extensions (should not be loaded for FSKit)
- Current FUSE mounts
- macFUSE preferences
- Go environment
- Provides recommendations based on findings

### 4. Standalone Test Program

**Location**: `cmd/fskit-test/main.go`

A minimal FUSE filesystem that can be used to test mounting in isolation.

**Build:**
```bash
go build -tags fuse -o fskit-test ./cmd/fskit-test
```

**Run with FSKit:**
```bash
./fskit-test -mountpoint /tmp/test-mount -fskit -v
```

**Run without FSKit (legacy FUSE):**
```bash
./fskit-test -mountpoint /tmp/test-mount -no-fskit -v
```

**Features:**
- Simple read-only filesystem with one test file
- Tests basic operations (readdir, stat, read)
- Can enable/disable FSKit backend
- Verbose logging
- Clean unmount on Ctrl+C

## Debugging Strategy

Follow this sequence to isolate the issue:

### Step 1: Check System Requirements

Run the debug script:
```bash
./scripts/debug-fskit.sh
```

Expected output should show:
- ✅ macOS version 15.4+ (or confirm it's older)
- ✅ macFUSE installed
- ✅ No kernel extensions loaded (for FSKit)

### Step 2: Run Basic Tests

Run tests 1, 2, and 6 to verify the environment:
```bash
go test -v -tags fuse -run "TestFSKit_(MacFUSEInstallation|BasicFUSEMount|MountCommandDetails)" ./internal/fs
```

These tests will show:
- Whether macFUSE is properly installed
- Whether basic FUSE mounting (without FSKit) works
- System configuration details

### Step 3: Test FSKit Specifically

Run test 3 to check if FSKit backend works:
```bash
go test -v -tags fuse -run TestFSKit_FSKitBackendMount ./internal/fs
```

If this fails, FSKit backend is not available or not working.

### Step 4: Test Different Options

Run test 4 to try alternative mount option formats:
```bash
go test -v -tags fuse -run TestFSKit_DifferentOptionFormats ./internal/fs
```

This will try:
- `backend=fskit`
- `modules=fskit`
- Combined options with commas
- Different daemon timeout settings

### Step 5: Test Standalone Program

Build and run the standalone test program:
```bash
go build -tags fuse -o fskit-test ./cmd/fskit-test

# Try with FSKit
./fskit-test -mountpoint /tmp/test1 -fskit -v

# Try without FSKit
./fskit-test -mountpoint /tmp/test2 -no-fskit -v
```

This isolates the mounting issue from the rest of the application.

### Step 6: Check cgofuse Compatibility

The issue might be that cgofuse doesn't properly pass the `backend=fskit` option to macFUSE.

Check cgofuse source and documentation:
- https://github.com/winfsp/cgofuse
- Look for FSKit backend support
- Check if there's a specific API for FSKit

## Common Issues and Solutions

### Issue 1: macOS Version Too Old

**Symptom:** macOS version < 15.4

**Solution:** 
- FSKit requires macOS 15.4 or later
- Either upgrade macOS or remove the `backend=fskit` option to use legacy FUSE

### Issue 2: macFUSE Not Installed

**Symptom:** `mount_macfuse` not found, no `/Library/Filesystems/macfuse.fs`

**Solution:**
- Install macFUSE from https://macfuse.io/
- Requires macFUSE 5.0+ for FSKit support
- May need to restart after installation

### Issue 3: FSKit Backend Not Recognized

**Symptom:** Mount fails even with correct macOS version and macFUSE installed

**Possible causes:**
1. cgofuse doesn't support `backend=fskit` option
2. Option format is incorrect
3. macFUSE version doesn't support FSKit
4. FSKit backend not enabled in macFUSE installation

**Solutions:**
- Check cgofuse version and documentation
- Try alternative option formats (test 4)
- Check macFUSE version: needs 5.0+ with FSKit support
- Try mounting without FSKit option as fallback

### Issue 4: Permission Issues

**Symptom:** Mount fails with permission error

**Solution:**
- **For `/Volumes` mountpoints**: DO NOT create the directory manually - the macFUSE mount helper creates it
- `/Volumes` is root-owned; applications cannot create directories there without root privileges
- The `mount_macfuse` helper (setuid root) creates the mountpoint automatically during mounting
- **For non-`/Volumes` mountpoints** (testing): Check directory permissions and ensure write access to parent
- Check macFUSE preferences: `defaults read /Library/Preferences/com.github.macfuse`

### Issue 5: Stale Mounts

**Symptom:** "mountpoint is already in use"

**Solution:**
- The recovery mechanism should handle this
- Manual cleanup: `diskutil unmount force /path/to/mountpoint`
- Or: `umount -f /path/to/mountpoint`

## Expected Test Results

### If FSKit is Working:

```
Test 1: ✅ macFUSE installed, version 5.x+
Test 2: ✅ Basic FUSE mount succeeds
Test 3: ✅ FSKit backend mount succeeds
Test 4: ✅ At least one option format succeeds
Test 7: ✅ Full integration succeeds
```

### If FSKit is NOT Available:

```
Test 1: ✅ macFUSE installed OR ❌ macFUSE not found
Test 2: ✅ Basic FUSE mount succeeds OR ❌ Basic mount fails
Test 3: ❌ FSKit backend mount fails
Test 4: ❌ All FSKit option formats fail
Test 7: ❌ Full integration fails
```

If Test 2 succeeds but Test 3 fails, it indicates:
- Basic FUSE works
- FSKit specifically is not available
- Should consider falling back to non-FSKit mounting

## Fallback Strategy

If FSKit doesn't work, the application should fall back to regular FUSE mounting:

```go
mountOpts := []string{
    "-o", "ro",
    "-o", "fsname=fusestream",
    "-o", "local",
    "-o", "volname=FuseStream",
}

// Only add FSKit if explicitly requested and system supports it
if shouldUseFSKit() {
    mountOpts = append(mountOpts, "-o", "backend=fskit")
}
```

## Next Steps

Based on test results:

1. **If all tests pass on CI but fail locally:**
   - Compare system configurations
   - Check macFUSE versions
   - Check macOS versions

2. **If FSKit tests fail but basic FUSE works:**
   - Implement fallback to non-FSKit mounting
   - Make FSKit optional via config flag
   - Update documentation to reflect FSKit is optional

3. **If basic FUSE tests fail:**
   - macFUSE installation issue
   - System compatibility issue
   - cgofuse compatibility issue

4. **If cgofuse doesn't support FSKit:**
   - Consider alternative FUSE library
   - Or implement direct mount syscalls
   - Or remove FSKit requirement and use legacy FUSE

## References

- [macFUSE GitHub](https://github.com/macfuse/macfuse)
- [macFUSE Getting Started](https://github.com/macfuse/macfuse/wiki/Getting-Started)
- [macFUSE Downloads](https://macfuse.io/)
- [cgofuse GitHub](https://github.com/winfsp/cgofuse)
- [Apple FSKit Documentation](https://developer.apple.com/documentation/filesystems)
