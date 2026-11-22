package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NativeSteerItem represents one queued user steering turn (text + optional media)
// delivered to the running in-process NativeEngine at the next safe step boundary.
type NativeSteerItem struct {
	UserMsgID string
	Prompt    string
	Blocks    []NativeContentBlock
}

func (s *Service) nextNativeAssistantMessageID(nowMs int64) string {
	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()
	return fmt.Sprintf("msg_a_%d_%d", nowMs+1, seq)
}

func (s *Service) pruneEmptyAssistantTurn(sessionID, asstMsgID string) {
	if s == nil || s.store == nil || sessionID == "" || asstMsgID == "" {
		return
	}
	_, _, _ = s.store.UpdateWithoutActivity(sessionID, func(r *SessionRecord) {
		for i := len(r.Turns) - 1; i >= 0; i-- {
			if r.Turns[i].Info.ID == asstMsgID && r.Turns[i].Info.Role == "assistant" && len(r.Turns[i].Parts) == 0 {
				r.Turns = append(r.Turns[:i], r.Turns[i+1:]...)
				return
			}
		}
	})
}

// trySteerBusyNativePrompt handles a user submission arriving while a native
// in-process turn is actively running. Instead of aborting the running turn, it
// persists the user's steer message, deduplicates retries via clientRequestId,
// supersedes any unexecuted pending approval/question, and queues the steer
// item for the next safe model step boundary.
func (s *Service) trySteerBusyNativePrompt(
	ctx context.Context,
	rec SessionRecord,
	body PromptRequest,
	promptText, callerSystem, clientReqID, reqDigest string,
	fileParts []MessagePart,
	resolvedSkill ResolvedSkill,
) (handled bool, httpStatus int, receipt PromptReceipt, err error) {
	s.mu.Lock()
	ap := s.procs[rec.ID]
	busy := ap != nil && ap.runtime == string(RuntimeNative) && !ap.wasAborted() && !ap.hasPendingError() && ap.steerQueue != nil
	s.mu.Unlock()
	if !busy {
		return false, 0, PromptReceipt{}, nil
	}

	mode := firstNonEmptyStr(body.Agent, rec.Mode, "build")
	modelID := rec.ModelID
	providerID := rec.ProviderID
	if body.Model != nil && strings.TrimSpace(body.Model.ModelID) != "" {
		modelID = strings.TrimSpace(body.Model.ModelID)
		if strings.TrimSpace(body.Model.ProviderID) != "" {
			providerID = strings.TrimSpace(body.Model.ProviderID)
		}
	}
	llmPromptText, resolvedMode, resolvedProvID, resolvedModelID, _ := resolveSlashCommandAndAgentOverrides(
		promptText,
		rec.WorktreePath,
		mode,
		providerID,
		modelID,
		callerSystem,
		string(RuntimeNative),
	)
	mode = resolvedMode
	providerID = resolvedProvID
	modelID = resolvedModelID
	effort := strings.TrimSpace(body.Variant)

	s.mu.Lock()
	curAP := s.procs[rec.ID]
	if curAP == nil || curAP != ap || curAP.wasAborted() || curAP.hasPendingError() || curAP.steerQueue == nil {
		s.mu.Unlock()
		return false, 0, PromptReceipt{}, nil
	}
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	now := time.Now().UnixMilli()
	steerMsgID := fmt.Sprintf("msg_u_%d_%d", now, seq)

	if clientReqID != "" && s.ledger != nil {
		if adm := s.ledger.AdmitRequest(RequestKindPrompt, rec.ID, rec.ID, clientReqID, reqDigest, map[string]string{"userMsgID": steerMsgID}); adm.State != RequestAdmissionNew {
			if adm.State == RequestAdmissionDone {
				var cached PromptReceipt
				if uErr := json.Unmarshal(adm.ResponseBody, &cached); uErr == nil {
					cached.Deduplicated = true
					return true, adm.HTTPStatus, cached, nil
				}
			}
			return true, adm.HTTPStatus, PromptReceipt{}, &CodedRequestError{
				HTTPStatus:      adm.HTTPStatus,
				Code:            adm.ErrorCode,
				Message:         adm.ErrorMessage,
				ClientRequestID: clientReqID,
			}
		}
	}

	userInfo := MessageInfo{
		ID:           steerMsgID,
		SessionID:    rec.ID,
		Role:         "user",
		Time:         MessageTime{Created: now},
		Agent:        mode,
		ModelID:      modelID,
		ProviderID:   providerID,
		ContinueTask: body.ContinueTask,
	}
	storedParts, deliveryParts, steerPrompt := s.prepareTurnAttachmentsForModel(rec, providerID, modelID, false, now, seq, steerMsgID, llmPromptText, fileParts)
	if llmPromptText != promptText {
		for i := range storedParts {
			if storedParts[i].Type == "text" {
				storedParts[i].Text = promptText
				break
			}
		}
	}
	if body.SkillSelection != nil {
		s.recordUserSkillOverride(steerMsgID, promptText, body.SkillSelection)
		storedParts, deliveryParts = applySkillSelectionToTurnParts(
			rec.ID,
			steerMsgID,
			now,
			seq,
			llmPromptText,
			body.SkillSelection,
			true,
			resolvedSkill.Content,
			storedParts,
			deliveryParts,
		)
		steerPrompt = formatInlineSkillPrompt(body.SkillSelection.Name, resolvedSkill.Content, "") + "\n\n" + steerPrompt
	}

	_, _, _ = s.store.Update(rec.ID, func(r *SessionRecord) {
		r.Mode = mode
		if modelID != "" {
			r.ModelID = modelID
		}
		if providerID != "" {
			r.ProviderID = providerID
		}
	})
	s.recordTurnSnapshot(rec.ID, steerMsgID)

	// Persist and broadcast the user's steer turn immediately so UI & disk stay in sync.
	evts := make([]SSEEvent, 0, 1+len(storedParts))
	evts = append(evts, SSEEvent{
		Type:       "message.updated",
		Properties: map[string]interface{}{"info": userInfo},
	})
	for _, sp := range storedParts {
		evts = append(evts, SSEEvent{
			Type:       "message.part.updated",
			Properties: map[string]interface{}{"part": sp},
		})
	}
	s.broadcast(rec.ID, evts...)

	s.updateInflightSteer(rec.ID, steerMsgID, mode, providerID, modelID, effort, steerPrompt)
	ap.steerQueue.PushItem(NativeSteerItem{
		UserMsgID: steerMsgID,
		Prompt:    steerPrompt,
		Blocks:    buildNativeUserBlocks(steerPrompt, deliveryParts),
	})
	ap.stall.touch(time.Now())

	// If the turn is paused awaiting user approval or a question, supersede the
	// unexecuted tool call cleanly so the engine advances immediately to the next
	// safe step and consumes the queued steer instruction without running the tool.
	s.cancelSessionApprovals(rec.ID, "Superseded by a new user steering message — follow the latest user instructions instead.")

	_ = ctx
	receipt = PromptReceipt{
		OK:              true,
		InstructionID:   seq,
		MessageID:       steerMsgID,
		ClientRequestID: clientReqID,
	}
	if clientReqID != "" && s.ledger != nil {
		if raw, mErr := json.Marshal(receipt); mErr == nil {
			s.ledger.CompleteRequest(RequestKindPrompt, rec.ID, rec.ID, clientReqID, reqDigest, http.StatusOK, raw, map[string]string{"userMsgID": steerMsgID})
		}
	} else {
		s.storePromptReceipt(rec.ID, clientReqID, receipt)
	}
	return true, http.StatusOK, receipt, nil
}

