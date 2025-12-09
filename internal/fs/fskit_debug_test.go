//go:build fuse && darwin

package fs

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// isGraphicalSession checks if we're running in a graphical user session
// FUSE mounts require a full user session context to work properly
func isGraphicalSession() bool {
	// Check if we're in CI environment
	if os.Getenv("CI") != "" {
		return false
	}
	
	// Check if DISPLAY or related environment variables are set
	// On macOS, check for proper user session
	if runtime.GOOS == "darwin" {
		// Check if running as a console user (has GUI access)
		cmd := exec.Command("stat", "-f", "%Su", "/dev/console")
		output, err := cmd.Output()
		if err != nil {
			return false
		}
		consoleUser := strings.TrimSpace(string(output))
		currentUser := os.Getenv("USER")
		
		// If we're not the console user, we probably don't have GUI access
		if consoleUser != currentUser {
			return false
		}
	}
	
	return true
}

// Test 1: Check FSKit support (user-space only, no kext)
func TestFSKit_FSKitSupport(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}

	log.Println("=== Test 1: Checking FSKit Support (User-Space) ===")

	// Check if macFUSE is installed
	checkCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("❌ Command '%s %v' failed: %v", name, args, err)
			log.Printf("   Output: %s", string(output))
		} else {
			log.Printf("✅ Command '%s %v' succeeded", name, args)
			log.Printf("   Output: %s", string(output))
		}
	}

	// Check for macFUSE installation (user-space mount helper)
	log.Println("\n--- Checking for macFUSE user-space helper ---")
	checkCmd("which", "mount_macfuse")
	checkCmd("ls", "-la", "/Library/Filesystems/macfuse.fs")
	
	// Check macFUSE version
	log.Println("\n--- Checking macFUSE version ---")
	checkCmd("sh", "-c", "cat /Library/Filesystems/macfuse.fs/Contents/version.plist 2>/dev/null || echo 'Version file not found'")
	
	// Check for FSKit support
	log.Println("\n--- Checking for FSKit support ---")
	checkCmd("ls", "-la", "/Library/Filesystems/macfuse.fs/Contents/Resources")
	checkCmd("sh", "-c", "system_profiler SPSoftwareDataType | grep 'System Version'")
}

// Test 2: FUSE mount with FSKit option (backend=fskit)
func TestFSKit_FSKitBackendMount(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}
	
	if !isGraphicalSession() {
		t.Skip("Skipping mount test: requires graphical user session (not available in CI/SSH)")
	}

	log.Println("\n=== Test 2: FUSE Mount with backend=fskit ===")

	fs := &simpleTestFS{}
	
	mountpoint := filepath.Join(os.TempDir(), fmt.Sprintf("fuse-test-fskit-%d", time.Now().Unix()))
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		t.Fatalf("Failed to create mountpoint: %v", err)
	}
	defer os.RemoveAll(mountpoint)

	log.Printf("Mountpoint: %s", mountpoint)

	host := fuse.NewFileSystemHost(fs)
	
	mountOpts := []string{
		"-o", "ro",
		"-o", "fsname=fusetest",
		"-o", "local",
		"-o", "volname=FuseTest",
		"-o", "backend=fskit",  // FSKit option
	}
	
	log.Printf("Mount options: %v", mountOpts)

	mountResult := make(chan bool, 1)
	go func() {
		success := host.Mount(mountpoint, mountOpts)
		mountResult <- success
		log.Printf("Mount result: %v", success)
	}()

	select {
	case success := <-mountResult:
		if !success {
			t.Errorf("❌ FUSE mount with backend=fskit failed")
			log.Println("   This indicates FSKit backend is not available or not working")
		} else {
			log.Println("✅ FUSE mount with backend=fskit succeeded")
			time.Sleep(500 * time.Millisecond)
			
			entries, err := os.ReadDir(mountpoint)
			if err != nil {
				log.Printf("⚠️  Warning: Could not read mountpoint: %v", err)
			} else {
				log.Printf("✅ Successfully read mountpoint, %d entries", len(entries))
			}
			
			if !host.Unmount() {
				log.Println("⚠️  Warning: Unmount returned false")
			} else {
				log.Println("✅ Unmount succeeded")
			}
		}
	case <-time.After(10 * time.Second):
		t.Errorf("❌ FUSE mount with backend=fskit timed out")
		host.Unmount()
	}
}

