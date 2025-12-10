// Package logging provides FUSE operation tracing for debugging deadlocks and stalls.
package logging

import (
	"fmt"
	"log"
	"time"
)

// FUSETrace wraps a FUSE operation with entry/exit logging and duration tracking.
// This is critical for diagnosing deadlocks and stalls in FUSE callbacks.
//
// Usage:
//   defer FUSETrace("Read", "fh=%d off=%d len=%d", fh, offset, len(buff))()
//
// This will log:
//   [FUSE] Read ENTER fh=123 off=4096 len=131072
//   [FUSE] Read EXIT  fh=123 dur=2.3ms
func FUSETrace(operation string, format string, args ...interface{}) func() {
	t0 := time.Now()
	
	// Log entry (non-blocking via async logger)
	log.Printf("[FUSE] %s ENTER %s", operation, fmt.Sprintf(format, args...))
	
	// Return cleanup function that logs exit with duration
	return func() {
		dur := time.Since(t0)
		log.Printf("[FUSE] %s EXIT  dur=%v", operation, dur)
	}
}

// FUSETraceSimple wraps a FUSE operation with minimal logging (just operation name).
//
// Usage:
//   defer FUSETraceSimple("Statfs")()
func FUSETraceSimple(operation string) func() {
	t0 := time.Now()
	log.Printf("[FUSE] %s ENTER", operation)
	return func() {
		dur := time.Since(t0)
		log.Printf("[FUSE] %s EXIT  dur=%v", operation, dur)
	}
}
