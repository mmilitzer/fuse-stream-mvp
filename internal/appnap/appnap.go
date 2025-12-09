// Package appnap provides App Nap prevention for macOS to prevent process throttling.
// This is different from sleep prevention - it prevents macOS from throttling the app
// when it loses focus (App Nap), which is critical for FUSE filesystem operations.
package appnap

// PreventAppNap prevents macOS from throttling this process via App Nap.
// Returns a release function that must be called to release the activity token.
// This uses NSProcessInfo beginActivityWithOptions which is different from
// IOPMAssertion (which prevents system sleep but not App Nap throttling).
func PreventAppNap(reason string) (release func(), err error) {
	return preventAppNapImpl(reason)
}
