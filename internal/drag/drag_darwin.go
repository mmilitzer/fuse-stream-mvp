//go:build darwin

package drag

/*
#cgo CFLAGS: -x objective-c -fmodules -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>
void StartFileDrag(const char *cpath);
*/
import "C"
import (
	"fmt"
	"os"
	"unsafe"
)

// StartFileDrag begins a native macOS drag of a real filesystem path.
func StartFileDrag(path string) error {
	// Verify the file exists before attempting to drag
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("file does not exist: %w", err)
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	C.StartFileDrag(cPath)
	return nil
}
