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
	Text           string          `json:"text"`
	Agent          string          `json:"agent"`
	Variant        string          `json:"variant"`
	System         string          `json:"system"`
	Model          *PromptModel    `json:"model"`
	Parts          []MessagePart   `json:"parts"`
	SkillSelection *SkillSelection `json:"skillSelection,omitempty"`
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
	unlock := s.turnMu.lock(sessionID)
	defer unlock()

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
				return http.StatusConflict, adoptErr
			}
		} else {
			if putErr := s.store.Put(adopted); putErr != nil {
				return http.StatusInternalServerError, putErr
			}
			for _, id := range adopted.OpenCodeIDs {
				s.managedOpenCode.Store(id, true)
			}
			rec = adopted
			ok = true
		}
	}
	if !ok {
		return http.StatusNotFound, errors.New("runner session not found")
	}
	if reqDir := strings.TrimSpace(directory); reqDir != "" &&
		normalizeComparePath(reqDir) != normalizeComparePath(rec.WorktreePath) &&
		normalizeComparePath(reqDir) != normalizeComparePath(rec.ProjectDir) {
		return http.StatusForbidden, errors.New("session directory does not match its sandbox")
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
		return http.StatusBadRequest, errors.New("prompt text is required")
	}

	if rec.Subagent {
		return http.StatusConflict, errSubagentSessionReadOnly
	}
	if err := validateSessionDirectory(rec); err != nil {
		return http.StatusBadRequest, err
	}
	var resolvedSkill ResolvedSkill
	if body.SkillSelection != nil {
		resolved, err := s.resolveSkillSelection(ctx, rec.WorktreePath, rec.ID, rec.Runtime, *body.SkillSelection)
		if err != nil {
			return http.StatusConflict, err
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
				return http.StatusConflict, checkErr
			}
			err := s.steerOpenCodeTurn(ctx, rec, firstNonEmptyStr(body.Agent, rec.Mode, "build"), modelID, providerID, body.Variant, body.System, promptText, body.Parts, body.SkillSelection)
			if err != nil {
				return http.StatusBadGateway, err
			}
			return http.StatusOK, nil
		}
	}

	mode := firstNonEmptyStr(body.Agent, rec.Mode, "build")
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
	case string(RuntimeCodeBuddy):
		binPath = agentcli.ResolveCodeBuddyBinary()
		missingMsg = "CodeBuddy CLI (codebuddy) binary not found in PATH"
	case string(RuntimeQoder):
		binPath, _ = agentcli.ResolveQoderBinaryForModel(modelID)
		missingMsg = "Qoder CLI (qodercli / qoderclicn) binary not found in PATH"
	}
	if binPath == "" && rec.Runtime != "opencode" {
		return http.StatusBadRequest, errors.New(missingMsg)
	}

	s.mu.Lock()
	if s.procs[sessionID] != nil {
		s.mu.Unlock()
		return http.StatusConflict, errors.New("session already has an active CLI process running")
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

	_, _, _ = s.store.Update(sessionID, func(r *SessionRecord) {
		r.ModelID = modelID
		r.Mode = mode
		r.ProviderID = rec.ProviderID
	})
	s.recordTurnSnapshot(sessionID, userMsgID)
	if updated, ok := s.store.Get(sessionID); ok {
		rec = updated
	}

	var err error
	if rec.Runtime == "opencode" {
		if checkErr := s.ensureOpenCodeModelAvailable(ctx, rec.WorktreePath, rec.ProviderID, modelID); checkErr != nil {
			return http.StatusConflict, checkErr
		}
		err = s.startOpenCodeTurn(rec, mode, modelID, effort, body.System, userInfo, storedParts, deliveryParts)
	} else {
		input := runtimeMemoryContext(rec) + runtimeContext(rec)
		if body.SkillSelection != nil {
			input += formatInlineSkillPrompt(body.SkillSelection.Name, resolvedSkill.Content, "") + "\n\n"
		}
		if body.System != "" {
			input += body.System + "\n\n"
		}
		err = s.startTurnProcess(rec, binPath, mode, modelID, effort, input+cliPrompt, userInfo, storedParts, deliveryParts, asstMsgID)
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusOK, nil
}
