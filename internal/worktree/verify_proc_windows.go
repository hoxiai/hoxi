//go:build windows

package worktree

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

func setVerifyProcessGroup(cmd *exec.Cmd) {
	subproc.HideWindow(cmd)
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func killVerifyProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if pid := cmd.Process.Pid; pid > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = subproc.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
		cancel()
	}
	_ = cmd.Process.Kill()
}
