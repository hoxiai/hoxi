package statusbar

import (
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
)

type Summary struct {
	Attention        int  `json:"attention"`
	Running          int  `json:"running"`
	Queued           int  `json:"queued"`
	Available        bool `json:"available"`
	KeepAwakeEnabled bool `json:"keepAwakeEnabled"`
	KeepAwakeActive  bool `json:"keepAwakeActive"`
}

type Tracker struct {
	mu                 sync.Mutex
	available          bool
	busySessions       map[string]bool
	stalledSessions    map[string]bool   // busy sessions silent long enough to look stuck
	pendingApprovals   map[string]string // requestID -> sessionID
	degradedProvider   map[string]bool
	keepAwakeEnabled   bool
	keepAwakeActive    bool
	keepAwakeCmd       *exec.Cmd
	startKeepAwakeHook func() (func(), error)
	keepAwakeStopHook  func()
	approvalListener   func(ApprovalNotice)
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

func (t *Tracker) SetAvailable(avail bool) {
	t.mu.Lock()
	t.available = avail
	t.syncKeepAwakeLocked()
	summary := t.snapshotLocked()
	t.mu.Unlock()
	Update(summary)
}

func (t *Tracker) SetSessionBusy(sessionID string, busy bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	if busy {
		t.busySessions[sessionID] = true
	} else {
		delete(t.busySessions, sessionID)
		delete(t.stalledSessions, sessionID)
		for reqID, sid := range t.pendingApprovals {
			if sid == sessionID {
				delete(t.pendingApprovals, reqID)
			}
		}
	}
	t.syncKeepAwakeLocked()
	summary := t.snapshotLocked()
	t.mu.Unlock()
	Update(summary)
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
	summary := t.snapshotLocked()
	t.mu.Unlock()
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
		delete(t.pendingApprovals, requestID)
	}
	t.syncKeepAwakeLocked()
	summary := t.snapshotLocked()
	t.mu.Unlock()
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
	summary := t.snapshotLocked()
	t.mu.Unlock()
	Update(summary)
}

func (t *Tracker) Snapshot() Summary {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() Summary {
	return Summary{
		Attention:        len(t.pendingApprovals) + len(t.stalledSessions) + len(t.degradedProvider),
		Running:          len(t.busySessions),
		Queued:           0,
		Available:        t.available,
		KeepAwakeEnabled: t.keepAwakeEnabled,
		KeepAwakeActive:  t.keepAwakeActive,
	}
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
				t.busySessions[p.SessionID] = true
			} else {
				delete(t.busySessions, p.SessionID)
				delete(t.stalledSessions, p.SessionID)
			}
			changed = true
		}
	case "session.idle", "session.error":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.SessionID != "" {
			delete(t.busySessions, p.SessionID)
			delete(t.stalledSessions, p.SessionID)
			changed = true
		}
	case "permission.asked", "question.asked":
		var p struct {
			ID         string `json:"id"`
			SessionID  string `json:"sessionID"`
			Permission string `json:"permission"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.ID != "" {
			kind := "approval"
			if evtType == "question.asked" {
				kind = "question"
			}
			announce = t.addPendingLocked(kind, p.ID, p.SessionID, p.Permission)
			changed = true
		}
	case "permission.replied", "question.replied", "question.rejected":
		var p struct {
			RequestID string `json:"requestID"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.RequestID != "" {
			delete(t.pendingApprovals, p.RequestID)
			changed = true
		}
	}
	var summary Summary
	if changed {
		t.syncKeepAwakeLocked()
		summary = t.snapshotLocked()
	}
	t.mu.Unlock()

	if changed {
		Update(summary)
	}
	if announce != nil {
		announce()
	}
}
