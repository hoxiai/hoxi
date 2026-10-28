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

// PromptRequest is one user turn: the JSON body of POST .../prompt, also
// submitted in process by RunPromptAndWait.
type PromptRequest struct {
	Text            string          `json:"text"`
	Agent           string          `json:"agent"`
	Variant         string          `json:"variant"`
	System          string          `json:"system"`
	Model           *PromptModel    `json:"model"`
	Parts           []MessagePart   `json:"parts"`
	SkillSelection  *SkillSelection `json:"skillSelection,omitempty"`
	ClientRequestID string          `json:"clientRequestId,omitempty"`
}

// Stable execution guidance shared by desktop, mobile, CLI and orchestration
// prompts. Keep it out of visible user text and out of the approval policy.
const commandExecutionGuidance = `Hoxi command execution:
Keep build, test and workspace-check progress visible. Unless the user explicitly requests it, do not pipe long-running checks into tail/head or filters that suppress progress: tail -N waits for EOF, and grep may buffer output.
Use the tool's bounded output when available. If a full log is needed, save it to a unique temporary file and inspect bounded excerpts while the command runs. Preserve and check the original command's exit status; a successful tail/grep does not prove the check passed.
Set a suitable tool timeout and test timeout. If a check stays silent, inspect its current stage and existing log before retrying; do not start duplicate checks or repeatedly wait without reporting progress. Reuse build caches unless a clean build is required.
When navigating code, follow: locate symbol/file or fetch a full surgical package in one step (prefer hoxi_code_index tool or 'hoxi code-index context|search|inspect|callers|callees|impact|refresh') -> inspect the target function and minimal surrounding lines instead of whole files -> check callers, callees, blast radius (impact) and related tests -> edit -> run targeted verification.
When the user asks to split work into parallel coding subtasks or a multi-task convoy plan, output a fenced hoxi-subtasks JSON block: {"title":"plan title","concurrency":3,"tasks":[{"id":"task-1","prompt":"self-contained instructions and verification","dependsOn":[]}]} so Hoxi renders an interactive subtask orchestration card in chat.`

