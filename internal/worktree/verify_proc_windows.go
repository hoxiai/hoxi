//go:build windows

package worktree

import "os/exec"

func setVerifyProcessGroup(_ *exec.Cmd) {}

func killVerifyProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
