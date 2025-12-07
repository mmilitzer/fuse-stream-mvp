package sleep

// TODO M2+: Implement sleep prevention when file handles are open
//
// macOS: Use IOKit power assertions via cgo
// Example:
//   IOPMAssertionCreateWithName(kIOPMAssertionTypeNoIdleSleep, ...)
//
// Linux: Use systemd-inhibit or similar mechanism
// Example:
//   systemd-inhibit --what=sleep --why="File transfer in progress" ...
//
// For now, this is a no-op stub.

func PreventSleep() (release func(), err error) {
	// No-op: return a dummy release function
	return func() {}, nil
}