// resumeNativeTurnFromLedger resumes an interrupted native Go Agent turn from
// its persisted SessionRecord.Turns + task-ledger.json checkpoint without
// re-executing already-completed mutating tool calls or approvals.
func (s *Service) resumeNativeTurnFromLedger(_ context.Context, rec SessionRecord, entry InflightTurnEntry) error {
	s.mu.Lock()
	eng := s.nativeEngine
	s.mu.Unlock()
	if eng == nil {
		return errors.New("native Go agent engine is not configured")
	}

	nowMs := time.Now().UnixMilli()
	s.settleSessionInFlightParts(rec.ID, nowMs, false)
	s.clearInterruptedTurnAbortError(rec.ID)

	mode := firstNonEmptyStr(entry.Mode, rec.Mode, "build")
	providerID := firstNonEmptyStr(entry.ProviderID, rec.ProviderID)
	modelID := firstNonEmptyStr(entry.ModelID, rec.ModelID)
	effort := strings.TrimSpace(entry.Effort)

	updated, _, _ := s.store.Update(rec.ID, func(r *SessionRecord) {
		r.Runtime = string(RuntimeNative)
		r.ExternalID = ""
		r.Mode = mode
		if providerID != "" {
			r.ProviderID = providerID
		}
		if modelID != "" {
			r.ModelID = modelID
		}
	})
	if updated.ID != "" {
		rec = updated
	} else if latest, ok := s.store.Get(rec.ID); ok {
		rec = latest
	}

	callerSys := strings.TrimSpace(entry.CallerSystem)
	if storeCtx := s.resolveSessionContextBlock(rec.ID, rec.WorktreePath); storeCtx != "" &&
		!strings.Contains(callerSys, "<hoxi_work_store_profile>") &&
		!strings.Contains(callerSys, "<hoxi_work_app>") {
		if callerSys == "" {
			callerSys = storeCtx
		} else {
			callerSys = storeCtx + "\n\n" + callerSys
		}
	}
	nativeSystem := strings.TrimSpace(effectiveCommandExecutionGuidance() + "\n\n" + callerSys)

	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	userMsgID := firstNonEmptyStr(entry.UserMsgID, fmt.Sprintf("msg_u_%d_%d", nowMs, seq))
	asstMsgID := fmt.Sprintf("msg_a_%d_%d", nowMs+1, seq)

	hasPersistedTurn := false
	if strings.TrimSpace(entry.UserMsgID) != "" {
		for _, t := range rec.Turns {
			if t.Info.ID == entry.UserMsgID {
				hasPersistedTurn = true
				break
			}
		}
	}
	if !hasPersistedTurn && entry.StartedAt > 0 && len(rec.Turns) > 0 {
		last := rec.Turns[len(rec.Turns)-1]
		if last.Info.Time.Created >= entry.StartedAt {
			hasPersistedTurn = true
		}
	}

	entry.Runtime = string(RuntimeNative)
	entry.ExternalID = ""
	entry.UserMsgID = userMsgID
	entry.AsstMsgID = asstMsgID
	entry.GracefulRestart = false
	entry.RecoveryCount++
	entry.UpdatedAt = nowMs
	s.recordInflightTurn(entry)

	userInfo := MessageInfo{
		ID:          userMsgID,
		SessionID:   rec.ID,
		Role:        "user",
		Time:        MessageTime{Created: firstPositiveInt64(entry.StartedAt, nowMs)},
		Agent:       mode,
		ServiceTier: fastServiceTierValue(entry.FastMode && !rec.Subagent && s.ApprovalPolicy().Mode != ApprovalModeUnattended),
		ModelID:     modelID,
		ProviderID:  rec.ProviderID,
	}

	if !hasPersistedTurn {
		promptBody := strings.TrimSpace(entry.PromptText)
		if promptBody == "" {
			promptBody = restartContinuationPrompt
		}
		part := MessagePart{
			ID:        fmt.Sprintf("prt_%d_%d_txt", nowMs, seq),
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      promptBody,
		}
		return s.launchNativeTurn(rec, mode, modelID, effort, nativeSystem, promptBody, userInfo, []MessagePart{part}, asstMsgID, false, part)
	}

	return s.launchNativeTurn(rec, mode, modelID, effort, nativeSystem, restartContinuationPrompt, userInfo, nil, asstMsgID, true)
}

