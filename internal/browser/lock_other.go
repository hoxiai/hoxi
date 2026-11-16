//go:build !darwin && !linux && !windows

package browser

func isLocalPIDAlive(pid int) bool {
	return pid > 0
}

func isOSProfileLockHeld(_ string) bool {
	return false
}
