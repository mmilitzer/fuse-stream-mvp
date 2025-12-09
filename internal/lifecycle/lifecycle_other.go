//go:build !darwin

package lifecycle

// observeActivationImpl is a no-op on non-macOS platforms.
func observeActivationImpl(callback ActivationCallback) (cleanup func(), err error) {
	// No-op: NSApplication lifecycle is macOS-specific
	return func() {}, nil
}
