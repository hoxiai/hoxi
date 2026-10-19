package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type Service struct {
	rootDir   string
	store     *Store
	worktrees *worktree.Manager
	status    *statusbar.Tracker
	gate      *approvalGate

	mu               sync.Mutex
	turnMu           sync.Mutex
	procs            map[string]*activeProcess
	childBusy        map[string]bool
	subs             map[chan SSEEvent]string
	seqSeed          int64
	openCodeURL      string
	openCodePassword string
	openCodeClient   *http.Client
	managedOpenCode  sync.Map
}

func NewService(rootDir string, worktrees *worktree.Manager, status *statusbar.Tracker, customStorePath string) *Service {
	if worktrees == nil {
		worktrees = worktree.NewManager(rootDir)
	}
	svc := &Service{
		rootDir:   rootDir,
		store:     NewStore(customStorePath),
		worktrees: worktrees,
		status:    status,
		gate:      newApprovalGate(),
		procs:     make(map[string]*activeProcess),
		childBusy: make(map[string]bool),
		subs:      make(map[chan SSEEvent]string),
	}
	for _, info := range svc.store.List("") {
		rec, _ := svc.store.Meta(info.ID)
		if rec.Subagent || validateSessionDirectory(rec) != nil {
			continue
		}
		if info.SandboxMode == "local" {
			if _, err := existingDirectory(info.ProjectID); err == nil {
				_ = svc.worktrees.ForDirectory(info.ProjectID).UseLocalDirectory(info.ID)
			}
		} else {
			_ = svc.worktrees.ForDirectory(rec.ProjectDir).RestoreSession(rec.ID, rec.WorktreePath, rec.Branch, rec.BaseCommit)
		}
	}
	svc.worktrees.SetApplyGuard(svc.guardWorktreeApply)
	return svc
}

func (s *Service) Store() *Store {
	return s.store
}

func (s *Service) CloseAll() {
	s.mu.Lock()
	active := make([]*activeProcess, 0, len(s.procs))
	for _, p := range s.procs {
		active = append(active, p)
	}
	s.mu.Unlock()
	for _, p := range active {
		p.Abort()
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/runner/antigravity/models", s.handleAntigravityModels)
	mux.HandleFunc("/api/hoxi/runner/claude-code/models", s.handleClaudeModels)
	mux.HandleFunc("/api/hoxi/runner/antigravity/hook", s.handleAgyHookInstall)
	mux.HandleFunc(hookPreToolPath, s.handleHookPreTool)
	mux.HandleFunc(claudeHookPreToolPath, s.handleClaudeHookPreTool)
	mux.HandleFunc("/api/hoxi/runner/permission", s.handlePermissionRoutes)
	mux.HandleFunc("/api/hoxi/runner/permission/", s.handlePermissionRoutes)
	mux.HandleFunc("/api/hoxi/event", s.handleUnifiedSSE)
	mux.HandleFunc("/api/hoxi/runner/event", s.handleGlobalSSE)
	mux.HandleFunc("/api/hoxi/sessions", s.handleConversationsRoot)
	mux.HandleFunc("/api/hoxi/sessions/status", s.handleConversationStatuses)
	mux.HandleFunc("/api/hoxi/runner/sessions", s.handleSessionsRoot)
	mux.HandleFunc("/api/hoxi/runner/sessions/", s.handleSessionSubroutes)
}

func (s *Service) Subscribe(sessionID string) (chan SSEEvent, func()) {
	ch := make(chan SSEEvent, 128)
	s.mu.Lock()
	s.subs[ch] = strings.TrimSpace(sessionID)
	s.mu.Unlock()
	unsub := func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
	return ch, unsub
}

func (s *Service) broadcast(sessionID string, evts ...SSEEvent) {
	for _, evt := range evts {
		s.persistEventToStore(sessionID, evt)
		s.mu.Lock()
		for ch, filterSid := range s.subs {
			if filterSid == "" || filterSid == sessionID {
				select {
				case ch <- evt:
				default:
				}
			}
		}
		s.mu.Unlock()
	}
}

func (s *Service) persistEventToStore(sessionID string, evt SSEEvent) {
	switch evt.Type {
	case "message.updated":
		if info, ok := evt.Properties["info"].(MessageInfo); ok {
			s.store.UpsertTurnInfo(sessionID, info)
		}
	case "message.part.updated":
		if part, ok := evt.Properties["part"].(MessagePart); ok {
			s.store.UpsertTurnPart(sessionID, part)
		}
	case "message.part.delta":
		msgID, _ := evt.Properties["messageID"].(string)
		partID, _ := evt.Properties["partID"].(string)
		delta, _ := evt.Properties["delta"].(string)
		if msgID != "" && partID != "" && delta != "" {
			s.store.AppendPartDelta(sessionID, msgID, partID, delta)
		}
	case "session.idle", "session.error":
		s.store.Flush()
	}
}

func (s *Service) handleAntigravityModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	bin := ResolveAgyBinary()
	models, _, _ := DiscoverAgyModelsWithRefresh(forceRefresh)
	defaultModel := ""
	if len(models) > 0 {
		defaultModel = models[0].ID
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"installed":  bin != "",
		"available":  bin != "",
		"binaryPath": bin,
		"default":    defaultModel,
		"models":     models,
	})
}

