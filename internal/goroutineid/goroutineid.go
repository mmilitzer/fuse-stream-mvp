package goroutineid

import (
	"bytes"
	"runtime"
	"strconv"
)

// Get returns the goroutine ID of the caller.
// This is useful for debugging and logging concurrent operations.
// Implementation uses runtime.Stack to extract the goroutine ID.
func Get() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	// Stack trace format: "goroutine 123 [running]:\n..."
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
