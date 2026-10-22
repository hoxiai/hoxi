package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/sandbox"
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
		directory := r.URL.Query().Get("directory")
		projectID := strings.TrimSpace(r.URL.Query().Get("projectID"))
		if projectID == "" && directory != "" {
			if resolved, ok := s.projectIDForDirectory(directory); ok {
				projectID = resolved
			}
		}
		writeJSON(w, http.StatusOK, s.listConversations(r.Context(), projectID, directory))
	case http.MethodPost:
		s.handleConversationCreate(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// listConversations degrades to Hoxi's own sessions when OpenCode is down.
func (s *Service) listConversations(ctx context.Context, projectID, directory string) []json.RawMessage {
	type entry struct {
		updated int64
		raw     json.RawMessage
	}
	var entries []entry
	owned := make(map[string]bool)
	for _, info := range s.store.ListProject(projectID, s.projectScopeDirectory(projectID, directory)) {
		raw, err := json.Marshal(info)
		if err != nil {
			continue
		}
		owned[info.ID] = true
		entries = append(entries, entry{info.Time.Updated, raw})
	}
	seenEngine := make(map[string]bool)
	for _, engineDir := range s.conversationDirectories(projectID, directory) {
		status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/session", directoryQuery(engineDir, url.Values{"limit": {"200"}}), nil)
		var items []json.RawMessage
		if err != nil || status != http.StatusOK || json.Unmarshal(raw, &items) != nil {
			continue
		}
		for _, item := range items {
			var info struct {
				ID        string `json:"id"`
				Directory string `json:"directory"`
				Time      struct {
					Updated int64 `json:"updated"`
				} `json:"time"`
				Metadata map[string]interface{} `json:"metadata"`
			}
			if json.Unmarshal(item, &info) != nil || info.ID == "" || owned[info.ID] || seenEngine[info.ID] ||
				s.IsManagedOpenCodeSession(info.ID) || info.Metadata["hoxiConversation"] != nil {
				continue
			}
			entryProjectID := projectID
			if entryProjectID == "" {
				entryProjectID, _ = s.projectIDForDirectory(engineDir)
			}
			itemProjectID := ""
			if itemDir := strings.TrimSpace(info.Directory); itemDir != "" {
				itemProjectID, _ = s.projectIDForDirectory(itemDir)
				// OpenCode may return sessions from nested directories that share
				// its git workspace. Filter by the session's own directory instead
				// of trusting the query root.
				if entryProjectID != "" && itemProjectID != "" && itemProjectID != entryProjectID {
					continue
				}
				if entryProjectID != "" && itemProjectID == "" &&
					normalizeComparePath(itemDir) != normalizeComparePath(engineDir) {
					continue
				}
			}
			if entryProjectID == "" {
				entryProjectID = itemProjectID
			}
			seenEngine[info.ID] = true
			entries = append(entries, entry{info.Time.Updated, withProjectID(item, entryProjectID)})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].updated > entries[j].updated })
	out := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.raw)
	}
	return out
}

// projectScopeDirectory supplies a legacy path fallback when a request only
// carries a project ID.
func (s *Service) projectScopeDirectory(projectID, directory string) string {
	if strings.TrimSpace(directory) != "" || projectID == "" || s.projectResolver == nil {
		return directory
	}
	if roots := s.projectResolver.ProjectRoots(projectID); len(roots) > 0 {
		return roots[0]
	}
	return directory
}

// conversationDirectories lists the engine roots that belong to one project.
// Falling back to the request directory keeps legacy/unresolved projects and
// the global feed working while still covering every folder of a multi-root
// project.
func (s *Service) conversationDirectories(projectID, directory string) []string {
	var dirs []string
	seen := make(map[string]bool)
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	if projectID != "" && s.projectResolver != nil {
		for _, root := range s.projectResolver.ProjectRoots(projectID) {
			add(root)
		}
	}
	if len(dirs) == 0 {
		add(directory)
	}
	return dirs
}

// withProjectID gives unadopted OpenCode sessions the same stable attribution
// as Hoxi-owned conversations before they are adopted.
func withProjectID(item json.RawMessage, projectID string) json.RawMessage {
	if strings.TrimSpace(projectID) == "" {
		return item
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(item, &object) != nil {
		return item
	}
	if _, exists := object["projectID"]; exists {
		return item
	}
	raw, err := json.Marshal(projectID)
	if err != nil {
		return item
	}
	object["projectID"] = raw
	merged, err := json.Marshal(object)
	if err != nil {
		return item
	}
	return merged
}

func (s *Service) handleConversationStatuses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	directory := r.URL.Query().Get("directory")
	projectID := strings.TrimSpace(r.URL.Query().Get("projectID"))
	if projectID == "" && directory != "" {
		if resolved, ok := s.projectIDForDirectory(directory); ok {
			projectID = resolved
		}
	}
	writeJSON(w, http.StatusOK, s.conversationStatuses(r.Context(), projectID, directory))
}