func isThinkingSignatureErrorText(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "thoughtsignature") || strings.Contains(lower, "thought_signature") || strings.Contains(lower, "encrypted_content") {
		return true
	}
	if !strings.Contains(lower, "thinking") && !strings.Contains(lower, "redacted_thinking") {
		return false
	}
	return strings.Contains(lower, "signature") ||
		strings.Contains(lower, "must start with a thinking block") ||
		strings.Contains(lower, "expected `thinking`") ||
		strings.Contains(lower, "expected 'thinking'") ||
		strings.Contains(lower, "cannot be modified")
}

func clearTurnsThinkingSignatures(turns []MessageTurn) bool {
	changed := false
	for i := range turns {
		if turns[i].Info.Role != "assistant" {
			continue
		}
		for j := range turns[i].Parts {
			p := &turns[i].Parts[j]
			switch p.Type {
			case "reasoning":
				if p.Metadata == nil {
					p.Metadata = map[string]interface{}{}
				}
				if inv, _ := p.Metadata["signatureInvalidated"].(bool); !inv {
					p.Metadata["signatureInvalidated"] = true
					changed = true
				}
				if _, ok := p.Metadata["signature"]; ok {
					delete(p.Metadata, "signature")
					changed = true
				}
				if _, ok := p.Metadata["redactedData"]; ok {
					delete(p.Metadata, "redactedData")
					changed = true
				}
			case "tool":
				if p.State != nil && p.State.Metadata != nil {
					if _, ok := p.State.Metadata["thoughtSignature"]; ok {
						delete(p.State.Metadata, "thoughtSignature")
						changed = true
					}
				}
			}
		}
	}
	return changed
}

func (s *Service) clearSessionThinkingSignatures(sessionID string) {
	if s == nil || s.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	_, _, _ = s.store.UpdateWithoutActivity(sessionID, func(r *SessionRecord) {
		clearTurnsThinkingSignatures(r.Turns)
	})
}

