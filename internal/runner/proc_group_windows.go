//go:build windows

package runner

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {}

// Windows has no POSIX process groups; only the CLI itself is signalled.
func signalProcessGroup(p *os.Process, sig os.Signal) error {
	if p == nil {
		return nil
	}
	if sig == os.Kill {
		return p.Kill()
	}
	return p.Signal(sig)
}

func descendantPIDs(pid int) []int { return nil }

func killPIDs(pids []int) {}
