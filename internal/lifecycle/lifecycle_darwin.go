//go:build darwin

package lifecycle

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Foundation
// Forward declaration of C functions
int FSMVP_StartObservingActivation(int callbackID);
int FSMVP_StopObservingActivation(int observerID);
*/
import "C"
import (
	"fmt"
	"log"
	"sync"
)

var (
	mu             sync.Mutex
	nextCallbackID int
	callbacks      = make(map[int]ActivationCallback)
	observerID     C.int
	observerActive bool
)

//export FSMVP_OnActivationChange
func FSMVP_OnActivationChange(callbackID C.int, active C.int) {
	mu.Lock()
	callback, exists := callbacks[int(callbackID)]
	mu.Unlock()

	if !exists {
		log.Printf("[lifecycle] Warning: callback %d not found", callbackID)
		return
	}

	isActive := active != 0
	callback(isActive)
}

func observeActivationImpl(callback ActivationCallback) (cleanup func(), err error) {
	mu.Lock()
	defer mu.Unlock()

	// Only support one observer for now
	if observerActive {
		return nil, fmt.Errorf("activation observer already active")
	}

	// Register callback
	cbID := nextCallbackID
	nextCallbackID++
	callbacks[cbID] = callback

	// Start observing
	oid := C.FSMVP_StartObservingActivation(C.int(cbID))
	if oid < 0 {
		delete(callbacks, cbID)
		return nil, fmt.Errorf("failed to start observing activation events")
	}

	observerID = oid
	observerActive = true
	log.Printf("[lifecycle] Activation observer started (observer ID: %d, callback ID: %d)", observerID, cbID)

	// Return cleanup function
	capturedCbID := cbID
	capturedOid := oid
	return func() {
		mu.Lock()
		defer mu.Unlock()

		if !observerActive {
			return
		}

		result := C.FSMVP_StopObservingActivation(capturedOid)
		if result != 0 {
			log.Printf("[lifecycle] Warning: failed to stop observer: ID=%d", capturedOid)
		} else {
			log.Printf("[lifecycle] Activation observer stopped (observer ID: %d, callback ID: %d)", 
				capturedOid, capturedCbID)
		}

		delete(callbacks, capturedCbID)
		observerActive = false
	}, nil
}
