//go:build !darwin && !linux

package drag

import "fmt"

// StartFileDrag is not supported on non-macOS/Linux platforms.
func StartFileDrag(path string) error {
	return fmt.Errorf("native drag not supported on this OS")
}
