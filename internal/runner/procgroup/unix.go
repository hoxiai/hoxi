//go:build !windows

package procgroup

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

	"github.com/hoxiai/hoxi/internal/subproc"
)

// SetProcessGroup starts the CLI as the leader of its own process group, so
// stopping a turn also reaches the shells, dev servers and watchers it spawned.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// SignalProcessGroup delivers sig to every process in p's group. The group ID
// stays reserved while any member is alive, so it is safe after p is reaped.
func SignalProcessGroup(p *os.Process, sig os.Signal) error {
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

func ProcessGroupAlive(p *os.Process) bool {
	if p == nil || p.Pid <= 1 {
		return false
	}
	err := syscall.Kill(-p.Pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func SelfProcessGroupID() int {
	pgid, _ := syscall.Getpgid(os.Getpid())
	return pgid
}

// DescendantPIDs lists the live descendants of pid. CLIs such as Claude Code
// run each tool command in a process group of its own, which a group signal
// misses once the CLI can no longer clean up after itself.
func DescendantPIDs(pid int) []int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, "ps", "-axo", "pid=,ppid=")
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

func KillPIDs(pids []int) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func ReadProcessSnapshots() (map[int]Snapshot, map[int][]int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,pgid=,args=")
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return nil, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(stdout, 4<<20))
	_ = cmd.Wait()

	byPID := make(map[int]Snapshot)
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
		byPID[pid] = Snapshot{PID: pid, PPID: ppid, PGID: pgid, Args: args}
		children[ppid] = append(children[ppid], pid)
	}
	return byPID, children
}

const openCodeShellExitGrace = 2 * time.Second

func stopOpenCodeToolShell(shell int, byPID map[int]Snapshot, children map[int][]int, protected map[int]bool) int {
	if shell <= 1 || protected[shell] {
		return 0
	}
	shellPGID := byPID[shell].PGID
	if err := syscall.Kill(shell, syscall.SIGUSR1); err != nil {
		return 0
	}
	signalled := map[int]time.Time{}
	signal := func(pid int, sig syscall.Signal) {
		if pgid := byPID[pid].PGID; pgid > 1 && pgid != shellPGID && !protected[pgid] {
			_ = syscall.Kill(-pgid, sig)
		}
		_ = syscall.Kill(pid, sig)
	}
	deadline := time.Now().Add(openCodeShellExitGrace)
	for ProcessAlive(shell) {
		if time.Now().After(deadline) {
			byPID, children = ReadProcessSnapshots()
			return len(signalled) + killSubtrees([]int{shell}, byPID, children, protected)
		}
		for _, pid := range CollectSubtreePIDs(shell, children)[1:] {
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
		byPID, children = ReadProcessSnapshots()
	}
	if shellPGID > 1 && !protected[shellPGID] {
		byPID, _ = ReadProcessSnapshots()
		for pid, snap := range byPID {
			if snap.PGID == shellPGID && pid > 1 && !protected[pid] {
				signalled[pid] = time.Now()
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	return len(signalled) + 1
}

func ProcessAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func killSubtrees(roots []int, byPID map[int]Snapshot, children map[int][]int, protected map[int]bool) int {
	targetSet := make(map[int]bool)
	var targetPIDs []int
	targetPGIDs := make(map[int]bool)
	for _, root := range roots {
		for _, pid := range CollectSubtreePIDs(root, children) {
			if pid <= 1 || protected[pid] || targetSet[pid] {
				continue
			}
			targetSet[pid] = true
			targetPIDs = append(targetPIDs, pid)
			if pgid := byPID[pid].PGID; pgid > 1 && !protected[pgid] {
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
