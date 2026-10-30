//go:build windows

package subproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

var procGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	if !hasConsole() {
		cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	}
}

// hasConsole reports whether Hoxi itself is attached to a console. The `hoxi`
// CLI running in a terminal is, and its children (the OpenCode TUI included)
// must share that console, so CREATE_NO_WINDOW is only used when there is none:
// the desktop GUI, where a child would otherwise get a console window.
func hasConsole() bool {
	if procGetConsoleWindow.Find() != nil {
		return false
	}
	hwnd, _, _ := procGetConsoleWindow.Call()
	return hwnd != 0
}
