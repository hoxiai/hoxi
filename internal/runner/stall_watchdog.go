package runner

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The stall watchdog measures silence, not total runtime: a busy turn whose
// CLI (or OpenCode session) has produced no output for warnAfter is flagged
// as possibly stuck (status bar Attention + a `session.stall` event), and is
// stopped after abortAfter (when enabled and no tool subprocess is still
// running). Long but chatty turns are never cut short, and time spent waiting
// on the user's approval does not count as silence.

const (
	defaultStallWarnAfter  = 15 * time.Minute
	defaultStallAbortAfter = 0

	// Durations such as "90s" or "10m"; "0" disables warning / auto-stop.
	envStallWarnAfter  = "HOXI_RUNNER_STALL_WARN"
	envStallAbortAfter = "HOXI_RUNNER_STALL_ABORT"

	stallErrorName = "RunnerStalledError"
)

type stallPolicy struct {
	warnAfter  time.Duration // 0 disables the watchdog
	abortAfter time.Duration // 0 only warns
	interval   time.Duration
}

func stallPolicyFromEnv() stallPolicy {
	p := stallPolicy{warnAfter: defaultStallWarnAfter, abortAfter: defaultStallAbortAfter}
	if d, ok := envDuration(envStallWarnAfter); ok {
		p.warnAfter = d
	}
	if d, ok := envDuration(envStallAbortAfter); ok {
		p.abortAfter = d
	}
	return p.normalized()
}

func (p stallPolicy) normalized() stallPolicy {
	if p.abortAfter > 0 && p.abortAfter < p.warnAfter {
		p.abortAfter = p.warnAfter
	}
	if p.interval <= 0 {
		p.interval = min(max(p.warnAfter/10, time.Second), 15*time.Second)
	}
	return p
}

func envDuration(key string) (time.Duration, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// stallState is the watchdog's view of one active turn.
type stallState struct {
	lastActivity atomic.Int64 // unix ms of the latest output
	stalledSince atomic.Int64 // lastActivity when flagged; 0 while healthy

	// mu serializes stall publication so the latest state is always the one
	// delivered last, and guards the fields below.
	mu       sync.Mutex
	awaiting map[string]bool // OpenCode permission/question IDs open on the user
	abortErr *MessageError
}

func (st *stallState) touch(now time.Time) {
	st.lastActivity.Store(now.UnixMilli())
}

func (st *stallState) abortError() *MessageError {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.abortErr
}

func (st *stallState) setAwaiting(id string, waiting bool) {
	if id == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if waiting {
		if st.awaiting == nil {
			st.awaiting = make(map[string]bool)
		}
		st.awaiting[id] = true
	} else {
		delete(st.awaiting, id)
	}
}

func (st *stallState) hasAwaiting() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.awaiting) > 0
}

// noteActivity records output from the turn and clears a stall flag.
func (s *Service) noteActivity(ap *activeProcess) {
	ap.stall.touch(time.Now())
	if ap.stall.stalledSince.Swap(0) != 0 {
		s.publishStall(ap)
	}
}

// noteOpenCodeActivity feeds OpenCode session events into the watchdog of
// the conversation's active turn, tracking questions asked of the user.
func (s *Service) noteOpenCodeActivity(sessionID string, evt SSEEvent) {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil {
		return
	}
	switch evt.Type {
	case "permission.asked", "question.asked", "question.v2.asked":
		id, _ := evt.Properties["id"].(string)
		ap.stall.setAwaiting(id, true)
	case "permission.replied", "question.replied", "question.rejected", "question.v2.replied", "question.v2.rejected":
		id, _ := evt.Properties["requestID"].(string)
		ap.stall.setAwaiting(id, false)
	}
	s.noteActivity(ap)
}

