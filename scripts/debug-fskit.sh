#!/bin/bash
#
# FSKit Debug Script
# Run this script on macOS to diagnose FSKit/macFUSE mounting issues
#

set -e

echo "========================================"
echo "FSKit/macFUSE Debug Script"
echo "========================================"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
check_cmd() {
    if eval "$1" &>/dev/null; then
        echo -e "${GREEN}✅ $2${NC}"
        return 0
    else
        echo -e "${RED}❌ $2${NC}"
        return 1
    fi
}

info() {
    echo -e "${YELLOW}ℹ️  $1${NC}"
}

# 1. System Information
echo "=== 1. System Information ==="
sw_vers
echo ""
uname -a
echo ""

# 2. macOS Version Check (FSKit requires 15.4+)
echo "=== 2. macOS Version Check ==="
OS_VERSION=$(sw_vers -productVersion)
echo "macOS Version: $OS_VERSION"

MAJOR=$(echo "$OS_VERSION" | cut -d. -f1)
MINOR=$(echo "$OS_VERSION" | cut -d. -f2)

if [ "$MAJOR" -ge 15 ] && [ "$MINOR" -ge 4 ]; then
    echo -e "${GREEN}✅ macOS version supports FSKit (15.4+)${NC}"
elif [ "$MAJOR" -ge 16 ]; then
    echo -e "${GREEN}✅ macOS version supports FSKit${NC}"
else
    echo -e "${RED}❌ macOS version $OS_VERSION does not support FSKit (requires 15.4+)${NC}"
    echo -e "${YELLOW}   FSKit will not be available on this system${NC}"
fi
echo ""

# 3. macFUSE Installation Check
echo "=== 3. macFUSE Installation Check ==="

# Check for mount_macfuse
if check_cmd "which mount_macfuse" "mount_macfuse binary found"; then
    MOUNT_MACFUSE_PATH=$(which mount_macfuse)
    echo "   Path: $MOUNT_MACFUSE_PATH"
else
    echo -e "${RED}   mount_macfuse not found in PATH${NC}"
fi
echo ""

# Check for macFUSE filesystem bundle
if [ -d "/Library/Filesystems/macfuse.fs" ]; then
    echo -e "${GREEN}✅ macFUSE filesystem bundle found${NC}"
    echo "   Checking version..."
    
    if [ -f "/Library/Filesystems/macfuse.fs/Contents/version.plist" ]; then
        VERSION_PLIST="/Library/Filesystems/macfuse.fs/Contents/version.plist"
        echo "   Version plist: $VERSION_PLIST"
        
        # Try to extract version
        if command -v plutil &>/dev/null; then
            plutil -p "$VERSION_PLIST" 2>/dev/null || cat "$VERSION_PLIST"
        else
            cat "$VERSION_PLIST"
        fi
    fi
    
    echo ""
    echo "   Contents:"
    ls -la /Library/Filesystems/macfuse.fs/Contents/ 2>&1 || true
else
    echo -e "${RED}❌ macFUSE filesystem bundle not found${NC}"
fi
echo ""

# Check for kernel extension (legacy)
if [ -d "/Library/Extensions/macfuse.kext" ]; then
    echo -e "${YELLOW}⚠️  Legacy macFUSE kernel extension found${NC}"
    echo "   Path: /Library/Extensions/macfuse.kext"
    echo "   Note: FSKit should use user-space extension, not kext"
else
    echo -e "${GREEN}✅ No legacy kernel extension (good for FSKit)${NC}"
fi
echo ""

# 4. Check loaded kernel extensions
echo "=== 4. Loaded Kernel Extensions ==="
echo "Checking for macFUSE/OSXFUSE kexts..."

if kextstat | grep -i "osxfuse\|macfuse" ; then
    info "macFUSE/OSXFUSE kernel extensions are loaded"
else
    echo "No macFUSE/OSXFUSE kernel extensions loaded"
fi
echo ""

# 5. Check current FUSE mounts
echo "=== 5. Current FUSE Mounts ==="
if mount | grep -i "fuse\|macfuse"; then
    info "FUSE mounts found"
