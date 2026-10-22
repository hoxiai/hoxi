package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agyhook"
)

// Headless runner CLIs cannot prompt, so their PreToolUse hooks call back
// here and block until the user decides. agy runs `hoxi agy-hook`, where only
// `auto_approve` grants permission (docs/antigravity-approval-hook.md);
// Claude Code posts directly through an HTTP hook (claude_hook_gate.go).

const (
	hookPreToolPath     = "/api/hoxi/runner/hook/pretool"
	runnerPermissionID  = "per_rnr_"
	hookDecisionApprove = "auto_approve"
	hookDecisionDeny    = "deny"

	hookToolRunCommand = "run_command"
)

type hookCredential struct {
	runtime   string
	sessionID string
	messageID string
}

// approvalAsk is one shell command a runner CLI wants to run, normalized
// from the runtime-specific hook payload.
type approvalAsk struct {
	command     string
	cwd         string
	description string
	callID      string
	source      string
}

type approvalReply struct {
	Reply   string
	Message string
	Source  string
}

type pendingApproval struct {
	req     map[string]interface{}
	sid     string
	callKey string
	reply   chan approvalReply
	done    chan struct{}
	result  approvalReply
}

type approvalGate struct {
	mu          sync.Mutex
	baseURL     string
	autoInstall bool
	tokens      map[[32]byte]hookCredential
	pending     map[string]*pendingApproval
	byCallKey   map[string]*pendingApproval
	always      map[string]map[string]ApprovalGrant
	toolAlways  map[string]map[string]ApprovalGrant
	waitLimit   time.Duration
}

func newApprovalGate() *approvalGate {
	return &approvalGate{
		tokens:     make(map[[32]byte]hookCredential),
		pending:    make(map[string]*pendingApproval),
		byCallKey:  make(map[string]*pendingApproval),
		always:     make(map[string]map[string]ApprovalGrant),
		toolAlways: make(map[string]map[string]ApprovalGrant),
		waitLimit:  agyhook.ClientTimeout - 5*time.Second,
	}
}

