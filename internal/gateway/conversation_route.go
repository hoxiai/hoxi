package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	conversationPrefix         = "/api/hoxi/sessions/"
	maxSkillSelectionScanBytes = 64 << 20
)

type internalConversationRouteKey struct{}

func markInternalConversationRoute(r *http.Request) {
	*r = *r.WithContext(context.WithValue(r.Context(), internalConversationRouteKey{}, true))
}

func isInternalConversationRoute(r *http.Request) bool {
	v, _ := r.Context().Value(internalConversationRouteKey{}).(bool)
	return v
}

func requestHasSkillSelection(r *http.Request) bool {
	return requestNeedsRunner(r, nil)
}

func requestNeedsRunner(r *http.Request, isImageModel func(providerID, modelID string) bool) bool {
	if r == nil || r.Body == nil || r.Method != http.MethodPost {
		return false
	}
	// Scan top-level keys as a stream and hand the backend the full body:
	// image attachments travel inline, so prompts are often several MB.
	body := r.Body
	var consumed bytes.Buffer
	found := scanRunnerPromptHints(json.NewDecoder(io.TeeReader(io.LimitReader(body, maxSkillSelectionScanBytes), &consumed)), isImageModel)
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(&consumed, body), body}
	return found
}

func scanSkillSelection(dec *json.Decoder) bool {
	return scanRunnerPromptHints(dec, nil)
}

func scanRunnerPromptHints(dec *json.Decoder, isImageModel func(providerID, modelID string) bool) bool {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return false
		}
		switch key, _ := tok.(string); key {
		case "skillSelection":
			trimmed := bytes.TrimSpace(value)
			if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
				return true
			}
		case "runtime":
			var rt string
			if json.Unmarshal(value, &rt) == nil && runner.IsValidRuntime(strings.TrimSpace(rt)) {
				return true
			}
		case "model":
			var m struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
			}
			if json.Unmarshal(value, &m) == nil {
				pid := strings.TrimSpace(m.ProviderID)
				mid := strings.TrimSpace(m.ModelID)
				if runner.IsValidRuntime(pid) {
					return true
				}
				if isImageModel != nil && isImageModel(pid, mid) {
					return true
				}
			}
		}
	}
	return false
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
	// Runtime switches, tool-stop actions, bound Work store sessions, skill-selected prompts, and image-model prompts always go to the runner.
	if action != "runtime" && action != "stop-tool" && !g.runner.OwnsSession(id) {
		hasStoreCtx := g.workApps != nil && g.workApps.FormatSessionStoreContext(id) != ""
		needsRunner := (action == "prompt" || action == "prompt_async") && (hasStoreCtx || requestNeedsRunner(r, g.runner.IsImageModel))
		if !needsRunner {
			if r.Method == http.MethodDelete && action == "" {
				g.runner.DeleteSessionAttachments(id)
			}
			markInternalConversationRoute(r)
			setRequestPath(r, "/session/"+id+suffix)
			return true
		}
	}
	setRequestPath(r, "/api/hoxi/runner/sessions/"+id+suffix)
	return true
}

func setRequestPath(r *http.Request, p string) {
	r.URL.Path = p
	r.URL.RawPath = ""
}
