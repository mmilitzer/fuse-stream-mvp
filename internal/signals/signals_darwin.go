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
