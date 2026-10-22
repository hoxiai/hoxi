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

// DesktopNotificationPermission is the host's notification authorization state.
// Available is false when the build has no native transport (headless, no app
// bundle or non-darwin host).
type DesktopNotificationPermission struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

// SetDesktopNotifier wires the native notification transport. It is nil in
// headless mode, where the route reports notifications as unavailable.
func (g *Gateway) SetDesktopNotifier(fn func(DesktopNotification) error) {
	g.notifyMu.Lock()
	g.onDesktopNotify = fn
	g.notifyMu.Unlock()
}

// SetDesktopNotificationPermission wires the native permission inspection and
// prompt. Both hooks are nil in headless mode, where the routes report
// notifications as unavailable.
func (g *Gateway) SetDesktopNotificationPermission(
	status func() DesktopNotificationPermission,
	request func() DesktopNotificationPermission,
) {
	g.notifyMu.Lock()
	g.onDesktopNotifyStatus = status
	g.onDesktopNotifyAsk = request
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

	mux.HandleFunc("/api/hoxi/desktop/notify/permission", func(w http.ResponseWriter, r *http.Request) {
		g.notifyMu.RLock()
		statusFn := g.onDesktopNotifyStatus
		askFn := g.onDesktopNotifyAsk
		g.notifyMu.RUnlock()

		unavailable := DesktopNotificationPermission{Available: false, Status: "unavailable"}
		switch r.Method {
		case http.MethodGet:
			if statusFn == nil {
				writeJSON(w, http.StatusOK, unavailable)
				return
			}
			writeJSON(w, http.StatusOK, statusFn())
		case http.MethodPost:
			if askFn == nil {
				writeJSON(w, http.StatusOK, unavailable)
				return
			}
			writeJSON(w, http.StatusOK, askFn())
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})
}
