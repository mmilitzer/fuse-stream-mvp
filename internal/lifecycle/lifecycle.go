// Package lifecycle provides NSApplication lifecycle event monitoring
// to detect when the app becomes active/inactive (foreground/background).
package lifecycle

// ActivationCallback is called when the app activation state changes.
// active=true when app becomes active (foreground), false when it resigns active (background).
type ActivationCallback func(active bool)

// ObserveActivation starts observing NSApplication activation events.
// The callback will be invoked whenever the app transitions between active/inactive states.
// Returns a cleanup function that should be called to stop observing.
func ObserveActivation(callback ActivationCallback) (cleanup func(), err error) {
	return observeActivationImpl(callback)
}
