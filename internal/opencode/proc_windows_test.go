//go:build windows

package opencode

import (
	"os/exec"
	"testing"
)

// The daemon must not get a visible console window; subproc decides the
// creation flags, configureChildProcess must keep them.
func TestConfigureChildProcessHidesWindow(t *testing.T) {
	for _, attach := range []bool{false, true} {
		cmd := exec.Command("opencode.exe", "serve")
		configureChildProcess(cmd, attach)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
			t.Fatalf("attach=%v: window not hidden", attach)
		}
	}
}