else
    echo "No FUSE mounts currently active"
fi
echo ""

# 6. Check macFUSE preferences
echo "=== 6. macFUSE Preferences ==="
if defaults read /Library/Preferences/com.github.macfuse 2>/dev/null; then
    echo -e "${GREEN}✅ macFUSE preferences found${NC}"
else
    echo "No macFUSE preferences found (may be default)"
fi
echo ""

# 7. Go environment
echo "=== 7. Go Environment ==="
if command -v go &>/dev/null; then
    go version
    echo "GOPATH: $GOPATH"
    echo "GOROOT: $GOROOT"
else
    echo -e "${RED}❌ Go not found${NC}"
fi
echo ""

# 8. cgofuse check
echo "=== 8. cgofuse Library Check ==="
if [ -f "go.mod" ]; then
    echo "Checking go.mod for cgofuse..."
    grep "cgofuse" go.mod || echo "cgofuse not found in go.mod"
else
    echo "No go.mod found in current directory"
fi
echo ""

# 9. Try a simple mount test
echo "=== 9. Simple Mount Test ==="
TEST_DIR="/tmp/fuse-debug-test-$$"
echo "Creating test mountpoint: $TEST_DIR"
mkdir -p "$TEST_DIR"

echo ""
echo "Attempting basic mount command check..."
if command -v mount_macfuse &>/dev/null; then
    echo "mount_macfuse is available"
    echo "Checking mount_macfuse help..."
    mount_macfuse -h 2>&1 | head -20 || true
else
    echo -e "${RED}mount_macfuse command not available${NC}"
fi

echo ""
echo "Cleaning up test directory..."
rmdir "$TEST_DIR" 2>/dev/null || true

# 10. Summary and Recommendations
echo ""
echo "========================================"
echo "=== Summary and Recommendations ==="
echo "========================================"
echo ""

# Check all critical components
HAS_MACOS_15_4=false
HAS_MACFUSE=false

if [ "$MAJOR" -ge 15 ] && [ "$MINOR" -ge 4 ]; then
    HAS_MACOS_15_4=true
elif [ "$MAJOR" -ge 16 ]; then
    HAS_MACOS_15_4=true
fi

if [ -d "/Library/Filesystems/macfuse.fs" ] && command -v mount_macfuse &>/dev/null; then
    HAS_MACFUSE=true
fi

if [ "$HAS_MACOS_15_4" = true ] && [ "$HAS_MACFUSE" = true ]; then
    echo -e "${GREEN}✅ System appears ready for FSKit${NC}"
    echo "   - macOS version: $OS_VERSION (15.4+ required)"
    echo "   - macFUSE installed: Yes"
    echo ""
    echo "Next steps:"
    echo "  1. Run the test suite: go test -v -tags fuse -run TestFSKit ./internal/fs"
    echo "  2. Check test output for specific mount failures"
    echo "  3. If FSKit backend fails, try without 'backend=fskit' option"
else
    echo -e "${RED}❌ System NOT ready for FSKit${NC}"
    echo ""
    if [ "$HAS_MACOS_15_4" = false ]; then
        echo -e "${RED}   • macOS version too old ($OS_VERSION < 15.4)${NC}"
        echo "     - FSKit requires macOS 15.4 or later"
        echo "     - You may need to upgrade macOS or use legacy FUSE backend"
    fi
    echo ""
    if [ "$HAS_MACFUSE" = false ]; then
        echo -e "${RED}   • macFUSE not properly installed${NC}"
        echo "     - Download from: https://macfuse.io/"
        echo "     - Requires macFUSE 5.0+ for FSKit support"
        echo "     - After installation, may need to restart"
    fi
fi

echo ""
echo "For more information:"
echo "  - macFUSE documentation: https://github.com/macfuse/macfuse/wiki"
echo "  - FSKit information: https://github.com/macfuse/macfuse/wiki/Getting-Started"
echo ""
echo "Debug script completed."
