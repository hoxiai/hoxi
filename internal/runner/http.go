package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type Service struct {
	rootDir     string
	store       *Store
	attachments *attachment.Store
	worktrees   *worktree.Manager
	status      *statusbar.Tracker
	gate        *approvalGate
	audit       *approvalAuditStore
	deferred    *deferredStore
	policy      *approvalPolicyStore
	aiReviews   *aiReviewCache
	ownedProcs  *ownedProcessLedger
	doomLoop    *doomLoopGuard
	stall       stallPolicy

	mu              sync.Mutex
	llm             llm.Client
	turnMu          sessionTurnLocks
	procs           map[string]*activeProcess
	childBusy       map[string]bool
	subs            map[chan SSEEvent]string
	seqSeed         int64
	openCode        *ocapi.Client
	managedOpenCode sync.Map
	// childTurns caches OpenCode subagent transcripts read while filtering diffs.
	childTurns      sync.Map // session ID -> childTurnsEntry
	editedFiles     sync.Map // session ID -> editedFilesEntry
	userSkillByMsg  sync.Map // message ID -> userSkillOverride
	stoppedTools    sync.Map // sessionID\x00(part|call):id -> bool
	skillResolver   func(ctx context.Context, directory, sessionID string, sel SkillSelection) (ResolvedSkill, error)
	projectResolver ProjectResolver
	// permissionRules returns OpenCode's `permission` block; onPolicySaved
	// keeps OpenCode asking Hoxi after the approval policy changes.
	permissionRules func() map[string]interface{}
	onPolicySaved   func()
	// onSummary delivers the end-of-turn count line. Counts only, no command text.
	onSummary func(sessionID, body string)
	// openCodeAutoReplies tracks OpenCode requests the policy is answering.
	openCodeAutoReplies sync.Map // request ID -> openCodeAutoReply* state or openCodeSettledMark
	turnStats           sync.Map // session ID -> *turnCounts
	promptReceipts      sync.Map // sessionID\x00clientRequestID -> cachedPromptReceipt
	permissionReplies   sync.Map // permissionID\x00clientRequestID -> cachedPermissionReply
	cliSystemDelivered  sync.Map // sessionID\x00runtime -> last delivered caller system prompt

	// openCodeTimeoutRetries overrides openCodeTimeoutRetryDelays in tests.
	openCodeTimeoutRetries []time.Duration
}

// SetSummaryNotifier receives the end-of-turn count line. The body contains
// counts only, never a command, path or session title.
func (s *Service) SetSummaryNotifier(fn func(sessionID, body string)) {
	if s != nil {
		s.onSummary = fn
	}
}

// SetLLM wires the internal LLM client used by opt-in approval AI review.
func (s *Service) SetLLM(client llm.Client) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.llm = client
	s.mu.Unlock()
}

