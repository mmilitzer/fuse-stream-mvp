//go:build !darwin

package drag

import "fmt"

// StartFileDrag is not supported on non-macOS platforms.
func StartFileDrag(path string) error {
	return fmt.Errorf("native drag not supported on this OS")
}