// PromptReceipt is returned by POST .../prompt and .../prompt_async so callers
// can correlate node-assigned instruction/message IDs and detect deduplicated retries.
type PromptReceipt struct {
	OK              bool   `json:"ok"`
	InstructionID   int64  `json:"instructionId,omitempty"`
	MessageID       string `json:"messageId,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
	Deduplicated    bool   `json:"deduplicated,omitempty"`
}

type cachedPromptReceipt struct {
	receipt   PromptReceipt
	expiresAt time.Time
}

const idempotencyReceiptTTL = 15 * time.Minute

func (s *Service) lookupPromptReceipt(sessionID, clientRequestID string) (PromptReceipt, bool) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return PromptReceipt{}, false
	}
	key := sessionID + "\x00" + clientRequestID
	raw, ok := s.promptReceipts.Load(key)
	if !ok {
		return PromptReceipt{}, false
	}
	entry, ok := raw.(cachedPromptReceipt)
	if !ok || time.Now().After(entry.expiresAt) {
		s.promptReceipts.Delete(key)
		return PromptReceipt{}, false
	}
	out := entry.receipt
	out.Deduplicated = true
	return out, true
}

func (s *Service) storePromptReceipt(sessionID, clientRequestID string, receipt PromptReceipt) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return
	}
	now := time.Now()
	s.promptReceipts.Range(func(k, v interface{}) bool {
		if entry, ok := v.(cachedPromptReceipt); !ok || now.After(entry.expiresAt) {
			s.promptReceipts.Delete(k)
		}
		return true
	})
	s.promptReceipts.Store(sessionID+"\x00"+clientRequestID, cachedPromptReceipt{
		receipt:   receipt,
		expiresAt: now.Add(idempotencyReceiptTTL),
	})
}

// PromptModel selects the provider/model for one turn.
type PromptModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// startPrompt starts (or, for a busy OpenCode session, steers) one turn and
// returns the HTTP status that describes the outcome. directory is the
// caller's optional directory selector, checked against the session sandbox.
func (s *Service) startPrompt(ctx context.Context, sessionID, directory string, body PromptRequest) (int, error) {
	status, _, err := s.startPromptWithReceipt(ctx, sessionID, directory, body)
	return status, err
}

func (s *Service) startPromptWithReceipt(ctx context.Context, sessionID, directory string, body PromptRequest) (int, PromptReceipt, error) {
	unlock := s.turnMu.lock(sessionID)
	defer unlock()

	clientReqID := strings.TrimSpace(body.ClientRequestID)
	if cached, ok := s.lookupPromptReceipt(sessionID, clientReqID); ok {
		return http.StatusOK, cached, nil
	}

	rec, ok := s.store.Get(sessionID)
	unmanagedBusySteer := false
	if !ok && body.SkillSelection != nil {
		adopted, adoptErr := s.adoptOpenCodeSession(ctx, sessionID, directory)
		if adoptErr != nil {
			if adoptErr.Error() == "cannot change runtime while the session is running" {
				dir := s.resolveUnmanagedSessionDir(sessionID, directory)
				projectID, _ := s.ensureProjectID(dir)
				rec = SessionRecord{
					ID:           sessionID,
					ProjectID:    projectID,
					Runtime:      "opencode",
					ExternalID:   sessionID,
					ProjectDir:   dir,
					WorktreePath: dir,
					SandboxMode:  "local",
				}
				ok = true
				unmanagedBusySteer = true
			} else {
				return http.StatusConflict, PromptReceipt{}, adoptErr
			}
		} else {
			if putErr := s.store.Put(adopted); putErr != nil {
				return http.StatusInternalServerError, PromptReceipt{}, putErr
			}
			for _, id := range adopted.OpenCodeIDs {
				s.managedOpenCode.Store(id, true)
			}
			rec = adopted
			ok = true
		}
	}
	if !ok {
		return http.StatusNotFound, PromptReceipt{}, errors.New("runner session not found")
	}
	if reqDir := strings.TrimSpace(directory); reqDir != "" &&
		normalizeComparePath(reqDir) != normalizeComparePath(rec.WorktreePath) &&
		normalizeComparePath(reqDir) != normalizeComparePath(rec.ProjectDir) {
		return http.StatusForbidden, PromptReceipt{}, errors.New("session directory does not match its sandbox")
	}

	promptText := strings.TrimSpace(body.Text)
	var fileParts []MessagePart
	for _, p := range body.Parts {
		if p.Type == "file" && strings.TrimSpace(p.URL) != "" {
			fileParts = append(fileParts, p)
		}
	}
	if promptText == "" {
		var texts []string
		for _, p := range body.Parts {
			if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
				texts = append(texts, strings.TrimSpace(p.Text))
			}
		}
		promptText = strings.Join(texts, "\n")
	}
	if promptText == "" && len(fileParts) == 0 && body.SkillSelection == nil {
		return http.StatusBadRequest, PromptReceipt{}, errors.New("prompt text is required")
	}

	if rec.Subagent {
		return http.StatusConflict, PromptReceipt{}, errSubagentSessionReadOnly
	}
	if err := validateSessionDirectory(rec); err != nil {
		return http.StatusBadRequest, PromptReceipt{}, err
	}
	callerSystem := strings.TrimSpace(body.System)
	body.System = strings.TrimSpace(commandExecutionGuidance + "\n\n" + callerSystem)
	var resolvedSkill ResolvedSkill
	if body.SkillSelection != nil {
		resolved, err := s.resolveSkillSelection(ctx, rec.WorktreePath, rec.ID, rec.Runtime, *body.SkillSelection)
		if err != nil {
			return http.StatusConflict, PromptReceipt{}, err
		}
		resolvedSkill = resolved
	}
	if rec.Runtime == "opencode" {
		s.mu.Lock()
		busy := unmanagedBusySteer || s.procs[sessionID] != nil
		s.mu.Unlock()
		if busy {
			modelID, providerID := rec.ModelID, rec.ProviderID
			if body.Model != nil {
				modelID, providerID = body.Model.ModelID, body.Model.ProviderID
			}
			if checkErr := s.ensureOpenCodeModelAvailable(ctx, rec.WorktreePath, firstNonEmptyStr(providerID, rec.ProviderID), firstNonEmptyStr(modelID, rec.ModelID)); checkErr != nil {
				return http.StatusConflict, PromptReceipt{}, checkErr
			}
			steerSeq, steerMsgID, err := s.steerOpenCodeTurn(ctx, rec, firstNonEmptyStr(body.Agent, rec.Mode, "build"), modelID, providerID, body.Variant, body.System, promptText, body.Parts, body.SkillSelection)
			if err != nil {
				return http.StatusBadGateway, PromptReceipt{}, err
			}
			receipt := PromptReceipt{
				OK:              true,
				InstructionID:   steerSeq,
				MessageID:       steerMsgID,
				ClientRequestID: clientReqID,
			}
			s.storePromptReceipt(sessionID, clientReqID, receipt)
			return http.StatusOK, receipt, nil
		}
	}

	mode := firstNonEmptyStr(body.Agent, rec.Mode, "build")
	if !RuntimeCapabilities(rec.Runtime, false).Approval && !isReadOnlyAgentMode(mode) {
		return http.StatusConflict, PromptReceipt{}, fmt.Errorf("runtime %s does not support the unified approval gate; switch to Plan mode or a runtime with approval gate support", rec.Runtime)
	}
	modelID := rec.ModelID
	if body.Model != nil && strings.TrimSpace(body.Model.ModelID) != "" {
		modelID = strings.TrimSpace(body.Model.ModelID)
		if body.Model.ProviderID != "" {
			rec.ProviderID = body.Model.ProviderID
		}
	}
	effort := strings.TrimSpace(body.Variant)
	if rec.Runtime == string(RuntimeAntigravity) {
		modelID, effort = ResolveAgyModelAndEffort(modelID, effort)
	}

	binPath := ResolveAgyBinary()
	missingMsg := "Antigravity CLI (agy) binary not found in ~/.gemini/bin/agy or PATH"
	switch rec.Runtime {
	case string(RuntimeClaudeCode):
		binPath = ResolveClaudeBinary()
		missingMsg = "Claude Code CLI (claude) binary not found in ~/.local/bin/claude or PATH"
	case string(RuntimeGeminiCLI):
		binPath, _ = ResolveGeminiTurnBinary(modelID)
		missingMsg = "Gemini CLI (gemini / agy) binary not found in ~/.gemini/bin or PATH"
	case string(RuntimeCodeBuddy):
		binPath = agentcli.ResolveCodeBuddyBinary()
		missingMsg = "CodeBuddy CLI (codebuddy) binary not found in PATH"
	case string(RuntimeQoder):
		binPath, _ = agentcli.ResolveQoderBinaryForModel(modelID)
		missingMsg = "Qoder CLI (qodercli / qoderclicn) binary not found in PATH"
	}
	if binPath == "" && rec.Runtime != "opencode" {
		return http.StatusBadRequest, PromptReceipt{}, errors.New(missingMsg)
	}

	s.mu.Lock()
	if s.procs[sessionID] != nil {
		s.mu.Unlock()
		return http.StatusConflict, PromptReceipt{}, errors.New("session already has an active CLI process running")
	}
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	now := time.Now().UnixMilli()
	userMsgID := fmt.Sprintf("msg_u_%d_%d", now, seq)
	asstMsgID := fmt.Sprintf("msg_a_%d_%d", now+1, seq)
	userInfo := MessageInfo{
		ID:         userMsgID,
		SessionID:  sessionID,
		Role:       "user",
		Time:       MessageTime{Created: now},
		Agent:      mode,
		ModelID:    modelID,
		ProviderID: rec.ProviderID,
	}
	storedParts, deliveryParts, cliPrompt := s.prepareTurnAttachments(rec, now, seq, userMsgID, promptText, fileParts)
	if body.SkillSelection != nil {
		s.recordUserSkillOverride(userMsgID, promptText, body.SkillSelection)
		storedParts, deliveryParts = applySkillSelectionToTurnParts(
			sessionID,
			userMsgID,
			now,
			seq,
			promptText,
			body.SkillSelection,
			rec.Runtime != "opencode",
			resolvedSkill.Content,
			storedParts,
			deliveryParts,
		)
	}

	resetGeminiResume := rec.Runtime == string(RuntimeGeminiCLI) &&
		strings.TrimSpace(rec.ExternalID) != "" &&
		ShouldGeminiCLIUseAgy(rec.ModelID) != ShouldGeminiCLIUseAgy(modelID)
	_, _, _ = s.store.Update(sessionID, func(r *SessionRecord) {
		r.ModelID = modelID
		r.Mode = mode
		r.ProviderID = rec.ProviderID
		if resetGeminiResume {
			r.ExternalID = ""
			r.ContextPending = len(r.Turns) > 0
			r.ContextTurnCount = len(r.Turns)
		}
	})
	s.recordTurnSnapshot(sessionID, userMsgID)
	if updated, ok := s.store.Get(sessionID); ok {
		rec = updated
	}

	var err error
	if rec.Runtime == "opencode" {
		if checkErr := s.ensureOpenCodeModelAvailable(ctx, rec.WorktreePath, rec.ProviderID, modelID); checkErr != nil {
			return http.StatusConflict, PromptReceipt{}, checkErr
		}
		err = s.startOpenCodeTurn(rec, mode, modelID, effort, body.System, userInfo, storedParts, deliveryParts)
	} else {
		input := runtimeMemoryContext(rec) + runtimeContext(rec)
		if body.SkillSelection != nil {
			input += formatInlineSkillPrompt(body.SkillSelection.Name, resolvedSkill.Content, "") + "\n\n"
		}
		if cliSys := s.nativeCLISystemForTurn(rec, callerSystem, body.System); cliSys != "" {
			input += cliSys + "\n\n"
		}
		err = s.startTurnProcess(rec, binPath, mode, modelID, effort, input+cliPrompt, userInfo, storedParts, deliveryParts, asstMsgID)
		if err == nil {
			s.recordCLISystemDelivered(rec.ID, rec.Runtime, callerSystem)
		}
	}
	if err != nil {
		return http.StatusInternalServerError, PromptReceipt{}, err
	}
	receipt := PromptReceipt{
		OK:              true,
		InstructionID:   seq,
		MessageID:       userMsgID,
		ClientRequestID: clientReqID,
	}
	s.storePromptReceipt(sessionID, clientReqID, receipt)
	return http.StatusOK, receipt, nil
}

// nativeCLISystemForTurn avoids repeating commandExecutionGuidance and unchanged
// caller system instructions inside every user message of a resumed native CLI
// conversation (--resume / --conversation), where Turn 1 already placed them in
// the CLI's persistent conversation history.
func (s *Service) nativeCLISystemForTurn(rec SessionRecord, callerSystem, fullSystem string) string {
	if strings.TrimSpace(rec.ExternalID) == "" || rec.ContextPending {
		return fullSystem
	}
	prev, ok := s.cliSystemDelivered.Load(rec.ID + "\x00" + rec.Runtime)
	if !ok {
		return fullSystem
	}
	prevCaller, _ := prev.(string)
	if callerSystem == "" || callerSystem == prevCaller {
		return ""
	}
	return callerSystem
}

func (s *Service) recordCLISystemDelivered(sessionID, runtime, callerSystem string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runtime) == "" {
		return
	}
	s.cliSystemDelivered.Store(sessionID+"\x00"+runtime, callerSystem)
}

func (s *Service) fallbackCLISystemForFreshRetry(sessionID, runtime string) string {
	sys := commandExecutionGuidance
	if prev, ok := s.cliSystemDelivered.Load(sessionID + "\x00" + runtime); ok {
		if caller, _ := prev.(string); strings.TrimSpace(caller) != "" {
			sys += "\n\n" + strings.TrimSpace(caller)
		}
	}
	return sys
}

