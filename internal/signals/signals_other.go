//go:build !darwin

package signals

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// SetupSignalHandler sets up signal handling for graceful shutdown on non-macOS platforms.
func SetupSignalHandler(onInterrupt func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	go func() {
		sig := <-sigChan
		log.Printf("[signals] Received signal: %v - triggering graceful shutdown", sig)
		if onInterrupt != nil {
			onInterrupt()
		}
	}()
	
	log.Println("[signals] Signal handler installed")
}
