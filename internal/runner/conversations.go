package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The /api/hoxi/sessions collection: Hoxi conversations plus the OpenCode
// sessions Hoxi has not adopted yet. Engine handles never appear as chats.
// Per-session routes are mapped onto their owner by the gateway.

// OwnsSession reports whether Hoxi's store is the source of truth for id.
func (s *Service) OwnsSession(id string) bool {
	_, ok := s.store.Meta(id)
	return ok
}

func (s *Service) openCodeRaw(ctx context.Context, method, path string, query url.Values, body []byte) (int, []byte, error) {
	return s.openCode.Raw(ctx, method, path, query, body)
}

func directoryQuery(directory string, extra url.Values) url.Values {
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	if strings.TrimSpace(directory) != "" {
		q.Set("directory", directory)
	}
	return q
}

func (s *Service) handleConversationsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.listConversations(r.Context(), r.URL.Query().Get("directory")))
	case http.MethodPost:
		s.handleConversationCreate(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// listConversations degrades to Hoxi's own sessions when OpenCode is down.
func (s *Service) listConversations(ctx context.Context, directory string) []json.RawMessage {
	type entry struct {
		updated int64
		raw     json.RawMessage
	}
	var entries []entry
	owned := make(map[string]bool)
	for _, info := range s.store.List(directory) {
		raw, err := json.Marshal(info)
		if err != nil {
			continue
		}
		owned[info.ID] = true
		entries = append(entries, entry{info.Time.Updated, raw})
	}
	status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/session", directoryQuery(directory, url.Values{"limit": {"200"}}), nil)
	var items []json.RawMessage
	if err == nil && status == http.StatusOK && json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			var info struct {
				ID   string `json:"id"`
				Time struct {
					Updated int64 `json:"updated"`
				} `json:"time"`
				Metadata map[string]interface{} `json:"metadata"`
			}
			if json.Unmarshal(item, &info) != nil || info.ID == "" || owned[info.ID] ||
				s.IsManagedOpenCodeSession(info.ID) || info.Metadata["hoxiConversation"] != nil {
				continue
			}
			entries = append(entries, entry{info.Time.Updated, item})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].updated > entries[j].updated })
	out := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.raw)
	}
	return out
}

func (s *Service) handleConversationStatuses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	writeJSON(w, http.StatusOK, s.conversationStatuses(r.Context(), r.URL.Query().Get("directory")))
}

// conversationStatuses reports engine-handle activity under the conversation
// ID only, via the runner's own process table.
func (s *Service) conversationStatuses(ctx context.Context, directory string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage)
	status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/session/status", directoryQuery(directory, nil), nil)
	var engine map[string]json.RawMessage
	if err == nil && status == http.StatusOK && json.Unmarshal(raw, &engine) == nil {
		for id, st := range engine {
			if !s.IsManagedOpenCodeSession(id) {
				out[id] = st
			}
		}
	}
	for _, id := range s.busySessionIDs() {
		out[id] = json.RawMessage(`{"type":"busy"}`)
	}
	// Reconnecting clients restore the stall notice from the snapshot.
	for id, since := range s.stalledSessions() {
		out[id] = json.RawMessage(fmt.Sprintf(`{"type":"busy","stalledSince":%d}`, since))
	}
	return out
}

// handleConversationCreate creates a Hoxi-owned SessionRecord for all runtimes.
// When the runtime is OpenCode, it also creates an OpenCode session as the
// backing execution handle and records it in OpenCodeIDs.
func (s *Service) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Title       string `json:"title"`
		Directory   string `json:"directory"`
		Runtime     string `json:"runtime"`
		ModelID     string `json:"modelID"`
		ProviderID  string `json:"providerID"`
		Mode        string `json:"mode"`
		SandboxMode string `json:"sandboxMode"`
	}
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &body) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid session request")
		return
	}
	if IsValidRuntime(body.Runtime) {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		s.handleCreateSession(w, r)
		return
	}
	if body.Runtime != "" && body.Runtime != "opencode" {
		writeJSONError(w, http.StatusBadRequest, "unsupported session runtime")
		return
	}
	projDir := firstNonEmptyStr(body.Directory, r.URL.Query().Get("directory"), s.rootDir)
	projDir, err = filepath.Abs(projDir)
	if err == nil {
		projDir, err = existingDirectory(projDir)
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	sandboxMode, err := normalizeSandboxMode(body.SandboxMode)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	sessionID := GenerateRunnerSessionID("opencode")
	now := time.Now().UnixMilli()
	title := strings.TrimSpace(body.Title)
	rec := SessionRecord{
		ID:          sessionID,
		Runtime:     "opencode",
		ProjectDir:  projDir,
		SandboxMode: sandboxMode,
		ModelID:     strings.TrimSpace(body.ModelID),
		ProviderID:  strings.TrimSpace(body.ProviderID),
		Mode:        firstNonEmptyStr(body.Mode, "build"),
		Title:       title,
		CreatedAt:   now,
		UpdatedAt:   now,
		Turns:       []MessageTurn{},
	}
	if err := s.provisionSessionDirectory(&rec); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	rec.ProviderID, rec.ModelID = s.resolveOpenCodeModel(r.Context(), rec.WorktreePath, rec.ProviderID, rec.ModelID)
	payload := map[string]interface{}{
		"metadata": map[string]string{"hoxiConversation": rec.ID},
	}
	if rec.Title != "" {
		payload["title"] = rec.Title
	}
	var engineInfo SessionInfo
	if err := s.openCodeJSON(r.Context(), http.MethodPost, "/session", rec.WorktreePath, payload, &engineInfo); err != nil || strings.TrimSpace(engineInfo.ID) == "" {
		if rec.SandboxMode == "worktree" {
			_ = s.worktrees.ForDirectory(rec.ProjectDir).RemoveWithOptions(rec.ID, true, false)
		}
		if err == nil {
			err = fmt.Errorf("OpenCode returned an empty session ID")
		}
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
		return
	}
	rec.ExternalID = strings.TrimSpace(engineInfo.ID)
	rec.OpenCodeIDs = []string{rec.ExternalID}
	rec.OpenCodeDirectories = map[string]string{rec.ExternalID: rec.WorktreePath}
	if rec.Title == "" {
		rec.Title = firstNonEmptyStr(strings.TrimSpace(engineInfo.Title), "New Session")
	}
	if err := s.store.Put(rec); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.managedOpenCode.Store(rec.ExternalID, true)
	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.created",
		Properties: map[string]interface{}{"info": info},
	})
	writeJSON(w, http.StatusOK, info)
}
