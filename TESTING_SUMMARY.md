# FSKit Debugging Tests - Quick Start

## What Was Created

A comprehensive debugging suite to diagnose the FSKit mounting failure on macOS. The tests systematically check each component with increasing complexity to pinpoint the exact issue.

## How to Use

### Option 1: Automated CI Tests (Recommended)

The GitHub Actions workflow will run automatically when you push to the `feature/m3-macos-fskit` branch.

**View Results:**
1. Go to: https://github.com/mmilitzer/fuse-stream-mvp/actions
2. Look for "FSKit Debug Tests" workflow
3. Click on the latest run
4. Check each test's output
5. Download "fskit-debug-logs" artifact for detailed logs

**Manual Trigger:**
```bash
gh workflow run fskit-debug.yml
```

### Option 2: Run Tests Locally on macOS

**Prerequisites:**
- macOS machine with macFUSE installed
- Go 1.22+
- Terminal access

**Run all tests:**
```bash
cd /path/to/fuse-stream-mvp
go test -v -tags fuse -run TestFSKit ./internal/fs
```

**Run specific test:**
```bash
# Test if macFUSE is installed
go test -v -tags fuse -run TestFSKit_MacFUSEInstallation ./internal/fs

# Test basic FUSE mounting (without FSKit)
go test -v -tags fuse -run TestFSKit_BasicFUSEMount ./internal/fs

# Test FSKit backend specifically
go test -v -tags fuse -run TestFSKit_FSKitBackendMount ./internal/fs

# Test full integration
go test -v -tags fuse -run TestFSKit_FullIntegration ./internal/fs
```

### Option 3: Run Debug Script

**Quick system check:**
```bash
cd /path/to/fuse-stream-mvp
./scripts/debug-fskit.sh
```

This will show:
- macOS version and FSKit compatibility
- macFUSE installation status
- Current FUSE mounts
- Recommendations

### Option 4: Standalone Test Program

**Build:**
```bash
go build -tags fuse -o fskit-test ./cmd/fskit-test
```

**Test with FSKit:**
```bash
./fskit-test -mountpoint /tmp/test-fskit -fskit -v
```

**Test without FSKit (legacy FUSE):**
```bash
./fskit-test -mountpoint /tmp/test-legacy -no-fskit -v
```

## What Each Test Does

| Test | Purpose | What It Checks |
|------|---------|----------------|
| Test 1 | Installation | macFUSE installed? Correct version? |
| Test 2 | Basic Mount | Can we mount FUSE at all? (no FSKit) |
| Test 3 | FSKit Mount | Does `backend=fskit` option work? |
| Test 4 | Option Formats | Try different ways to pass FSKit options |
| Test 5 | Recovery | Does mount recovery mechanism work? |
| Test 6 | System Info | What's the system configuration? |
| Test 7 | Full Integration | Does the exact app mount sequence work? |

## Interpreting Results

### Scenario A: All tests pass ✅

The system supports FSKit and mounting should work. The issue is likely:
- A timing problem
- A specific edge case not covered by tests
- Something in the application flow before/after mounting

**Next steps:**
- Run the actual application with verbose logging
- Compare application mount code with test code
- Check for initialization order issues

### Scenario B: Test 2 passes, Test 3 fails ❌

Basic FUSE works but FSKit specifically doesn't.

**This means:**
- macFUSE is installed and working
- FSKit backend is not available or not working
- The `backend=fskit` option is not supported

**Next steps:**
- Check macOS version (need 15.4+)
- Check macFUSE version (need 5.0+)
- Consider falling back to legacy FUSE (remove `backend=fskit`)

### Scenario C: Test 2 fails ❌

Basic FUSE mounting doesn't work at all.

**This means:**
- macFUSE not installed correctly
- System compatibility issue
- cgofuse not working properly

**Next steps:**
- Run debug script: `./scripts/debug-fskit.sh`
- Check macFUSE installation
- Try reinstalling macFUSE

### Scenario D: Test 1 shows macFUSE not installed ❌

**Next steps:**
- Install macFUSE from https://macfuse.io/
- Need version 5.0+ for FSKit support
- May need to restart after installation

## Expected Output Examples

### Success Case (Test 3):

```
=== Test 3: FUSE Mount with backend=fskit ===
Mountpoint: /tmp/fuse-test-fskit-1234567890
Mount options: [-o ro -o fsname=fusetest -o local -o volname=FuseTest -o backend=fskit]
✅ FUSE mount with backend=fskit succeeded
✅ Successfully read mountpoint, 1 entries
✅ Unmount succeeded
--- PASS: TestFSKit_FSKitBackendMount (1.23s)
```

### Failure Case (Test 3):

```
=== Test 3: FUSE Mount with backend=fskit ===
Mountpoint: /tmp/fuse-test-fskit-1234567890
Mount options: [-o ro -o fsname=fusetest -o local -o volname=FuseTest -o backend=fskit]
❌ FUSE mount with backend=fskit failed
   This indicates FSKit backend is not available or not working
--- FAIL: TestFSKit_FSKitBackendMount (5.12s)
```

## Key Files

- **Tests**: `internal/fs/fskit_debug_test.go`
- **CI Workflow**: `.github/workflows/fskit-debug.yml`
- **Debug Script**: `scripts/debug-fskit.sh`
- **Test Program**: `cmd/fskit-test/main.go`
- **Full Guide**: `FSKIT_DEBUG.md`

## Quick Decision Tree

```
Is macFUSE installed?
├─ NO → Install macFUSE 5.0+ from https://macfuse.io/
└─ YES → Does Test 2 pass?
    ├─ NO → Reinstall macFUSE or check system compatibility
    └─ YES → Does Test 3 pass?
        ├─ NO → Remove backend=fskit option (use legacy FUSE)
        └─ YES → Issue is in application code, not mount itself
```

## Troubleshooting Commands

```bash
# Check macOS version
sw_vers

# Check macFUSE installation
ls -la /Library/Filesystems/macfuse.fs
which mount_macfuse

# Check current mounts
mount | grep -i fuse

# Clean up stale mounts
diskutil unmount force /path/to/mountpoint
# or
umount -f /path/to/mountpoint

# Check kernel extensions (should be empty for FSKit)
kextstat | grep -i fuse
```

## Getting Help

If tests show unexpected results:

1. **Collect logs**: Run with `-v` flag and save output
2. **Check CI artifacts**: Download logs from GitHub Actions
3. **Run debug script**: Capture output of `./scripts/debug-fskit.sh`
4. **System info**: Include `sw_vers` and `mount_macfuse -h` output

## Notes

- Tests create temporary mountpoints in `/tmp/fuse-test-*`
- Tests clean up automatically, but manual cleanup is safe:
  ```bash
  for dir in /tmp/fuse-test-*; do
    diskutil unmount force "$dir" 2>/dev/null || true
    rm -rf "$dir" 2>/dev/null || true
  done
  ```
- Tests require `-tags fuse` build tag
- Tests only run on macOS (darwin) with FUSE support
- Each test runs independently with its own mountpoint
