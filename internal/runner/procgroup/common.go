package procgroup

import (
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/runner/shellpolicy"
)

const (
	StopMarkerPrefix = "# hoxi-stop:"
	stopMarkerPrefix = StopMarkerPrefix
)

type Snapshot struct {
	PID        int
	PPID       int
	PGID       int
	CreateTime uint64
	Args       string
}

func CollectSubtreePIDs(root int, children map[int][]int) []int {
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

func IsAgentRuntimeProcessArgs(args string) bool {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return false
	}
	for _, tok := range fields {
		base := strings.Trim(strings.ToLower(strings.TrimSpace(tok)), `"'`)
		if idx := strings.LastIndexAny(base, `/\`); idx >= 0 {
			base = base[idx+1:]
		}
		for _, ext := range []string{".exe", ".cmd", ".bat"} {
			base = strings.TrimSuffix(base, ext)
		}
		switch base {
		case "claude", "agy", "gemini", "codebuddy", "cbc", "qodercli", "qoderclicn", "qodercn", "opencode":
			return true
		}
	}
	return false
}

func IsNonToolHelperArgs(args string) bool {
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
	for _, sub := range shellpolicy.SplitCommandChain(trimmed) {
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

func subtreeMatchesNeedles(pids []int, byPID map[int]Snapshot, needles []string) bool {
	if len(needles) == 0 {
		return false
	}
	for _, pid := range pids {
		args := byPID[pid].Args
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

func HasLiveChildToolProcess(rootPID int, byPID map[int]Snapshot, children map[int][]int, commands ...string) bool {
	if rootPID <= 1 || len(byPID) == 0 || len(commands) == 0 {
		return false
	}
	var needles []string
	for _, cmd := range commands {
		needles = append(needles, commandMatchNeedles(cmd)...)
	}
	if len(needles) == 0 {
		return false
	}
	parentPID := rootPID
	if direct := children[parentPID]; len(direct) == 1 {
		only := direct[0]
		if IsAgentRuntimeProcessArgs(byPID[only].Args) {
			parentPID = only
		}
	}
	for _, c := range children[parentPID] {
		if c <= 1 || IsNonToolHelperArgs(byPID[c].Args) {
			continue
		}
		if subtreeMatchesNeedles(CollectSubtreePIDs(c, children), byPID, needles) {
			return true
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

// StopChildToolProcesses terminates the child tool command process trees under
// the session's own CLI process (cliPID) whose command line matches command,
// without signalling the CLI itself, so the turn receives the interrupted tool
// result and continues. It never looks outside cliPID's tree and kills nothing
// when no candidate matches command.
func StopChildToolProcesses(cliPID int, command string) int {
	return stopToolProcessesUnder(cliPID, command, toolStopScope{stepIntoRuntime: true})
}

func matchesOpenCodeServePort(args, port string) bool {
	lower := strings.ToLower(args)
	if port == "" || !strings.Contains(lower, "opencode") || !strings.Contains(lower, "serve") {
		return false
	}
	return strings.Contains(args, "--port "+port) || strings.Contains(args, "--port="+port)
}

func SelectOpenCodeDaemonPID(byPID map[int]Snapshot, children map[int][]int, port string) int {
	matching := make(map[int]bool)
	for pid, snap := range byPID {
		if matchesOpenCodeServePort(snap.Args, port) {
			matching[pid] = true
		}
	}
	if len(matching) == 0 {
		return 0
	}
	bestPID := 0
	for pid := range matching {
		hasMatchingDescendant := false
		for _, subPID := range CollectSubtreePIDs(pid, children) {
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

// OpenCodeDaemonPID resolves the pid of the `opencode serve` process that owns
// the engine port in baseURL, so a per-tool stop can look inside its children.
func OpenCodeDaemonPID(baseURL string) int {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return 0
	}
	port := strings.TrimSpace(u.Port())
	if port == "" {
		return 0
	}
	byPID, children := ReadProcessSnapshots()
	return SelectOpenCodeDaemonPID(byPID, children, port)
}

func stopToolProcessesUnder(rootPID int, command string, scope toolStopScope) int {
	if rootPID <= 1 {
		return 0
	}
	needles := commandMatchNeedles(command)
	if len(needles) == 0 {
		return 0
	}
	byPID, children := ReadProcessSnapshots()
	if len(byPID) == 0 {
		return 0
	}
	selfPID := os.Getpid()
	selfPGID := SelfProcessGroupID()
	protected := map[int]bool{
		1:       true,
		4:       true,
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
	if p := byPID[rootPID]; p.PGID > 1 {
		protected[p.PGID] = true
	}
	parentPID := rootPID
	if scope.stepIntoRuntime {
		if direct := children[parentPID]; len(direct) == 1 {
			only := direct[0]
			if IsAgentRuntimeProcessArgs(byPID[only].Args) && len(children[only]) > 0 {
				protected[only] = true
				parentPID = only
			}
		}
	}
	for _, c := range children[parentPID] {
		if !protected[c] && !IsNonToolHelperArgs(byPID[c].Args) {
			candidateRoots = append(candidateRoots, c)
		}
	}

	// Only subtrees whose command line matches are stopped; no match means
	// nothing is killed (never fall back to every candidate).
	var selectedRoots []int
	for _, root := range candidateRoots {
		sub := CollectSubtreePIDs(root, children)
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

func subtreeMatchesRegexp(pids []int, byPID map[int]Snapshot, re *regexp.Regexp) bool {
	if re == nil {
		return false
	}
	for _, pid := range pids {
		if args := byPID[pid].Args; args != "" && re.MatchString(args) {
			return true
		}
	}
	return false
}

// StopOpenCodeToolProcessByMarker kills the single subtree under daemonPID
// whose command line carries marker. It never signals the daemon and kills
// nothing unless exactly one subtree matches.
func StopOpenCodeToolProcessByMarker(daemonPID int, marker string) int {
	if daemonPID <= 1 || strings.TrimSpace(marker) == "" {
		return 0
	}
	byPID, children := ReadProcessSnapshots()
	if len(byPID) == 0 {
		return 0
	}
	selfPID := os.Getpid()
	selfPGID := SelfProcessGroupID()
	protected := map[int]bool{
		1:         true,
		4:         true,
		selfPID:   true,
		daemonPID: true,
	}
	if selfPGID > 1 {
		protected[selfPGID] = true
	}
	if p := byPID[daemonPID]; p.PGID > 1 {
		protected[p.PGID] = true
	}

	var candidateRoots []int
	for _, root := range children[daemonPID] {
		if protected[root] {
			continue
		}
		args := byPID[root].Args
		if IsAgentRuntimeProcessArgs(args) || IsNonToolHelperArgs(args) {
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
			if subtreeCarriesStopMarker(CollectSubtreePIDs(root, children), byPID, exactMarker) {
				roots = append(roots, root)
			}
		}
	}
	if len(roots) == 0 {
		if callID := extractStopMarkerCallID(marker); callID != "" {
			re, err := regexp.Compile(regexp.QuoteMeta(stopMarkerPrefix) + `[^:\s\\]+:` + regexp.QuoteMeta(callID) + `(?:$|\\012|[^A-Za-z0-9_-])`)
			if err == nil {
				for _, root := range candidateRoots {
					if subtreeMatchesRegexp(CollectSubtreePIDs(root, children), byPID, re) {
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
	if root := roots[0]; ArgsCarryStopMarker(byPID[root].Args, marker) {
		return stopOpenCodeToolShell(root, byPID, children, protected)
	}
	return killSubtrees(roots, byPID, children, protected)
}

func subtreeCarriesStopMarker(pids []int, byPID map[int]Snapshot, marker string) bool {
	for _, pid := range pids {
		if ArgsCarryStopMarker(byPID[pid].Args, marker) {
			return true
		}
	}
	return false
}

func ArgsCarryStopMarker(args, marker string) bool {
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

func isStopMarkerIDByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return b == '_' || b == '-' || b == '.' || b == ':'
}
