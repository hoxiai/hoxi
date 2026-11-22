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

const summarizeTimeout = 3 * time.Minute

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
	var usage NativeTokenUsage
	var summary string
	var summaryErr error
	if metered, ok := eng.(interface {
		SummarizeWithUsage(context.Context, string, string, string, func(NativeTokenUsage)) (string, error)
	}); ok {
		summary, summaryErr = metered.SummarizeWithUsage(ctx, providerID, modelID, transcript, usage.Add)
	} else {
		summary, summaryErr = eng.Summarize(ctx, providerID, modelID, transcript)
	}
	if summaryErr != nil && usage == (NativeTokenUsage{}) {
		writeJSONError(w, http.StatusBadGateway, summaryErr.Error())
		return
	}

	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	trimmedSummary := strings.TrimSpace(summary)
	asstMsgID := fmt.Sprintf("msg_a_cmp_%d_%d", nowMs, seq)
	var parentUserID string
	for i := len(rec.Turns) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(rec.Turns[i].Info.Role), "user") {
			parentUserID = rec.Turns[i].Info.ID
			break
		}
	}
	compPart := MessagePart{
		ID:        "prt_cmp_" + asstMsgID,
		SessionID: rec.ID,
		MessageID: asstMsgID,
		Type:      "compaction",
		Text:      trimmedSummary,
		Metadata:  map[string]interface{}{"scope": "after_turn"},
		Time:      &PartTime{Start: nowMs, End: nowMs + 1},
	}
	summaryBody := "### Compacted Context Summary\n\n" + trimmedSummary
	asstPart := MessagePart{
		ID:        fmt.Sprintf("prt_a_cmp_%d_%d", nowMs, seq),
		SessionID: rec.ID,
		MessageID: asstMsgID,
		Type:      "text",
		Text:      summaryBody,
		Time:      &PartTime{Start: nowMs, End: nowMs + 1},
	}
	tokens := buildNativeMessageTokens(usage, NativeTokenUsage{}, usage)
	if summaryErr == nil {
		// The checkpoint occupies the new context; only provider receipts are billable.
		tokens.Context = max(1, len(summaryBody)/4)
		tokens.Total = tokens.Context
	}
	checkpointTurn := MessageTurn{
		Info: MessageInfo{
			ID:         asstMsgID,
			SessionID:  rec.ID,
			Role:       "assistant",
			ParentID:   parentUserID,
			Time:       MessageTime{Created: nowMs, Completed: nowMs + 1},
			Agent:      firstNonEmptyStr(rec.Mode, "build"),
			ProviderID: providerID,
			ModelID:    modelID,
			Finish:     "stop",
			Tokens:     tokens,
			Cost:       usage.CostUSD,
		},
		Parts: []MessagePart{compPart, asstPart},
	}

	if summaryErr != nil {
		// Persist paid segments even when the remaining request fails. An empty
		// accounting message has no checkpoint or model-visible history content.
		checkpointTurn.Info.Finish = "error"
		checkpointTurn.Parts = []MessagePart{}
	}
	_, ok, err := s.store.Update(rec.ID, func(sr *SessionRecord) {
		sr.Turns = append(sr.Turns, checkpointTurn)
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": checkpointTurn.Info}})
	for _, part := range checkpointTurn.Parts {
		s.broadcast(rec.ID, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
	}
	if summaryErr != nil {
		writeJSONError(w, http.StatusBadGateway, summaryErr.Error())
		return
	}
	s.syncSessionMemoryFile(rec.ID)
	writeJSON(w, http.StatusOK, true)
}

func formatTurnsForNativeSummary(turns []MessageTurn) string {
	var b strings.Builder
	startIdx := 0
	if compIdx, summary, scope, _, ok := findLatestCompactionCheckpoint(turns); ok {
		fmt.Fprintf(&b, "[PRIOR CHECKPOINT]\n%s\n\n", summary)
		switch scope {
		case "before_step":
			startIdx = compIdx
		case "after_turn":
			startIdx = compIdx + 1
		default:
			startIdx = compIdx
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
				if len(p.State.Input) > 0 {
					input, _ := json.Marshal(p.State.Input)
					fmt.Fprintf(&b, "[TOOL INPUT %s]\n%s\n", p.Tool, input)
				}
				fmt.Fprintf(&b, "[TOOL %s (%s)] %s\n%s\n\n", p.Tool, p.State.Status, p.State.Title, out)
			}
		}
	}
	// The engine chunks the complete transcript to the selected model's budget.
	return strings.TrimSpace(b.String())
}
