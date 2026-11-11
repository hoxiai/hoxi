//go:build darwin || linux

package browser

import (
	"errors"
	"syscall"
)

func isLocalPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}

func isOSProfileLockHeld(_ string) bool {
	return false
}
