package runner

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	mu           sync.Mutex
	baseURL      string
	autoInstall  bool
	tokens       map[[32]byte]hookCredential
	pending      map[string]*pendingApproval
	byCallKey    map[string]*pendingApproval
	dedupWaiters map[string]int
	always       map[string]map[string]ApprovalGrant
	toolAlways   map[string]map[string]ApprovalGrant
	classGrants  map[string]map[string]ApprovalGrant // session -> recoverable class
	openCodeAsks map[string]openCodeAsk              // OpenCode request ID -> request shown to the user
	waitLimit    time.Duration
}

func newApprovalGate() *approvalGate {
	return &approvalGate{
		tokens:       make(map[[32]byte]hookCredential),
		pending:      make(map[string]*pendingApproval),
		byCallKey:    make(map[string]*pendingApproval),
		dedupWaiters: make(map[string]int),
		always:       make(map[string]map[string]ApprovalGrant),
		toolAlways:   make(map[string]map[string]ApprovalGrant),
		classGrants:  make(map[string]map[string]ApprovalGrant),
		openCodeAsks: make(map[string]openCodeAsk),
		waitLimit:    agyhook.ClientTimeout - 5*time.Second,
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

func (s *Service) hookBaseURL() string {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	return s.gate.baseURL
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
		if isReadToolAllowedInWorktree(rec.WorktreePath, p.ToolCall.Name, p.ToolCall.Args) &&
			!isSecretFilePath(resolveHookToolPath(rec.WorktreePath, p.ToolCall.Args)) {
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
		if pathWithin(rec.WorktreePath, path) && !isProtectedWorktreePath(rec.WorktreePath, path) &&
			s.policyApprovesWorktreeEdit(rec, path) {
			return hookDecisionApprove, ""
		}
		// Outside the worktree, protected paths, and edits the policy wants
		// confirmed all go through the same gate as every other runtime.
		approved, reason := s.gateToolCall(r, cred, p.ToolCall.Name, p.ToolCall.Args, callID)
		if approved {
			return hookDecisionApprove, ""
		}
		return hookDecisionDeny, reason
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
	if s.gate.hasAlways(cred.sessionID, ask.command) {
		return true, ""
	}
	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []string{ask.command},
		"metadata":   map[string]interface{}{"command": ask.command, "source": ask.source},
	}
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		if allow, category := unattendedDecision(item, rec.WorktreePath); !allow && category != "" {
			s.recordUnattendedDeny(cred, item, category)
			return false, unattendedDenyReason(category)
		}
		if unattendedIsolated(rec) && s.policyApprovesIsolated(item, rec.WorktreePath, true) {
			if decideCommandPermissionIn(ask.command, rec.WorktreePath) != "allow" {
				s.recordPolicyApproval(cred, item)
			}
			s.noteTurn(cred.sessionID, "allow")
			return true, ""
		}
	}
	if s.policyApproves(item, rec.WorktreePath) {
		// Read-only commands are routine; only audit what the policy let
		// through beyond them.
		if decideCommandPermissionIn(ask.command, rec.WorktreePath) != "allow" {
			s.recordPolicyApproval(cred, item)
		}
		return true, ""
	}
	if s.classGrantApproves(item, cred.sessionID, rec.WorktreePath) {
		item["id"] = runnerPermissionID + randomHex(12)
		s.recordRunnerApprovalAudit(cred, item, "once", approvalReply{Reply: "once", Source: grantSourceSession})
		return true, ""
	}
	if ok, reason := s.aiReviewCommand(r.Context(), item, rec.WorktreePath); ok {
		s.recordAIReviewApproval(cred, item, reason)
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
	annotateGrantClasses(req, s.sessionWorktree(cred.sessionID))
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

// approvalDedupWaiters reports how many hook calls are currently coalesced onto
// an already-pending approval. Tests use it to wait for a duplicate to join
// before answering, instead of racing the answer against the duplicate's
// arrival (which would make the duplicate a fresh request).
func (s *Service) approvalDedupWaiters() int {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	total := 0
	for _, n := range s.gate.dedupWaiters {
		total += n
	}
	return total
}

func pendingCallKey(sessionID string, req map[string]interface{}) string {	toolMap, _ := req["tool"].(map[string]interface{})
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
			// Track deduplicated waiters so callers (tests, diagnostics) can
			// tell a duplicate has actually joined before the first answer
			// lands. A duplicate that arrives after the answer is a genuine
			// new request, so this is the only observable "coalesced" signal.
			s.gate.dedupWaiters[callKey]++
			s.gate.mu.Unlock()
			defer func() {
				s.gate.mu.Lock()
				if remaining := s.gate.dedupWaiters[callKey] - 1; remaining > 0 {
					s.gate.dedupWaiters[callKey] = remaining
				} else {
					delete(s.gate.dedupWaiters, callKey)
				}
				s.gate.mu.Unlock()
			}()
			select {
			case <-existing.done:
				return existing.result
			case <-r.Context().Done():
				return approvalReply{Reply: "reject", Message: "approval hook was interrupted", Source: "system"}
			}
		}
	}
	limit := s.approvalWaitLimit()
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

	policy := s.ApprovalPolicy()
	timeoutText := approvalTimeoutReason(policy)
	if policy.Mode == ApprovalModeUnattended && isQuestionRequest(req) {
		timeoutText = unattendedQuestionTimeoutReason()
	}
	answer := approvalReply{Reply: "reject", Message: timeoutText, Source: approvalSourceTimeout}
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
		s.rememberDeferred(cred.sessionID, id, "timeout", permissionSummaryText(req))
		s.noteTurn(cred.sessionID, "timeout")
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
	if trimmed == "" || isDestructiveCommand(trimmed) || isOpaqueShellCommand(trimmed, "") {
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
	if trimmed == "" || isDestructiveCommand(trimmed) || isOpaqueShellCommand(trimmed, "") {
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
	delete(g.classGrants, sessionID)
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
