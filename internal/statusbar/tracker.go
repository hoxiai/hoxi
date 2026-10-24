package statusbar

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

type Summary struct {
	Attention        int             `json:"attention"`
	PendingApprovals int             `json:"pendingApprovals"`
	Running          int             `json:"running"`
	Queued           int             `json:"queued"`
	Available        bool            `json:"available"`
	KeepAwakeEnabled bool            `json:"keepAwakeEnabled"`
	KeepAwakeActive  bool            `json:"keepAwakeActive"`
	PendingTargets   []SessionTarget `json:"pendingTargets,omitempty"`
	RunningTargets   []SessionTarget `json:"runningTargets,omitempty"`
}

type Tracker struct {
	mu                 sync.Mutex
	available          bool
	busySessions       map[string]bool
	busyOrder          []string
	stalledSessions    map[string]bool   // busy sessions silent long enough to look stuck
	pendingApprovals   map[string]string // requestID -> sessionID
	pendingOrder       []string
	degradedProvider   map[string]bool
	keepAwakeEnabled   bool
	keepAwakeActive    bool
	keepAwakeCmd       *exec.Cmd
	startKeepAwakeHook func() (func(), error)
	keepAwakeStopHook  func()
	approvalListener   func(ApprovalNotice)
	sessionResolver    func(sessionID string) (resolvedID, title, directory string)
}

func NewTracker() *Tracker {
	return &Tracker{
		available:        true,
		busySessions:     make(map[string]bool),
		stalledSessions:  make(map[string]bool),
		pendingApprovals: make(map[string]string),
		degradedProvider: make(map[string]bool),
		keepAwakeEnabled: loadPersistedKeepAwakeEnabled(),
	}
}

// SetSessionResolver wires an optional lookup that maps a session ID (including
// subagent or runtime engine IDs) to its root Hoxi session ID, title and directory.
func (t *Tracker) SetSessionResolver(fn func(sessionID string) (resolvedID, title, directory string)) {
	t.mu.Lock()
	t.sessionResolver = fn
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
}

// ResolveTarget resolves one session ID into a SessionTarget using the registered resolver.
func (t *Tracker) ResolveTarget(sessionID string) SessionTarget {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionTarget{}
	}
	t.mu.Lock()
	resolver := t.sessionResolver
	t.mu.Unlock()
	return resolveOneTarget(sessionID, resolver)
}

// FirstAttentionTarget returns the highest-priority session needing user
// attention (pending approval first, then stalled session).
func (t *Tracker) FirstAttentionTarget() (SessionTarget, bool) {
	snap := t.Snapshot()
	if len(snap.PendingTargets) == 0 {
		return SessionTarget{}, false
	}
	return snap.PendingTargets[0], true
}

// FirstRunningTarget returns the first currently executing session.
func (t *Tracker) FirstRunningTarget() (SessionTarget, bool) {
	snap := t.Snapshot()
	if len(snap.RunningTargets) == 0 {
		return SessionTarget{}, false
	}
	return snap.RunningTargets[0], true
}

func (t *Tracker) SetAvailable(avail bool) {
	t.mu.Lock()
	t.available = avail
	t.syncKeepAwakeLocked()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
}

func (t *Tracker) SetSessionBusy(sessionID string, busy bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	if busy {
		t.markBusyLocked(sessionID, true)
	} else {
		t.markBusyLocked(sessionID, false)
		for reqID, sid := range t.pendingApprovals {
			if sid == sessionID {
				t.removePendingLocked(reqID)
			}
		}
	}
	t.syncKeepAwakeLocked()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
}

func (t *Tracker) markBusyLocked(sessionID string, busy bool) {
	if busy {
		if !t.busySessions[sessionID] {
			t.busySessions[sessionID] = true
			t.busyOrder = append(t.busyOrder, sessionID)
		}
		return
	}
	delete(t.busySessions, sessionID)
	delete(t.stalledSessions, sessionID)
	for i, id := range t.busyOrder {
		if id == sessionID {
			t.busyOrder = append(t.busyOrder[:i], t.busyOrder[i+1:]...)
			break
		}
	}
}

func (t *Tracker) removePendingLocked(requestID string) {
	delete(t.pendingApprovals, requestID)
	for i, id := range t.pendingOrder {
		if id == requestID {
			t.pendingOrder = append(t.pendingOrder[:i], t.pendingOrder[i+1:]...)
			break
		}
	}
}

// SetSessionStalled raises Attention for a busy session that looks stuck.
// It is ignored once the session is no longer busy.
func (t *Tracker) SetSessionStalled(sessionID string, stalled bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	if stalled && t.busySessions[sessionID] {
		t.stalledSessions[sessionID] = true
	} else {
		delete(t.stalledSessions, sessionID)
	}
	t.syncKeepAwakeLocked()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
}

func (t *Tracker) SetPendingApproval(requestID, sessionID string, pending bool) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	t.mu.Lock()
	var announce func()
	if pending {
		announce = t.addPendingLocked("approval", requestID, sessionID, "")
	} else {
		t.removePendingLocked(requestID)
	}
	t.syncKeepAwakeLocked()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
	if announce != nil {
		announce()
	}
}

// SetProviderDegraded flags a provider with persistent internal LLM failures so
// the tray shows Attention until the provider recovers.
func (t *Tracker) SetProviderDegraded(providerID string, degraded bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return
	}
	t.mu.Lock()
	if degraded {
		t.degradedProvider[providerID] = true
	} else {
		delete(t.degradedProvider, providerID)
	}
	t.syncKeepAwakeLocked()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
}

