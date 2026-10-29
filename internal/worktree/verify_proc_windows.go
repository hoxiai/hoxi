//go:build windows

package worktree

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func setVerifyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func killVerifyProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if pid := cmd.Process.Pid; pid > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
		cancel()
	}
	_ = cmd.Process.Kill()
}
