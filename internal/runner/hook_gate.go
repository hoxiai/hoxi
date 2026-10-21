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
	"path/filepath"
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
}

type pendingApproval struct {
	req   map[string]interface{}
	sid   string
	reply chan approvalReply
}

type approvalGate struct {
	mu          sync.Mutex
	baseURL     string
	autoInstall bool
	tokens      map[[32]byte]hookCredential
	pending     map[string]*pendingApproval
	always      map[string]map[string]bool
	toolAlways  map[string]map[string]bool
	waitLimit   time.Duration
}

func newApprovalGate() *approvalGate {
	return &approvalGate{
		tokens:     make(map[[32]byte]hookCredential),
		pending:    make(map[string]*pendingApproval),
		always:     make(map[string]map[string]bool),
		toolAlways: make(map[string]map[string]bool),
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
		case p.reply <- approvalReply{Reply: "reject", Message: reason}:
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
	if action == hookActionAsk && s.gate.hasToolAlways(cred.sessionID, hookToolGrantKey(p.ToolCall.Name, p.ToolCall.Args)) {
		return hookDecisionApprove, ""
	}
	if ok, reason := s.gateDoomLoop(r, cred, p.ToolCall.Name, args, callID); !ok {
		return hookDecisionDeny, reason
	}

	switch action {
	case hookActionRead:
		// Reads are non-destructive and follow the same policy as safe shell
		// commands (`cat`), so Hoxi grants them instead of letting agy's
		// headless mode soft-deny the call. The sandbox still blocks writes
		// outside the worktree.
		return hookDecisionApprove, ""
	case hookActionEdit:
		path := resolveHookToolPath(rec.WorktreePath, p.ToolCall.Args)
		if path == "" {
			return "allow", ""
		}
		if pathWithin(rec.WorktreePath, path) {
			return hookDecisionApprove, ""
		}
		return hookDecisionDeny, fmt.Sprintf("Hoxi blocked %s outside this session's directory: %s", p.ToolCall.Name, path)
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
	}
	return "allow", ""
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
	if decideCommandPermission(ask.command) == "allow" || s.gate.hasAlways(cred.sessionID, ask.command) {
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
		s.gate.addAlways(cred.sessionID, command)
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

// awaitReply publishes a permission request and blocks until the user
// answers, the hook call is interrupted, or the wait limit passes.
func (s *Service) awaitReply(r *http.Request, cred hookCredential, id string, req map[string]interface{}) approvalReply {
	pa := &pendingApproval{req: req, sid: cred.sessionID, reply: make(chan approvalReply, 1)}
	s.gate.mu.Lock()
	s.gate.pending[id] = pa
	limit := s.gate.waitLimit
	s.gate.mu.Unlock()
	if s.status != nil {
		s.status.SetPendingApproval(id, cred.sessionID, true)
	}
	s.broadcast(cred.sessionID, SSEEvent{Type: "permission.asked", Properties: req})

	answer := approvalReply{Reply: "reject", Message: "approval timed out"}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case answer = <-pa.reply:
	case <-r.Context().Done():
		answer = approvalReply{Reply: "reject", Message: "approval hook was interrupted"}
	case <-timer.C:
	}

	s.gate.mu.Lock()
	delete(s.gate.pending, id)
	s.gate.mu.Unlock()
	if s.status != nil {
		s.status.SetPendingApproval(id, cred.sessionID, false)
	}
	s.broadcast(cred.sessionID, SSEEvent{Type: "permission.replied", Properties: map[string]interface{}{
		"sessionID": cred.sessionID, "requestID": id, "reply": answer.Reply,
	}})
	return answer
}

// Session "always" grants live in Hoxi only, never in agy's global settings,
// and never cover destructive commands (AGENTS.md rule 6).
func (g *approvalGate) addAlways(sessionID, command string) {
	if isDestructiveCommand(command) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.always[sessionID] == nil {
		g.always[sessionID] = make(map[string]bool)
	}
	g.always[sessionID][strings.TrimSpace(command)] = true
}

func (g *approvalGate) hasAlways(sessionID, command string) bool {
	if isDestructiveCommand(command) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.always[sessionID][strings.TrimSpace(command)]
}

// Tool "always" grants are keyed by tool + exact args, so allowing one MCP
// call never blanket-approves a different one.
func (g *approvalGate) addToolAlways(sessionID, key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.toolAlways[sessionID] == nil {
		g.toolAlways[sessionID] = make(map[string]bool)
	}
	g.toolAlways[sessionID][key] = true
}

func (g *approvalGate) hasToolAlways(sessionID, key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.toolAlways[sessionID][key]
}

func (s *Service) handlePermissionRoutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/runner/permission"), "/")
	if sub == "" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listPendingApprovals(r.URL.Query().Get("directory")))
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
	if sub == "" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, http.StatusOK, s.listUnifiedPermissions(r.Context(), r.URL.Query().Get("directory")))
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
	status, respRaw, err := s.replyOpenCodePermission(r.Context(), id, r.URL.Query().Get("directory"), raw)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(respRaw)
}

