//go:build !windows

package subproc

import "os/exec"

func hideWindow(*exec.Cmd) {}