func extractUserTurnPlainText(parts []MessagePart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func (s *Service) cycleHasMutatingEffect(turns []MessageTurn) bool {
	var childIDs []string
	for _, t := range turns {
		if t.Info.Role != "assistant" {
			continue
		}
		for _, p := range t.Parts {
			if p.Type != "tool" || p.State == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(p.Tool), "task") {
				if cid := stringMetaField(p.State.Metadata, "sessionId"); cid != "" {
					childIDs = append(childIDs, cid)
				}
			}
			if !isMutatingToolPart(p) {
				continue
			}
			outcome := stringMetaField(p.State.Metadata, "outcome")
			if outcome == ToolOutcomeUnknown || p.State.Status == "running" || p.State.Status == "completed" ||
				(p.State.Status == "error" && outcome != ToolOutcomeCancelled) {
				return true
			}
		}
	}
	for _, cid := range childIDs {
		if cSide, cUnk := s.childSessionMutatingState(cid); cSide || cUnk {
			return true
		}
	}
	return false
}

func (s *Service) computeRetryCycleMutated(rec SessionRecord, continueTask, canResumePartialNative bool) bool {
	if !continueTask || !canResumePartialNative || len(rec.Turns) == 0 {
		return false
	}
	cycleStart := 0
	for i := len(rec.Turns) - 1; i >= 0; i-- {
		if rec.Turns[i].Info.Role == "user" {
			cycleStart = i
			break
		}
	}
	return s.cycleHasMutatingEffect(rec.Turns[cycleStart:])
}

func prepareFailedTurnRetryLocked(
	r *SessionRecord,
	continueTask bool,
	canResumePartialNative bool,
	cycleMutated bool,
	promptText string,
) (resumedUser *MessageInfo, clearedAsst *MessageInfo) {
	if r == nil || !continueTask || len(r.Turns) == 0 {
		return nil, nil
	}
	for i := len(r.Turns) - 1; i >= 0; i-- {
		errObj := r.Turns[i].Info.Error
		if errObj != nil && errObj.Data != nil && isThinkingSignatureErrorText(errObj.Data.Message) {
			clearTurnsThinkingSignatures(r.Turns)
			break
		}
	}

	trimmedPrompt := strings.TrimSpace(promptText)
	if trimmedPrompt == "" || len(r.Turns) < 2 {
		return nil, nil
	}

	lastIdx := len(r.Turns) - 1
	last := r.Turns[lastIdx]
	if last.Info.Role != "assistant" || last.Info.Error == nil {
		return nil, nil
	}
	if last.Info.Error.Data != nil && last.Info.Error.Data.Kind == ToolOutcomeUnknown {
		return nil, nil
	}

	// Case 1: Empty failed assistant turn immediately following the matching user turn -> replace in place.
	prev := r.Turns[lastIdx-1]
	if len(last.Parts) == 0 && prev.Info.Role == "user" && extractUserTurnPlainText(prev.Parts) == trimmedPrompt {
		r.Turns = r.Turns[:lastIdx-1]
		return nil, nil
	}

	// Case 2: Partial read-only Native turn with completed tool steps -> clear error and resume from completed steps.
	if !canResumePartialNative || cycleMutated {
		return nil, nil
	}
	cycleStart := -1
	for i := lastIdx; i >= 0; i-- {
		if r.Turns[i].Info.Role == "user" {
			cycleStart = i
			break
		}
	}
	if cycleStart < 0 || extractUserTurnPlainText(r.Turns[cycleStart].Parts) != trimmedPrompt {
		return nil, nil
	}
	hasCompletedTool := false
	for i := cycleStart + 1; i <= lastIdx; i++ {
		if r.Turns[i].Info.Role != "assistant" {
			return nil, nil
		}
		for _, p := range r.Turns[i].Parts {
			if p.Type == "tool" && p.State != nil && p.State.Status == "completed" {
				hasCompletedTool = true
			}
		}
	}
	if !hasCompletedTool {
		return nil, nil
	}
	for len(r.Turns) > cycleStart+1 {
		tail := r.Turns[len(r.Turns)-1]
		if tail.Info.Role == "assistant" && tail.Info.Error != nil && len(tail.Parts) == 0 {
			r.Turns = r.Turns[:len(r.Turns)-1]
			continue
		}
		break
	}
	if len(r.Turns) <= cycleStart+1 {
		return nil, nil
	}
	targetAsst := &r.Turns[len(r.Turns)-1]
	if targetAsst.Info.Error != nil {
		targetAsst.Info.Error = nil
		targetAsst.Info.Finish = "tool-calls"
		copiedAsst := cloneMessageInfo(targetAsst.Info)
		clearedAsst = &copiedAsst
	}
	copiedUser := cloneMessageInfo(r.Turns[cycleStart].Info)
	return &copiedUser, clearedAsst
}
