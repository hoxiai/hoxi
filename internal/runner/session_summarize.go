package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	summarizeTimeout           = 3 * time.Minute
	maxNativeSummaryInputChars = 120000
)

// handleSessionSummarize compacts an OpenCode or Native Go Agent conversation.
// External CLI runtimes compact their own context, so they report false.
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
	isOpenCode := rec.Runtime == "opencode" && strings.TrimSpace(rec.ExternalID) != ""
	isNative := rec.Runtime == string(RuntimeNative)
	if !isOpenCode && !isNative {
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

	if isNative {
		s.handleNativeSessionSummarize(w, r, rec, providerID, modelID)
		return
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
	continuations := make(map[string]bool)
	for _, turn := range rec.Turns {
		continuations[turn.Info.ID] = turn.Info.ContinueTask
	}
	for _, turn := range turns {
		turn.Info.SessionID = rec.ID
		turn.Info.ContinueTask = turn.Info.ContinueTask || continuations[turn.Info.ID]
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

func (s *Service) handleNativeSessionSummarize(
	w http.ResponseWriter,
	r *http.Request,
	rec SessionRecord,
	providerID, modelID string,
) {
	s.mu.Lock()
	eng := s.nativeEngine
	s.mu.Unlock()
	if eng == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "native engine is not configured")
		return
	}
	if providerID == "" || modelID == "" {
		writeJSONError(w, http.StatusBadRequest, "providerID and modelID are required")
		return
	}
	transcript := formatTurnsForNativeSummary(rec.Turns)
	if strings.TrimSpace(transcript) == "" {
		writeJSON(w, http.StatusOK, false)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), summarizeTimeout)
	defer cancel()
	summary, err := eng.Summarize(ctx, providerID, modelID, transcript)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	userMsgID := fmt.Sprintf("msg_u_cmp_%d_%d", nowMs, seq)
	asstMsgID := fmt.Sprintf("msg_a_cmp_%d_%d", nowMs+1, seq)
	userPart := MessagePart{
		ID:        fmt.Sprintf("prt_u_cmp_%d_%d", nowMs, seq),
		SessionID: rec.ID,
		MessageID: userMsgID,
		Type:      "text",
		Text:      "[Context Compacted]",
	}
	summaryBody := "### Compacted Context Summary\n\n" + strings.TrimSpace(summary)
	asstPart := MessagePart{
		ID:        fmt.Sprintf("prt_a_cmp_%d_%d", nowMs+1, seq),
		SessionID: rec.ID,
		MessageID: asstMsgID,
		Type:      "text",
		Text:      summaryBody,
		Time:      &PartTime{Start: nowMs, End: nowMs + 1},
	}
	estTokens := max(1, len(summaryBody)/4)
	compacted := []MessageTurn{
		{
			Info: MessageInfo{
				ID:         userMsgID,
				SessionID:  rec.ID,
				Role:       "user",
				Time:       MessageTime{Created: nowMs},
				Agent:      firstNonEmptyStr(rec.Mode, "build"),
				ProviderID: providerID,
				ModelID:    modelID,
			},
			Parts: []MessagePart{userPart},
		},
		{
			Info: MessageInfo{
				ID:         asstMsgID,
				SessionID:  rec.ID,
				Role:       "assistant",
				ParentID:   userMsgID,
				Time:       MessageTime{Created: nowMs, Completed: nowMs + 1},
				Agent:      firstNonEmptyStr(rec.Mode, "build"),
				ProviderID: providerID,
				ModelID:    modelID,
				Finish:     "stop",
				Tokens:     &MessageTokens{Total: estTokens, Output: estTokens},
			},
			Parts: []MessagePart{asstPart},
		},
	}

	updated, ok, err := s.store.Update(rec.ID, func(sr *SessionRecord) {
		sr.Turns = compacted
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	for _, turn := range updated.Turns {
		s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": turn.Info}})
		for _, part := range turn.Parts {
			s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
		}
	}
	s.syncSessionMemoryFile(rec.ID)
	writeJSON(w, http.StatusOK, true)
}

func formatTurnsForNativeSummary(turns []MessageTurn) string {
	var b strings.Builder
	startIdx := 0
	if compIdx, summary, scope, _, ok := findLatestCompactionCheckpoint(turns); ok {
		fmt.Fprintf(&b, "[PRIOR CHECKPOINT]\n%s\n\n", summary)
		startIdx = compIdx
		if scope != "before_step" {
			if parentID := strings.TrimSpace(turns[compIdx].Info.ParentID); parentID != "" {
				for k := compIdx - 1; k >= 0; k-- {
					if turns[k].Info.ID == parentID {
						startIdx = k
						break
					}
				}
			} else if compIdx > 0 && strings.EqualFold(strings.TrimSpace(turns[compIdx-1].Info.Role), "user") {
				startIdx = compIdx - 1
			}
		}
	}
	for _, t := range turns[startIdx:] {
		role := strings.ToUpper(strings.TrimSpace(t.Info.Role))
		if role == "" {
			role = "MESSAGE"
		}
		for _, p := range t.Parts {
			switch p.Type {
			case "text":
				if strings.TrimSpace(p.Text) != "" {
					fmt.Fprintf(&b, "[%s]\n%s\n\n", role, strings.TrimSpace(p.Text))
				}
			case "file":
				fmt.Fprintf(&b, "[%s ATTACHMENT: %s (%s)]\n\n", role, p.Filename, p.Mime)
			case "tool":
				if p.State == nil {
					continue
				}
				out := strings.TrimSpace(firstNonEmptyStr(p.State.Output, p.State.Error))
				if len(out) > 1200 {
					out = out[:1200] + "...[truncated]"
				}
				fmt.Fprintf(&b, "[TOOL %s (%s)] %s\n%s\n\n", p.Tool, p.State.Status, p.State.Title, out)
			}
		}
	}
	text := strings.TrimSpace(b.String())
	if len(text) > maxNativeSummaryInputChars {
		head := maxNativeSummaryInputChars / 3
		tail := maxNativeSummaryInputChars - head
		text = text[:head] + "\n\n...[earlier turns omitted]...\n\n" + text[len(text)-tail:]
	}
	return text
}
