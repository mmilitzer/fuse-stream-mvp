package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/fs"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
)

var (
	instance *Daemon
	mu       sync.Mutex
)

type Daemon struct {
	fs     fs.FS
	ctx    context.Context
	cancel context.CancelFunc
}

func Start(ctx context.Context, mountpoint string, client *api.Client, cfg *config.Config) error {
	mu.Lock()
	defer mu.Unlock()

	if instance != nil {
		return fmt.Errorf("daemon already started")
	}

	log.Println("[daemon] Starting FuseStream services...")

	// Ensure mountpoint exists
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		return fmt.Errorf("create mountpoint: %w", err)
	}

	// Create and start filesystem (FUSE or stub based on build tags)
	daemonCtx, cancel := context.WithCancel(ctx)
	filesystem := fs.New(client, cfg)
	
	if err := filesystem.Start(fs.MountOptions{Mountpoint: mountpoint}); err != nil {
		cancel()
		return fmt.Errorf("start filesystem: %w", err)
	}

	instance = &Daemon{
		fs:     filesystem,
		ctx:    daemonCtx,
		cancel: cancel,
	}

	// Handle graceful shutdown
	go func() {
		<-daemonCtx.Done()
		log.Println("[daemon] Shutting down...")
		
		// Release resources (app nap, sleep prevention, evict files)
		filesystem.ReleaseResources()
		
		// Trigger unmount in a separate goroutine so we don't block shutdown
		// The FUSE mount goroutine needs Unmount() to be called to complete,
		// otherwise it blocks forever and prevents app exit
		go func() {
			log.Println("[daemon] Triggering async unmount...")
			// Call Stop() which will call Unmount() internally
			// Resources are already released, so this just does the unmount
			if err := filesystem.Stop(); err != nil {
				log.Printf("[daemon] Error during unmount: %v", err)
			} else {
				log.Println("[daemon] Unmount completed")
			}
		}()
		
		log.Println("[daemon] Shutdown complete")
	}()

	if filesystem.Mounted() {
		log.Printf("[daemon] FUSE filesystem mounted at %s", mountpoint)
	} else {
		log.Printf("[daemon] Running in stub mode (FUSE unavailable)")
	}
	return nil
}

func GetFS() fs.FS {
	mu.Lock()
	defer mu.Unlock()
	
	if instance == nil {
		return nil
	}
	return instance.fs
}

func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	
	if instance != nil {
		instance.cancel()
		instance = nil
	}
}

// UnmountFS attempts to unmount the filesystem and returns an error if it fails.
// This is used by the app delegate to cleanly unmount before termination.
func UnmountFS() error {
	mu.Lock()
	defer mu.Unlock()
	
	if instance == nil || instance.fs == nil {
		return nil // Nothing to unmount
	}
	
	return instance.fs.Stop()
}

func KeepAlive(ctx context.Context) {
	log.Println("[daemon] Running in headless mode. Press Ctrl+C to exit.")
	<-ctx.Done()
	
	// Give graceful shutdown a moment to complete
	time.Sleep(500 * time.Millisecond)
}
