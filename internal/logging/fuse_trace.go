// Package logging provides FUSE operation tracing for debugging deadlocks and stalls.
package logging

// FUSETrace wraps a FUSE operation with entry/exit logging and duration tracking.
// This is critical for diagnosing deadlocks and stalls in FUSE callbacks.
// Uses non-blocking logger to prevent any possibility of deadlocks.
//
// Usage:
//   defer FUSETrace("Read", "fh=%d off=%d len=%d", fh, offset, len(buff))()
//
// This will log:
//   [FUSE] Read ENTER fh=123 off=4096 len=131072
//   [FUSE] Read EXIT  fh=123 dur=2.3ms
func FUSETrace(operation string, format string, args ...interface{}) func() {
	return FUSETraceNonBlocking(operation, format, args...)
}

// FUSETraceSimple wraps a FUSE operation with minimal logging (just operation name).
//
// Usage:
//   defer FUSETraceSimple("Statfs")()
func FUSETraceSimple(operation string) func() {
	return FUSETraceNonBlocking(operation, "", nil...)
}
