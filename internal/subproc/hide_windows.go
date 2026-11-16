//go:build windows

package subproc

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	procGetConsoleWindow = kernel32DLL.NewProc("GetConsoleWindow")
	windowsUTF8InitOnce  sync.Once
)

func init() {
	ensureWindowsUTF8Defaults()
}

func ensureWindowsUTF8Defaults() {
	windowsUTF8InitOnce.Do(func() {
		if hasConsole() {
			if procSetConsoleCP.Find() == nil {
				_, _, _ = procSetConsoleCP.Call(cpUTF8)
			}
			if procSetConsoleOutputCP.Find() == nil {
				_, _, _ = procSetConsoleOutputCP.Call(cpUTF8)
			}
		}
		setDefaultEnv := func(key, value string) {
			if strings.TrimSpace(os.Getenv(key)) == "" {
				_ = os.Setenv(key, value)
			}
		}
		if strings.TrimSpace(os.Getenv("LC_ALL")) == "" {
			setDefaultEnv("LANG", "C.UTF-8")
			setDefaultEnv("LC_CTYPE", "UTF-8")
		}
		setDefaultEnv("PYTHONUTF8", "1")
		setDefaultEnv("PYTHONIOENCODING", "utf-8")
		setDefaultEnv("LESSCHARSET", "utf-8")
		if scriptPath := WindowsBashEnvScriptPath(); scriptPath != "" {
			setDefaultEnv("BASH_ENV", scriptPath)
		}
	})
}

func hideWindow(cmd *exec.Cmd) {
	ensureWindowsUTF8Defaults()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	if !hasConsole() {
		cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	}
	if len(cmd.Env) > 0 {
		cmd.Env = WithUTF8Env(cmd.Env)
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
