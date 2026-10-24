//go:build !windows

package runner

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
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
		case "claude", "agy", "gemini", "codebuddy", "cbc", "qodercli", "qoderclicn", "qodercn", "opencode":
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

// toolStopScope describes which process tree a per-tool stop may touch.
type toolStopScope struct {
	// stepIntoRuntime unwraps a single wrapper child (sandbox-exec, sh) that
	// directly runs the agent CLI. Only per-session CLI trees need it.
	stepIntoRuntime bool
}

// stopChildToolProcesses terminates the child tool command process trees under
// the session's own CLI process (cliPID) whose command line matches command,
// without signalling the CLI itself, so the turn receives the interrupted tool
// result and continues. It never looks outside cliPID's tree and kills nothing
// when no candidate matches command.
func stopChildToolProcesses(cliPID int, command string) int {
	return stopToolProcessesUnder(cliPID, command, toolStopScope{stepIntoRuntime: true})
}

func matchesOpenCodeServePort(args, port string) bool {
	if port == "" || !strings.Contains(args, "opencode") || !strings.Contains(args, "serve") {
		return false
	}
	return strings.Contains(args, "--port "+port) || strings.Contains(args, "--port="+port)
}

func selectOpenCodeDaemonPID(byPID map[int]procSnapshot, children map[int][]int, port string) int {
	matching := make(map[int]bool)
	for pid, snap := range byPID {
		if matchesOpenCodeServePort(snap.args, port) {
			matching[pid] = true
		}
	}
	if len(matching) == 0 {
		return 0
	}
	bestPID := 0
	for pid := range matching {
		hasMatchingDescendant := false
		for _, subPID := range collectSubtreePIDs(pid, children) {
			if subPID != pid && matching[subPID] {
				hasMatchingDescendant = true
				break
			}
		}
		if !hasMatchingDescendant && (bestPID == 0 || pid > bestPID) {
			bestPID = pid
		}
	}
	if bestPID != 0 {
		return bestPID
	}
	for pid := range matching {
		if bestPID == 0 || pid > bestPID {
			bestPID = pid
		}
	}
	return bestPID
}

// openCodeDaemonPID resolves the pid of the `opencode serve` process that owns
// the engine port in baseURL, so a per-tool stop can look inside its children.
func openCodeDaemonPID(baseURL string) int {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return 0
	}
	port := strings.TrimSpace(u.Port())
	if port == "" {
		return 0
	}
	byPID, children := readProcessSnapshots()
	return selectOpenCodeDaemonPID(byPID, children, port)
}

