//go:build windows

package subproc

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideWindowMatchesConsolePresence(t *testing.T) {
	cmd := Command("git.exe", "status")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow not set")
	}
	noWindow := cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW != 0
	if noWindow == hasConsole() {
		t.Fatalf("CREATE_NO_WINDOW=%v but hasConsole=%v", noWindow, hasConsole())
	}
}

func TestHideWindowKeepsExistingFlags(t *testing.T) {
	cmd := exec.Command("git.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	HideWindow(cmd)
	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("existing flags dropped: %#x", cmd.SysProcAttr.CreationFlags)
	}
}
