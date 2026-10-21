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

type procSnapshot struct {
	pid  int
	ppid int
	pgid int
	args string
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

func collectSubtreePIDs(root int, children map[int][]int) []int {
	var out []int
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		out = append(out, curr)
		for _, c := range children[curr] {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, c)
			}
		}
	}
	return out
}

func isAgentRuntimeProcessArgs(args string) bool {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return false
	}
	for _, tok := range fields {
		base := strings.ToLower(strings.TrimSpace(tok))
		if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
			base = base[idx+1:]
		}
		switch base {
		case "claude", "agy", "codebuddy", "cbc", "qodercli", "qoderclicn", "qodercn", "opencode":
			return true
		}
	}
	return false
}

func isNonToolHelperArgs(args string) bool {
	lower := strings.ToLower(strings.TrimSpace(args))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"agy-hook",
		"claude-hook",
		"gopls",
		"typescript-language-server",
		"vtsls",
		"pyright",
		"rust-analyzer",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func commandMatchNeedles(command string) []string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	seen := make(map[string]bool)
	var needles []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) < 3 || seen[s] {
			return
		}
		seen[s] = true
		needles = append(needles, s)
	}
	add(trimmed)
	if nl := strings.IndexByte(trimmed, '\n'); nl > 0 {
		add(trimmed[:nl])
	}
	for _, sub := range splitCommandChain(trimmed) {
		add(sub)
		fields := strings.Fields(sub)
		for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
			fields = fields[1:]
		}
		if len(fields) > 0 {
			cleaned := strings.Join(fields, " ")
			add(cleaned)
			if len(fields) >= 2 {
				add(fields[0] + " " + fields[1])
			}
		}
	}
	return needles
}

func subtreeMatchesNeedles(pids []int, byPID map[int]procSnapshot, needles []string) bool {
	if len(needles) == 0 {
		return false
	}
	for _, pid := range pids {
		args := byPID[pid].args
		if args == "" {
			continue
		}
		for _, n := range needles {
			if strings.Contains(args, n) {
				return true
			}
		}
	}
	return false
}

// stopChildToolProcesses terminates the child tool command process trees under
// the session's own CLI process (cliPID) whose command line matches command,
// without signalling the CLI itself, so the turn receives the interrupted tool
// result and continues. It never looks outside cliPID's tree (the shared
// `opencode serve` daemon serves every OpenCode session, its terminals and MCP
// servers) and kills nothing when no candidate matches command.
func stopChildToolProcesses(cliPID int, command string) int {
	if cliPID <= 1 {
		return 0
	}
	needles := commandMatchNeedles(command)
	if len(needles) == 0 {
		return 0
	}
	byPID, children := readProcessSnapshots()
	if len(byPID) == 0 {
		return 0
	}
	selfPID := os.Getpid()
	selfPGID, _ := syscall.Getpgid(selfPID)
	protected := map[int]bool{
		1:       true,
		selfPID: true,
	}
	if selfPGID > 1 {
		protected[selfPGID] = true
	}

	if _, ok := byPID[cliPID]; !ok {
		return 0
	}
	var candidateRoots []int
	protected[cliPID] = true
	if p := byPID[cliPID]; p.pgid > 1 {
		protected[p.pgid] = true
	}
	parentPID := cliPID
	// If cliPID is a wrapper (e.g. sandbox-exec or sh wrapper) whose single
	// child is the agent runtime binary itself, step into that binary.
	if direct := children[parentPID]; len(direct) == 1 {
		only := direct[0]
		if isAgentRuntimeProcessArgs(byPID[only].args) && len(children[only]) > 0 {
			protected[only] = true
			parentPID = only
		}
	}
	for _, c := range children[parentPID] {
		if !protected[c] && !isNonToolHelperArgs(byPID[c].args) {
			candidateRoots = append(candidateRoots, c)
		}
	}

	// Only subtrees whose command line matches are stopped; no match means
	// nothing is killed (never fall back to every candidate).
	var selectedRoots []int
	for _, root := range candidateRoots {
		sub := collectSubtreePIDs(root, children)
		if subtreeMatchesNeedles(sub, byPID, needles) {
			selectedRoots = append(selectedRoots, root)
		}
	}
	if len(selectedRoots) == 0 {
		return 0
	}

	targetSet := make(map[int]bool)
	var targetPIDs []int
	targetPGIDs := make(map[int]bool)
	for _, root := range selectedRoots {
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