func (s *Service) handleClaudeModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	bin := ResolveClaudeBinary()
	models := DiscoverClaudeModels()
	defaultModel := ""
	if len(models) > 0 {
		defaultModel = models[0].ID
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"installed":  bin != "",
		"available":  bin != "",
		"binaryPath": bin,
		"default":    defaultModel,
		"models":     models,
	})
}

func (s *Service) handleSessionsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		dir := r.URL.Query().Get("directory")
		writeJSON(w, http.StatusOK, s.store.List(dir))
	case http.MethodPost:
		s.handleCreateSession(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) handleSessionSubroutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/hoxi/runner/sessions/")
	if sub == "status" && r.Method == http.MethodGet {
		res := make(map[string]SessionStatus)
		for _, sid := range s.busySessionIDs() {
			res[sid] = SessionStatus{Type: "busy"}
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	parts := strings.SplitN(sub, "/", 2)
	sessionID := strings.TrimSpace(parts[0])
	action := ""
	if len(parts) > 1 {
		action = strings.TrimSpace(parts[1])
	}
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session id required")
		return
	}

	switch action {
	case "":
		s.handleSingleSession(w, r, sessionID)
	case "message":
		rec, ok := s.store.Get(sessionID)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "runner session not found")
			return
		}
		turns := rec.Turns
		if turns == nil {
			turns = []MessageTurn{}
		}
		writeJSON(w, http.StatusOK, turns)
	case "prompt", "prompt_async":
		s.handleSessionPrompt(w, r, sessionID)
	case "runtime":
		s.handleRuntimeSwitch(w, r, sessionID)
	case "abort":
		s.handleSessionAbort(w, r, sessionID)
	case "event":
		s.serveSSE(w, r, sessionID)
	default:
		writeJSONError(w, http.StatusNotFound, "unknown runner endpoint")
	}
}

