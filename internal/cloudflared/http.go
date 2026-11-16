package cloudflared

import (
	"encoding/json"
	"io"
	"net/http"
)

// RegisterRoutes mounts the desktop-only Cloudflare Tunnel management API. It is
// deliberately absent from the gateway's tunnel allow-list: a paired controller
// must never read the connector token or change the connector.
func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/cloudflared/status", m.handleStatus)
	mux.HandleFunc("/api/hoxi/cloudflared/enable", m.handleEnable)
	mux.HandleFunc("/api/hoxi/cloudflared/disable", m.handleDisable)
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handleEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if err := m.Enable(req.Token, req.Hostname); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handleDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := m.Disable(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
