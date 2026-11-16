//go:build windows

package reclaim

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	supported                        = true
	stillActive                      = 259 // STATUS_PENDING returned by GetExitCodeProcess
	processCommandLineInformation    = 60  // ProcessCommandLineInformation in NtQueryInformationProcess
	maxProcessCommandLineBufferBytes = 1 << 20
)

// inspectProcess reads the creation time and command-line argv of pid from the
// Windows kernel. Creation time has 100-nanosecond resolution, so a recycled
// PID never matches an earlier process incarnation.
func inspectProcess(pid int) (procInfo, error) {
	if pid <= 0 {
		return procInfo{}, errNoProcess
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return procInfo{}, errNoProcess
		}
		return procInfo{}, err
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return procInfo{}, err
	}
	if exitCode != stillActive {
		return procInfo{}, errNoProcess
	}

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return procInfo{}, err
	}
	info := procInfo{
		start: fmt.Sprintf("%d:%d", creation.HighDateTime, creation.LowDateTime),
	}
	if args, err := readProcessCommandLineArgs(h); err == nil {
		info.args = args
	}
	return info, nil
}

func readProcessCommandLineArgs(h windows.Handle) ([]string, error) {
	var retLen uint32
	_ = windows.NtQueryInformationProcess(h, processCommandLineInformation, nil, 0, &retLen)
	if retLen == 0 || retLen > maxProcessCommandLineBufferBytes {
		return nil, errors.New("command line unavailable")
	}
	buf := make([]byte, retLen)
	if err := windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&buf[0]), retLen, &retLen); err != nil {
		return nil, err
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	cmdLine := strings.TrimSpace(us.String())
	if cmdLine == "" {
		return nil, errors.New("empty command line")
	}
	return windows.DecomposeCommandLine(cmdLine)
}

func signalProcess(pid int, _ syscall.Signal) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return syscall.ESRCH
		}
		return err
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err == nil && exitCode != stillActive {
		return syscall.ESRCH
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			if gErr := windows.GetExitCodeProcess(h, &exitCode); gErr == nil && exitCode != stillActive {
				return nil
			}
		}
		return err
	}
	return nil
}

// lockFile takes an exclusive file lock on path using LockFileEx, polling so a
// stuck holder can delay startup by at most timeout.
func lockFile(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	deadline := time.Now().Add(timeout)
	for {
		var ol windows.Overlapped
		err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
		if err == nil {
			return func() {
				var unlockOl windows.Overlapped
				_ = windows.UnlockFileEx(h, 0, 1, 0, &unlockOl)
				_ = f.Close()
			}, nil
		}
		if (!errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING)) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ownedByUser(info fs.FileInfo) bool {
	return info != nil
}
