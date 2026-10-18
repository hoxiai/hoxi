package gateway

import (
	"net/http"
	"path"
	"strings"

	"github.com/hoxiai/hoxi/internal/privacy"
)

func (g *Gateway) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, g.privacy.Status())
	case http.MethodPut:
		status, err := privacy.Decode(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid privacy consent: "+err.Error())
			return
		}
		if g.privacy == nil || g.privacy.Set(status) != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not persist privacy consent")
			return
		}
		writeJSON(w, http.StatusOK, status)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) guardPrivacy(w http.ResponseWriter, r *http.Request) bool {
	if !requiresAIConsent(r.Method, r.URL.Path) || g.privacy.Status().Accepted {
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": "AI-data consent required", "code": "privacy_consent_required", "version": privacy.Version,
	})
	return false
}

func requiresAIConsent(method, requestPath string) bool {
	p := path.Clean(requestPath)
	if method == http.MethodOptions {
		return false
	}
	// Grok forwards arbitrary paths and methods except this local model listing.
	if p == "/api/hoxi/bridge/grok-cli" || strings.HasPrefix(p, "/api/hoxi/bridge/grok-cli/") {
		return !(method == http.MethodGet && requestPath == "/api/hoxi/bridge/grok-cli/v1/models")
	}
	if method != http.MethodPost {
		return false
	}
	switch p {
	case "/api/hoxi/provider/test-model", "/api/hoxi/provider/audit",
		"/api/hoxi/provider/summarize-title", "/api/hoxi/provider/generate-commit-message":
		return true
	}
	if strings.HasPrefix(p, "/api/hoxi/runner/sessions/") {
		parts := strings.Split(strings.TrimPrefix(p, "/api/hoxi/runner/sessions/"), "/")
		return len(parts) == 2 && (strings.TrimSpace(parts[1]) == "prompt" || strings.TrimSpace(parts[1]) == "prompt_async")
	}
	p = strings.TrimPrefix(p, "/api/")
	p = strings.TrimPrefix(p, "/")
	parts := strings.Split(p, "/")
	if len(parts) != 3 || parts[0] != "session" || parts[1] == "" {
		return false
	}
	switch parts[2] {
	case "message", "prompt", "prompt_async", "command", "summarize", "shell", "init":
		return true
	}
	return false
}
