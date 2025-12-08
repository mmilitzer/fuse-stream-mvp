//go:build fuse && darwin

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// Simple test filesystem
type testFS struct {
	fuse.FileSystemBase
}

func (fs *testFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	switch path {
	case "/":
		stat.Mode = fuse.S_IFDIR | 0755
		stat.Uid = uint32(os.Getuid())
		stat.Gid = uint32(os.Getgid())
		return 0
	case "/test.txt":
		stat.Mode = fuse.S_IFREG | 0644
		stat.Size = 13
		stat.Uid = uint32(os.Getuid())
		stat.Gid = uint32(os.Getgid())
		return 0
	default:
		return -fuse.ENOENT
	}
}

func (fs *testFS) Readdir(path string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	if path == "/" {
		fill(".", nil, 0)
		fill("..", nil, 0)
		fill("test.txt", nil, 0)
		return 0
	}
	return -fuse.ENOENT
}

func (fs *testFS) Open(path string, flags int) (int, uint64) {
	if path == "/test.txt" {
		return 0, 0
	}
	return -fuse.ENOENT, ^uint64(0)
}

func (fs *testFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
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

func main() {
	// Command line flags
	mountpoint := flag.String("mountpoint", "", "Mount point directory (required)")
	useFSKit := flag.Bool("fskit", false, "Use FSKit backend (backend=fskit)")
	noFSKit := flag.Bool("no-fskit", false, "Explicitly disable FSKit backend")
	verbose := flag.Bool("v", false, "Verbose output")
	
	flag.Parse()

	if *mountpoint == "" {
		// Default to temp directory
		*mountpoint = filepath.Join(os.TempDir(), fmt.Sprintf("fskit-test-%d", time.Now().Unix()))
		log.Printf("No mountpoint specified, using: %s", *mountpoint)
	}

	// Create mountpoint if it doesn't exist
	if err := os.MkdirAll(*mountpoint, 0755); err != nil {
		log.Fatalf("Failed to create mountpoint: %v", err)
	}

	log.Printf("=== FSKit Test Program ===")
	log.Printf("Mountpoint: %s", *mountpoint)
	log.Printf("Use FSKit: %v", *useFSKit)
	log.Printf("Explicitly disable FSKit: %v", *noFSKit)

	// Create filesystem
	fs := &testFS{}
	host := fuse.NewFileSystemHost(fs)

	// Build mount options
	mountOpts := []string{
		"-o", "ro",
		"-o", "fsname=fskittest",
	}

	// macOS-specific options
	mountOpts = append(mountOpts,
		"-o", "local",
		"-o", "volname=FSKitTest",
	)

	// Add FSKit backend option if requested
	if *useFSKit && !*noFSKit {
		log.Println("Adding backend=fskit option")
		mountOpts = append(mountOpts, "-o", "backend=fskit")
	} else if *noFSKit {
		log.Println("FSKit explicitly disabled, mounting without backend option")
	} else {
		log.Println("Mounting without FSKit backend option (default FUSE)")
	}

	if *verbose {
		log.Printf("Mount options: %v", mountOpts)
	}

	// Handle signals for clean unmount
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Mount in goroutine
	// Note: host.Mount() is blocking and runs the FUSE event loop
	// It only returns when unmounted or if mount fails immediately
	mountErr := make(chan error, 1)
	go func() {
		log.Println("Attempting to mount...")
		success := host.Mount(*mountpoint, mountOpts)
		if !success {
			log.Println("❌ Mount call returned false (mount failed)")
			mountErr <- fmt.Errorf("mount failed")
		} else {
			log.Println("Mount call returned (filesystem unmounted)")
		}
	}()

	// Wait for filesystem to be ready by checking if we can access it
	log.Println("Waiting for filesystem to be ready...")
	deadline := time.Now().Add(5 * time.Second)
	mounted := false
	for time.Now().Before(deadline) {
		select {
		case err := <-mountErr:
			log.Fatalf("❌ Mount failed: %v", err)
		default:
			// Try to read the directory to confirm FUSE is responding
			time.Sleep(100 * time.Millisecond)
			if _, err := os.ReadDir(*mountpoint); err == nil {
				log.Println("✅ Filesystem is ready and responding!")
				mounted = true
				goto mounted_success
			}
		}
	}

	if !mounted {
		log.Fatalf("❌ Mount timed out - filesystem not responding after 5 seconds")
	}

mounted_success:

	// Test filesystem access
	log.Println("\n=== Testing Filesystem Access ===")
	
	// Test 1: List directory
	log.Printf("Test 1: Reading directory %s", *mountpoint)
	entries, err := os.ReadDir(*mountpoint)
	if err != nil {
		log.Printf("❌ Failed to read directory: %v", err)
	} else {
		log.Printf("✅ Successfully read directory, found %d entries:", len(entries))
		for _, entry := range entries {
			log.Printf("  - %s", entry.Name())
		}
	}

	// Test 2: Stat file
	testFile := filepath.Join(*mountpoint, "test.txt")
	log.Printf("\nTest 2: Stating file %s", testFile)
	info, err := os.Stat(testFile)
	if err != nil {
		log.Printf("❌ Failed to stat file: %v", err)
	} else {
		log.Printf("✅ Successfully stat'd file:")
		log.Printf("  - Name: %s", info.Name())
		log.Printf("  - Size: %d bytes", info.Size())
		log.Printf("  - Mode: %s", info.Mode())
	}

	// Test 3: Read file
	log.Printf("\nTest 3: Reading file %s", testFile)
	content, err := os.ReadFile(testFile)
	if err != nil {
		log.Printf("❌ Failed to read file: %v", err)
	} else {
		log.Printf("✅ Successfully read file:")
		log.Printf("  - Content: %s", string(content))
	}

	log.Println("\n=== Filesystem tests completed ===")
	log.Println("Press Ctrl+C to unmount and exit")

	// Wait for interrupt signal
	<-sigChan
	log.Println("\nReceived interrupt signal, unmounting...")

	// Unmount
	if !host.Unmount() {
		log.Println("❌ Unmount failed")
		os.Exit(1)
	}

	log.Println("✅ Successfully unmounted")
	log.Println("Cleaning up mountpoint...")
	
	// Clean up
	time.Sleep(200 * time.Millisecond)
	if err := os.Remove(*mountpoint); err != nil {
		log.Printf("Warning: Failed to remove mountpoint: %v", err)
	}

	log.Println("Done!")
}
