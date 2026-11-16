//go:build !windows

package subproc

import "os/exec"

func startTied(cmd *exec.Cmd, _ func(error)) error {
	return cmd.Start()
}