func (t *Tracker) Snapshot() Summary {
	t.mu.Lock()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	return summary
}

func (t *Tracker) snapshotLocked() Summary {
	return Summary{
		Attention:        len(t.pendingApprovals) + len(t.stalledSessions) + len(t.degradedProvider),
		PendingApprovals: len(t.pendingApprovals),
		Running:          len(t.busySessions),
		Queued:           0,
		Available:        t.available,
		KeepAwakeEnabled: t.keepAwakeEnabled,
		KeepAwakeActive:  t.keepAwakeActive,
	}
}

func (t *Tracker) snapshotWithTargetsLocked() (Summary, []string, []string, func(string) (string, string, string)) {
	summary := t.snapshotLocked()
	var pendingIDs []string
	seenPending := make(map[string]bool)
	for _, reqID := range t.pendingOrder {
		sid := strings.TrimSpace(t.pendingApprovals[reqID])
		if sid != "" && !seenPending[sid] {
			seenPending[sid] = true
			pendingIDs = append(pendingIDs, sid)
		}
	}
	if len(t.stalledSessions) > 0 {
		var stalled []string
		for sid := range t.stalledSessions {
			if !seenPending[sid] {
				stalled = append(stalled, sid)
			}
		}
		sort.Strings(stalled)
		pendingIDs = append(pendingIDs, stalled...)
	}
	runningIDs := make([]string, 0, len(t.busyOrder))
	for _, sid := range t.busyOrder {
		if t.busySessions[sid] {
			runningIDs = append(runningIDs, sid)
		}
	}
	return summary, pendingIDs, runningIDs, t.sessionResolver
}

func resolveOneTarget(sessionID string, resolver func(string) (string, string, string)) SessionTarget {
	resolvedID := strings.TrimSpace(sessionID)
	title := ""
	dir := ""
	if resolver != nil {
		rID, rTitle, rDir := resolver(resolvedID)
		if strings.TrimSpace(rID) != "" {
			resolvedID = strings.TrimSpace(rID)
		}
		title = strings.TrimSpace(rTitle)
		dir = strings.TrimSpace(rDir)
	}
	return SessionTarget{
		SessionID: resolvedID,
		Title:     title,
		Directory: dir,
	}
}

func enrichSummary(
	summary *Summary,
	pendingIDs []string,
	runningIDs []string,
	resolver func(string) (string, string, string),
) {
	summary.PendingTargets = buildSessionTargets(pendingIDs, resolver)
	summary.RunningTargets = buildSessionTargets(runningIDs, resolver)
}

func buildSessionTargets(ids []string, resolver func(string) (string, string, string)) []SessionTarget {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	out := make([]SessionTarget, 0, min(len(ids), maxMenuTargets))
	for _, id := range ids {
		target := resolveOneTarget(id, resolver)
		if target.SessionID == "" || seen[target.SessionID] {
			continue
		}
		seen[target.SessionID] = true
		out = append(out, target)
		if len(out) >= maxMenuTargets {
			break
		}
	}
	return out
}

// InspectSSELine passively parses OpenCode `data: {...}` SSE lines to keep the macOS status bar synchronized.
func (t *Tracker) InspectSSELine(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload[0] != '{' {
		return
	}
	// Fast pre-filter: skip JSON unmarshaling on high-frequency token deltas and tool output events
	if !strings.Contains(payload, `"session.`) &&
		!strings.Contains(payload, `"permission.`) &&
		!strings.Contains(payload, `"question.`) {
		return
	}

	var raw struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Payload    *struct {
			Type       string          `json:"type"`
			Properties json.RawMessage `json:"properties"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return
	}
	evtType := raw.Type
	evtProps := raw.Properties
	if raw.Payload != nil && raw.Payload.Type != "" {
		evtType = raw.Payload.Type
		evtProps = raw.Payload.Properties
	}

	changed := false
	var announce func()
	t.mu.Lock()
	switch evtType {
	case "session.status":
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.SessionID != "" {
			if p.Status.Type == "busy" || p.Status.Type == "retry" {
				t.markBusyLocked(p.SessionID, true)
			} else {
				t.markBusyLocked(p.SessionID, false)
			}
			changed = true
		}
	case "session.idle", "session.error":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.SessionID != "" {
			t.markBusyLocked(p.SessionID, false)
			changed = true
		}
	case "permission.asked", "question.asked", "question.v2.asked":
		var p struct {
			ID         string `json:"id"`
			SessionID  string `json:"sessionID"`
			Permission string `json:"permission"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.ID != "" {
			kind := "approval"
			if evtType == "question.asked" || evtType == "question.v2.asked" {
				kind = "question"
			}
			announce = t.addPendingLocked(kind, p.ID, p.SessionID, p.Permission)
			changed = true
		}
	case "permission.replied", "question.replied", "question.rejected", "question.v2.replied", "question.v2.rejected":
		var p struct {
			RequestID string `json:"requestID"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.RequestID != "" {
			t.removePendingLocked(p.RequestID)
			changed = true
		}
	}
	var (
		summary    Summary
		pendingIDs []string
		runningIDs []string
		resolver   func(string) (string, string, string)
	)
	if changed {
		t.syncKeepAwakeLocked()
		summary, pendingIDs, runningIDs, resolver = t.snapshotWithTargetsLocked()
	}
	t.mu.Unlock()

	if changed {
		enrichSummary(&summary, pendingIDs, runningIDs, resolver)
		Update(summary)
	}
	if announce != nil {
		announce()
	}
}