type hookPayload struct {
	ConversationID string   `json:"conversationId"`
	StepIdx        int      `json:"stepIdx"`
	WorkspacePaths []string `json:"workspacePaths"`
	ToolCall       struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"toolCall"`
}

// SetHookBaseURL records the loopback HTTP origin agy hooks call back to.
func (s *Service) SetHookBaseURL(base string) {
	s.gate.mu.Lock()
	s.gate.baseURL = strings.TrimRight(strings.TrimSpace(base), "/")
	s.gate.mu.Unlock()
}

// issueHookCredential mints a per-process credential for one runner turn.
// With no base URL the CLI falls back to its own policy, matching pre-gate
// behavior.
func (s *Service) issueHookCredential(runtime, sessionID, messageID string) (string, string, func()) {
	g := s.gate
	g.mu.Lock()
	base := g.baseURL
	g.mu.Unlock()
	token := randomHex(32)
	if base == "" || token == "" {
		return "", "", func() {}
	}
	key := sha256.Sum256([]byte(token))
	g.mu.Lock()
	g.tokens[key] = hookCredential{runtime: runtime, sessionID: sessionID, messageID: messageID}
	g.mu.Unlock()
	return base, token, func() {
		g.mu.Lock()
		delete(g.tokens, key)
		g.mu.Unlock()
	}
}

// issueHookEnv returns the environment `hoxi agy-hook` reads for one agy turn.
func (s *Service) issueHookEnv(sessionID, messageID string) ([]string, func()) {
	base, token, revoke := s.issueHookCredential(string(RuntimeAntigravity), sessionID, messageID)
	if base == "" {
		return nil, revoke
	}
	return []string{
		agyhook.EnvSessionID + "=" + sessionID,
		agyhook.EnvHookURL + "=" + base + hookPreToolPath,
		agyhook.EnvHookToken + "=" + token,
	}, revoke
}

func (g *approvalGate) lookup(token string) (hookCredential, bool) {
	if token == "" {
		return hookCredential{}, false
	}
	key := sha256.Sum256([]byte(token))
	g.mu.Lock()
	defer g.mu.Unlock()
	cred, ok := g.tokens[key]
	return cred, ok
}

// cancelSessionApprovals rejects every pending request of a stopped session.
func (s *Service) cancelSessionApprovals(sessionID, reason string) {
	s.gate.mu.Lock()
	var targets []*pendingApproval
	for _, p := range s.gate.pending {
		if p.sid == sessionID {
			targets = append(targets, p)
		}
	}
	s.gate.mu.Unlock()
	for _, p := range targets {
		select {
		case p.reply <- approvalReply{Reply: "reject", Message: reason, Source: "system"}:
		default:
		}
	}
}

// authorizeHook checks a hook callback's loopback origin and credential.
func (s *Service) authorizeHook(w http.ResponseWriter, r *http.Request, runtime string) (hookCredential, bool) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return hookCredential{}, false
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeJSONError(w, http.StatusForbidden, "loopback only")
		return hookCredential{}, false
	}
	cred, ok := s.gate.lookup(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !ok || cred.runtime != runtime || cred.sessionID != strings.TrimSpace(r.Header.Get(agyhook.HeaderSession)) {
		writeJSONError(w, http.StatusUnauthorized, "invalid hook credential")
		return hookCredential{}, false
	}
	return cred, true
}

func (s *Service) handleHookPreTool(w http.ResponseWriter, r *http.Request) {
	cred, ok := s.authorizeHook(w, r, string(RuntimeAntigravity))
	if !ok {
		return
	}
	var payload hookPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&payload); err != nil {
		writeHookDecision(w, hookDecisionDeny, "Hoxi could not parse the tool call")
		return
	}
	decision, reason := s.decideHookCall(r, cred, payload)
	writeHookDecision(w, decision, reason)
}

func (s *Service) decideHookCall(r *http.Request, cred hookCredential, p hookPayload) (string, string) {
	rec, ok := s.store.Get(cred.sessionID)
	if !ok {
		return hookDecisionDeny, "Hoxi session no longer exists"
	}
	if p.ConversationID != "" && rec.ExternalID != "" && p.ConversationID != rec.ExternalID {
		return hookDecisionDeny, "Antigravity conversation does not belong to this Hoxi session"
	}
	args, _ := json.Marshal(p.ToolCall.Args)
	callID := fmt.Sprintf("call_%s_%d", cred.messageID, p.StepIdx)
	action := hookToolAction(p.ToolCall.Name)
	if _, _, destructive, _ := s.inspectHookToolRisk(cred.sessionID, p.ToolCall.Name, p.ToolCall.Args); !destructive &&
		action != hookActionCommand &&
		s.gate.hasToolAlways(cred.sessionID, hookToolGrantKey(p.ToolCall.Name, p.ToolCall.Args)) {
		return hookDecisionApprove, ""
	}
	if ok, reason := s.gateDoomLoop(r, cred, p.ToolCall.Name, args, callID); !ok {
		return hookDecisionDeny, reason
	}

	switch action {
	case hookActionRead:
		if isReadToolAllowedInWorktree(rec.WorktreePath, p.ToolCall.Name, p.ToolCall.Args) {
			return hookDecisionApprove, ""
		}
		approved, reason := s.gateToolCall(r, cred, p.ToolCall.Name, p.ToolCall.Args, callID)
		if approved {
			return hookDecisionApprove, ""
		}
		return hookDecisionDeny, reason
	case hookActionEdit:
		path := resolveHookToolPath(rec.WorktreePath, p.ToolCall.Args)
		if path == "" {
			approved, reason := s.gateToolCall(r, cred, p.ToolCall.Name, p.ToolCall.Args, callID)
			if approved {
				return hookDecisionApprove, ""
			}
			return hookDecisionDeny, reason
		}
		if !pathWithin(rec.WorktreePath, path) {
			return hookDecisionDeny, fmt.Sprintf("Hoxi blocked %s outside this session's directory: %s", p.ToolCall.Name, path)
		}
		if isProtectedWorktreePath(rec.WorktreePath, path) {
			approved, reason := s.gateToolCall(r, cred, p.ToolCall.Name, p.ToolCall.Args, callID)
			if approved {
				return hookDecisionApprove, ""
			}
			return hookDecisionDeny, reason
		}
		return hookDecisionApprove, ""
	case hookActionAsk:
		approved, reason := s.gateToolCall(r, cred, p.ToolCall.Name, p.ToolCall.Args, callID)
		if approved {
			return hookDecisionApprove, ""
		}
		return hookDecisionDeny, reason
	case hookActionCommand:
		command, _ := p.ToolCall.Args["CommandLine"].(string)
		cwd, _ := p.ToolCall.Args["Cwd"].(string)
		summary, _ := p.ToolCall.Args["toolSummary"].(string)
		approved, reason := s.gateCommand(r, cred, rec, approvalAsk{
			command:     command,
			cwd:         cwd,
			description: summary,
			callID:      callID,
			source:      string(RuntimeAntigravity),
		})
		if approved {
			return hookDecisionApprove, ""
		}
		return hookDecisionDeny, reason
	case hookActionPassthrough:
		return "allow", ""
	}
	// Unknown tools are gated instead of passed through: agy's headless mode
	// soft-denies any tool that needs permission, which ends the turn without
	// giving the user a chance to approve it.
	approved, reason := s.gateToolCall(r, cred, p.ToolCall.Name, p.ToolCall.Args, callID)
	if approved {
		return hookDecisionApprove, ""
	}
	return hookDecisionDeny, reason
}

// gateCommand applies the shared command policy and, when needed, blocks on
// the user's decision. It reports whether the command may run.
func (s *Service) gateCommand(r *http.Request, cred hookCredential, rec SessionRecord, ask approvalAsk) (bool, string) {
	if strings.TrimSpace(ask.command) == "" {
		return false, "empty command"
	}
	if ask.cwd != "" && !pathWithin(rec.WorktreePath, ask.cwd) {
		return false, "command working directory is outside this session's directory"
	}
	if decideCommandPermissionIn(ask.command, rec.WorktreePath) == "allow" || s.gate.hasAlways(cred.sessionID, ask.command) {
		return true, ""
	}
	return s.awaitApproval(r, cred, ask)
}

func (s *Service) awaitApproval(r *http.Request, cred hookCredential, ask approvalAsk) (bool, string) {
	id := runnerPermissionID + randomHex(12)
	command := ask.command
	meta := map[string]interface{}{"command": command, "source": ask.source}
	if ask.cwd != "" {
		meta["cwd"] = ask.cwd
	}
	if desc := strings.TrimSpace(ask.description); desc != "" {
		meta["description"] = desc
	}
	req := map[string]interface{}{
		"id":         id,
		"sessionID":  cred.sessionID,
		"permission": "bash",
		"patterns":   []string{command},
		"metadata":   meta,
		"always":     []string{command},
		"tool": map[string]interface{}{
			"messageID": cred.messageID,
			"callID":    ask.callID,
		},
	}
	answer := s.awaitReply(r, cred, id, req)
	switch answer.Reply {
	case "always":
		s.gate.addAlways(cred.sessionID, command, answer.Source)
		return true, ""
	case "once":
		return true, ""
	}
	reason := "The user rejected this command in Hoxi"
	if msg := strings.TrimSpace(answer.Message); msg != "" {
		reason += ": " + msg
	}
	return false, reason
}

func pendingCallKey(sessionID string, req map[string]interface{}) string {
	toolMap, _ := req["tool"].(map[string]interface{})
	callID, _ := toolMap["callID"].(string)
	callID = strings.TrimSpace(callID)
	if sessionID == "" || callID == "" {
		return ""
	}
	perm, _ := req["permission"].(string)
	return sessionID + "\x00" + perm + "\x00" + callID
}

// awaitReply publishes a permission request and blocks until the user
// answers, the hook call is interrupted, or the wait limit passes.
func (s *Service) awaitReply(r *http.Request, cred hookCredential, id string, req map[string]interface{}) approvalReply {
	callKey := pendingCallKey(cred.sessionID, req)
	s.gate.mu.Lock()
	if callKey != "" {
		if existing := s.gate.byCallKey[callKey]; existing != nil {
			s.gate.mu.Unlock()
			select {
			case <-existing.done:
				return existing.result
			case <-r.Context().Done():
				return approvalReply{Reply: "reject", Message: "approval hook was interrupted", Source: "system"}
			}
		}
	}
	limit := s.gate.waitLimit
	nowMs := time.Now().UnixMilli()
	req["createdAt"] = nowMs
	if limit > 0 {
		req["expiresAt"] = nowMs + limit.Milliseconds()
	}
	pa := &pendingApproval{
		req:     req,
		sid:     cred.sessionID,
		callKey: callKey,
		reply:   make(chan approvalReply, 1),
		done:    make(chan struct{}),
	}
	s.gate.pending[id] = pa
	if callKey != "" {
		s.gate.byCallKey[callKey] = pa
	}
	s.gate.mu.Unlock()
	if s.status != nil {
		s.status.SetPendingApproval(id, cred.sessionID, true)
	}
	s.broadcast(cred.sessionID, SSEEvent{Type: "permission.asked", Properties: req})

	answer := approvalReply{Reply: "reject", Message: "approval timed out", Source: "system"}
	decision := "timeout"
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case answer = <-pa.reply:
		decision = answer.Reply
		if answer.Source == "system" && answer.Reply == "reject" && answer.Message != "approval timed out" {
			decision = "interrupted"
		}
	case <-r.Context().Done():
		answer = approvalReply{Reply: "reject", Message: "approval hook was interrupted", Source: "system"}
		decision = "interrupted"
	case <-timer.C:
	}

	s.gate.mu.Lock()
	pa.result = answer
	close(pa.done)
	delete(s.gate.pending, id)
	if callKey != "" && s.gate.byCallKey[callKey] == pa {
		delete(s.gate.byCallKey, callKey)
	}
	s.gate.mu.Unlock()
	if s.status != nil {
		s.status.SetPendingApproval(id, cred.sessionID, false)
	}
	s.recordRunnerApprovalAudit(cred, req, decision, answer)
	s.broadcast(cred.sessionID, SSEEvent{Type: "permission.replied", Properties: map[string]interface{}{
		"sessionID": cred.sessionID,
		"requestID": id,
		"reply":     answer.Reply,
		"source":    firstNonEmptyStr(answer.Source, "desktop"),
		"reason":    answer.Message,
	}})
	return answer
}

// Session "always" grants live in Hoxi only, never in agy's global settings,
// and never cover destructive commands (AGENTS.md rule 6).
func (g *approvalGate) addAlways(sessionID, command, source string) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || isDestructiveCommand(trimmed) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.always[sessionID] == nil {
		g.always[sessionID] = make(map[string]ApprovalGrant)
	}
	g.always[sessionID][trimmed] = ApprovalGrant{
		ID:        makeGrantID(sessionID, "bash", trimmed),
		SessionID: sessionID,
		Kind:      "bash",
		Tool:      "bash",
		Key:       trimmed,
		Pattern:   redactApprovalText(trimmed, 240),
		Scope:     "session_memory",
		Source:    firstNonEmptyStr(source, "desktop"),
		CreatedAt: time.Now().UnixMilli(),
	}
}

func (g *approvalGate) hasAlways(sessionID, command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || isDestructiveCommand(trimmed) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.always[sessionID][trimmed]
	return ok
}

// Tool "always" grants are keyed by tool + exact args, so allowing one MCP
// call never blanket-approves a different one.
func (g *approvalGate) addToolAlways(sessionID, key, tool, pattern, source string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.toolAlways[sessionID] == nil {
		g.toolAlways[sessionID] = make(map[string]ApprovalGrant)
	}
	g.toolAlways[sessionID][key] = ApprovalGrant{
		ID:        makeGrantID(sessionID, "tool", key),
		SessionID: sessionID,
		Kind:      "tool",
		Tool:      firstNonEmptyStr(strings.TrimSpace(tool), "tool"),
		Key:       key,
		Pattern:   firstNonEmptyStr(strings.TrimSpace(pattern), tool, key),
		Scope:     "session_memory",
		Source:    firstNonEmptyStr(source, "desktop"),
		CreatedAt: time.Now().UnixMilli(),
	}
}

func (g *approvalGate) hasToolAlways(sessionID, key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.toolAlways[sessionID][key]
	return ok
}

func (g *approvalGate) forgetSession(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.always, sessionID)
	delete(g.toolAlways, sessionID)
}

func (s *Service) handlePermissionRoutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/runner/permission"), "/")
	switch sub {
	case "":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listPendingApprovals(r.URL.Query().Get("directory")))
		return
	case "audit":
		s.handleApprovalAudit(w, r)
		return
	case "grants":
		s.handleApprovalGrants(w, r)
		return
	}
	id, action, _ := strings.Cut(sub, "/")
	if action != "reply" || r.Method != http.MethodPost {
		writeJSONError(w, http.StatusNotFound, "unknown permission endpoint")
		return
	}
	s.replyRunnerPermission(w, r, id)
}

func (s *Service) handleUnifiedPermissionRoutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/permission"), "/")
	switch sub {
	case "":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listUnifiedPermissions(r.Context(), r.URL.Query().Get("directory")))
		return
	case "audit":
		s.handleApprovalAudit(w, r)
		return
	case "grants":
		s.handleApprovalGrants(w, r)
		return
	}
	id, action, _ := strings.Cut(sub, "/")
	if action != "reply" || r.Method != http.MethodPost {
		writeJSONError(w, http.StatusNotFound, "unknown permission endpoint")
		return
	}
	if strings.HasPrefix(id, runnerPermissionID) {
		s.replyRunnerPermission(w, r, id)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid reply")
		return
	}
	var parsedReply struct {
		Reply   string `json:"reply"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsedReply); err != nil ||
		(parsedReply.Reply != "once" && parsedReply.Reply != "always" && parsedReply.Reply != "reject") {
		writeJSONError(w, http.StatusBadRequest, "reply must be once, always or reject")
		return
	}
	source := requestApprovalSource(r)
	if source == "tunnel" && parsedReply.Reply == "always" {
		writeJSONError(w, http.StatusForbidden, "always approval is not allowed over the remote tunnel")
		return
	}
	dir := r.URL.Query().Get("directory")
	status, respRaw, err := s.replyOpenCodePermission(r.Context(), id, dir, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
		return
	}
	if status >= 200 && status < 300 {
		s.recordOpenCodeApprovalAudit(id, dir, parsedReply.Reply, parsedReply.Message, source)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(respRaw)
}