// Test 3: Try different FSKit option formats
func TestFSKit_DifferentOptionFormats(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}
	
	if !isGraphicalSession() {
		t.Skip("Skipping mount test: requires graphical user session (not available in CI/SSH)")
	}

	log.Println("\n=== Test 3: Testing Different FSKit Option Formats ===")

	testCases := []struct {
		name string
		opts []string
	}{
		{
			name: "backend=fskit as single -o",
			opts: []string{"-o", "ro", "-o", "fsname=fusetest", "-o", "backend=fskit"},
		},
		{
			name: "modules=fskit",
			opts: []string{"-o", "ro", "-o", "fsname=fusetest", "-o", "modules=fskit"},
		},
		{
			name: "combined options with comma",
			opts: []string{"-o", "ro,fsname=fusetest,backend=fskit"},
		},
		{
			name: "daemon_timeout option",
			opts: []string{"-o", "ro", "-o", "fsname=fusetest", "-o", "daemon_timeout=60"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			log.Printf("\n--- Testing: %s ---", tc.name)
			
			fs := &simpleTestFS{}
			mountpoint := filepath.Join(os.TempDir(), fmt.Sprintf("fuse-test-%d", time.Now().Unix()))
			if err := os.MkdirAll(mountpoint, 0755); err != nil {
				t.Fatalf("Failed to create mountpoint: %v", err)
			}
			defer os.RemoveAll(mountpoint)

			log.Printf("Mountpoint: %s", mountpoint)
			log.Printf("Options: %v", tc.opts)

			host := fuse.NewFileSystemHost(fs)
			
			mountResult := make(chan bool, 1)
			go func() {
				success := host.Mount(mountpoint, tc.opts)
				mountResult <- success
			}()

			select {
			case success := <-mountResult:
				if success {
					log.Printf("✅ Mount succeeded with: %s", tc.name)
					time.Sleep(200 * time.Millisecond)
					host.Unmount()
				} else {
					log.Printf("❌ Mount failed with: %s", tc.name)
				}
			case <-time.After(5 * time.Second):
				log.Printf("⏱️  Mount timed out with: %s", tc.name)
				host.Unmount()
			}
		})
	}
}

// Test 4: Verify mount recovery mechanism
func TestFSKit_MountRecovery(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}

	log.Println("\n=== Test 4: Mount Recovery Mechanism ===")

	mountpoint := filepath.Join(os.TempDir(), fmt.Sprintf("fuse-test-recovery-%d", time.Now().Unix()))
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		t.Fatalf("Failed to create mountpoint: %v", err)
	}
	defer os.RemoveAll(mountpoint)

	log.Printf("Mountpoint: %s", mountpoint)

	// Test the recovery function logic
	log.Println("\n--- Testing mount recovery function ---")
	
	// Create a temporary fuseFS just for testing recovery
	testFS := &fuseFS{
		mountpoint: mountpoint,
		ctx:        context.Background(),
	}
	
	err := testFS.recoverStaleMountMacOS()
	if err != nil {
		log.Printf("⚠️  Recovery returned error: %v", err)
	} else {
		log.Println("✅ Recovery function completed successfully")
	}

	// Check if mountpoint still exists and is accessible
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Errorf("❌ Mountpoint not accessible after recovery: %v", err)
	} else {
		log.Printf("✅ Mountpoint exists and is accessible (IsDir: %v)", info.IsDir())
	}
}

// Test 5: Check mount command execution details
func TestFSKit_MountCommandDetails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}

	log.Println("\n=== Test 5: Mount Command Details ===")

	// Try to understand what cgofuse is doing under the hood
	log.Println("\n--- Checking current mounts ---")
	cmd := exec.Command("mount")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to run 'mount': %v", err)
	} else {
		lines := strings.Split(string(output), "\n")
		log.Println("Current FUSE/macFUSE mounts:")
		for _, line := range lines {
			if strings.Contains(line, "fuse") || strings.Contains(line, "macfuse") {
				log.Printf("  %s", line)
			}
		}
	}

	// Check for macFUSE load options
	log.Println("\n--- Checking macFUSE load options ---")
	cmd = exec.Command("sh", "-c", "defaults read /Library/Preferences/com.github.macfuse 2>/dev/null || echo 'No preferences found'")
	output, err = cmd.CombinedOutput()
	log.Printf("macFUSE preferences: %s", string(output))

	// Check system version for FSKit compatibility
	log.Println("\n--- Checking macOS version for FSKit compatibility ---")
	cmd = exec.Command("sw_vers")
	output, err = cmd.CombinedOutput()
	if err == nil {
		log.Printf("System version:\n%s", string(output))
		
		// Parse version to check if >= 15.4
		cmd = exec.Command("sw_vers", "-productVersion")
		output, _ = cmd.CombinedOutput()
		version := strings.TrimSpace(string(output))
		log.Printf("Product version: %s", version)
		
		parts := strings.Split(version, ".")
		if len(parts) >= 2 {
			major := parts[0]
			minor := parts[1]
			log.Printf("Major: %s, Minor: %s", major, minor)
			
			if major == "15" || major == "14" {
				log.Printf("⚠️  macOS version may not support FSKit (requires 15.4+)")
			} else {
				log.Printf("✅ macOS version should support FSKit")
			}
		}
	}
}

