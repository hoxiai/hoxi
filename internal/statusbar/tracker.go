package statusbar

import (
	"encoding/json"
	"strings"
	"sync"
)

type Summary struct {
	Attention int  `json:"attention"`
	Running   int  `json:"running"`
	Queued    int  `json:"queued"`
	Available bool `json:"available"`
}

type Tracker struct {
	mu               sync.Mutex
	available        bool
	busySessions     map[string]bool
	pendingApprovals map[string]string // requestID -> sessionID
}

func NewTracker() *Tracker {
	return &Tracker{
		available:        true,
		busySessions:     make(map[string]bool),
		pendingApprovals: make(map[string]string),
	}
}

func (t *Tracker) SetAvailable(avail bool) {
	t.mu.Lock()
	t.available = avail
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
		for reqID, sid := range t.pendingApprovals {
			if sid == sessionID {
				delete(t.pendingApprovals, reqID)
			}
		}
	}
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
	if pending {
		t.pendingApprovals[requestID] = sessionID
	} else {
		delete(t.pendingApprovals, requestID)
	}
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
		Attention: len(t.pendingApprovals),
		Running:   len(t.busySessions),
		Queued:    0,
		Available: t.available,
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
			}
			changed = true
		}
	case "session.idle", "session.error":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.SessionID != "" {
			delete(t.busySessions, p.SessionID)
			changed = true
		}
	case "permission.asked", "question.asked":
		var p struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(evtProps, &p) == nil && p.ID != "" {
			t.pendingApprovals[p.ID] = p.SessionID
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
		summary = t.snapshotLocked()
	}
	t.mu.Unlock()

	if changed {
		Update(summary)
	}
}
