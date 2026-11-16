//go:build windows

package opencode

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/hoxiai/hoxi/internal/subproc"
)

func configureChildProcess(cmd *exec.Cmd, attachStdio bool) {
	if cmd == nil {
		return
	}
	subproc.HideWindow(cmd)
	if !attachStdio {
		return
	}
	// In a -H windowsgui binary, os.Stdout / os.Stderr carry NULL (0) or invalid
	// handles unless explicitly redirected. Leaving cmd.Stdout / cmd.Stderr nil
	// in that case lets Go's os/exec open NUL (os.DevNull) safely instead of
	// passing an invalid handle to CreateProcess.
	if validWindowsStdHandle(os.Stdout) {
		cmd.Stdout = os.Stdout
	}
	if validWindowsStdHandle(os.Stderr) {
		cmd.Stderr = os.Stderr
	}
}

func validWindowsStdHandle(f *os.File) bool {
	if f == nil {
		return false
	}
	fd := f.Fd()
	if fd == 0 || fd == ^uintptr(0) {
		return false
	}
	ft, err := syscall.GetFileType(syscall.Handle(fd))
	return err == nil && ft != syscall.FILE_TYPE_UNKNOWN
}
