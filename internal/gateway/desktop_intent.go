package gateway

import (
	"path/filepath"
	"strings"
)

const (
	maxDesktopEntityIDLength = 96
	desktopSessionIDPrefix   = "ses_"
	desktopMessageIDPrefix   = "msg_"

	DesktopIntentActionAttention     = "attention"
	DesktopIntentActionRunning       = "running"
	DesktopIntentActionNewThread     = "new-thread"
	DesktopIntentActionHandoff       = "handoff"
	DesktopIntentActionPolicy        = "policy"
	DesktopIntentActionSettings      = "settings"
	DesktopIntentActionSyncKeepAwake = "sync-keep-awake"
)

// DesktopIntent is a one-shot navigation request produced by a deep link, the
// CLI, or the native status bar menu. The desktop UI consumes it and focuses
// the referenced session or view.
type DesktopIntent struct {
	Action    string `json:"action,omitempty"`
	SessionID string `json:"sessionID,omitempty"`
	MessageID string `json:"messageID,omitempty"`
	Directory string `json:"directory,omitempty"`
}

// SetDesktopIntentNotifier registers a wake-up hook for the desktop webview. It
// carries no payload: the UI fetches the pending intent through the gateway so
// an early event emitted before the frontend subscribes can never be lost.
func (g *Gateway) SetDesktopIntentNotifier(fn func()) {
	g.intentMu.Lock()
	g.onDesktopIntent = fn
	g.intentMu.Unlock()
}

// QueueDesktopIntent replaces any pending intent and notifies the desktop UI.
func (g *Gateway) QueueDesktopIntent(intent DesktopIntent) {
	intent.Action = strings.TrimSpace(intent.Action)
	if intent.Action != "" && !validDesktopIntentAction(intent.Action) {
		return
	}
	intent.SessionID = strings.TrimSpace(intent.SessionID)
	if intent.SessionID != "" && !validDesktopEntityID(intent.SessionID, desktopSessionIDPrefix) {
		return
	}
	if intent.Action == "" && intent.SessionID == "" {
		return
	}
	intent.MessageID = strings.TrimSpace(intent.MessageID)
	if intent.MessageID != "" && !validDesktopEntityID(intent.MessageID, desktopMessageIDPrefix) {
		return
	}
	dir, ok := sanitizeDesktopDirectory(intent.Directory)
	if !ok {
		return
	}
	intent.Directory = dir
	g.intentMu.Lock()
	g.pendingIntent = &intent
	notify := g.onDesktopIntent
	g.intentMu.Unlock()
	if notify != nil {
		notify()
	}
}

func validDesktopIntentAction(action string) bool {
	switch action {
	case DesktopIntentActionAttention,
		DesktopIntentActionRunning,
		DesktopIntentActionNewThread,
		DesktopIntentActionHandoff,
		DesktopIntentActionPolicy,
		DesktopIntentActionSettings,
		DesktopIntentActionSyncKeepAwake:
		return true
	default:
		return false
	}
}

func validDesktopEntityID(value, prefix string) bool {
	if value == "" || len(value) <= len(prefix) || len(value) > maxDesktopEntityIDLength {
		return false
	}
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func sanitizeDesktopDirectory(dir string) (string, bool) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return "", true
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	if !filepath.IsAbs(trimmed) {
		return "", false
	}
	return filepath.Clean(trimmed), true
}

// ConsumeDesktopIntent returns and clears the pending intent.
func (g *Gateway) ConsumeDesktopIntent() (DesktopIntent, bool) {
	g.intentMu.Lock()
	defer g.intentMu.Unlock()
	if g.pendingIntent == nil {
		return DesktopIntent{}, false
	}
	intent := *g.pendingIntent
	g.pendingIntent = nil
	return intent, true
}