func (s *Service) replyOpenCodePermission(ctx context.Context, id, directory string, raw []byte) (int, []byte, error) {
	path := "/permission/" + url.PathEscape(id) + "/reply"
	primary := strings.TrimSpace(directory)
	rememberedDir := ""
	if s.audit != nil {
		if snap, ok := s.audit.lookupOpenCodePerm(id); ok {
			rememberedDir = strings.TrimSpace(snap.Directory)
		}
	}
	if primary == "" {
		primary = rememberedDir
	}
	status, respRaw, err := s.openCodeRaw(ctx, http.MethodPost, path, directoryQuery(primary, nil), raw)
	if err != nil || status != http.StatusNotFound {
		return status, respRaw, err
	}
	candidates := s.openCodeCandidateDirs("")
	if rememberedDir != "" {
		candidates = append([]string{rememberedDir}, candidates...)
	}
	seen := map[string]bool{normalizeComparePath(primary): true}
	for _, candidate := range candidates {
		key := normalizeComparePath(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		if st, body, retryErr := s.openCodeRaw(ctx, http.MethodPost, path, directoryQuery(candidate, nil), raw); retryErr == nil && st != http.StatusNotFound {
			return st, body, nil
		}
	}
	return status, respRaw, nil
}

func (s *Service) openCodeCandidateDirs(directory string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	add := func(d string) {
		trimmed := strings.TrimSpace(d)
		key := normalizeComparePath(trimmed)
		if !seen[key] {
			seen[key] = true
			out = append(out, trimmed)
		}
	}
	normFilter := normalizeComparePath(directory)
	add(directory)
	addRec := func(rec SessionRecord, ok bool) {
		if !ok || rec.Runtime != "opencode" {
			return
		}
		if normFilter != "" && normalizeComparePath(rec.ProjectDir) != normFilter && normalizeComparePath(rec.WorktreePath) != normFilter {
			return
		}
		if rec.WorktreePath != "" {
			add(rec.WorktreePath)
		}
		if rec.ProjectDir != "" {
			add(rec.ProjectDir)
		}
	}
	for _, sid := range s.busySessionIDs() {
		addRec(s.store.Meta(sid))
	}
	if normFilter == "" {
		for _, info := range s.store.List("") {
			addRec(s.store.Meta(info.ID))
		}
	}
	return out
}

func (s *Service) replyRunnerPermission(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Reply   string `json:"reply"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid reply")
		return
	}
	if body.Reply != "once" && body.Reply != "always" && body.Reply != "reject" {
		writeJSONError(w, http.StatusBadRequest, "reply must be once, always or reject")
		return
	}
	source := requestApprovalSource(r)
	if source == "tunnel" && body.Reply == "always" {
		writeJSONError(w, http.StatusForbidden, "always approval is not allowed over the remote tunnel")
		return
	}
	s.gate.mu.Lock()
	pa := s.gate.pending[id]
	s.gate.mu.Unlock()
	if pa == nil {
		writeJSONError(w, http.StatusNotFound, "permission request not found")
		return
	}
	select {
	case pa.reply <- approvalReply{Reply: body.Reply, Message: body.Message, Source: source}:
		writeJSON(w, http.StatusOK, true)
	default:
		writeJSONError(w, http.StatusConflict, "permission request already answered")
	}
}

func (s *Service) listUnifiedPermissions(ctx context.Context, directory string) []map[string]interface{} {
	engineToHoxi := make(map[string]string)
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
		}
		if rec.ExternalID != "" {
			engineToHoxi[rec.ExternalID] = rec.ID
		}
		for _, ocID := range rec.OpenCodeIDs {
			if ocID != "" {
				engineToHoxi[ocID] = rec.ID
			}
		}
	}
	s.mu.Lock()
	for hoxiID, ap := range s.procs {
		if ap == nil {
			continue
		}
		ap.openCodeChildren.Range(func(key, _ interface{}) bool {
			if childID, ok := key.(string); ok && childID != "" {
				engineToHoxi[childID] = hoxiID
			}
			return true
		})
	}
	s.mu.Unlock()
	out := make([]map[string]interface{}, 0)
	seenIDs := make(map[string]bool)
	for _, dir := range s.openCodeCandidateDirs(directory) {
		status, raw, err := s.openCodeRaw(ctx, http.MethodGet, "/permission", directoryQuery(dir, nil), nil)
		var enginePerms []map[string]interface{}
		if err == nil && status == http.StatusOK && json.Unmarshal(raw, &enginePerms) == nil {
			for _, item := range enginePerms {
				pid, _ := item["id"].(string)
				if pid != "" {
					if seenIDs[pid] {
						continue
					}
					seenIDs[pid] = true
				}
				if sid, ok := item["sessionID"].(string); ok {
					if hoxiID := engineToHoxi[sid]; hoxiID != "" {
						item["sessionID"] = hoxiID
					}
				}
				if dir != "" {
					meta, _ := item["metadata"].(map[string]interface{})
					if meta == nil {
						meta = make(map[string]interface{})
						item["metadata"] = meta
					}
					if firstString(meta["directory"], meta["cwd"]) == "" {
						meta["directory"] = dir
					}
				}
				if s.audit != nil {
					s.audit.rememberOpenCodePerm(item, dir)
				}
				out = append(out, item)
			}
		}
	}
	out = append(out, s.listPendingApprovals(directory)...)
	return out
}

func (s *Service) listPendingApprovals(directory string) []map[string]interface{} {
	dir := normalizeComparePath(directory)
	s.gate.mu.Lock()
	all := make([]*pendingApproval, 0, len(s.gate.pending))
	for _, p := range s.gate.pending {
		all = append(all, p)
	}
	s.gate.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(all))
	for _, p := range all {
		if dir != "" {
			rec, ok := s.store.Get(p.sid)
			if !ok || (normalizeComparePath(rec.ProjectDir) != dir && normalizeComparePath(rec.WorktreePath) != dir) {
				continue
			}
		}
		out = append(out, p.req)
	}
	return out
}

func writeHookDecision(w http.ResponseWriter, decision, reason string) {
	writeJSON(w, http.StatusOK, agyhook.Decision{Decision: decision, Reason: reason})
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
