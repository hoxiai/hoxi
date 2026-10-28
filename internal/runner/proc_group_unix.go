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

func selfProcessGroupID() int {
	pgid, _ := syscall.Getpgid(os.Getpid())
	return pgid
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

func readProcessSnapshots() (map[int]procSnapshot, map[int][]int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,pgid=,args=")
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return nil, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(stdout, 4<<20))
	_ = cmd.Wait()

	byPID := make(map[int]procSnapshot)
	children := make(map[int][]int)
	for _, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		pgid, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil || pid <= 0 {
			continue
		}
		// Extract args after the 3 numeric columns.
		rest := line
		for i := 0; i < 3; i++ {
			rest = strings.TrimLeft(rest, " \t")
			idx := strings.IndexAny(rest, " \t")
			if idx < 0 {
				rest = ""
				break
			}
			rest = rest[idx:]
		}
		args := strings.TrimSpace(rest)
		byPID[pid] = procSnapshot{pid: pid, ppid: ppid, pgid: pgid, args: args}
		children[ppid] = append(children[ppid], pid)
	}
	return byPID, children
}

// openCodeShellExitGrace bounds how long a stop waits for the tagged shell to
// exit through its stop trap before killing it outright.
const openCodeShellExitGrace = 2 * time.Second

// stopOpenCodeToolShell ends one OpenCode bash command so that its shell exits
// with a code instead of dying from a signal (see openCodeStopTrap). The shell
// gets SIGUSR1, which its trap holds until the running command is gone, and
// only its descendants are terminated. Once the shell has exited, whatever is
// left of its process group (background jobs that still hold its output) is
// killed too. A shell that does not exit in time, because it predates the trap
// or the command replaced it, is killed with its subtree as before.
func stopOpenCodeToolShell(shell int, byPID map[int]procSnapshot, children map[int][]int, protected map[int]bool) int {
	if shell <= 1 || protected[shell] {
		return 0
	}
	shellPGID := byPID[shell].pgid
	if err := syscall.Kill(shell, syscall.SIGUSR1); err != nil {
		return 0
	}
	signalled := map[int]time.Time{}
	signal := func(pid int, sig syscall.Signal) {
		if pgid := byPID[pid].pgid; pgid > 1 && pgid != shellPGID && !protected[pgid] {
			_ = syscall.Kill(-pgid, sig)
		}
		_ = syscall.Kill(pid, sig)
	}
	deadline := time.Now().Add(openCodeShellExitGrace)
	for processAlive(shell) {
		if time.Now().After(deadline) {
			byPID, children = readProcessSnapshots()
			return len(signalled) + killSubtrees([]int{shell}, byPID, children, protected)
		}
		for _, pid := range collectSubtreePIDs(shell, children)[1:] {
			if pid <= 1 || protected[pid] {
				continue
			}
			first, seen := signalled[pid]
			switch {
			case !seen:
				signalled[pid] = time.Now()
				signal(pid, syscall.SIGTERM)
			case time.Since(first) > 150*time.Millisecond:
				signal(pid, syscall.SIGKILL)
			}
		}
		time.Sleep(15 * time.Millisecond)
		byPID, children = readProcessSnapshots()
	}
	// Background jobs outlive the shell in its process group and keep its
	// output open, which also holds the tool result back.
	if shellPGID > 1 && !protected[shellPGID] {
		byPID, _ = readProcessSnapshots()
		for pid, snap := range byPID {
			if snap.pgid == shellPGID && pid > 1 && !protected[pid] {
				signalled[pid] = time.Now()
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	return len(signalled) + 1
}

func processAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func killSubtrees(roots []int, byPID map[int]procSnapshot, children map[int][]int, protected map[int]bool) int {
	targetSet := make(map[int]bool)
	var targetPIDs []int
	targetPGIDs := make(map[int]bool)
	for _, root := range roots {
		for _, pid := range collectSubtreePIDs(root, children) {
			if pid <= 1 || protected[pid] || targetSet[pid] {
				continue
			}
			targetSet[pid] = true
			targetPIDs = append(targetPIDs, pid)
			if pgid := byPID[pid].pgid; pgid > 1 && !protected[pgid] {
				targetPGIDs[pgid] = true
			}
		}
	}
	if len(targetPIDs) == 0 {
		return 0
	}

	for pgid := range targetPGIDs {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}
	for _, pid := range targetPIDs {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range targetPIDs {
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				alive = true
				break
			}
		}
		if !alive {
			return len(targetPIDs)
		}
		time.Sleep(15 * time.Millisecond)
	}

	for pgid := range targetPGIDs {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	for _, pid := range targetPIDs {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return len(targetPIDs)
}
