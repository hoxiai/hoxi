//go:build !windows

package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// setProcessGroup starts the CLI as the leader of its own process group, so
// stopping a turn also reaches the shells, dev servers and watchers it spawned.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroup delivers sig to every process in p's group. The group ID
// stays reserved while any member is alive, so it is safe after p is reaped.
func signalProcessGroup(p *os.Process, sig os.Signal) error {
	if p == nil {
		return nil
	}
	sysSig, ok := sig.(syscall.Signal)
	if !ok {
		return p.Signal(sig)
	}
	err := syscall.Kill(-p.Pid, sysSig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// descendantPIDs lists the live descendants of pid. CLIs such as Claude Code
// run each tool command in a process group of its own, which a group signal
// misses once the CLI can no longer clean up after itself.
func descendantPIDs(pid int) []int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=")
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(stdout, 4<<20))
	_ = cmd.Wait()
	children := make(map[int][]int)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		child, err1 := strconv.Atoi(fields[0])
		parent, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil {
			children[parent] = append(children[parent], child)
		}
	}
	var out []int
	queue := []int{pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out
}

func killPIDs(pids []int) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