// conversationStatuses reports engine-handle activity under the conversation
// ID only, via the runner's own process table.
func (s *Service) conversationStatuses(ctx context.Context, projectID, directory string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage)
	for _, engineDir := range s.conversationDirectories(projectID, directory) {
		status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/session/status", directoryQuery(engineDir, nil), nil)
		var engine map[string]json.RawMessage
		if err != nil || status != http.StatusOK || json.Unmarshal(raw, &engine) != nil {
			continue
		}
		for id, st := range engine {
			if !s.IsManagedOpenCodeSession(id) {
				out[id] = st
			}
		}
	}
	allowed := make(map[string]bool)
	for _, info := range s.store.ListProject(projectID, s.projectScopeDirectory(projectID, directory)) {
		allowed[info.ID] = true
	}
	for _, id := range s.busySessionIDs() {
		if allowed[id] {
			out[id] = json.RawMessage(`{"type":"busy"}`)
		}
	}
	// Reconnecting clients restore the stall notice from the snapshot.
	for id, since := range s.stalledSessions() {
		if allowed[id] {
			out[id] = json.RawMessage(fmt.Sprintf(`{"type":"busy","stalledSince":%d}`, since))
		}
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
	var body CreateSessionRequest
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &body) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid session request")
		return
	}
	info, status, err := s.CreateConversation(r.Context(), body, r.URL.Query().Get("directory"))
	if err != nil {
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// CreateConversation creates a Hoxi conversation on any runtime: the shared
// implementation of POST /api/hoxi/sessions, also used in process (convoys).
// directory is the fallback project directory when body.Directory is empty.
func (s *Service) CreateConversation(ctx context.Context, body CreateSessionRequest, directory string) (SessionInfo, int, error) {
	var err error
	if IsValidRuntime(body.Runtime) {
		return s.CreateSession(body, directory)
	}
	if body.Runtime != "" && body.Runtime != "opencode" {
		return SessionInfo{}, http.StatusBadRequest, errors.New("unsupported session runtime")
	}
	projDir := firstNonEmptyStr(body.Directory, directory, s.rootDir)
	projDir, err = filepath.Abs(projDir)
	if err == nil {
		projDir, err = existingDirectory(projDir)
	}
	if err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	sandboxMode, err := normalizeSandboxMode(body.SandboxMode)
	if err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	if body.CommandSandbox && !sandbox.Available() {
		return SessionInfo{}, http.StatusBadRequest, errors.New(sandbox.UnavailableReason())
	}
	projectID, err := s.ensureProjectID(projDir)
	if err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	sessionID := GenerateRunnerSessionID("opencode")
	now := time.Now().UnixMilli()
	title := strings.TrimSpace(body.Title)
	rec := SessionRecord{
		ID:             sessionID,
		ProjectID:      projectID,
		Runtime:        "opencode",
		ProjectDir:     projDir,
		SandboxMode:    sandboxMode,
		CommandSandbox: body.CommandSandbox,
		ModelID:        strings.TrimSpace(body.ModelID),
		ProviderID:     strings.TrimSpace(body.ProviderID),
		Mode:           firstNonEmptyStr(body.Mode, "build"),
		Title:          title,
		CreatedAt:      now,
		UpdatedAt:      now,
		Turns:          []MessageTurn{},
	}
	if err := s.provisionSessionDirectory(&rec); err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	rec.ProviderID, rec.ModelID = s.resolveOpenCodeModel(ctx, rec.WorktreePath, rec.ProviderID, rec.ModelID)
	payload := map[string]interface{}{
		"metadata": map[string]string{"hoxiConversation": rec.ID},
	}
	if rec.Title != "" {
		payload["title"] = rec.Title
	}
	var engineInfo SessionInfo
	if err := s.openCodeJSON(ctx, http.MethodPost, "/session", rec.WorktreePath, payload, &engineInfo); err != nil || strings.TrimSpace(engineInfo.ID) == "" {
		if rec.SandboxMode == "worktree" {
			_ = s.worktrees.ForDirectory(rec.ProjectDir).RemoveWithOptions(rec.ID, true, false)
		}
		if err == nil {
			err = fmt.Errorf("OpenCode returned an empty session ID")
		}
		return SessionInfo{}, http.StatusBadGateway, fmt.Errorf("opencode service unavailable: %w", err)
	}
	rec.ExternalID = strings.TrimSpace(engineInfo.ID)
	rec.OpenCodeIDs = []string{rec.ExternalID}
	rec.OpenCodeDirectories = map[string]string{rec.ExternalID: rec.WorktreePath}
	if rec.Title == "" {
		rec.Title = firstNonEmptyStr(strings.TrimSpace(engineInfo.Title), "New Session")
	}
	if err := s.store.Put(rec); err != nil {
		return SessionInfo{}, http.StatusInternalServerError, err
	}
	_ = syncOpenCodeCommandSandbox(rec)
	s.managedOpenCode.Store(rec.ExternalID, true)
	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.created",
		Properties: map[string]interface{}{"info": info},
	})
	return info, http.StatusOK, nil
}
