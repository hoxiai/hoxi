//go:build !windows

package worktree

import (
	"os/exec"
	"syscall"
	"time"
)

const verifyTermGrace = time.Second

func setVerifyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killVerifyProcessGroup asks the whole group to stop, gives dev servers a
// short grace period to release their port, then kills what is left.
func killVerifyProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid > 0 && syscall.Kill(-pid, syscall.SIGTERM) == nil {
		deadline := time.Now().Add(verifyTermGrace)
		for time.Now().Before(deadline) && syscall.Kill(-pid, 0) == nil {
			time.Sleep(50 * time.Millisecond)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}
