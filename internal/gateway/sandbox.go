package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

type sandboxSessionResponse struct {
	SessionID string `json:"sessionID"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Network   string `json:"network,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// handleSandboxSession toggles OS-level command confinement for both native
// runner sessions and upstream OpenCode sessions. OpenCode sessions are
// enforced by the sandbox plugin overlay at tool-execution time.
func (g *Gateway) handleSandboxSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
		if sessionID == "" {
			writeJSONError(w, http.StatusBadRequest, "sessionID is required")
			return
		}
		g.writeSandboxSession(w, sessionID)
	case http.MethodDelete:
		sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
		if sessionID == "" {
			writeJSONError(w, http.StatusBadRequest, "sessionID is required")
			return
		}
		g.applySandboxSession(w, r, sessionID, false, sandbox.NetworkDeny, "")
	case http.MethodPost:
		var body struct {
			SessionID string `json:"sessionID"`
			Directory string `json:"directory"`
			Enabled   bool   `json:"enabled"`
			Network   string `json:"network"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid sandbox request")
			return
		}
		sessionID := strings.TrimSpace(body.SessionID)
		if sessionID == "" {
			writeJSONError(w, http.StatusBadRequest, "sessionID is required")
			return
		}
		mode, err := sandbox.NormalizeNetworkMode(body.Network)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		g.applySandboxSession(w, r, sessionID, body.Enabled, mode, strings.TrimSpace(body.Directory))
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) writeSandboxSession(w http.ResponseWriter, sessionID string) {
	available := sandbox.Available()
	reason := ""
	if !available {
		reason = sandbox.UnavailableReason()
	}
	if g.runner != nil {
		if rec, ok := g.runner.Store().Get(sessionID); ok {
			writeJSON(w, http.StatusOK, sandboxSessionResponse{
				SessionID: sessionID,
				Enabled:   rec.CommandSandbox && available,
				Available: available,
				Network:   string(sandbox.NetworkAllow),
				Reason:    reason,
			})
			return
		}
	}
	snapshot, enabled, err := g.sandbox.Get(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	network := string(snapshot.Network)
	if network == "" {
		network = string(sandbox.NetworkDeny)
	}
	writeJSON(w, http.StatusOK, sandboxSessionResponse{
		SessionID: sessionID,
		Enabled:   enabled && available,
		Available: available,
		Network:   network,
		Reason:    reason,
	})
}

func (g *Gateway) applySandboxSession(w http.ResponseWriter, r *http.Request, sessionID string, enabled bool, network sandbox.NetworkMode, directory string) {
	if enabled && !sandbox.Available() {
		writeJSONError(w, http.StatusBadRequest, sandbox.UnavailableReason())
		return
	}
	if g.runner != nil {
		if _, ok := g.runner.Store().Get(sessionID); ok {
			info, err := g.runner.SetCommandSandbox(sessionID, enabled)
			if err != nil {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, sandboxSessionResponse{
				SessionID: info.ID,
				Enabled:   info.CommandSandbox,
				Available: sandbox.Available(),
				Network:   string(sandbox.NetworkAllow),
			})
			return
		}
	}
	if !enabled {
		if err := g.sandbox.Remove(sessionID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		g.writeSandboxSession(w, sessionID)
		return
	}
	directory, err := g.resolveSandboxDirectory(r, sessionID, directory)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := g.sandbox.Set(sessionID, directory, true, network); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	g.writeSandboxSession(w, sessionID)
}

// resolveSandboxDirectory prefers Hoxi's authoritative worktree binding and
// rejects a conflicting directory selector from the client.
func (g *Gateway) resolveSandboxDirectory(r *http.Request, sessionID, directory string) (string, error) {
	if g.worktrees != nil {
		binding, known, err := g.worktrees.BindingForSession(sessionID)
		if err != nil {
			return "", err
		}
		if known {
			if directory != "" && !sameDirectory(directory, binding.Directory) && !sameSandboxParentDirectory(binding.Directory, directory) {
				return "", errors.New("session directory does not match its sandbox")
			}
			return binding.Directory, nil
		}
	}
	if directory == "" {
		return "", errors.New("directory is required for this session")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return "", errors.New("directory does not exist")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}
