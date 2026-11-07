package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
)

func (g *Gateway) resolveProjectMemoryRoot(rawDir, sessionID string) (string, bool) {
	var candidates []string
	if sessionID != "" && g.worktrees != nil {
		if binding, known, err := g.worktrees.BindingForSession(sessionID); err == nil && known && binding.Directory != "" {
			candidates = []string{binding.Directory}
		}
	}
	if len(candidates) == 0 {
		if trimmed := strings.TrimSpace(rawDir); trimmed != "" {
			candidates = append(candidates, trimmed)
		}
		if g.rootDir != "" {
			candidates = append(candidates, g.rootDir)
		}
	}
	for _, cand := range candidates {
		cleaned := strings.TrimSpace(expandUserPath(cand))
		if cleaned == "" {
			continue
		}
		if abs, err := filepath.Abs(cleaned); err == nil {
			cleaned = filepath.Clean(abs)
		} else {
			cleaned = filepath.Clean(cleaned)
		}
		if root := hoxipaths.ProjectRootForWorktree(cleaned); root != "" {
			cleaned = root
		}
		if st, err := os.Stat(cleaned); err == nil && st.IsDir() {
			return cleaned, true
		}
	}
	return "", false
}

func (g *Gateway) handleMemory(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "PUT or POST required")
		return
	}
	var body struct {
		Scope     string `json:"scope"`
		SessionID string `json:"sessionID"`
		Directory string `json:"directory"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, runner.MaxMemoryDocumentBytes+4096)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Content) > runner.MaxMemoryDocumentBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "memory document exceeds maximum size")
		return
	}
	scope := strings.ToLower(strings.TrimSpace(body.Scope))
	if scope == "" {
		scope = "project"
	}
	if scope != "project" && scope != "session" {
		writeJSONError(w, http.StatusBadRequest, "scope must be 'project' or 'session'")
		return
	}
	sid := runner.SanitizeMemorySessionID(body.SessionID)
	if scope == "session" && sid == "" {
		writeJSONError(w, http.StatusBadRequest, "valid sessionID is required for session memory")
		return
	}
	rawDir := strings.TrimSpace(body.Directory)
	if rawDir == "" {
		rawDir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	projectRoot, ok := g.resolveProjectMemoryRoot(rawDir, sid)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "workspace directory not found")
		return
	}

	targetFile, normalized, err := runner.SaveMemoryDocument(projectRoot, scope, sid, body.Content)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"scope":   scope,
		"path":    targetFile,
		"content": normalized,
	})
}

func (g *Gateway) handleMemoryPromote(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		SessionID string `json:"sessionID"`
		Directory string `json:"directory"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, runner.MaxMemoryDocumentBytes+4096)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sid := runner.SanitizeMemorySessionID(body.SessionID)
	rawDir := strings.TrimSpace(body.Directory)
	if rawDir == "" {
		rawDir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	projectRoot, ok := g.resolveProjectMemoryRoot(rawDir, sid)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "workspace directory not found")
		return
	}

	projFile, updated, added, err := runner.PromoteSessionMemoryToProject(projectRoot, sid, body.Content)
	if err != nil {
		if errors.Is(err, runner.ErrNoPromotableRules) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"path":       projFile,
		"content":    updated,
		"addedCount": added,
	})
}
