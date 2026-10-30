//go:build windows

package runner

import (
	"os/exec"
	"syscall"
	"testing"
)

// Runtime CLIs keep their own process group and still hide their window.
func TestSetProcessGroupHidesWindow(t *testing.T) {
	cmd := exec.Command("claude.exe")
	setProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("window not hidden")
	}
	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("CREATE_NEW_PROCESS_GROUP missing, flags=%#x", cmd.SysProcAttr.CreationFlags)
	}
}
