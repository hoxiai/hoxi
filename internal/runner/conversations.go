package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The /api/hoxi/sessions collection: Hoxi conversations plus the OpenCode
// sessions Hoxi has not adopted yet. Engine handles never appear as chats.
// Per-session routes are mapped onto their owner by the gateway.

const maxOpenCodeResponseBytes = 32 << 20

// OwnsSession reports whether Hoxi's store is the source of truth for id.
func (s *Service) OwnsSession(id string) bool {
	_, ok := s.store.Meta(id)
	return ok
}

func (s *Service) openCodeRaw(ctx context.Context, method, path string, query url.Values, body []byte) (int, []byte, error) {
	if s.openCodeClient == nil {
		return 0, nil, fmt.Errorf("OpenCode connection is unavailable")
	}
	target := s.openCodeURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth("opencode", s.openCodePassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.openCodeClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenCodeResponseBytes))
	return resp.StatusCode, raw, err
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
	s.mu.Lock()
	for id := range s.procs {
		out[id] = json.RawMessage(`{"type":"busy"}`)
	}
	s.mu.Unlock()
	return out
}

// handleConversationCreate keeps CLI runtimes in the runner and, until new
// OpenCode chats get Hoxi records, creates those directly in OpenCode.
func (s *Service) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Runtime   string `json:"runtime"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
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
	payload := map[string]string{}
	if title := strings.TrimSpace(body.Title); title != "" {
		payload["title"] = title
	}
	encoded, _ := json.Marshal(payload)
	directory := firstNonEmptyStr(body.Directory, r.URL.Query().Get("directory"))
	status, out, err := s.openCodeRaw(r.Context(), http.MethodPost, "/session", directoryQuery(directory, nil), encoded)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out)
}
