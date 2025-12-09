//go:build !darwin

package ui

import "log"

func showQuitConfirmationDialog() {
	log.Println("[ui] Quit confirmation dialog not implemented on this platform")
}
