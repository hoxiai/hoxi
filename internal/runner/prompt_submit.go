package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// PromptRequest is one user turn: the JSON body of POST .../prompt, also
// submitted in process by RunPromptAndWait.
type PromptImageOptions struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
	Count       int    `json:"count,omitempty"`
}

type PromptRequest struct {
	Text            string              `json:"text"`
	Runtime         string              `json:"runtime,omitempty"`
	Agent           string              `json:"agent"`
	Variant         string              `json:"variant"`
	FastMode        bool                `json:"fastMode,omitempty"`
	System          string              `json:"system"`
	Model           *PromptModel        `json:"model"`
	Orchestrator    *PromptOrchestrator `json:"orchestrator,omitempty"`
	ImageOptions    *PromptImageOptions `json:"imageOptions,omitempty"`
	Parts           []MessagePart       `json:"parts"`
	SkillSelection  *SkillSelection     `json:"skillSelection,omitempty"`
	ClientRequestID string              `json:"clientRequestId,omitempty"`
	ContinueTask    bool                `json:"continueTask,omitempty"`
}

func resolveRequestedPromptRuntime(body PromptRequest, currentRuntime string) string {
	if rt := strings.TrimSpace(body.Runtime); rt == "opencode" || IsValidRuntime(rt) {
		return rt
	}
	if body.Model != nil {
		if prov := strings.TrimSpace(body.Model.ProviderID); IsValidRuntime(prov) {
			return prov
		}
	}
	return currentRuntime
}

// Stable execution guidance shared by desktop, mobile, CLI and orchestration
// prompts. Keep it out of visible user text and out of the approval policy.
const commandExecutionGuidance = `Hoxi command execution:
Keep build, test and workspace-check progress visible. Unless the user explicitly requests it, do not pipe long-running checks into tail/head or filters that suppress progress: tail -N waits for EOF, and grep may buffer output.
Use the tool's bounded output when available. If a full log is needed, save it to a unique temporary file and inspect bounded excerpts while the command runs. Preserve and check the original command's exit status; a successful tail/grep does not prove the check passed.
Set a suitable tool timeout and test timeout. If a check stays silent, inspect its current stage and existing log before retrying; do not start duplicate checks or repeatedly wait without reporting progress. Reuse build caches unless a clean build is required.
When navigating code, follow: locate symbol/file or fetch a full surgical package in one step (prefer hoxi_code_index tool or 'hoxi code-index context|search|inspect|callers|callees|impact|refresh') -> inspect the target function and minimal surrounding lines instead of whole files -> check callers, callees, blast radius (impact) and related tests. Edit and then run targeted verification only when the task asks for changes.
When a filename is only known at runtime (for example, a hashed build asset), use the file tools to list matching names, then search or read the exact path returned by the listing. For minified files, request a bounded excerpt around the match.
When the user asks to generate or edit visual/image assets (icons, banners, illustrations, mockups, or photo edits), prefer calling the hoxi_image tool (or 'hoxi image generate "<prompt>" --out <workspace-relative-path>' in CLI sessions) so Hoxi checks the active model's native capability first, then invokes configured local image models (falling back to hoxi_tools only when no local image model is available). For video or voiceover/TTS generation, prefer calling hoxi_video / hoxi_speech (or 'hoxi video|speech generate' in CLI sessions) with the same native/local-first check before falling back to hoxi_tools. When you create or save a visual asset (chart, diagram, screenshot, SVG, or raster image) via scripts, shell commands, or file writes rather than hoxi_image, embed it directly in your reply using Markdown image syntax ('![short description](workspace-relative-path)') so the user sees it inline.
Reviews, audits and questions are read-only: report findings and do not edit, commit, push or publish unless the user asks for a fix or that action. Within the work the user asked for, keep going without asking whether to continue; stop to ask only for a real blocker, a choice the user must make, or an action that needs approval.
Handle normal requests—including small multi-file edits or a few related items—directly in the main turn without emitting a hoxi-subtasks block or splitting into background sessions. ONLY when the user explicitly asks to spawn/start a background subtask and continue chatting in the main thread (e.g. "开一个子任务…然后继续对话" / "background subtask"), or explicitly asks to split work into isolated parallel coding subtasks: do NOT call the built-in synchronous task/subagent tool (which blocks the current turn until the subagent finishes). Instead, output a fenced hoxi-subtasks JSON block and end your turn immediately so Hoxi runs the subtask(s) asynchronously in isolated background sessions while the main conversation stays responsive: {"title":"plan title","autoStart":false,"concurrency":3,"tasks":[{"id":"task-1","prompt":"self-contained instructions and verification","dependsOn":[]}]} (set "autoStart":true ONLY when the user explicitly asks to start the background subtask(s) right away; otherwise keep "autoStart":false to propose the plan for review first).`