// noteOpenCodeChildEvent keeps a turn alive while its OpenCode subagents
// (`task` tool child sessions) work: their events carry the child session ID,
// so the relay would otherwise drop them and the parent would look silent.
// It reports whether evt belonged to a child and was consumed.
func (s *Service) noteOpenCodeChildEvent(rec SessionRecord, evt SSEEvent) bool {
	s.mu.Lock()
	ap := s.procs[rec.ID]
	s.mu.Unlock()
	if ap == nil || rec.ExternalID == "" {
		return false
	}
	if evt.Type == "session.created" || evt.Type == "session.updated" {
		info, _ := evt.Properties["info"].(map[string]interface{})
		id, _ := info["id"].(string)
		parentID, _ := info["parentID"].(string)
		if id == "" || id == rec.ExternalID || parentID == "" {
			return false
		}
		if parentID != rec.ExternalID && !ap.isOpenCodeChild(parentID) {
			return false
		}
		ap.openCodeChildren.Store(id, true)
		prevParent, hadParent := s.openCodeChildParent.Swap(id, rec.ID)
		s.managedOpenCode.Store(id, true)
		if !hadParent || prevParent != rec.ID {
			_, _, _ = s.store.updateWithoutActivity(rec.ID, func(r *SessionRecord) {
				if !slices.Contains(r.OpenCodeChildIDs, id) {
					r.OpenCodeChildIDs = append(r.OpenCodeChildIDs, id)
				}
			})
		}
		s.noteOpenCodeActivity(rec.ID, evt)
		mappedInfo := make(map[string]interface{}, len(info)+3)
		for k, v := range info {
			mappedInfo[k] = v
		}
		mappedInfo["parentID"] = rec.ID
		if dir := firstNonEmptyStr(rec.WorktreePath, rec.ProjectDir); dir != "" {
			mappedInfo["directory"] = dir
		}
		if rec.ProjectID != "" {
			mappedInfo["projectID"] = rec.ProjectID
		}
		s.broadcast(id, SSEEvent{
			Type:       evt.Type,
			Properties: map[string]interface{}{"info": mappedInfo},
		})
		return true
	}
	sid := openCodeEventSessionID(evt)
	if sid == "" || sid == rec.ExternalID || !ap.isOpenCodeChild(sid) {
		return false
	}
	switch evt.Type {
	case "permission.asked", "permission.replied",
		"question.asked", "question.replied", "question.rejected",
		"question.v2.asked", "question.v2.replied", "question.v2.rejected":
		if evt.Properties != nil {
			evt.Properties["subagentSessionID"] = sid
			if meta, ok := evt.Properties["metadata"].(map[string]interface{}); ok && meta != nil {
				if firstString(meta["subagentSessionID"]) == "" {
					meta["subagentSessionID"] = sid
				}
			} else if evt.Type == "permission.asked" {
				evt.Properties["metadata"] = map[string]interface{}{"subagentSessionID": sid}
			}
			evt.Properties["sessionID"] = rec.ExternalID
		}
		return false
	case "session.status", "session.idle":
		if evt.Type == "session.status" {
			if st, ok := parseSessionStatus(evt.Properties["status"]); ok && st.Type == "retry" {
				if _, cut := shouldCutOffTurnRetry(st, time.Now()); cut {
					s.abortOpenCodeRunawayTurn(SessionRecord{ExternalID: sid, WorktreePath: rec.WorktreePath})
					return true
				}
			}
		}
		s.noteOpenCodeActivity(rec.ID, evt)
		s.broadcast(sid, evt)
		return true
	}
	s.noteOpenCodeActivity(rec.ID, evt)
	return true
}

func (ap *activeProcess) isOpenCodeChild(sessionID string) bool {
	_, ok := ap.openCodeChildren.Load(sessionID)
	return ok
}

func (s *Service) isManagedOpenCodeChild(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	if _, ok := s.openCodeChildParent.Load(sessionID); ok {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ap := range s.procs {
		if ap != nil && ap.isOpenCodeChild(sessionID) {
			return true
		}
	}
	return false
}

func openCodeEventSessionID(evt SSEEvent) string {
	if sid, ok := evt.Properties["sessionID"].(string); ok {
		return sid
	}
	for _, key := range []string{"info", "part"} {
		if obj, ok := evt.Properties[key].(map[string]interface{}); ok {
			if sid, ok := obj["sessionID"].(string); ok {
				return sid
			}
		}
	}
	return ""
}

// stalledSessions maps each flagged active turn to its stalledSince.
func (s *Service) stalledSessions() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64)
	for id, ap := range s.procs {
		if since := ap.stall.stalledSince.Load(); since != 0 {
			out[id] = since
		}
	}
	return out
}

func (s *Service) watchStall(ap *activeProcess) {
	if s.stall.warnAfter <= 0 {
		return
	}
	ticker := time.NewTicker(s.stall.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ap.done:
			return
		case now := <-ticker.C:
			if !s.checkStall(ap, now) {
				return
			}
		}
	}
}

// checkStall evaluates one tick; it reports false once the turn was stopped.
func (s *Service) checkStall(ap *activeProcess, now time.Time) bool {
	if s.awaitingUser(ap) {
		// The clock restarts when the user answers.
		s.noteActivity(ap)
		return true
	}
	last := ap.stall.lastActivity.Load()
	idle := now.Sub(time.UnixMilli(last))
	if s.stall.abortAfter > 0 && idle >= s.stall.abortAfter && !s.hasRunningToolSubprocess(ap) {
		s.abortStalled(ap, idle)
		return false
	}
	if idle >= s.stall.warnAfter && ap.stall.stalledSince.CompareAndSwap(0, last) {
		s.publishStall(ap)
	}
	return true
}

