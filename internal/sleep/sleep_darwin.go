//go:build darwin

package sleep

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <CoreFoundation/CoreFoundation.h>

// Wrapper to create a power assertion
IOReturn CreateAssertion(IOPMAssertionID *assertionID) {
    CFStringRef reasonForActivity = CFSTR("FuseStream file transfer in progress");
    IOReturn result = IOPMAssertionCreateWithName(
        kIOPMAssertionTypeNoIdleSleep,
        kIOPMAssertionLevelOn,
        reasonForActivity,
        assertionID
    );
    return result;
}

// Wrapper to release a power assertion
IOReturn ReleaseAssertion(IOPMAssertionID assertionID) {
    return IOPMAssertionRelease(assertionID);
}
*/
import "C"
import (
	"fmt"
	"log"
	"sync"
)

var (
	mu              sync.Mutex
	assertionID     C.IOPMAssertionID
	assertionActive bool
)

func preventSleepImpl() (release func(), err error) {
	mu.Lock()
	defer mu.Unlock()

	// Only create assertion if not already active
	if assertionActive {
		log.Printf("[sleep] Sleep prevention already active (assertion ID: %d)", assertionID)
		return func() {}, nil
	}

	var aid C.IOPMAssertionID
	result := C.CreateAssertion(&aid)
	if result != C.kIOReturnSuccess {
		return nil, fmt.Errorf("failed to create sleep assertion: IOReturn=%d", result)
	}

	assertionID = aid
	assertionActive = true
	log.Printf("[sleep] Sleep prevention enabled (assertion ID: %d)", assertionID)

	// Return a release function that releases the assertion
	return func() {
		mu.Lock()
		defer mu.Unlock()

		if !assertionActive {
			return
		}

		result := C.ReleaseAssertion(assertionID)
		if result != C.kIOReturnSuccess {
			log.Printf("[sleep] Warning: failed to release sleep assertion: IOReturn=%d", result)
		} else {
			log.Printf("[sleep] Sleep prevention disabled (released assertion ID: %d)", assertionID)
		}
		assertionActive = false
	}, nil
}