func (s *Service) replyOpenCodePermission(ctx context.Context, id, directory string, raw []byte) (int, []byte, error) {
	path := "/permission/" + url.PathEscape(id) + "/reply"
	primary := strings.TrimSpace(directory)
	status, respRaw, err := s.openCodeRaw(ctx, http.MethodPost, path, directoryQuery(primary, nil), raw)
	if err != nil || status != http.StatusNotFound {
		return status, respRaw, err
	}
	for _, candidate := range s.openCodeCandidateDirs("") {
		if normalizeComparePath(candidate) == normalizeComparePath(primary) {
			continue
		}
		st, body, retryErr := s.openCodeRaw(ctx, http.MethodPost, path, directoryQuery(candidate, nil), raw)
		if retryErr == nil && st != http.StatusNotFound {
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
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	normFilter := normalizeComparePath(directory)
	add(directory)
	for _, sid := range s.busySessionIDs() {
		rec, ok := s.store.Meta(sid)
		if !ok || rec.Runtime != "opencode" {
			continue
		}
		if normFilter != "" &&
			normalizeComparePath(rec.ProjectDir) != normFilter &&
			normalizeComparePath(rec.WorktreePath) != normFilter {
			continue
		}
		if rec.WorktreePath != "" {
			add(rec.WorktreePath)
		}
		if rec.ProjectDir != "" {
			add(rec.ProjectDir)
		}
	}
	if normFilter == "" {
		for _, info := range s.store.List("") {
			rec, ok := s.store.Meta(info.ID)
			if !ok || rec.Runtime != "opencode" {
				continue
			}
			if rec.WorktreePath != "" {
				add(rec.WorktreePath)
			}
			if rec.ProjectDir != "" {
				add(rec.ProjectDir)
			}
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
	s.gate.mu.Lock()
	pa := s.gate.pending[id]
	s.gate.mu.Unlock()
	if pa == nil {
		writeJSONError(w, http.StatusNotFound, "permission request not found")
		return
	}
	select {
	case pa.reply <- approvalReply{Reply: body.Reply, Message: body.Message}:
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

func pathWithin(base, target string) bool {
	resolve := func(p string) string {
		p = filepath.Clean(strings.TrimSpace(p))
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real
		}
		// Writes often target a file that does not exist yet. Resolve the
		// nearest existing ancestor so /var vs /private/var symlinks on macOS
		// cannot make an in-worktree path look like an escape.
		suffix := ""
		for cur := p; ; {
			parent := filepath.Dir(cur)
			if parent == cur {
				return p
			}
			suffix = filepath.Join(filepath.Base(cur), suffix)
			cur = parent
			if real, err := filepath.EvalSymlinks(cur); err == nil {
				return filepath.Join(real, suffix)
			}
		}
	}
	if strings.TrimSpace(base) == "" {
		return false
	}
	rel, err := filepath.Rel(resolve(base), resolve(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
