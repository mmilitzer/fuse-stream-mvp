//go:build linux

package drag

import (
	"fmt"
	"log"
)

// StartFileDrag is not supported on Linux for M3 (macOS-only MVP).
// Linux users should use "Reveal in Files" functionality instead.
func StartFileDrag(path string) error {
	log.Printf("[drag] Linux drag-and-drop is not supported in M3 (macOS-only). Path: %s", path)
	return fmt.Errorf("drag-and-drop not supported on Linux (M3 macOS-only)")
}
