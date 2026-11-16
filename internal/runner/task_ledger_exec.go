package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

func (s *Service) waitForOpenCodeReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if s.openCode != nil {
			probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			status, _, err := s.openCode.Raw(probeCtx, http.MethodGet, "/global/health", nil, nil)
			cancel()
			if err == nil && status == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("opencode daemon did not become ready in time to resume turn")
		}
		pollMs := restartEnginePollIntervalMs.Load()
		if pollMs <= 0 {
			pollMs = 150
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(pollMs) * time.Millisecond):
		}
	}
}

func (s *Service) resumeOpenCodeTurnFromLedger(ctx context.Context, rec SessionRecord, entry InflightTurnEntry) error {
	extID := strings.TrimSpace(firstNonEmptyStr(rec.ExternalID, entry.ExternalID))
	mode := firstNonEmptyStr(entry.Mode, rec.Mode, "build")
	providerID := firstNonEmptyStr(entry.ProviderID, rec.ProviderID)
	modelID := firstNonEmptyStr(entry.ModelID, rec.ModelID)

	setupCtx, cancelSetup := context.WithTimeout(ctx, 15*time.Second)
	defer cancelSetup()
	resolvedProvider, resolvedModel := s.resolveOpenCodeModel(setupCtx, rec.WorktreePath, providerID, modelID)
	providerID = firstNonEmptyStr(resolvedProvider, providerID)
	modelID = firstNonEmptyStr(resolvedModel, modelID)
	effort := s.resolveOpenCodeVariant(setupCtx, rec.WorktreePath, providerID, modelID, entry.Effort)

	if extID != "" && extID != rec.ExternalID {
		updated, _, err := s.store.Update(rec.ID, func(r *SessionRecord) {
			r.ExternalID = extID
			r.ProviderID = providerID
			r.ModelID = modelID
			r.Mode = mode
		})
		if err == nil {
			rec = updated
		}
		s.managedOpenCode.Store(extID, true)
	}

	baseSystem := strings.TrimSpace(commandExecutionGuidance + "\n\n" + strings.TrimSpace(entry.CallerSystem))
	nowMs := time.Now().UnixMilli()

	// If the process exited before an OpenCode session was even created and no
	// tool work happened, replay the initial user turn cleanly.
	if strings.TrimSpace(rec.ExternalID) == "" {
		if strings.TrimSpace(entry.PromptText) == "" {
			return errors.New("cannot resume OpenCode turn without external session or prompt")
		}
		s.mu.Lock()
		s.seqSeed++
		seq := s.seqSeed
		s.mu.Unlock()
		userMsgID := firstNonEmptyStr(entry.UserMsgID, fmt.Sprintf("msg_u_%d_%d", nowMs, seq))
		userInfo := MessageInfo{
			ID:         userMsgID,
			SessionID:  rec.ID,
			Role:       "user",
			Time:       MessageTime{Created: firstPositiveInt64(entry.StartedAt, nowMs)},
			Agent:      mode,
			ModelID:    modelID,
			ProviderID: providerID,
		}
		part := MessagePart{
			ID:        fmt.Sprintf("prt_%d_%d_txt", nowMs, seq),
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      entry.PromptText,
		}
		entry.UserMsgID = userMsgID
		entry.GracefulRestart = false
		entry.RecoveryCount++
		entry.UpdatedAt = nowMs
		s.recordInflightTurn(entry)
		return s.startOpenCodeTurn(rec, mode, modelID, effort, baseSystem, userInfo, []MessagePart{part}, []MessagePart{part})
	}

	if err := syncOpenCodeCommandSandbox(rec); err != nil {
		return err
	}
	// Pull any messages OpenCode persisted before the restart and settle incomplete parts.
	s.reconcileOpenCodePostTurnMessages(rec, &activeProcess{sessionID: rec.ID, runtime: "opencode"}, entry.UserMsgID, nil)
	s.abandonOpenCodeInFlightParts(rec.ID, nowMs)
	s.clearInterruptedTurnAbortError(rec.ID)

	if latest, ok := s.store.Get(rec.ID); ok {
		rec = latest
	}
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	stream, err := s.openCodeRequest(turnCtx, "GET", "/event", rec.WorktreePath, nil)
	if err != nil {
		cancelTurn()
		return err
	}
	reusedExistingSession := strings.TrimSpace(rec.ExternalID) != ""
	ap := &activeProcess{sessionID: rec.ID, runtime: "opencode", done: make(chan struct{}), idleCh: make(chan struct{}, 1)}
	ap.setOpenCodeRecord(rec)
	ap.cancel = func() {
		curRec := ap.openCodeRecord()
		abortCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		s.rejectOpenCodeSessionPermissions(abortCtx, curRec)
		_ = s.openCodeJSON(abortCtx, "POST", "/session/"+curRec.ExternalID+"/abort", curRec.WorktreePath, map[string]interface{}{}, nil)
		cancelTurn()
	}
	ap.stall.touch(time.Now())

	entry.ExternalID = rec.ExternalID
	entry.GracefulRestart = false
	entry.RecoveryCount++
	entry.UpdatedAt = nowMs
	s.recordInflightTurn(entry)

	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()
	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}
	s.broadcast(rec.ID, SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "busy"}}})
	go s.watchStall(ap)

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanOpenCodeSSEDataLines(stream.Body, func(payload []byte) {
			s.relayOpenCodeEventWithFiles(ap.openCodeRecord(), payload, nil)
		})
	}()

	go func() {
		defer func() {
			cancelTurn()
			_ = stream.Body.Close()
			<-streamDone
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.rejectOpenCodeSessionPermissions(cleanupCtx, ap.openCodeRecord())
			cleanupCancel()
			if !ap.wasRestarting() {
				s.clearInflightTurn(rec.ID)
			}
			s.abandonOpenCodeInFlightParts(rec.ID, time.Now().UnixMilli())
			s.store.FlushSession(rec.ID)
			s.releaseActiveProcess(rec.ID, ap)
			if !ap.wasRestarting() {
				if errEvt, ok := ap.takePendingError(); ok {
					s.publishOpenCodeEvent(rec.ID, errEvt)
				}
				s.broadcast(rec.ID, SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}})
			}
			close(ap.done)
		}()

		recoverMsgID := fmt.Sprintf("%s%d_restart_%d", internalRecoveryMsgPrefix, time.Now().UnixMilli(), entry.RecoveryCount)
		payload := map[string]interface{}{
			"messageID": recoverMsgID,
			"agent":     mode,
			"parts": []map[string]interface{}{
				{"type": "text", "text": restartContinuationPrompt},
			},
		}
		if providerID != "" && modelID != "" {
			payload["model"] = map[string]string{"providerID": providerID, "modelID": modelID}
		}
		if effort != "" {
			payload["variant"] = effort
		}
		system := openCodeTurnSystem(baseSystem, rec)
		if system != "" {
			payload["system"] = system
		}
		postErr := s.openCodeJSON(turnCtx, "POST", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, payload, nil)
		if rotated, ok := s.rotateOpenCodeSessionIfBroken(turnCtx, rec, ap, reusedExistingSession, recoverMsgID, postErr); ok {
			rec = rotated
			if system = openCodeTurnSystem(baseSystem, rec); system != "" {
				payload["system"] = system
			}
			postErr = s.openCodeJSON(turnCtx, "POST", "/session/"+rec.ExternalID+"/message", rec.WorktreePath, payload, nil)
		}
		if postErr == nil && ap.wasSteered() && !ap.wasAborted() && !ap.hasPendingError() {
			s.waitForOpenCodeIdle(turnCtx, rec, ap)
		}
		if ((postErr == nil && !ap.hasPendingError()) || ap.reasoningLoop.hasPendingRecovery()) && !ap.wasAborted() {
			postErr = s.recoverOpenCodeReasoningIfNeeded(turnCtx, rec, ap, mode, modelID, effort, system, postErr)
		}
		_ = stream.Body.Close()
		<-streamDone
		if postErr != nil && ap.reasoningLoop.shouldSuppressAbortError(postErr.Error()) {
			postErr = nil
		}
		if postErr == nil || ap.reasoningLoop.hasTriggered() || ap.stall.abortError() != nil || ap.wasRestarting() {
			s.markContextDelivered(rec.ID)
			s.reconcileOpenCodePostTurnMessages(rec, ap, entry.UserMsgID, nil)
			s.syncSessionMemoryFile(rec.ID)
		}
		if stallErr := ap.stall.abortError(); stallErr != nil {
			s.stampAbortedTurnErrorOnStore(rec.ID, stallErr)
			ap.setPendingError(SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": stallErr}})
		} else if postErr != nil && !ap.wasAborted() && !ap.hasPendingError() {
			ap.setPendingError(SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": openCodeEngineError(postErr)}})
		}
	}()
	return nil
}

