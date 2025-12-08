//go:build darwin

package drag

/*
#cgo CFLAGS: -x objective-c -fmodules -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>
int FSMVP_StartFileDrag(const char *cpath);
*/
import "C"
import (
	"fmt"
	"os"
	"unsafe"
)

// StartFileDrag begins a native macOS drag of a real filesystem path.
// This must be called from a mousedown event handler for Cocoa to access
// the current NSEvent on the main thread.
func StartFileDrag(path string) error {
	// Verify the file exists before attempting to drag
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("file does not exist: %w", err)
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	// Call C function and check return code
	// 0 = success, 1 = validation error, 2 = file not found
	ret := C.FSMVP_StartFileDrag(cPath)
	if ret != 0 {
		if ret == 2 {
			return fmt.Errorf("file not found by Cocoa: %s", path)
		}
		return fmt.Errorf("failed to start drag (error code %d)", ret)
	}
	
	return nil
}
