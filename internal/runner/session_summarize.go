package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const summarizeTimeout = 3 * time.Minute

// handleSessionSummarize compacts an OpenCode-backed conversation. Native CLI
// runtimes compact their own context, so they only report that nothing ran.
func (s *Service) handleSessionSummarize(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	// Serialize with prompts and other turn mutations: compaction rewrites the
	// upstream context, so it must not overlap a running or starting turn.
	unlock := s.turnMu.lock(sessionID)
	defer unlock()
	var body struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil && err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "invalid summarize request")
		return
	}

	rec, ok := s.store.Get(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	if rec.Subagent {
		writeJSONError(w, http.StatusConflict, errSubagentSessionReadOnly.Error())
		return
	}
	if rec.Runtime != "opencode" || strings.TrimSpace(rec.ExternalID) == "" {
		writeJSON(w, http.StatusOK, false)
		return
	}

	s.mu.Lock()
	busy := s.procs[sessionID] != nil
	s.mu.Unlock()
	if busy {
		writeJSONError(w, http.StatusConflict, "cannot compact while the session is running")
		return
	}

	providerID := strings.TrimSpace(body.ProviderID)
	modelID := strings.TrimSpace(body.ModelID)
	if providerID == "" {
		providerID = rec.ProviderID
	}
	if modelID == "" {
		modelID = rec.ModelID
	}

	ctx, cancel := context.WithTimeout(r.Context(), summarizeTimeout)
	defer cancel()
	providerID, modelID = s.resolveOpenCodeModel(ctx, rec.WorktreePath, providerID, modelID)
	if providerID == "" || modelID == "" {
		writeJSONError(w, http.StatusBadRequest, "providerID and modelID are required")
		return
	}

	payload := map[string]string{"providerID": providerID, "modelID": modelID}
	if err := s.openCodeJSON(ctx, http.MethodPost, "/session/"+rec.ExternalID+"/summarize", rec.WorktreePath, payload, nil); err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	var turns []MessageTurn
	if err := s.openCodeJSON(ctx, http.MethodGet, "/session/"+rec.ExternalID+"/message", rec.WorktreePath, nil, &turns); err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	normalized := make([]MessageTurn, 0, len(turns))
	for _, turn := range turns {
		turn.Info.SessionID = rec.ID
		parts := make([]MessagePart, 0, len(turn.Parts))
		for _, part := range turn.Parts {
			part.SessionID = rec.ID
			if !s.normalizeOpenCodePart(rec, &part, nil) {
				continue
			}
			parts = append(parts, part)
		}
		turn.Parts = parts
		normalized = append(normalized, turn)
	}

	seenMessages := make(map[string]bool, len(rec.Turns))
	seenParts := make(map[partKey]bool)
	for _, turn := range rec.Turns {
		seenMessages[turn.Info.ID] = true
		for _, part := range turn.Parts {
			seenParts[partKey{messageID: part.MessageID, partID: part.ID}] = true
		}
	}

	updated, ok, err := s.store.Update(sessionID, func(r *SessionRecord) {
		r.Turns = normalized
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	rec = updated

	for _, turn := range normalized {
		if !seenMessages[turn.Info.ID] {
			s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": turn.Info}})
		}
		for _, part := range turn.Parts {
			if seenParts[partKey{messageID: part.MessageID, partID: part.ID}] {
				continue
			}
			s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
		}
	}
	s.syncSessionMemoryFile(rec.ID)

	writeJSON(w, http.StatusOK, true)
}
