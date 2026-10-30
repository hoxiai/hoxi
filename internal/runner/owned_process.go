package runner

import (
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Owned processes let a session stop what it started itself (a dev server it
// launched with `nohup … &`, a watcher, a tunnel) without asking, while a
// kill that reaches anything else still asks.
//
// Ownership is by process group. Each shell a session runs leads a process
// group of its own, and background jobs keep that group after the shell has
// exited and they were reparented to launchd. While a session's shell command
// runs, Hoxi finds that shell (an OpenCode shell by the `# hoxi-stop:` tag the
// sandbox plugin puts in its command line, a CLI runtime's shells as group
// leaders inside the runtime's own process tree) and records its group. A
// process belongs to the session when its group is one of these.
//
// The record is in memory, per Hoxi session, and only Hoxi writes it: a model
// can neither add a group nor tag a process it did not start. A group is
// dropped once no process holds it, and a group whose leader PID now runs a
// different command line is treated as reused and dropped too.

// maxOwnedGroupsPerSession bounds one session's record.
const maxOwnedGroupsPerSession = 256

// ownedShellScanDelays are when a running shell command is looked for after
// its tool call starts. A shell that exits before the first scan finds it
// leaves no record, and a later kill of what it started keeps asking.
var ownedShellScanDelays = []time.Duration{0, 150 * time.Millisecond, 400 * time.Millisecond, time.Second, 2500 * time.Millisecond}

// approvalSourceOwnProcess marks requests auto-approved because their only
// risky part was a kill of the session's own processes.
const approvalSourceOwnProcess = "own_process"

type ownedProcessGroup struct {
	pgid       int
	leaderArgs string // the group leader's command line when recorded
}

type ownedProcessLedger struct {
	mu       sync.Mutex
	groups   map[string][]ownedProcessGroup // Hoxi session ID -> groups
	watching map[string]bool                // sessionID\x00callID being looked for
}

func newOwnedProcessLedger() *ownedProcessLedger {
	return &ownedProcessLedger{groups: map[string][]ownedProcessGroup{}, watching: map[string]bool{}}
}

// startWatch reports whether the caller should look for this call's shell;
// each call is looked for once.
func (l *ownedProcessLedger) startWatch(sessionID, callID string) bool {
	if l == nil || sessionID == "" || callID == "" {
		return false
	}
	key := sessionID + "\x00" + callID
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watching[key] {
		return false
	}
	l.watching[key] = true
	return true
}

// endWatch forgets a call after its scans, keeping the map small. A call ID
// that comes back later is a new tool call and is looked for again.
func (l *ownedProcessLedger) endWatch(sessionID, callID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.watching, sessionID+"\x00"+callID)
	l.mu.Unlock()
}

func (l *ownedProcessLedger) record(sessionID string, pgid int, leaderArgs string) {
	if l == nil || sessionID == "" || pgid <= 1 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	groups := l.groups[sessionID]
	for i, g := range groups {
		if g.pgid == pgid {
			groups[i].leaderArgs = leaderArgs
			return
		}
	}
	groups = append(groups, ownedProcessGroup{pgid: pgid, leaderArgs: leaderArgs})
	if len(groups) > maxOwnedGroupsPerSession {
		groups = groups[len(groups)-maxOwnedGroupsPerSession:]
	}
	l.groups[sessionID] = groups
}

// liveGroups prunes every session's record against byPID and returns the
// groups sessionID still owns.
func (l *ownedProcessLedger) liveGroups(sessionID string, byPID map[int]procSnapshot) map[int]bool {
	if l == nil || len(byPID) == 0 {
		return nil
	}
	held := make(map[int]bool, len(byPID))
	for _, snap := range byPID {
		if snap.pgid > 1 {
			held[snap.pgid] = true
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[int]bool{}
	for sid, groups := range l.groups {
		kept := groups[:0]
		for _, g := range groups {
			if !held[g.pgid] {
				continue
			}
			if leader, alive := byPID[g.pgid]; alive && leader.args != g.leaderArgs {
				continue // the leader PID was reused by another command
			}
			kept = append(kept, g)
			if sid == sessionID {
				out[g.pgid] = true
			}
		}
		if len(kept) == 0 {
			delete(l.groups, sid)
		} else {
			l.groups[sid] = kept
		}
	}
	return out
}

// noteOpenCodeShellEvent starts looking for the shell of a running OpenCode
// bash call. evt is the raw daemon event; its sessionID is the engine
// session (the turn's own or one of its subagents').
func (s *Service) noteOpenCodeShellEvent(rec SessionRecord, evt SSEEvent) {
	if s.ownedProcs == nil || s.openCode == nil || evt.Type != "message.part.updated" {
		return
	}
	part, _ := evt.Properties["part"].(map[string]interface{})
	if part == nil || firstString(part["type"]) != "tool" || firstString(part["tool"]) != "bash" {
		return
	}
	state, _ := part["state"].(map[string]interface{})
	if state == nil || firstString(state["status"]) != "running" {
		return
	}
	engineSID, callID := firstString(part["sessionID"]), firstString(part["callID"])
	if engineSID == "" || callID == "" {
		return
	}
	if engineSID != rec.ExternalID {
		s.mu.Lock()
		ap := s.procs[rec.ID]
		s.mu.Unlock()
		if ap == nil || !ap.isOpenCodeChild(engineSID) {
			return
		}
	}
	marker := openCodeStopMarker(engineSID, callID)
	u, err := url.Parse(strings.TrimSpace(s.openCode.BaseURL()))
	if err != nil || marker == "" || u.Port() == "" {
		return
	}
	port := u.Port()
	sessionID := rec.ID
	if !s.ownedProcs.startWatch(sessionID, callID) {
		return
	}
	go func() {
		defer s.ownedProcs.endWatch(sessionID, callID)
		for _, delay := range ownedShellScanDelays {
			time.Sleep(delay)
			byPID, children := readProcessSnapshots()
			if shell := findOpenCodeShellGroup(byPID, children, port, marker); shell > 1 {
				s.ownedProcs.record(sessionID, shell, byPID[shell].args)
				return
			}
		}
	}()
}

// findOpenCodeShellGroup returns the shell carrying marker among the direct
// children of the OpenCode daemon on port, when it leads its own process
// group. Only the daemon starts its direct children, so a model cannot plant
// a tag there; a forged tag deeper in the tree is ignored.
func findOpenCodeShellGroup(byPID map[int]procSnapshot, children map[int][]int, port, marker string) int {
	daemon := selectOpenCodeDaemonPID(byPID, children, port)
	if daemon <= 1 || marker == "" {
		return 0
	}
	for _, child := range children[daemon] {
		snap := byPID[child]
		if child > 1 && snap.pgid == child && argsCarryStopMarker(snap.args, marker) {
			return child
		}
	}
	return 0
}

// noteRuntimeShellEvent records the process groups a CLI runtime's shell
// tools lead while one runs. Only groups whose leader sits in the runtime's
// own process tree are recorded.
func (s *Service) noteRuntimeShellEvent(sessionID string, evt SSEEvent) {
	if s.ownedProcs == nil || evt.Type != "message.part.updated" {
		return
	}
	part, ok := evt.Properties["part"].(MessagePart)
	if !ok || part.Type != "tool" || part.State == nil || part.State.Status != "running" || part.CallID == "" {
		return
	}
	if !bashToolNames[strings.ToLower(strings.TrimSpace(part.Tool))] {
		return
	}
	root := s.runtimeRootPID(sessionID)
	if root <= 1 || !s.ownedProcs.startWatch(sessionID, part.CallID) {
		return
	}
	callID := part.CallID
	go func() {
		defer s.ownedProcs.endWatch(sessionID, callID)
		protected := selfProcessGroupID()
		for _, delay := range ownedShellScanDelays {
			time.Sleep(delay)
			byPID, children := readProcessSnapshots()
			if _, alive := byPID[root]; !alive {
				return
			}
			for _, pid := range runtimeShellGroups(byPID, children, root, protected) {
				s.ownedProcs.record(sessionID, pid, byPID[pid].args)
			}
		}
	}()
}

// runtimeShellGroups lists the process-group leaders inside root's tree. A
// group led from outside the tree is never listed, even when a process in
// the tree joined it.
func runtimeShellGroups(byPID map[int]procSnapshot, children map[int][]int, root, protected int) []int {
	var out []int
	for _, pid := range collectSubtreePIDs(root, children) {
		if snap, ok := byPID[pid]; ok && pid > 1 && snap.pgid == pid && pid != protected {
			out = append(out, pid)
		}
	}
	return out
}

// runtimeRootPID is the pid of the session's running CLI runtime, or 0 for
// OpenCode turns and idle sessions.
func (s *Service) runtimeRootPID(sessionID string) int {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil || ap.cmd == nil || ap.cmd.Process == nil {
		return 0
	}
	return ap.cmd.Process.Pid
}

// ownedProcessVerifier returns the check maskVerifiedProcessControl uses for
// one request. It reads the process table once, on first use.
func (s *Service) ownedProcessVerifier(sessionID string) func(processControlTarget) bool {
	var (
		once     sync.Once
		byPID    map[int]procSnapshot
		groups   map[int]bool
		subtree  map[int]bool
		blocked  map[int]bool
		verdicts = map[string]bool{}
	)
	load := func() {
		var children map[int][]int
		byPID, children = readProcessSnapshots()
		groups = s.ownedProcs.liveGroups(sessionID, byPID)
		blocked = map[int]bool{1: true, os.Getpid(): true}
		if pgid := selfProcessGroupID(); pgid > 1 {
			blocked[pgid] = true
		}
		subtree = map[int]bool{}
		if root := s.runtimeRootPID(sessionID); root > 1 {
			blocked[root] = true
			for _, pid := range collectSubtreePIDs(root, children)[1:] {
				subtree[pid] = true
			}
		}
	}
	ownedPID := func(pid int) bool {
		snap, ok := byPID[pid]
		if !ok || blocked[pid] || blocked[snap.pgid] || isAgentRuntimeProcessArgs(snap.args) {
			return false
		}
		return groups[snap.pgid] || subtree[pid]
	}
	return func(target processControlTarget) bool {
		if sessionID == "" || s.ownedProcs == nil {
			return false
		}
		once.Do(load)
		if len(byPID) == 0 {
			return false
		}
		key := strings.Join(target.pgrepArgs, "\x00")
		if len(target.pgrepArgs) > 0 {
			if v, seen := verdicts[key]; seen {
				return v
			}
			pids, ok := pgrepPIDs(target.pgrepArgs)
			verdict := ok
			for _, pid := range pids {
				if !ownedPID(pid) {
					verdict = false
					break
				}
			}
			verdicts[key] = verdict
			return verdict
		}
		if len(target.pids) == 0 {
			return false
		}
		for _, pid := range target.pids {
			if _, listed := byPID[pid]; !listed && !processAlive(pid) {
				continue // already gone: the kill signals nothing
			}
			if !ownedPID(pid) {
				return false
			}
		}
		return true
	}
}

// ownedProcessJudgedItem returns the copy of a shell request the policy
// should judge: verified kills of the session's own processes are masked.
// The original item stays what the user sees and what the audit records.
func (s *Service) ownedProcessJudgedItem(item map[string]interface{}, sessionID string) (map[string]interface{}, bool) {
	if s.ownedProcs == nil || strings.TrimSpace(sessionID) == "" || !isShellPermissionItem(item) {
		return item, false
	}
	if s.ApprovalPolicy().Mode == ApprovalModeManual {
		return item, false
	}
	verify := s.ownedProcessVerifier(sessionID)
	changed := false
	maskValue := func(v interface{}) interface{} {
		switch x := v.(type) {
		case string:
			if masked, ok := maskVerifiedProcessControl(unwrapSandboxCommand(x), verify); ok {
				changed = true
				return masked
			}
		case []string:
			out := make([]string, len(x))
			for i, e := range x {
				out[i] = e
				if masked, ok := maskVerifiedProcessControl(unwrapSandboxCommand(e), verify); ok {
					changed = true
					out[i] = masked
				}
			}
			return out
		case []interface{}:
			out := make([]interface{}, len(x))
			for i, e := range x {
				out[i] = e
				if str, isStr := e.(string); isStr {
					if masked, ok := maskVerifiedProcessControl(unwrapSandboxCommand(str), verify); ok {
						changed = true
						out[i] = masked
					}
				}
			}
			return out
		}
		return v
	}
	judged := make(map[string]interface{}, len(item))
	for k, v := range item {
		judged[k] = v
	}
	for _, key := range []string{"patterns", "pattern", "title"} {
		if v, ok := item[key]; ok {
			judged[key] = maskValue(v)
		}
	}
	if meta, ok := item["metadata"].(map[string]interface{}); ok {
		copied := make(map[string]interface{}, len(meta))
		for k, v := range meta {
			copied[k] = v
		}
		for _, key := range []string{"command", "cmd", "commands"} {
			if v, ok := meta[key]; ok {
				copied[key] = maskValue(v)
			}
		}
		judged["metadata"] = copied
	}
	if !changed {
		return item, false
	}
	return judged, true
}
