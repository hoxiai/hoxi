package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// DesktopNotification is a transport-neutral request from the workspace UI to
// the host notification service. It stays outside the remote tunnel allowlist.
type DesktopNotification struct {
	Title     string
	Body      string
	SessionID string
}

// SetDesktopNotifier wires the native notification transport. It is nil in
// headless mode, where the route reports notifications as unavailable.
func (g *Gateway) SetDesktopNotifier(fn func(DesktopNotification) error) {
	g.notifyMu.Lock()
	g.onDesktopNotify = fn
	g.notifyMu.Unlock()
}

func (g *Gateway) registerDesktopNotifyRoute(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/desktop/notify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		var body struct {
			Title     string `json:"title"`
			Body      string `json:"body"`
			SessionID string `json:"sessionID"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid notification payload")
			return
		}
		sessionID := strings.TrimSpace(body.SessionID)
		if sessionID != "" && !validDesktopEntityID(sessionID, desktopSessionIDPrefix) {
			writeJSONError(w, http.StatusBadRequest, "invalid sessionID")
			return
		}
		g.notifyMu.RLock()
		notifyFn := g.onDesktopNotify
		g.notifyMu.RUnlock()
		if notifyFn == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "desktop notifications unavailable")
			return
		}
		err := notifyFn(DesktopNotification{
			Title:     strings.TrimSpace(body.Title),
			Body:      strings.TrimSpace(body.Body),
			SessionID: sessionID,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}