// Test 6: Full integration test matching actual code
func TestFSKit_FullIntegration(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}
	
	if !isGraphicalSession() {
		t.Skip("Skipping mount test: requires graphical user session (not available in CI/SSH)")
	}

	log.Println("\n=== Test 6: Full Integration Test (Matching Actual Code) ===")

	fs := &simpleTestFS{}
	
	mountpoint := filepath.Join(os.TempDir(), fmt.Sprintf("fuse-test-full-%d", time.Now().Unix()))
	
	// Try recovery first (like the actual code)
	log.Println("\n--- Step 1: Create mountpoint ---")
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		t.Fatalf("Failed to create mountpoint: %v", err)
	}
	defer os.RemoveAll(mountpoint)
	log.Printf("✅ Mountpoint created: %s", mountpoint)

	log.Println("\n--- Step 2: Simulate mount recovery ---")
	testFS := &fuseFS{
		mountpoint: mountpoint,
		ctx:        context.Background(),
	}
	if err := testFS.recoverStaleMountMacOS(); err != nil {
		log.Printf("⚠️  Recovery warning: %v (continuing anyway)", err)
	} else {
		log.Println("✅ Recovery completed")
	}

	log.Println("\n--- Step 3: Attempt mount with exact options from code ---")
	host := fuse.NewFileSystemHost(fs)
	
	// Exact options from fs_fuse.go lines 94-107
	mountOpts := []string{
		"-o", "ro",
		"-o", "fsname=fusestream",
		"-o", "local",
		"-o", "volname=FuseStream",
		"-o", "backend=fskit",
	}
	
	log.Printf("Mount options: %v", mountOpts)
	log.Println("Attempting mount...")

	mountResult := make(chan bool, 1)
	go func() {
		success := host.Mount(mountpoint, mountOpts)
		mountResult <- success
		if !success {
			log.Println("❌ Mount failed in goroutine")
		} else {
			log.Println("✅ Mount succeeded in goroutine")
		}
	}()

	select {
	case success := <-mountResult:
		if !success {
			t.Errorf("❌ Full integration mount failed")
			log.Println("\nPossible issues:")
			log.Println("  1. macFUSE not installed or too old (need ≥5.0)")
			log.Println("  2. macOS version too old (need ≥15.4 for FSKit)")
			log.Println("  3. FSKit backend not available")
			log.Println("  4. cgofuse doesn't support backend=fskit option")
			log.Println("  5. Mountpoint permission issue")
		} else {
			log.Println("✅ Full integration mount succeeded!")
			
			// Wait for filesystem to be ready
			time.Sleep(100 * time.Millisecond)
			
			// Try to access it
			log.Println("\n--- Step 4: Test filesystem access ---")
			entries, err := os.ReadDir(mountpoint)
			if err != nil {
				log.Printf("❌ Failed to read mounted filesystem: %v", err)
			} else {
				log.Printf("✅ Successfully read mounted filesystem: %d entries", len(entries))
				for _, entry := range entries {
					log.Printf("  - %s", entry.Name())
				}
			}
			
			// Test file operations
			log.Println("\n--- Step 5: Test file stat ---")
			info, err := os.Stat(filepath.Join(mountpoint, "test.txt"))
			if err != nil {
				log.Printf("⚠️  Could not stat test file (expected for empty fs): %v", err)
			} else {
				log.Printf("✅ File stat succeeded: %s (%d bytes)", info.Name(), info.Size())
			}
			
			// Unmount
			log.Println("\n--- Step 6: Unmount ---")
			if !host.Unmount() {
				log.Println("❌ Unmount failed")
			} else {
				log.Println("✅ Unmount succeeded")
			}
		}
	case <-time.After(5 * time.Second):
		t.Errorf("❌ Full integration mount timed out")
		log.Println("Mount operation did not complete within 5 seconds")
		host.Unmount()
	}
}

// Simple test filesystem implementation
type simpleTestFS struct {
	fuse.FileSystemBase
}

func (fs *simpleTestFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	switch path {
	case "/":
		stat.Mode = fuse.S_IFDIR | 0755
		return 0
	case "/test.txt":
		stat.Mode = fuse.S_IFREG | 0644
		stat.Size = 13
		return 0
	default:
		return -fuse.ENOENT
	}
}

func (fs *simpleTestFS) Readdir(path string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	if path == "/" {
		fill(".", nil, 0)
		fill("..", nil, 0)
		fill("test.txt", nil, 0)
		return 0
	}
	return -fuse.ENOENT
}

func (fs *simpleTestFS) Open(path string, flags int) (int, uint64) {
	if path == "/test.txt" {
		return 0, 0
	}
	return -fuse.ENOENT, ^uint64(0)
}

func (fs *simpleTestFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	if path == "/test.txt" {
		content := []byte("Hello, FSKit!")
		if ofst < int64(len(content)) {
			n := copy(buff, content[ofst:])
			return n
		}
		return 0
	}
	return -fuse.ENOENT
}
