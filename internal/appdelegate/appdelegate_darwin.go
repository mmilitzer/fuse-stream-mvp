//go:build darwin

package appdelegate

/*
#cgo CFLAGS: -x objective-c -fmodules -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework Foundation

#include <stdlib.h>

void FSMVP_InstallAppDelegate();
void FSMVP_UnmountComplete(int success);
*/
import "C"
import (
	"log"
	"sync"
	"time"
)

var (
	mu                     sync.Mutex
	checkActiveUploadsFunc CheckActiveUploadsFunc
	unmountFunc            UnmountFunc
)

// Install installs the custom app delegate with the provided callback functions
func Install(checkUploads CheckActiveUploadsFunc, unmount UnmountFunc) {
	mu.Lock()
	defer mu.Unlock()

	checkActiveUploadsFunc = checkUploads
	unmountFunc = unmount

	C.FSMVP_InstallAppDelegate()
	log.Println("[appdelegate] App delegate installed with callbacks")
}

// These are exported functions called from Objective-C

//export FSMVP_HasActiveUploads
func FSMVP_HasActiveUploads() C.int {
	mu.Lock()
	defer mu.Unlock()

	if checkActiveUploadsFunc == nil {
		return 0
	}

	hasUploads := checkActiveUploadsFunc()
	if hasUploads {
		return 1
	}
	return 0
}

//export FSMVP_UnmountAsync
func FSMVP_UnmountAsync() {
	mu.Lock()
	unmountFn := unmountFunc
	mu.Unlock()

	if unmountFn == nil {
		log.Println("[appdelegate] No unmount function set, signaling immediate completion")
		C.FSMVP_UnmountComplete(1)
		return
	}

	log.Println("[appdelegate] Starting async unmount with 3-second timeout")

	// Run unmount in background with timeout
	go func() {
		done := make(chan error, 1)
		go func() {
			done <- unmountFn()
		}()

		select {
		case err := <-done:
			if err != nil {
				log.Printf("[appdelegate] Unmount failed: %v, will try force unmount", err)
				// Try force unmount
				if err := forceUnmountMacOS(); err != nil {
					log.Printf("[appdelegate] Force unmount failed: %v", err)
				}
			} else {
				log.Println("[appdelegate] Unmount successful")
			}
			C.FSMVP_UnmountComplete(1)
		case <-time.After(3 * time.Second):
			log.Println("[appdelegate] Unmount timeout (3s), forcing unmount")
			// Force unmount
			if err := forceUnmountMacOS(); err != nil {
				log.Printf("[appdelegate] Force unmount failed: %v", err)
			}
			C.FSMVP_UnmountComplete(1)
		}
	}()
}

// forceUnmountMacOS attempts to forcibly unmount the filesystem using diskutil/umount
func forceUnmountMacOS() error {
	// This would need to get the mountpoint from the filesystem
	// For now, we'll log that force unmount was attempted
	log.Println("[appdelegate] Force unmount triggered (implementation needed)")
	return nil
}

// ReplyToShouldTerminate is a helper function that can be called from Go code
// to signal that termination should proceed
func ReplyToShouldTerminate(shouldTerminate bool) {
	if shouldTerminate {
		C.FSMVP_UnmountComplete(1)
	} else {
		C.FSMVP_UnmountComplete(0)
	}
}