func resolveCLIBinaryForRuntime(rt, modelID string) (string, error) {
	switch rt {
	case string(RuntimeClaudeCode):
		if bin := ResolveClaudeBinary(); bin != "" {
			return bin, nil
		}
		return "", errors.New("Claude Code CLI (claude) binary not found")
	case string(RuntimeGeminiCLI):
		if bin, _ := ResolveGeminiTurnBinary(modelID); bin != "" {
			return bin, nil
		}
		return "", errors.New("Gemini CLI (gemini / agy) binary not found")
	case string(RuntimeCodeBuddy):
		if bin := agentcli.ResolveCodeBuddyBinary(); bin != "" {
			return bin, nil
		}
		return "", errors.New("CodeBuddy CLI (codebuddy) binary not found")
	case string(RuntimeQoder):
		if bin, _ := agentcli.ResolveQoderBinaryForModel(modelID); bin != "" {
			return bin, nil
		}
		return "", errors.New("Qoder CLI (qodercli / qoderclicn) binary not found")
	default:
		if bin := ResolveAgyBinary(); bin != "" {
			return bin, nil
		}
		return "", errors.New("Antigravity CLI (agy) binary not found")
	}
}

func (s *Service) resumeCLITurnFromLedger(rec SessionRecord, entry InflightTurnEntry) error {
	mode := firstNonEmptyStr(entry.Mode, rec.Mode, "build")
	modelID := firstNonEmptyStr(entry.ModelID, rec.ModelID)
	effort := strings.TrimSpace(entry.Effort)
	if rec.Runtime == string(RuntimeAntigravity) {
		modelID, effort = ResolveAgyModelAndEffort(modelID, effort)
	}
	binPath, err := resolveCLIBinaryForRuntime(rec.Runtime, modelID)
	if err != nil {
		return err
	}

	nowMs := time.Now().UnixMilli()
	s.abandonOpenCodeInFlightParts(rec.ID, nowMs)
	s.clearInterruptedTurnAbortError(rec.ID)

	extID := strings.TrimSpace(firstNonEmptyStr(rec.ExternalID, entry.ExternalID))
	resetAgySplitBrain := (rec.Runtime == string(RuntimeAntigravity) ||
		(rec.Runtime == string(RuntimeGeminiCLI) && ShouldGeminiCLIUseAgy(modelID))) &&
		len(rec.Turns) > 0 &&
		hasAgySplitBrainConversation(extID)
	if resetAgySplitBrain && quarantineEmptyAgySQLiteStore(extID) {
		resetAgySplitBrain = false
	}
	if resetAgySplitBrain {
		extID = ""
	}

	updated, _, _ := s.store.Update(rec.ID, func(r *SessionRecord) {
		r.Mode = mode
		r.ModelID = modelID
		if entry.ProviderID != "" {
			r.ProviderID = entry.ProviderID
		}
		r.ExternalID = extID
		if extID == "" && len(r.Turns) > 0 {
			r.ContextPending = true
			r.ContextTurnCount = len(r.Turns)
		}
	})
	if updated.ID != "" {
		rec = updated
	}

	s.mu.Lock()
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()
	userMsgID := firstNonEmptyStr(entry.UserMsgID, fmt.Sprintf("msg_u_%d_%d", nowMs, seq))
	asstMsgID := fmt.Sprintf("msg_a_%d_%d", nowMs+1, seq)

	entry.UserMsgID = userMsgID
	entry.AsstMsgID = asstMsgID
	entry.ExternalID = extID
	entry.GracefulRestart = false
	entry.RecoveryCount++
	entry.UpdatedAt = nowMs
	s.recordInflightTurn(entry)

	userInfo := MessageInfo{
		ID:         userMsgID,
		SessionID:  rec.ID,
		Role:       "user",
		Time:       MessageTime{Created: firstPositiveInt64(entry.StartedAt, nowMs)},
		Agent:      mode,
		ModelID:    modelID,
		ProviderID: rec.ProviderID,
	}

	if extID != "" {
		s.recordCLISystemDelivered(rec.ID, rec.Runtime, entry.CallerSystem)
		if startErr := s.startTurnProcessWithMode(rec, binPath, mode, modelID, effort, restartContinuationPrompt, userInfo, nil, nil, asstMsgID, false); startErr != nil {
			s.clearInflightTurn(rec.ID)
			return startErr
		}
		return nil
	}

	// No external CLI session handle was emitted before restart: replay from
	// stored prompt text and rehydrate prior conversation context if present.
	promptBody := strings.TrimSpace(entry.PromptText)
	if promptBody == "" {
		promptBody = restartContinuationPrompt
	}
	fullSystem := strings.TrimSpace(commandExecutionGuidance + "\n\n" + strings.TrimSpace(entry.CallerSystem))
	input := runtimeMemoryContext(rec) + runtimeContext(rec)
	if cliSys := s.nativeCLISystemForTurn(rec, entry.CallerSystem, fullSystem); cliSys != "" {
		input += cliSys + "\n\n"
	}
	var storedParts []MessagePart
	if strings.TrimSpace(entry.PromptText) != "" {
		storedParts = []MessagePart{{
			ID:        fmt.Sprintf("prt_%d_%d_txt", nowMs, seq),
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      entry.PromptText,
		}}
	}
	if startErr := s.startTurnProcessWithMode(rec, binPath, mode, modelID, effort, input+promptBody, userInfo, storedParts, storedParts, asstMsgID, len(storedParts) > 0); startErr != nil {
		s.clearInflightTurn(rec.ID)
		return startErr
	}
	s.recordCLISystemDelivered(rec.ID, rec.Runtime, entry.CallerSystem)
	return nil
}

func firstPositiveInt64(vals ...int64) int64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}
