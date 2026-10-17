package main

import (
	"encoding/json"
	"strings"
	"sync"
)

type desktopCodeSummary struct {
	Attention int  `json:"attention"`
	Running   int  `json:"running"`
	Queued    int  `json:"queued"`
	Available bool `json:"available"`
}

type desktopStatusTracker struct {
	mu               sync.Mutex
	available        bool
	busySessions     map[string]bool
	pendingApprovals map[string]string // requestID -> sessionID
}

func newDesktopStatusTracker() *desktopStatusTracker {
	return &desktopStatusTracker{
		available:        true,
		busySessions:     make(map[string]bool),
		pendingApprovals: make(map[string]string),
	}
}

func (t *desktopStatusTracker) setAvailable(avail bool) {
	t.mu.Lock()
	t.available = avail
	summary := t.snapshotLocked()
	t.mu.Unlock()
	updateDesktopStatusBar(summary)
}

func (t *desktopStatusTracker) snapshot() desktopCodeSummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *desktopStatusTracker) snapshotLocked() desktopCodeSummary {
	return desktopCodeSummary{
		Attention: len(t.pendingApprovals),
		Running:   len(t.busySessions),
		Queued:    0,
		Available: t.available,
	}
}

// inspectSSELine passively parses OpenCode `data: {...}` SSE lines to keep the macOS status bar synchronized.
func (t *desktopStatusTracker) inspectSSELine(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload[0] != '{' {
		return
	}

	var evt struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(payload), &evt); err != nil {
		return
	}

	changed := false
	t.mu.Lock()
	switch evt.Type {
	case "session.status":
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(evt.Properties, &p) == nil && p.SessionID != "" {
			if p.Status.Type == "busy" || p.Status.Type == "retry" {
				t.busySessions[p.SessionID] = true
			} else {
				delete(t.busySessions, p.SessionID)
			}
			changed = true
		}
	case "session.idle":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(evt.Properties, &p) == nil && p.SessionID != "" {
			delete(t.busySessions, p.SessionID)
			changed = true
		}
	case "permission.asked", "question.asked":
		var p struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(evt.Properties, &p) == nil && p.ID != "" {
			t.pendingApprovals[p.ID] = p.SessionID
			changed = true
		}
	case "permission.replied", "question.replied", "question.rejected":
		var p struct {
			RequestID string `json:"requestID"`
		}
		if json.Unmarshal(evt.Properties, &p) == nil && p.RequestID != "" {
			delete(t.pendingApprovals, p.RequestID)
			changed = true
		}
	}
	var summary desktopCodeSummary
	if changed {
		summary = t.snapshotLocked()
	}
	t.mu.Unlock()

	if changed {
		updateDesktopStatusBar(summary)
	}
}
