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

// SetupDebugSignalHandler sets up SIGQUIT handler for on-demand debugging.
// SIGQUIT (kill -QUIT <pid> or Ctrl+\) triggers a goroutine stack dump.
func SetupDebugSignalHandler(onQuit func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGQUIT)
	
	go func() {
		for sig := range sigChan {
			if sig == syscall.SIGQUIT {
				log.Println("[signals] Received SIGQUIT - dumping goroutine stacks")
				if onQuit != nil {
					onQuit()
				}
			}
		}
	}()
	
	log.Println("[signals] Debug signal handler installed (SIGQUIT will dump stacks)")
}