var (
	promptGuidanceMu    sync.RWMutex
	extraPromptGuidance []string
)

// RegisterPromptGuidance appends host extension execution guidance lines
// without leaking private domain terminology into the open-source host.
func RegisterPromptGuidance(guidance string) {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return
	}
	promptGuidanceMu.Lock()
	defer promptGuidanceMu.Unlock()
	for _, existing := range extraPromptGuidance {
		if existing == guidance {
			return
		}
	}
	extraPromptGuidance = append(extraPromptGuidance, guidance)
}

func effectiveCommandExecutionGuidance() string {
	promptGuidanceMu.RLock()
	extra := append([]string(nil), extraPromptGuidance...)
	promptGuidanceMu.RUnlock()
	if len(extra) == 0 {
		return commandExecutionGuidance
	}
	return commandExecutionGuidance + "\n" + strings.Join(extra, "\n")
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
	if s.ledger != nil {
		out, found := s.ledger.PeekRequest(RequestKindPrompt, sessionID, clientRequestID, "", nil)
		if !found || out.State != RequestAdmissionDone {
			return PromptReceipt{}, false
		}
		var cached PromptReceipt
		if err := json.Unmarshal(out.ResponseBody, &cached); err != nil {
			return PromptReceipt{}, false
		}
		cached.Deduplicated = true
		return cached, true
	}
	key := sessionID + "\x00" + clientRequestID
	entry, ok := s.promptReceipts.Load(key)
	if !ok {
		return PromptReceipt{}, false
	}
	if time.Now().After(entry.expiresAt) {
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
	expiresAt := now.Add(idempotencyReceiptTTL)
	s.promptReceipts.Range(func(k string, entry cachedPromptReceipt) bool {
		if now.After(entry.expiresAt) {
			s.promptReceipts.Delete(k)
		}
		return true
	})
	s.promptReceipts.Store(sessionID+"\x00"+clientRequestID, cachedPromptReceipt{
		receipt:   receipt,
		expiresAt: expiresAt,
	})
	if s.ledger != nil {
		s.ledger.PutPromptReceipt(sessionID, clientRequestID, receipt, expiresAt)
	}
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
	if s.ledger != nil && s.ledger.LoadErr() != nil {
		return http.StatusServiceUnavailable, PromptReceipt{}, &CodedRequestError{
			HTTPStatus: http.StatusServiceUnavailable, Code: ErrCodeRequestStorageUnavailable,
			Message:         "request ledger could not be loaded; restore the preserved ledger and restart before sending",
			ClientRequestID: clientReqID,
		}
	}
	reqDigest := ""
	if clientReqID != "" {
		reqDigest = PromptRequestDigest(sessionID, body)
	}
	if s.ledger == nil {
		if cached, ok := s.lookupPromptReceipt(sessionID, clientReqID); ok {
			return http.StatusOK, cached, nil
		}
	}

	rec, ok := s.store.Get(sessionID)
	unmanagedBusySteer := false
	pendingAdopted := false
	wantRuntime := resolveRequestedPromptRuntime(body, "opencode")
	reqImageTurn := body.Model != nil && s.IsDedicatedMediaModel(body.Model.ProviderID, body.Model.ModelID)
	hasStoreCtx := s.resolveSessionContextBlock(sessionID, directory) != ""
	needsOpenCodeAdopt := body.SkillSelection != nil || reqImageTurn || hasStoreCtx || (wantRuntime != "" && wantRuntime != "opencode")
	if !ok && (needsOpenCodeAdopt || (clientReqID != "" && s.openCode != nil && s.openCode.Available())) {
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
			} else if reqImageTurn {
				dir := s.resolveUnmanagedSessionDir(sessionID, directory)
				projectID, _ := s.ensureProjectID(dir)
				nowMs := time.Now().UnixMilli()
				rec = SessionRecord{
					ID:           sessionID,
					ProjectID:    projectID,
					Runtime:      "opencode",
					ProjectDir:   dir,
					WorktreePath: dir,
					SandboxMode:  "local",
					Title:        "New Session",
					CreatedAt:    nowMs,
					UpdatedAt:    nowMs,
					Turns:        []MessageTurn{},
				}
				ok = true
				pendingAdopted = true
			} else if !needsOpenCodeAdopt && strings.Contains(strings.ToLower(adoptErr.Error()), "not found") {
				return http.StatusNotFound, PromptReceipt{}, errors.New("runner session not found")
			} else {
				return http.StatusConflict, PromptReceipt{}, adoptErr
			}
		} else {
			rec = adopted
			ok = true
			pendingAdopted = true
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
	if clientReqID != "" && s.ledger != nil {
		if prev, found := s.ledger.PeekRequest(RequestKindPrompt, sessionID, clientReqID, reqDigest, nil); found {
			if prev.State == RequestAdmissionDone {
				var cached PromptReceipt
				if err := json.Unmarshal(prev.ResponseBody, &cached); err == nil {
					cached.Deduplicated = true
					return prev.HTTPStatus, cached, nil
				}
			}
			return prev.HTTPStatus, PromptReceipt{}, &CodedRequestError{
				HTTPStatus:      prev.HTTPStatus,
				Code:            prev.ErrorCode,
				Message:         prev.ErrorMessage,
				ClientRequestID: clientReqID,
			}
		}
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
	runtimeSwitched := false
	if targetRuntime := resolveRequestedPromptRuntime(body, rec.Runtime); targetRuntime != "" && targetRuntime != rec.Runtime && !unmanagedBusySteer {
		s.mu.Lock()
		ap := s.procs[sessionID]
		busy := ap != nil && !ap.hasPendingError() && !ap.wasAborted()
		s.mu.Unlock()
		if busy {
			return http.StatusConflict, PromptReceipt{}, errors.New("cannot change runtime while the session is running")
		}
		runtimeSwitched = true
		rec.Runtime = targetRuntime
		rec.ExternalID = ""
		rec.ContextPending = len(rec.Turns) > 0
		rec.ContextTurnCount = len(rec.Turns)
		if targetRuntime == "opencode" && IsValidRuntime(rec.ProviderID) {
			rec.ProviderID = ""
		}
	}
	orchSpec, orchErr := validatePromptOrchestratorForRuntime(body.Orchestrator, rec.Runtime)
	if orchErr != nil {
		return http.StatusBadRequest, PromptReceipt{}, orchErr
	}
	callerSystem, turnDirective := splitTurnDirective(body.System)
	requestedMode := firstNonEmptyStr(body.Agent, rec.Mode, "build")
	if storeCtx := s.resolveSessionContextBlock(rec.ID, rec.WorktreePath); storeCtx != "" {
		if isAppDevPromptMode(requestedMode, "") {
			storeCtx = stripTaggedPromptSection(storeCtx, "<hoxi_work_app>", "</hoxi_work_app>")
		}
		if storeCtx != "" && !strings.Contains(callerSystem, "<hoxi_work_store_profile>") && !strings.Contains(callerSystem, "<hoxi_work_app>") {
			if callerSystem == "" {
				callerSystem = storeCtx
			} else {
				callerSystem = storeCtx + "\n\n" + callerSystem
			}
		}
	}
	body.System = strings.TrimSpace(effectiveCommandExecutionGuidance() + "\n\n" + callerSystem)
	var resolvedSkill ResolvedSkill
	if body.SkillSelection != nil {
		resolved, err := s.resolveSkillSelection(ctx, rec.WorktreePath, rec.ID, rec.Runtime, *body.SkillSelection)
		if err != nil {
			return http.StatusConflict, PromptReceipt{}, err
		}
		resolvedSkill = resolved
	}
	candidateModelID, candidateProviderID := rec.ModelID, rec.ProviderID
	if body.Model != nil && strings.TrimSpace(body.Model.ModelID) != "" {
		candidateModelID = strings.TrimSpace(body.Model.ModelID)
		if strings.TrimSpace(body.Model.ProviderID) != "" {
			candidateProviderID = strings.TrimSpace(body.Model.ProviderID)
		}
	}
	isImageTurn := s.IsDedicatedMediaModel(candidateProviderID, candidateModelID)
	if rec.Runtime == string(RuntimeNative) && !isImageTurn {
		if handled, st, recpt, sErr := s.trySteerBusyNativePrompt(ctx, rec, body, promptText, callerSystem, clientReqID, reqDigest, fileParts, resolvedSkill); handled {
			return st, recpt, sErr
		}
	}
	openCodeDaemonUp := s.openCode != nil && s.openCode.Available()
	if rec.Runtime == "opencode" && !isImageTurn && openCodeDaemonUp {
		s.mu.Lock()
		ap := s.procs[sessionID]
		busy := unmanagedBusySteer || (ap != nil && ap.runtime == "opencode" && !ap.hasPendingError() && !ap.wasAborted())
		s.mu.Unlock()
		if busy {
			modelID, providerID := rec.ModelID, rec.ProviderID
			if body.Model != nil {
				modelID, providerID = body.Model.ModelID, body.Model.ProviderID
			}
			resolvedModel, checkErr := s.ensureOpenCodeModelAvailable(ctx, rec.WorktreePath, firstNonEmptyStr(providerID, rec.ProviderID), firstNonEmptyStr(modelID, rec.ModelID), false)
			if checkErr != nil {
				return http.StatusConflict, PromptReceipt{}, checkErr
			}
			if modelID != "" && resolvedModel != "" {
				modelID = resolvedModel
			}
			if pendingAdopted {
				if putErr := s.store.Put(rec); putErr != nil {
					return http.StatusInternalServerError, PromptReceipt{}, putErr
				}
				for _, id := range rec.OpenCodeIDs {
					s.managedOpenCode.Store(id, true)
				}
			}
			if clientReqID != "" && s.ledger != nil {
				if adm := s.ledger.AdmitRequest(RequestKindPrompt, sessionID, sessionID, clientReqID, reqDigest, nil); adm.State != RequestAdmissionNew {
					if adm.State == RequestAdmissionDone {
						var cached PromptReceipt
						if err := json.Unmarshal(adm.ResponseBody, &cached); err == nil {
							cached.Deduplicated = true
							return adm.HTTPStatus, cached, nil
						}
					}
					return adm.HTTPStatus, PromptReceipt{}, &CodedRequestError{
						HTTPStatus:      adm.HTTPStatus,
						Code:            adm.ErrorCode,
						Message:         adm.ErrorMessage,
						ClientRequestID: clientReqID,
					}
				}
			}
			steerSeq, steerMsgID, err := s.steerOpenCodeTurn(ctx, rec, firstNonEmptyStr(body.Agent, rec.Mode, "build"), modelID, providerID, body.Variant, body.System, promptText, body.Parts, body.SkillSelection)
			if err != nil {
				if clientReqID != "" && s.ledger != nil {
					s.ledger.MarkRequestUnknown(RequestKindPrompt, sessionID, clientReqID, err.Error())
					return http.StatusBadGateway, PromptReceipt{}, &CodedRequestError{
						HTTPStatus:      http.StatusBadGateway,
						Code:            ErrCodeOutcomeUnknown,
						Message:         err.Error(),
						ClientRequestID: clientReqID,
					}
				}
				return http.StatusBadGateway, PromptReceipt{}, err
			}
			receipt := PromptReceipt{
				OK:              true,
				InstructionID:   steerSeq,
				MessageID:       steerMsgID,
				ClientRequestID: clientReqID,
			}
			if clientReqID != "" && s.ledger != nil {
				if raw, mErr := json.Marshal(receipt); mErr == nil {
					s.ledger.CompleteRequest(RequestKindPrompt, sessionID, sessionID, clientReqID, reqDigest, http.StatusOK, raw, map[string]string{"userMsgID": steerMsgID})
				}
			} else {
				s.storePromptReceipt(sessionID, clientReqID, receipt)
			}
			return http.StatusOK, receipt, nil
		}
	}
	if (rec.Runtime == string(RuntimeNative) || (rec.Runtime == "opencode" && !openCodeDaemonUp)) && !isImageTurn {
		if handled, st, rcpt, sErr := s.trySteerBusyNativePrompt(ctx, rec, body, promptText, callerSystem, clientReqID, reqDigest, fileParts, resolvedSkill); handled {
			return st, rcpt, sErr
		}
	}

	mode := firstNonEmptyStr(body.Agent, rec.Mode, "build")
	modelID := rec.ModelID
	if body.Model != nil && strings.TrimSpace(body.Model.ModelID) != "" {
		modelID = strings.TrimSpace(body.Model.ModelID)
		if body.Model.ProviderID != "" {
			rec.ProviderID = body.Model.ProviderID
		} else if rec.Runtime == "opencode" && IsValidRuntime(rec.ProviderID) {
			rec.ProviderID = ""
		}
	}
	llmPromptText, resolvedMode, resolvedProvID, resolvedModelID, nextCallerSys := resolveSlashCommandAndAgentOverridesForRuntime(
		promptText,
		rec.WorktreePath,
		mode,
		rec.ProviderID,
		modelID,
		callerSystem,
		rec.Runtime,
		openCodeDaemonUp,
	)
	mode = resolvedMode
	rec.ProviderID = resolvedProvID
	modelID = resolvedModelID
	if nextCallerSys != callerSystem {
		callerSystem = nextCallerSys
		body.System = strings.TrimSpace(effectiveCommandExecutionGuidance() + "\n\n" + callerSystem)
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
	case string(RuntimeCodex):
		binPath = agentcli.ResolveCodexBinary()
		missingMsg = "Codex CLI (codex) binary not found in PATH"
	case string(RuntimeOpenCode):
		if !openCodeDaemonUp {
			binPath = agentcli.ResolveOpenCodeBinary()
			if binPath == "" && strings.TrimSpace(body.Runtime) != "opencode" {
				// Seamless upgrade continuation: legacy Hoxi sessions persisted with
				// Runtime="opencode" automatically continue on the in-process native
				// Go engine when neither `opencode serve` nor `opencode` CLI exists.
				s.recordMigrationJournalUpgraded(rec, rec.ExternalID, "")
				runtimeSwitched = true
				rec.Runtime = string(RuntimeNative)
				rec.ExternalID = ""
				rec.ContextPending = len(rec.Turns) > 0
				rec.ContextTurnCount = len(rec.Turns)
				binPath = "native"
			} else {
				missingMsg = "OpenCode CLI (opencode) binary not found in ~/.hoxi/bin/opencode or PATH (install via 'npm i -g opencode-ai' or 'brew install opencode', then reload runtimes in Settings)"
			}
		}
	case string(RuntimeNative):
		binPath = "native"
	}
	if binPath == "" && (rec.Runtime != "opencode" || !openCodeDaemonUp) && rec.Runtime != string(RuntimeNative) {
		return http.StatusBadRequest, PromptReceipt{}, errors.New(missingMsg)
	}
	isImageTurn = s.IsDedicatedMediaModel(rec.ProviderID, modelID)
	if rec.Runtime == "opencode" && !isImageTurn && openCodeDaemonUp {
		resolvedModel, checkErr := s.ensureOpenCodeModelAvailable(ctx, rec.WorktreePath, rec.ProviderID, modelID, true)
		if checkErr != nil {
			return http.StatusConflict, PromptReceipt{}, checkErr
		}
		if resolvedModel != "" && resolvedModel != modelID {
			modelID = resolvedModel
		}
	}

	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	steeredTurn := false
	if ap != nil && !ap.wasAborted() && !ap.hasPendingError() && RuntimeCapabilities(rec.Runtime, rec.Subagent).Steer {
		steeredTurn = true
		ap.markSteered()
		s.abortActiveProcess(sessionID)
	}
	finishSteerAbort := func() {
		if !steeredTurn {
			return
		}
		if s.status != nil {
			s.status.SetSessionBusy(sessionID, false)
		}
		s.broadcast(sessionID, SSEEvent{
			Type: "session.status",
			Properties: map[string]interface{}{
				"sessionID": sessionID,
				"status":    SessionStatus{Type: "idle"},
			},
		})
	}
	if ap != nil && (ap.wasAborted() || ap.hasPendingError()) {
		select {
		case <-ap.done:
		case <-ctx.Done():
			finishSteerAbort()
			return http.StatusRequestTimeout, PromptReceipt{}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
		if latest, ok := s.store.Get(sessionID); ok {
			if !runtimeSwitched {
				rec.ExternalID = latest.ExternalID
			} else {
				rec.ContextPending = len(latest.Turns) > 0
				rec.ContextTurnCount = len(latest.Turns)
			}
			rec.Turns = latest.Turns
		}
	}
	s.mu.Lock()
	if s.procs[sessionID] != nil {
		s.mu.Unlock()
		finishSteerAbort()
		return http.StatusConflict, PromptReceipt{}, errors.New("session already has an active CLI process running")
	}
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	preMutationRec, hadPreMutationRec := s.store.Get(sessionID)
	if pendingAdopted {
		if putErr := s.store.Put(rec); putErr != nil {
			finishSteerAbort()
			return http.StatusInternalServerError, PromptReceipt{}, putErr
		}
		for _, id := range rec.OpenCodeIDs {
			s.managedOpenCode.Store(id, true)
		}
	}

	now := time.Now().UnixMilli()
	userMsgID := fmt.Sprintf("msg_u_%d_%d", now, seq)
	asstMsgID := fmt.Sprintf("msg_a_%d_%d", now+1, seq)
	userInfo := MessageInfo{
		ID:           userMsgID,
		SessionID:    sessionID,
		Role:         "user",
		Time:         MessageTime{Created: now},
		Agent:        mode,
		ServiceTier:  fastServiceTierValue(body.FastMode && !rec.Subagent && s.ApprovalPolicy().Mode != ApprovalModeUnattended),
		ModelID:      modelID,
		ProviderID:   rec.ProviderID,
		ContinueTask: body.ContinueTask,
	}
	storedParts, deliveryParts, cliPrompt := s.prepareTurnAttachmentsForModel(rec, rec.ProviderID, modelID, isImageTurn, now, seq, userMsgID, llmPromptText, fileParts)
	if llmPromptText != promptText {
		for i := range storedParts {
			if storedParts[i].Type == "text" {
				storedParts[i].Text = promptText
				break
			}
		}
	}
	if body.SkillSelection != nil {
		s.recordUserSkillOverride(userMsgID, promptText, body.SkillSelection)
		storedParts, deliveryParts = applySkillSelectionToTurnParts(
			sessionID,
			userMsgID,
			now,
			seq,
			llmPromptText,
			body.SkillSelection,
			rec.Runtime != "opencode",
			resolvedSkill.Content,
			storedParts,
			deliveryParts,
		)
	}

	oldQoderBin, _ := agentcli.ResolveQoderBinaryForModel(rec.ModelID)
	newQoderBin, _ := agentcli.ResolveQoderBinaryForModel(modelID)
	resetQoderResume := rec.Runtime == string(RuntimeQoder) &&
		strings.TrimSpace(rec.ExternalID) != "" &&
		oldQoderBin != newQoderBin
	resetGeminiResume := rec.Runtime == string(RuntimeGeminiCLI) &&
		strings.TrimSpace(rec.ExternalID) != "" &&
		ShouldGeminiCLIUseAgy(rec.ModelID) != ShouldGeminiCLIUseAgy(modelID)
	resetAgySplitBrain := (rec.Runtime == string(RuntimeAntigravity) ||
		(rec.Runtime == string(RuntimeGeminiCLI) && ShouldGeminiCLIUseAgy(modelID))) &&
		len(rec.Turns) > 0 &&
		hasAgySplitBrainConversation(rec.ExternalID)
	if resetAgySplitBrain && quarantineEmptyAgySQLiteStore(rec.ExternalID) {
		// The history is intact in the Protobuf store: keep resuming it.
		resetAgySplitBrain = false
	}
	canResumePartialNative := rec.Runtime == string(RuntimeNative) && !isImageTurn && body.SkillSelection == nil && len(fileParts) == 0
	cycleMutated := s.computeRetryCycleMutated(rec, body.ContinueTask, canResumePartialNative)
	var resumedUserInfo, clearedFailedAsst *MessageInfo
	_, _, _ = s.store.Update(sessionID, func(r *SessionRecord) {
		resumedUserInfo, clearedFailedAsst = prepareFailedTurnRetryLocked(
			r, body.ContinueTask, canResumePartialNative, cycleMutated, promptText,
		)
		if runtimeSwitched {
			r.Runtime = rec.Runtime
			r.ExternalID = ""
			r.ContextPending = len(r.Turns) > 0
			r.ContextTurnCount = len(r.Turns)
		}
		r.ModelID = modelID
		r.Mode = mode
		r.ProviderID = rec.ProviderID
		if resetGeminiResume || resetQoderResume || resetAgySplitBrain {
			r.ExternalID = ""
			r.ContextPending = len(r.Turns) > 0
			r.ContextTurnCount = len(r.Turns)
		}
	})
	if resumedUserInfo != nil {
		userMsgID = resumedUserInfo.ID
	}
	s.recordTurnSnapshot(sessionID, userMsgID)
	if updated, ok := s.store.Get(sessionID); ok {
		rec = updated
	}

	if clientReqID != "" && s.ledger != nil {
		if adm := s.ledger.AdmitRequest(RequestKindPrompt, sessionID, sessionID, clientReqID, reqDigest, map[string]string{"userMsgID": userMsgID}); adm.State != RequestAdmissionNew {
			if hadPreMutationRec {
				_ = s.store.Put(preMutationRec)
			}
			finishSteerAbort()
			if adm.State == RequestAdmissionDone {
				var cached PromptReceipt
				if err := json.Unmarshal(adm.ResponseBody, &cached); err == nil {
					cached.Deduplicated = true
					return adm.HTTPStatus, cached, nil
				}
			}
			return adm.HTTPStatus, PromptReceipt{}, &CodedRequestError{
				HTTPStatus:      adm.HTTPStatus,
				Code:            adm.ErrorCode,
				Message:         adm.ErrorMessage,
				ClientRequestID: clientReqID,
			}
		}
	}

	s.recordInflightTurn(InflightTurnEntry{
		SessionID:       rec.ID,
		Runtime:         rec.Runtime,
		ExternalID:      rec.ExternalID,
		UserMsgID:       userMsgID,
		AsstMsgID:       asstMsgID,
		Mode:            mode,
		ProviderID:      rec.ProviderID,
		ModelID:         modelID,
		Effort:          effort,
		FastMode:        userInfo.ServiceTier == "fast",
		Orchestrator:    toLedgerOrchestrator(orchSpec),
		CallerSystem:    callerSystem,
		PromptText:      cliPrompt,
		ClientRequestID: clientReqID,
		StartedAt:       now,
		UpdatedAt:       now,
	})

	var err error
	if s.IsVideoModel(rec.ProviderID, modelID) {
		err = s.startVideoGenerationTurn(rec, mode, modelID, userInfo, storedParts, deliveryParts, asstMsgID, promptText, body.ImageOptions)
	} else if isImageTurn {
		err = s.startImageGenerationTurn(rec, mode, modelID, userInfo, storedParts, deliveryParts, asstMsgID, promptText, body.ImageOptions)
	} else if rec.Runtime == "opencode" && openCodeDaemonUp {
		err = s.startOpenCodeTurn(rec, mode, modelID, effort, joinPromptSections(body.System, turnDirective), userInfo, storedParts, deliveryParts)
	} else if rec.Runtime == string(RuntimeNative) {
		nativeSystem := body.System
		if clearedFailedAsst != nil {
			s.broadcast(sessionID, SSEEvent{
				Type:       "message.updated",
				Properties: map[string]interface{}{"info": *clearedFailedAsst},
			})
		}
		if resumedUserInfo != nil {
			resumed := *resumedUserInfo
			resumed.ServiceTier = userInfo.ServiceTier
			err = s.launchNativeTurn(rec, mode, modelID, effort, nativeSystem, restartContinuationPrompt, resumed, nil, asstMsgID, true, toNativeOrchestrator(orchSpec))
		} else {
			promptInput := withTurnDirective(turnDirective, cliPrompt)
			if body.SkillSelection != nil {
				promptInput = formatInlineSkillPrompt(body.SkillSelection.Name, resolvedSkill.Content, "") + "\n\n" + promptInput
			}
			err = s.launchNativeTurn(rec, mode, modelID, effort, nativeSystem, promptInput, userInfo, storedParts, asstMsgID, false, toNativeOrchestrator(orchSpec), deliveryParts...)
		}
	} else {
		input := runtimeMemoryContext(rec) + runtimeContext(rec)
		if body.SkillSelection != nil {
			input += formatInlineSkillPrompt(body.SkillSelection.Name, resolvedSkill.Content, "") + "\n\n"
		}
		if cliSys := s.nativeCLISystemForTurn(rec, callerSystem, body.System); cliSys != "" {
			input += cliSys + "\n\n"
		}
		err = s.startTurnProcess(rec, binPath, mode, modelID, effort, input+withTurnDirective(turnDirective, cliPrompt), userInfo, storedParts, deliveryParts, asstMsgID)
		if err == nil {
			s.recordCLISystemDeliveredForTurn(rec, callerSystem)
		}
	}
	if err != nil {
		if clientReqID != "" && s.ledger != nil {
			s.ledger.AbortUncommittedRequest(RequestKindPrompt, sessionID, clientReqID)
		}
		s.clearInflightTurn(sessionID)
		if current, ok := s.store.Get(sessionID); ok {
			prevIDs := make(map[string]bool, len(preMutationRec.OpenCodeIDs))
			for _, id := range preMutationRec.OpenCodeIDs {
				prevIDs[id] = true
			}
			for _, id := range current.OpenCodeIDs {
				if !prevIDs[id] {
					s.managedOpenCode.Delete(id)
				}
			}
		}
		if hadPreMutationRec {
			_ = s.store.Put(preMutationRec)
		} else if pendingAdopted {
			_ = s.store.Delete(sessionID)
			for _, id := range rec.OpenCodeIDs {
				s.managedOpenCode.Delete(id)
			}
		}
		finishSteerAbort()
		return http.StatusInternalServerError, PromptReceipt{}, err
	}
	if runtimeSwitched {
		if updated, ok := s.store.Get(sessionID); ok {
			info := updated.ToSessionInfo()
			s.broadcast(sessionID, SSEEvent{Type: "session.updated", Properties: map[string]interface{}{"info": info}})
		}
	}
	receipt := PromptReceipt{
		OK:              true,
		InstructionID:   seq,
		MessageID:       userMsgID,
		ClientRequestID: clientReqID,
	}
	if clientReqID != "" && s.ledger != nil {
		if raw, mErr := json.Marshal(receipt); mErr == nil {
			// Only this execution's allocated assistant ID is eligible for result inspection.
			// Runtimes that substitute other IDs fail closed; never select the latest reply.
			s.ledger.CompleteRequest(RequestKindPrompt, sessionID, sessionID, clientReqID, reqDigest, http.StatusOK, raw, map[string]string{"userMsgID": userMsgID, "assistantMsgID": asstMsgID})
		}
	} else {
		s.storePromptReceipt(sessionID, clientReqID, receipt)
	}
	return http.StatusOK, receipt, nil
}
