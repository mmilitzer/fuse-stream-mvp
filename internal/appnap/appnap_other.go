//go:build !darwin

package appnap

// preventAppNapImpl is a no-op on non-macOS platforms.
func preventAppNapImpl(reason string) (release func(), err error) {
	// No-op: App Nap is macOS-specific
	return func() {}, nil
}