func (s *Service) handleSingleSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		rec, ok := s.store.Get(sessionID)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "runner session not found")
			return
		}
		writeJSON(w, http.StatusOK, rec.ToSessionInfo())

	case http.MethodPatch:
		var body struct {
			Title       string  `json:"title"`
			SandboxMode *string `json:"sandboxMode"`
			Force       bool    `json:"force"`
			KeepBranch  *bool   `json:"keepBranch"`
			Time        *struct {
				Archived *int64 `json:"archived"`
			} `json:"time"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid session update")
			return
		}
		if body.SandboxMode != nil {
			keepBranch := true
			if body.KeepBranch != nil {
				keepBranch = *body.KeepBranch
			}
			info, err := s.changeSessionSandboxWithOptions(sessionID, *body.SandboxMode, body.Force, keepBranch)
			if err != nil {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, info)
			return
		}
		updated, ok, err := s.store.Update(sessionID, func(rec *SessionRecord) {
			if strings.TrimSpace(body.Title) != "" {
				rec.Title = strings.TrimSpace(body.Title)
			}
			if body.Time != nil && body.Time.Archived != nil {
				rec.ArchivedAt = *body.Time.Archived
			}
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "runner session not found")
			return
		}
		info := updated.ToSessionInfo()
		s.broadcast(sessionID, SSEEvent{Type: "session.updated", Properties: map[string]interface{}{"info": info}})
		writeJSON(w, http.StatusOK, info)

	case http.MethodDelete:
		if err := s.deleteConversation(r.Context(), sessionID); err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		s.broadcast(sessionID, SSEEvent{Type: "session.deleted", Properties: map[string]interface{}{"sessionID": sessionID}})
		w.WriteHeader(http.StatusNoContent)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) handleSessionPrompt(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	rec, ok := s.store.Get(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "runner session not found")
		return
	}
	var body struct {
		Text    string `json:"text"`
		Agent   string `json:"agent"`
		Variant string `json:"variant"`
		System  string `json:"system"`
		Model   *struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		} `json:"model"`
		Parts []MessagePart `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	promptText := strings.TrimSpace(body.Text)
	if promptText == "" {
		var texts []string
		for _, p := range body.Parts {
			if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
				texts = append(texts, strings.TrimSpace(p.Text))
			}
		}
		promptText = strings.Join(texts, "\n")
	}
	if promptText == "" {
		writeJSONError(w, http.StatusBadRequest, "prompt text is required")
		return
	}

	if rec.Subagent {
		writeJSONError(w, http.StatusConflict, errSubagentSessionReadOnly.Error())
		return
	}
	if err := validateSessionDirectory(rec); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rec.Runtime == "opencode" {
		s.mu.Lock()
		busy := s.procs[sessionID] != nil
		s.mu.Unlock()
		if busy {
			modelID, providerID := rec.ModelID, rec.ProviderID
			if body.Model != nil {
				modelID, providerID = body.Model.ModelID, body.Model.ProviderID
			}
			err := s.steerOpenCodeTurn(r.Context(), rec, firstNonEmptyStr(body.Agent, rec.Mode, "build"), modelID, providerID, body.Variant, body.System, promptText, body.Parts)
			if err != nil {
				writeJSONError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
			return
		}
	}

	binPath := ResolveAgyBinary()
	missingMsg := "Antigravity CLI (agy) binary not found in ~/.gemini/bin/agy or PATH"
	if rec.Runtime == string(RuntimeClaudeCode) {
		binPath = ResolveClaudeBinary()
		missingMsg = "Claude Code CLI (claude) binary not found in ~/.local/bin/claude or PATH"
	}
	if binPath == "" && rec.Runtime != "opencode" {
		writeJSONError(w, http.StatusBadRequest, missingMsg)
		return
	}

	s.mu.Lock()
	if s.procs[sessionID] != nil {
		s.mu.Unlock()
		writeJSONError(w, http.StatusConflict, "session already has an active CLI process running")
		return
	}
	s.seqSeed++
	seq := s.seqSeed
	s.mu.Unlock()

	mode := firstNonEmptyStr(body.Agent, rec.Mode, "build")
	modelID := rec.ModelID
	if body.Model != nil && strings.TrimSpace(body.Model.ModelID) != "" {
		modelID = strings.TrimSpace(body.Model.ModelID)
		if body.Model.ProviderID != "" {
			rec.ProviderID = body.Model.ProviderID
		}
	}
	effort := strings.TrimSpace(body.Variant)

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
	userParts := []MessagePart{
		{
			ID:        fmt.Sprintf("prt_u_%d_%d", now, seq),
			SessionID: sessionID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      promptText,
		},
	}
	for index, part := range body.Parts {
		if part.Type == "file" {
			part.ID = fmt.Sprintf("prt_f_%d_%d_%d", now, seq, index)
			part.SessionID, part.MessageID = sessionID, userMsgID
			userParts = append(userParts, part)
		}
	}

	_, _, _ = s.store.Update(sessionID, func(r *SessionRecord) {
		r.ModelID = modelID
		r.Mode = mode
		r.ProviderID = rec.ProviderID
	})

	var err error
	if rec.Runtime == "opencode" {
		err = s.startOpenCodeTurn(rec, mode, modelID, effort, body.System, userInfo, userParts)
	} else {
		input := runtimeContext(rec)
		if body.System != "" {
			input += body.System + "\n\n"
		}
		err = s.startTurnProcess(rec, binPath, mode, modelID, effort, input+promptText, userInfo, userParts, asstMsgID)
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