// hasRunningToolSubprocess reports whether the turn has an active tool call or
// subagent whose underlying OS process or child session is still running.
func (s *Service) hasRunningToolSubprocess(ap *activeProcess) bool {
	if ap == nil || ap.sessionID == "" {
		return false
	}
	rec, ok := s.store.Get(ap.sessionID)
	if !ok || len(rec.Turns) == 0 {
		return false
	}
	var runningBashCalls []string
	var runningCommands []string
	for i := len(rec.Turns) - 1; i >= 0; i-- {
		t := rec.Turns[i]
		if t.Info.Role == "user" && !isInternalRecoveryMessageID(t.Info.ID) {
			break
		}
		for _, p := range t.Parts {
			if p.Type != "tool" || p.State == nil || p.State.Status != "running" {
				continue
			}
			if bashToolNames[strings.ToLower(strings.TrimSpace(p.Tool))] {
				if callID := strings.TrimSpace(p.CallID); callID != "" {
					runningBashCalls = append(runningBashCalls, callID)
				}
				if p.State.Input != nil {
					if cmd := firstString(p.State.Input["command"], p.State.Input["CommandLine"], p.State.Input["cmd"]); cmd != "" {
						runningCommands = append(runningCommands, cmd)
					}
				}
			}
			if p.Tool == "task" && p.State.Metadata != nil {
				if childID := firstString(p.State.Metadata["sessionId"], p.State.Metadata["sessionID"]); childID != "" && s.isSessionBusy(childID) {
					return true
				}
			}
		}
	}
	if rootPID := s.runtimeRootPID(ap.sessionID); rootPID > 1 && len(runningCommands) > 0 {
		byPID, children := readProcessSnapshots()
		return hasLiveChildToolProcess(rootPID, byPID, children, runningCommands...)
	}
	if rec.Runtime == "opencode" && s.openCode != nil && len(runningBashCalls) > 0 {
		if u, err := url.Parse(strings.TrimSpace(s.openCode.BaseURL())); err == nil && u.Port() != "" {
			byPID, children := readProcessSnapshots()
			for _, callID := range runningBashCalls {
				marker := openCodeStopMarker(rec.ExternalID, callID)
				if findOpenCodeShellGroup(byPID, children, u.Port(), marker) > 1 {
					return true
				}
			}
		}
	}
	return false
}

func hasLiveChildToolProcess(rootPID int, byPID map[int]procSnapshot, children map[int][]int, commands ...string) bool {
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
		if isAgentRuntimeProcessArgs(byPID[only].args) {
			parentPID = only
		}
	}
	for _, c := range children[parentPID] {
		if c <= 1 || isNonToolHelperArgs(byPID[c].args) {
			continue
		}
		if subtreeMatchesNeedles(collectSubtreePIDs(c, children), byPID, needles) {
			return true
		}
	}
	return false
}

func (s *Service) awaitingUser(ap *activeProcess) bool {
	if ap.stall.hasAwaiting() {
		return true
	}
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	for _, p := range s.gate.pending {
		if p.sid == ap.sessionID {
			return true
		}
	}
	return false
}

// publishStall broadcasts the current stall state. Clients only show it
// while the session is busy, so a late event after the turn ends is harmless.
func (s *Service) publishStall(ap *activeProcess) {
	ap.stall.mu.Lock()
	defer ap.stall.mu.Unlock()
	since := ap.stall.stalledSince.Load()
	if s.status != nil {
		s.status.SetSessionStalled(ap.sessionID, since != 0)
	}
	s.broadcast(ap.sessionID, SSEEvent{
		Type:       "session.stall",
		Properties: map[string]interface{}{"sessionID": ap.sessionID, "stalledSince": since},
	})
}

func (s *Service) abortStalled(ap *activeProcess, idle time.Duration) {
	msg := fmt.Sprintf("No output for %s, so Hoxi stopped this run. Ask it to continue, or narrow down the task.", idle.Round(time.Second))
	s.abortTurn(ap, newClassifiedMessageError(stallErrorName, msg, 0))
}

// abortTurn stops the turn; err is reported as the turn's error once it ends.
func (s *Service) abortTurn(ap *activeProcess, err *MessageError) {
	ap.stall.mu.Lock()
	if ap.stall.abortErr == nil {
		ap.stall.abortErr = err
	}
	ap.stall.mu.Unlock()
	s.abortActiveProcess(ap.sessionID)
}
