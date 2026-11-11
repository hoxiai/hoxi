//go:build windows

package browser

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259 // STATUS_PENDING returned by GetExitCodeProcess

func isLocalPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false
		}
		return true
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == windowsStillActive
}

func isOSProfileLockHeld(profile string) bool {
	f, err := os.Open(filepath.Join(profile, "lockfile"))
	if err == nil {
		_ = f.Close()
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
