package daemon

import (
	"context"
	"log"
	"time"
)

// Start initializes the daemon services (FUSE mount + HTTP API server)
// This runs in the background whether in GUI or headless mode
func Start(ctx context.Context) error {
	log.Println("[daemon] Starting FuseStream services...")

	// TODO M2: Mount FUSE filesystem
	// TODO M2: Start localhost HTTP API for UI communication
	// TODO M2: Handle graceful shutdown on context cancellation

	// For M1: just keep alive
	go func() {
		<-ctx.Done()
		log.Println("[daemon] Shutting down...")
		// TODO M2: Unmount FUSE if no active file handles
		// TODO M2: Stop HTTP server
	}()

	log.Println("[daemon] Services ready (M1 stub)")
	return nil
}

// KeepAlive blocks until context is cancelled (for headless mode)
func KeepAlive(ctx context.Context) {
	log.Println("[daemon] Running in headless mode. Press Ctrl+C to exit.")
	<-ctx.Done()
	
	// Give graceful shutdown a moment to complete
	time.Sleep(500 * time.Millisecond)
}