func stopToolProcessesUnder(rootPID int, command string, scope toolStopScope) int {
	if rootPID <= 1 {
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

	if _, ok := byPID[rootPID]; !ok {
		return 0
	}
	var candidateRoots []int
	protected[rootPID] = true
	if p := byPID[rootPID]; p.pgid > 1 {
		protected[p.pgid] = true
	}
	parentPID := rootPID
	// If rootPID is a wrapper (e.g. sandbox-exec or sh wrapper) whose single
	// child is the agent runtime binary itself, step into that binary.
	if scope.stepIntoRuntime {
		if direct := children[parentPID]; len(direct) == 1 {
			only := direct[0]
			if isAgentRuntimeProcessArgs(byPID[only].args) && len(children[only]) > 0 {
				protected[only] = true
				parentPID = only
			}
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
	return killSubtrees(selectedRoots, byPID, children, protected)
}

func extractStopMarkerCallID(marker string) string {
	line := strings.TrimSpace(marker)
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = strings.TrimSpace(line[:nl])
	}
	if !strings.HasPrefix(line, stopMarkerPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(line, stopMarkerPrefix)
	idx := strings.LastIndexByte(rest, ':')
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(rest[idx+1:])
}

func subtreeMatchesRegexp(pids []int, byPID map[int]procSnapshot, re *regexp.Regexp) bool {
	if re == nil {
		return false
	}
	for _, pid := range pids {
		if args := byPID[pid].args; args != "" && re.MatchString(args) {
			return true
		}
	}
	return false
}

// stopOpenCodeToolProcessByMarker kills the single subtree under daemonPID
// whose command line carries marker. It never signals the daemon and kills
// nothing unless exactly one subtree matches.
func stopOpenCodeToolProcessByMarker(daemonPID int, marker string) int {
	if daemonPID <= 1 || strings.TrimSpace(marker) == "" {
		return 0
	}
	byPID, children := readProcessSnapshots()
	if len(byPID) == 0 {
		return 0
	}
	selfPID := os.Getpid()
	selfPGID, _ := syscall.Getpgid(selfPID)
	protected := map[int]bool{
		1:         true,
		selfPID:   true,
		daemonPID: true,
	}
	if selfPGID > 1 {
		protected[selfPGID] = true
	}
	if p := byPID[daemonPID]; p.pgid > 1 {
		protected[p.pgid] = true
	}

	var candidateRoots []int
	for _, root := range children[daemonPID] {
		if protected[root] {
			continue
		}
		args := byPID[root].args
		if isAgentRuntimeProcessArgs(args) || isNonToolHelperArgs(args) {
			protected[root] = true
			continue
		}
		candidateRoots = append(candidateRoots, root)
	}

	exactMarker := strings.TrimSpace(marker)
	if nl := strings.IndexByte(exactMarker, '\n'); nl >= 0 {
		exactMarker = strings.TrimSpace(exactMarker[:nl])
	}

	var roots []int
	if exactMarker != "" && !strings.HasPrefix(exactMarker, stopMarkerPrefix+":") {
		for _, root := range candidateRoots {
			// Whole-token match: a stop for call_1 must never hit call_12.
			if subtreeCarriesStopMarker(collectSubtreePIDs(root, children), byPID, exactMarker) {
				roots = append(roots, root)
			}
		}
	}
	if len(roots) == 0 {
		if callID := extractStopMarkerCallID(marker); callID != "" {
			re, err := regexp.Compile(regexp.QuoteMeta(stopMarkerPrefix) + `[^:\s\\]+:` + regexp.QuoteMeta(callID) + `(?:$|\\012|[^A-Za-z0-9_-])`)
			if err == nil {
				for _, root := range candidateRoots {
					if subtreeMatchesRegexp(collectSubtreePIDs(root, children), byPID, re) {
						roots = append(roots, root)
					}
				}
			}
		}
	}
	if len(roots) != 1 {
		return 0
	}
	// Only the tagged shell itself has the stop trap. Anything else falls
	// back to killing the subtree.
	if root := roots[0]; argsCarryStopMarker(byPID[root].args, marker) {
		return stopOpenCodeToolShell(root, byPID, children, protected)
	}
	return killSubtrees(roots, byPID, children, protected)
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

// subtreeCarriesStopMarker reports whether any process in pids carries marker
// as a whole token. A bare substring match would let the tag of `call_1` hit
// `call_12`, so the marker must not run on into further ID characters; what
// follows the real tag is the command's line break, which `ps` prints as an
// escape (`\012`) or `?`.
func subtreeCarriesStopMarker(pids []int, byPID map[int]procSnapshot, marker string) bool {
	for _, pid := range pids {
		if argsCarryStopMarker(byPID[pid].args, marker) {
			return true
		}
	}
	return false
}

func argsCarryStopMarker(args, marker string) bool {
	if marker == "" {
		return false
	}
	for offset := 0; offset < len(args); {
		i := strings.Index(args[offset:], marker)
		if i < 0 {
			return false
		}
		end := offset + i + len(marker)
		if end == len(args) || !isStopMarkerIDByte(args[end]) {
			return true
		}
		offset += i + 1
	}
	return false
}

// isStopMarkerIDByte reports bytes that may continue an engine session or
// tool call ID (`ses_…`, `call_…`, `toolu_…`, `functions.bash:0`).
func isStopMarkerIDByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return b == '_' || b == '-' || b == '.' || b == ':'
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
