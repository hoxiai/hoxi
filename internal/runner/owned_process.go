package runner

import (
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// approvalSourceOwnProcess marks requests auto-approved because their only
// risky part was a kill of the session's own processes.
const approvalSourceOwnProcess = "own_process"

var ownedShellScanDelays = []time.Duration{0, 150 * time.Millisecond, 400 * time.Millisecond, time.Second, 2500 * time.Millisecond}

func (s *Service) processControlToolVerifier(sessionID string) func(string) bool {
	path := os.Getenv("PATH")
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap != nil {
		ap.mu.Lock()
		if ap.cmd != nil && ap.cmd.Env != nil {
			path = ""
			for _, entry := range ap.cmd.Env {
				if strings.HasPrefix(entry, "PATH=") {
					path = strings.TrimPrefix(entry, "PATH=")
				}
			}
		}
		ap.mu.Unlock()
	}
	return func(name string) bool { return trustedProcessControlToolIn(name, path) }
}

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
	watch := s.ownedProcs.StartWatch(sessionID, callID)
	if watch == nil {
		return
	}
	go func() {
		defer s.ownedProcs.EndWatch(sessionID, callID, watch)
		for _, delay := range ownedShellScanDelays {
			if !watch.Wait(delay) {
				return
			}
			byPID, children := readProcessSnapshots()
			if shell := findOpenCodeShellGroup(byPID, children, port, marker); shell > 1 {
				s.ownedProcs.RecordWatched(sessionID, callID, watch, shell, byPID[shell].Args)
				return
			}
		}
	}()
}

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
	s.mu.Lock()
	hasActive := s.procs[sessionID] != nil
	s.mu.Unlock()
	if !hasActive {
		return
	}
	callID := part.CallID
	watch := s.ownedProcs.StartWatch(sessionID, callID)
	if watch == nil {
		return
	}
	go func() {
		defer s.ownedProcs.EndWatch(sessionID, callID, watch)
		protected := selfProcessGroupID()
		selfPID := os.Getpid()
		for _, delay := range ownedShellScanDelays {
			if !watch.Wait(delay) {
				return
			}
			root := s.runtimeRootPID(sessionID)
			if root <= 1 {
				s.mu.Lock()
				stillActive := s.procs[sessionID] != nil
				s.mu.Unlock()
				if !stillActive {
					return
				}
				continue
			}
			byPID, children := readProcessSnapshots()
			rootSnap, alive := byPID[root]
			if !alive {
				return
			}
			for _, pid := range runtimeShellGroups(byPID, children, root, protected) {
				s.ownedProcs.RecordWatched(sessionID, callID, watch, pid, byPID[pid].Args)
			}
			if rootSnap.PGID == 0 && rootSnap.CreateTime != 0 {
				for _, pid := range collectSubtreePIDs(root, children) {
					if pid <= 4 || pid == selfPID {
						continue
					}
					cSnap, ok := byPID[pid]
					if !ok || cSnap.CreateTime == 0 || cSnap.CreateTime < rootSnap.CreateTime || isAgentRuntimeProcessArgs(cSnap.Args) {
						continue
					}
					s.ownedProcs.RecordWatchedPID(sessionID, callID, watch, pid, cSnap.CreateTime, cSnap.Args)
				}
			}
		}
	}()
}

func (s *Service) runtimeRootPID(sessionID string) int {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil || ap.cmd == nil || ap.cmd.Process == nil {
		return 0
	}
	return ap.cmd.Process.Pid
}

func (s *Service) ownedProcessVerifier(sessionID string) func(processControlTarget) bool {
	var (
		once     sync.Once
		byPID    map[int]procSnapshot
		groups   map[int]bool
		pids     map[int]bool
		subtree  map[int]bool
		blocked  map[int]bool
		verdicts = map[string]bool{}
	)
	load := func() {
		var children map[int][]int
		byPID, children = readProcessSnapshots()
		groups = s.ownedProcs.LiveGroups(sessionID, byPID)
		pids = s.ownedProcs.LivePIDs(sessionID, byPID, children)
		blocked = map[int]bool{1: true, os.Getpid(): true}
		if pgid := selfProcessGroupID(); pgid > 1 {
			blocked[pgid] = true
		}
		subtree = map[int]bool{}
		if root := s.runtimeRootPID(sessionID); root > 1 {
			blocked[root] = true
			rootCreate := byPID[root].CreateTime
			for _, pid := range collectSubtreePIDs(root, children)[1:] {
				if rootCreate > 0 && byPID[pid].CreateTime < rootCreate {
					continue
				}
				subtree[pid] = true
			}
		}
	}
	ownedPID := func(pid int) bool {
		snap, ok := byPID[pid]
		if !ok || pid <= 1 || blocked[pid] || (snap.PGID > 0 && blocked[snap.PGID]) || isAgentRuntimeProcessArgs(snap.Args) {
			return false
		}
		return (snap.PGID > 1 && groups[snap.PGID]) || pids[pid] || subtree[pid]
	}
	return func(target processControlTarget) bool {
		if sessionID == "" || s.ownedProcs == nil {
			return false
		}
		once.Do(load)
		if len(byPID) == 0 {
			return false
		}
		key := strings.Join(target.PgrepArgs, "\x00")
		if len(target.PgrepArgs) > 0 {
			if v, seen := verdicts[key]; seen {
				return v
			}
			pids, ok := pgrepPIDs(target.PgrepArgs)
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
		if len(target.PIDs) == 0 {
			return false
		}
		for _, pid := range target.PIDs {
			if _, listed := byPID[pid]; !listed && !processAlive(pid) {
				continue
			}
			if !ownedPID(pid) {
				return false
			}
		}
		return true
	}
}

func (s *Service) ownedProcessJudgedItem(item map[string]interface{}, sessionID string) (map[string]interface{}, bool) {
	if s.ownedProcs == nil || strings.TrimSpace(sessionID) == "" || !isShellPermissionItem(item) {
		return item, false
	}
	if s.ApprovalPolicy().Mode == ApprovalModeManual {
		return item, false
	}
	verify := s.ownedProcessVerifier(sessionID)
	trusted := s.processControlToolVerifier(sessionID)
	changed := false
	maskValue := func(v interface{}) interface{} {
		switch x := v.(type) {
		case string:
			if masked, ok := maskVerifiedProcessControlWithResolver(unwrapSandboxCommand(x), verify, trusted); ok {
				changed = true
				return masked
			}
		case []string:
			out := make([]string, len(x))
			for i, e := range x {
				out[i] = e
				if masked, ok := maskVerifiedProcessControlWithResolver(unwrapSandboxCommand(e), verify, trusted); ok {
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
					if masked, ok := maskVerifiedProcessControlWithResolver(unwrapSandboxCommand(str), verify, trusted); ok {
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