func NewService(rootDir string, worktrees *worktree.Manager, status *statusbar.Tracker, customStorePath string) *Service {
	if worktrees == nil {
		worktrees = worktree.NewManager(rootDir)
	}
	attDir := ""
	if strings.TrimSpace(customStorePath) != "" {
		attDir = filepath.Join(filepath.Dir(customStorePath), "attachments")
	}
	svc := &Service{
		rootDir:     rootDir,
		store:       NewStore(customStorePath),
		attachments: attachment.NewStore(attDir),
		worktrees:   worktrees,
		status:      status,
		gate:        newApprovalGate(),
		audit:       newApprovalAuditStore(customStorePath),
		deferred:    newDeferredStore(customStorePath),
		policy:      newApprovalPolicyStore(customStorePath),
		aiReviews:   newAIReviewCache(),
		ownedProcs:  newOwnedProcessLedger(),
		doomLoop:    newDoomLoopGuard(),
		stall:       stallPolicyFromEnv(),
		procs:       make(map[string]*activeProcess),
		childBusy:   make(map[string]bool),
		subs:        make(map[chan SSEEvent]string),
	}
	for _, info := range svc.store.List("") {
		rec, _ := svc.store.Meta(info.ID)
		if rec.Subagent || validateSessionDirectory(rec) != nil {
			continue
		}
		if info.SandboxMode == "local" {
			if _, err := existingDirectory(rec.ProjectDir); err == nil {
				_ = svc.worktrees.ForDirectory(rec.ProjectDir).UseLocalDirectory(info.ID)
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
	if s.attachments != nil {
		s.attachments.RegisterRoutes(mux)
	}
	mux.HandleFunc("/api/hoxi/runner/antigravity/models", s.handleAntigravityModels)
	mux.HandleFunc("/api/hoxi/runner/claude-code/models", s.handleClaudeModels)
	mux.HandleFunc("/api/hoxi/runner/gemini-cli/models", s.handleGeminiModels)
	mux.HandleFunc("/api/hoxi/runner/codebuddy/models", s.handleCodeBuddyModels)
	mux.HandleFunc("/api/hoxi/runner/qoder/models", s.handleQoderModels)
	mux.HandleFunc("/api/hoxi/runner/antigravity/hook", s.handleAgyHookInstall)
	mux.HandleFunc(hookPreToolPath, s.handleHookPreTool)
	mux.HandleFunc(claudeHookPreToolPath, s.handleClaudeHookPreTool)
	mux.HandleFunc(geminiHookPreToolPath, s.handleGeminiHookPreTool)
	mux.HandleFunc("/api/hoxi/permission", s.handleUnifiedPermissionRoutes)
	mux.HandleFunc("/api/hoxi/permission/", s.handleUnifiedPermissionRoutes)
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
	ch := make(chan SSEEvent, 512)
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
		s.decorateStoppedToolEvent(sessionID, &evt)
		s.noteRuntimeShellEvent(sessionID, evt)
		if evt.Type == "session.error" {
			if entry := sessionErrorDiagnostic(sessionID, evt.Properties["error"]); entry != nil {
				if rec, ok := s.store.Get(sessionID); ok {
					entry.ProviderID, entry.ModelID = rec.ProviderID, rec.ModelID
				}
				diagnostics.Record(*entry)
			}
		}
		s.persistEventToStore(sessionID, evt)
		s.mu.Lock()
		for ch, filterSid := range s.subs {
			if filterSid == "" || filterSid == sessionID {
				enqueueStreamEvent(ch, evt)
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
	case "session.status":
		if status, ok := evt.Properties["status"].(SessionStatus); ok {
			s.store.SetActive(sessionID, status.Type == "busy" || status.Type == "retry")
		}
	case "session.idle", "session.error":
		s.store.FlushSession(sessionID)
		s.store.SetActive(sessionID, false)
		s.finishTurnSummary(sessionID, evt.Type)
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

func (s *Service) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	bin := ResolveGeminiBinary()
	models := DiscoverGeminiModelsWithRefresh(forceRefresh)
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

func (s *Service) handleCodeBuddyModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	bin := agentcli.ResolveCodeBuddyBinary()
	models := agentcli.DiscoverCodeBuddyModelsWithRefresh(forceRefresh)
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

func (s *Service) handleQoderModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	bin := agentcli.ResolveQoderBinary()
	models := agentcli.DiscoverQoderModelsWithRefresh(forceRefresh)
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
		raw, ok := s.store.MarshalTurns(sessionID)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "runner session not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(SanitizeOpenCodeToolJSON(raw))
	case "prompt", "prompt_async":
		s.handleSessionPrompt(w, r, sessionID)
	case "runtime":
		s.handleRuntimeSwitch(w, r, sessionID)
	case "abort":
		s.handleSessionAbort(w, r, sessionID)
	case "stop-tool":
		s.handleSessionStopTool(w, r, sessionID)
	case "summarize":
		s.handleSessionSummarize(w, r, sessionID)
	case "revert":
		s.handleSessionRevert(w, r, sessionID)
	case "unrevert":
		s.handleSessionUnrevert(w, r, sessionID)
	case "fork":
		s.handleSessionFork(w, r, sessionID)
	case "diff":
		s.handleSessionDiff(w, r, sessionID)
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
			Title          string  `json:"title"`
			SandboxMode    *string `json:"sandboxMode"`
			CommandSandbox *bool   `json:"commandSandbox"`
			Force          bool    `json:"force"`
			KeepBranch     *bool   `json:"keepBranch"`
			Time           *struct {
				Archived *int64 `json:"archived"`
			} `json:"time"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
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
		if body.CommandSandbox != nil {
			info, err := s.setCommandSandbox(sessionID, *body.CommandSandbox)
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
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body PromptRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.ClientRequestID) == "" {
		body.ClientRequestID = strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	}
	receiptStatus, receipt, err := s.startPromptWithReceipt(r.Context(), sessionID, r.URL.Query().Get("directory"), body)
	if err != nil {
		writeJSONError(w, receiptStatus, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}
