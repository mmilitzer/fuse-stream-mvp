//go:build !darwin

package dialog

import "log"

func confirmQuitImpl() bool {
	// On non-macOS platforms, just log and return true (allow quit)
	log.Println("[dialog] Quit confirmation not implemented on this platform - allowing quit")
	return true
}
