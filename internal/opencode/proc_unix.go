//go:build !windows

package opencode

import (
	"os"
	"os/exec"
)

func configureChildProcess(cmd *exec.Cmd, attachStdio bool) {
	if cmd == nil || !attachStdio {
		return
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
}
