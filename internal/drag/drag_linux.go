//go:build linux

package drag

/*
#cgo pkg-config: gtk4
#include "drag_linux.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"os"
	"unsafe"
)

// StartFileDrag begins a native Linux GTK4 drag of a real filesystem path.
func StartFileDrag(path string) error {
	// Verify the file exists before attempting to drag
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("file does not exist: %w", err)
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	result := C.FSMVP_StartFileDrag(cPath)
	if result != 0 {
		return fmt.Errorf("gtk drag failed with code %d", result)
	}

	return nil
}
