//go:build darwin

package signals

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

// Trigger app termination from the signal handler
void FSMVP_TriggerTermination() {
    dispatch_async(dispatch_get_main_queue(), ^{
        [[NSApplication sharedApplication] terminate:nil];
    });
}
*/
import "C"
import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// SetupSignalHandler sets up signal handling for graceful shutdown on macOS.
// SIGTERM triggers NSApp termination on the main thread (proper macOS termination flow).
// SIGINT is handled by canceling the context (for Ctrl+C in terminal).
func SetupSignalHandler(onInterrupt func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	go func() {
		sig := <-sigChan
		switch sig {
		case syscall.SIGTERM:
			// SIGTERM: Trigger proper macOS termination flow
			log.Println("[signals] Received SIGTERM - triggering app termination")
			C.FSMVP_TriggerTermination()
		case os.Interrupt:
			// SIGINT (Ctrl+C): Call the interrupt handler
			log.Println("[signals] Received SIGINT - triggering graceful shutdown")
			if onInterrupt != nil {
				onInterrupt()
			}
		}
	}()
	
	log.Println("[signals] Signal handler installed (SIGTERM will trigger NSApp terminate, SIGINT will call handler)")
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
