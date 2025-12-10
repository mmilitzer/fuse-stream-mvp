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
	fs       fs.FS
	ctx      context.Context
	cancel   context.CancelFunc
	shutdownComplete chan struct{} // Signals when shutdown is complete
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
		fs:               filesystem,
		ctx:              daemonCtx,
		cancel:           cancel,
		shutdownComplete: make(chan struct{}),
	}

	// Handle graceful shutdown
	go func() {
		<-daemonCtx.Done()
		log.Println("[daemon] Shutting down...")
		
		// Give time for active operations to complete
		time.Sleep(100 * time.Millisecond)
		
		if err := filesystem.Stop(); err != nil {
			log.Printf("[daemon] Error stopping filesystem: %v", err)
		}
		
		log.Println("[daemon] Shutdown complete")
		close(instance.shutdownComplete)
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

// WaitForShutdown blocks until the daemon shutdown is complete.
// This should be called after triggering shutdown (via context cancellation)
// to ensure cleanup operations finish before the process exits.
func WaitForShutdown() {
	mu.Lock()
	d := instance
	mu.Unlock()
	
	if d != nil && d.shutdownComplete != nil {
		log.Println("[daemon] Waiting for shutdown to complete...")
		<-d.shutdownComplete
		log.Println("[daemon] Shutdown wait complete")
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
	
	// Wait for graceful shutdown to complete
	WaitForShutdown()
}
