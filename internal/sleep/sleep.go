// Package sleep provides OS-specific sleep prevention during active file transfers.
package sleep

// PreventSleep prevents the system from sleeping while file transfers are active.
// Returns a release function that must be called to release the assertion.
func PreventSleep() (release func(), err error) {
	return preventSleepImpl()
}
