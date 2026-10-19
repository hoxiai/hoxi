package gateway

import (
	"net/http"
	"net/url"
	"strings"
)

const conversationPrefix = "/api/hoxi/sessions/"

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
	// Runtime switches adopt unmanaged OpenCode sessions, so they always go to the runner.
	if action != "runtime" && !g.runner.OwnsSession(id) {
		setRequestPath(r, "/session/"+id+suffix)
		return true
	}
	switch action {
	case "summarize":
		// CLI engines compact their own context; there is nothing to summarize.
		writeJSON(w, http.StatusOK, false)
		return false
	case "revert", "unrevert", "fork":
		writeJSONError(w, http.StatusNotImplemented, action+" is not supported for this session yet")
		return false
	case "diff":
		setRequestPath(r, "/api/hoxi/worktree/diff")
		values, _ := url.ParseQuery(r.URL.RawQuery)
		values.Set("sessionID", id)
		r.URL.RawQuery = values.Encode()
		return true
	}
	setRequestPath(r, "/api/hoxi/runner/sessions/"+id+suffix)
	return true
}

func setRequestPath(r *http.Request, p string) {
	r.URL.Path = p
	r.URL.RawPath = ""
}
