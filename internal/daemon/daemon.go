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
)

var (
	instance *Daemon
	mu       sync.Mutex
)

type Daemon struct {
	fs     *fs.FS
	ctx    context.Context
	cancel context.CancelFunc
}

func Start(ctx context.Context, mountpoint string, client *api.Client) error {
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

	// Create and mount filesystem
	daemonCtx, cancel := context.WithCancel(ctx)
	filesystem := fs.New(mountpoint, client)
	
	if err := filesystem.Mount(); err != nil {
		cancel()
		return fmt.Errorf("mount filesystem: %w", err)
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
		
		// Give time for active operations to complete
		time.Sleep(100 * time.Millisecond)
		
		if err := filesystem.Unmount(); err != nil {
			log.Printf("[daemon] Error unmounting: %v", err)
		}
		
		log.Println("[daemon] Shutdown complete")
	}()

	log.Printf("[daemon] FUSE filesystem mounted at %s", mountpoint)
	return nil
}

func GetFS() *fs.FS {
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

func KeepAlive(ctx context.Context) {
	log.Println("[daemon] Running in headless mode. Press Ctrl+C to exit.")
	<-ctx.Done()
	
	// Give graceful shutdown a moment to complete
	time.Sleep(500 * time.Millisecond)
}
