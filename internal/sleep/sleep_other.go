//go:build !darwin

package sleep

import "log"

func preventSleepImpl() (release func(), err error) {
	log.Printf("[sleep] Sleep prevention not implemented on this platform (no-op)")
	return func() {}, nil
}
