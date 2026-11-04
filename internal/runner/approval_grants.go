package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
)

// ApprovalGrant describes one in-memory session-scoped "always" grant.
// Grants live in memory only for the lifetime of the Hoxi process and never
// cover destructive commands.
type ApprovalGrant struct {
	ID           string `json:"id"`
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Kind         string `json:"kind"` // "bash" | "tool" | "doom_loop"
	Tool         string `json:"tool"` // "bash" or tool name
	// Key is the internal lookup key (the raw command or a tool+args hash).
	// It is never serialized: it can contain credentials the redacted
	// Pattern hides, and only the gate needs it.
	Key       string `json:"-"`       // internal lookup key
	Pattern   string `json:"pattern"` // redacted human-readable command or args summary
	Scope     string `json:"scope"`   // "session_memory"
	Source    string `json:"source"`  // "desktop"
	CreatedAt int64  `json:"createdAt"`
}

func makeGrantID(sessionID, kind, key string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + kind + "\x00" + key))
	return "grt_" + hex.EncodeToString(sum[:8])
}

func (s *Service) listApprovalGrants(sessionID, directory string) []ApprovalGrant {
	sidFilter := strings.TrimSpace(sessionID)
	dirFilter := normalizeComparePath(directory)

	raw := s.gate.listGrants()
	raw = append(raw, s.doomLoop.listExemptGrants()...)

	out := make([]ApprovalGrant, 0, len(raw))
	for _, g := range raw {
		if sidFilter != "" && g.SessionID != sidFilter {
			continue
		}
		title, rt, _ := s.enrichSessionMeta(g.SessionID)
		if dirFilter != "" {
			rec, ok := s.store.Meta(g.SessionID)
			if !ok || (normalizeComparePath(rec.ProjectDir) != dirFilter && normalizeComparePath(rec.WorktreePath) != dirFilter) {
				continue
			}
		}
		g.SessionTitle = title
		g.Runtime = rt
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Service) revokeApprovalGrants(id, sessionID string, all bool) int {
	id = strings.TrimSpace(id)
	sessionID = strings.TrimSpace(sessionID)
	revoked := s.gate.revokeGrants(id, sessionID, all)
	revoked += s.doomLoop.revokeExemptGrants(id, sessionID, all)
	return revoked
}

func (s *Service) handleApprovalGrants(w http.ResponseWriter, r *http.Request) {
	if isRemoteApprovalSource(requestApprovalSource(r)) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		grants := s.listApprovalGrants(q.Get("sessionID"), q.Get("directory"))
		writeJSON(w, http.StatusOK, map[string]interface{}{"grants": grants})
	case http.MethodDelete:
		id := strings.TrimSpace(q.Get("id"))
		sessionID := strings.TrimSpace(q.Get("sessionID"))
		all := q.Get("all") == "1" || q.Get("all") == "true"
		if id == "" && sessionID == "" && !all {
			writeJSONError(w, http.StatusBadRequest, "id, sessionID, or all=1 is required to revoke grants")
			return
		}
		revoked := s.revokeApprovalGrants(id, sessionID, all)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"revoked": revoked,
			"grants":  s.listApprovalGrants("", ""),
		})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or DELETE required")
	}
}

func (g *approvalGate) listGrants() []ApprovalGrant {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ApprovalGrant
	for _, cmds := range g.always {
		for _, gr := range cmds {
			out = append(out, gr)
		}
	}
	for _, tools := range g.toolAlways {
		for _, gr := range tools {
			out = append(out, gr)
		}
	}
	for _, classes := range g.classGrants {
		for _, gr := range classes {
			out = append(out, gr)
		}
	}
	return out
}

func (g *approvalGate) revokeGrants(id, sessionID string, all bool) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	revoked := 0
	for sid, cmds := range g.always {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for k, gr := range cmds {
			if all || (sessionID != "" && id == "") || (id != "" && gr.ID == id) {
				delete(cmds, k)
				revoked++
			}
		}
		if len(cmds) == 0 {
			delete(g.always, sid)
		}
	}
	for sid, classes := range g.classGrants {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for k, gr := range classes {
			if all || (sessionID != "" && id == "") || (id != "" && gr.ID == id) {
				delete(classes, k)
				revoked++
			}
		}
		if len(classes) == 0 {
			delete(g.classGrants, sid)
		}
	}
	for sid, tools := range g.toolAlways {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for k, gr := range tools {
			if all || (sessionID != "" && id == "") || (id != "" && gr.ID == id) {
				delete(tools, k)
				revoked++
			}
		}
		if len(tools) == 0 {
			delete(g.toolAlways, sid)
		}
	}
	return revoked
}
