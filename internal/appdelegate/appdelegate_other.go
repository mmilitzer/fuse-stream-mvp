//go:build !darwin

package appdelegate

import "log"

// Install is a no-op on non-Darwin platforms
func Install(checkUploads CheckActiveUploadsFunc, unmount UnmountFunc) {
	log.Println("[appdelegate] App delegate not supported on this platform")
}

// ReplyToShouldTerminate is a no-op on non-Darwin platforms
func ReplyToShouldTerminate(shouldTerminate bool) {
	// No-op
}
