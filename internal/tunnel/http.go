package tunnel

import (
	"encoding/json"
	"io"
	"net/http"
)

// RegisterRoutes mounts the desktop-only management API. The gateway's remote
// allowlist keeps these routes unreachable through the tunnel itself.
func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/tunnel/status", m.handleStatus)
	mux.HandleFunc("/api/hoxi/tunnel/qr", m.handleQR)
	mux.HandleFunc("/api/hoxi/tunnel/pair", m.handlePair)
	mux.HandleFunc("/api/hoxi/tunnel/pair/request", m.handlePairRequest)
	mux.HandleFunc("/api/hoxi/tunnel/pair/cancel", m.handlePairCancel)
	mux.HandleFunc("/api/hoxi/tunnel/disable", m.handleDisable)
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handleQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, m.RelayQRInfo())
}

func (m *Manager) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ControllerURL string `json:"controllerUrl"`
		Code          string `json:"code"`
		Name          string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if err := m.Pair(r.Context(), req.ControllerURL, req.Code, req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handlePairRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ControllerURL string `json:"controllerUrl"`
		Name          string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if err := m.RequestPair(r.Context(), req.ControllerURL, req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handlePairCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	m.CancelPair()
	writeJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handleDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := m.Unpair(); err != nil {
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
