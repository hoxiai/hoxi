package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// DesktopCompanionState carries the live companion state from the workspace UI
// to the host floating desktop pet window. Never exposed over remote tunnels.
type DesktopCompanionState struct {
	Visible         bool   `json:"visible"`
	Minimized       bool   `json:"minimized"`
	DarkTheme       bool   `json:"darkTheme"`
	Persona         string `json:"persona"`
	PersonaName     string `json:"personaName"`
	Mood            string `json:"mood"`
	BadgeLabel      string `json:"badgeLabel"`
	LevelText       string `json:"levelText"`
	Dialogue        string `json:"dialogue"`
	ShowBubble      bool   `json:"showBubble"`
	TargetSessionID string `json:"targetSessionID"`
	AccentHex       string `json:"accentHex"`
	AvatarKey       string `json:"avatarKey"`
	AvatarDataURL   string `json:"avatarDataURL,omitempty"`
}

// DesktopCompanionSyncStatus reports native floating pet capability and cache status.
type DesktopCompanionSyncStatus struct {
	Supported  bool `json:"supported"`
	NeedAvatar bool `json:"needAvatar"`
}

const maxDesktopCompanionBodyBytes = 24 << 20

// SetDesktopCompanionBridge wires the native floating desktop pet callbacks.
// Nil in headless or unsupported OS builds.
func (g *Gateway) SetDesktopCompanionBridge(
	syncFn func(DesktopCompanionState) DesktopCompanionSyncStatus,
	resetPosFn func(),
) {
	g.companionMu.Lock()
	g.onDesktopCompanionSync = syncFn
	g.onDesktopCompanionReset = resetPosFn
	g.companionMu.Unlock()
}

func (g *Gateway) registerDesktopCompanionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/desktop/companion", func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "desktop companion is local only")
			return
		}
		g.companionMu.RLock()
		syncFn := g.onDesktopCompanionSync
		g.companionMu.RUnlock()

		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, DesktopCompanionSyncStatus{
				Supported:  syncFn != nil,
				NeedAvatar: false,
			})
		case http.MethodPost:
			if syncFn == nil {
				writeJSON(w, http.StatusOK, DesktopCompanionSyncStatus{
					Supported:  false,
					NeedAvatar: false,
				})
				return
			}
			var body DesktopCompanionState
			if err := json.NewDecoder(io.LimitReader(r.Body, maxDesktopCompanionBodyBytes)).Decode(&body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid companion payload")
				return
			}
			sid := strings.TrimSpace(body.TargetSessionID)
			if sid != "" && !validDesktopEntityID(sid, desktopSessionIDPrefix) {
				sid = ""
			}
			body.TargetSessionID = sid
			writeJSON(w, http.StatusOK, syncFn(body))
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})

	mux.HandleFunc("/api/hoxi/desktop/companion/reset-position", func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "desktop companion is local only")
			return
		}
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		g.companionMu.RLock()
		resetFn := g.onDesktopCompanionReset
		g.companionMu.RUnlock()
		if resetFn != nil {
			resetFn()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": resetFn != nil})
	})
}
