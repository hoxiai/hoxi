package gateway

import "strings"

// DesktopIntent is a one-shot navigation request produced by a deep link or the
// CLI. The desktop UI consumes it and focuses the referenced session.
type DesktopIntent struct {
	SessionID string `json:"sessionID"`
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
	intent.SessionID = strings.TrimSpace(intent.SessionID)
	if intent.SessionID == "" {
		return
	}
	intent.MessageID = strings.TrimSpace(intent.MessageID)
	g.intentMu.Lock()
	g.pendingIntent = &intent
	notify := g.onDesktopIntent
	g.intentMu.Unlock()
	if notify != nil {
		notify()
	}
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
