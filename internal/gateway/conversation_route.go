package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const conversationPrefix = "/api/hoxi/sessions/"

type internalConversationRouteKey struct{}

func markInternalConversationRoute(r *http.Request) {
	*r = *r.WithContext(context.WithValue(r.Context(), internalConversationRouteKey{}, true))
}

func isInternalConversationRoute(r *http.Request) bool {
	v, _ := r.Context().Value(internalConversationRouteKey{}).(bool)
	return v
}

func requestHasSkillSelection(r *http.Request) bool {
	if r == nil || r.Body == nil || r.Method != http.MethodPost {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var probe struct {
		SkillSelection json.RawMessage `json:"skillSelection"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	trimmed := bytes.TrimSpace(probe.SkillSelection)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// routeConversation maps /api/hoxi/sessions/{id}[/action] onto the backend
// that owns the session: Hoxi's runner for conversations in its store, the
// OpenCode proxy for sessions it has not adopted. It runs before every other
// guard so AI consent, sandbox scope and update draining all judge the
// concrete path. It returns false once it has written the response itself.
func (g *Gateway) routeConversation(w http.ResponseWriter, r *http.Request) bool {
	if g.runner == nil || !strings.HasPrefix(r.URL.Path, conversationPrefix) {
		return true
	}
	rest := strings.TrimPrefix(r.URL.Path, conversationPrefix)
	if rest == "status" {
		return true
	}
	id, action, _ := strings.Cut(rest, "/")
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(action, "/") {
		writeJSONError(w, http.StatusNotFound, "unknown session endpoint")
		return false
	}
	suffix := ""
	if action != "" {
		suffix = "/" + action
	}
	// Runtime switches and skill-selected prompts adopt unmanaged OpenCode sessions, so they always go to the runner.
	hasSkill := (action == "prompt" || action == "prompt_async") && requestHasSkillSelection(r)
	if action != "runtime" && !hasSkill && !g.runner.OwnsSession(id) {
		if r.Method == http.MethodDelete && action == "" {
			g.runner.DeleteSessionAttachments(id)
		}
		markInternalConversationRoute(r)
		setRequestPath(r, "/session/"+id+suffix)
		return true
	}
	switch action {
	case "summarize":
		// CLI engines compact their own context; there is nothing to summarize.
		writeJSON(w, http.StatusOK, false)
		return false
	}
	setRequestPath(r, "/api/hoxi/runner/sessions/"+id+suffix)
	return true
}

func setRequestPath(r *http.Request, p string) {
	r.URL.Path = p
	r.URL.RawPath = ""
}
