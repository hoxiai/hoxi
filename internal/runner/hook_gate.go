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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agyhook"
)

// Antigravity headless mode cannot prompt, so agy's PreToolUse hook
// (`hoxi agy-hook`) calls back here and blocks until the user decides.
// Only `auto_approve` grants permission in headless mode; see
// docs/antigravity-approval-hook.md for the probe results behind this design.

const (
	hookPreToolPath     = "/api/hoxi/runner/hook/pretool"
	runnerPermissionID  = "per_rnr_"
	hookDecisionApprove = "auto_approve"
	hookDecisionDeny    = "deny"
)

type hookCredential struct {
	sessionID string
	messageID string
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
	waitLimit   time.Duration
}

func newApprovalGate() *approvalGate {
	return &approvalGate{
		tokens:    make(map[[32]byte]hookCredential),
		pending:   make(map[string]*pendingApproval),
		always:    make(map[string]map[string]bool),
		waitLimit: agyhook.ClientTimeout - 5*time.Second,
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

// issueHookEnv mints a per-process credential for one agy turn. With no base
// URL the hook falls back to agy's own policy, matching pre-gate behavior.
func (s *Service) issueHookEnv(sessionID, messageID string) ([]string, func()) {
	g := s.gate
	g.mu.Lock()
	base := g.baseURL
	g.mu.Unlock()
	token := randomHex(32)
	if base == "" || token == "" {
		return nil, func() {}
	}
	key := sha256.Sum256([]byte(token))
	g.mu.Lock()
	g.tokens[key] = hookCredential{sessionID: sessionID, messageID: messageID}
	g.mu.Unlock()
	env := []string{
		agyhook.EnvSessionID + "=" + sessionID,
		agyhook.EnvHookURL + "=" + base + hookPreToolPath,
		agyhook.EnvHookToken + "=" + token,
	}
	return env, func() {
		g.mu.Lock()
		delete(g.tokens, key)
		g.mu.Unlock()
	}
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

func (s *Service) handleHookPreTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeJSONError(w, http.StatusForbidden, "loopback only")
		return
	}
	cred, ok := s.gate.lookup(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !ok || cred.sessionID != strings.TrimSpace(r.Header.Get(agyhook.HeaderSession)) {
		writeJSONError(w, http.StatusUnauthorized, "invalid hook credential")
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
	if p.ToolCall.Name != "run_command" {
		return "allow", ""
	}
	if p.ConversationID != "" && rec.ExternalID != "" && p.ConversationID != rec.ExternalID {
		return hookDecisionDeny, "Antigravity conversation does not belong to this Hoxi session"
	}
	command, _ := p.ToolCall.Args["CommandLine"].(string)
	cwd, _ := p.ToolCall.Args["Cwd"].(string)
	if strings.TrimSpace(command) == "" {
		return hookDecisionDeny, "empty command"
	}
	if cwd != "" && !pathWithin(rec.WorktreePath, cwd) {
		return hookDecisionDeny, "command working directory is outside this session's directory"
	}
	if decideCommandPermission(command) == "allow" || s.gate.hasAlways(cred.sessionID, command) {
		return hookDecisionApprove, ""
	}
	return s.awaitApproval(r, cred, p, command, cwd)
}

func (s *Service) awaitApproval(r *http.Request, cred hookCredential, p hookPayload, command, cwd string) (string, string) {
	id := runnerPermissionID + randomHex(12)
	meta := map[string]interface{}{"command": command, "source": "antigravity"}
	if cwd != "" {
		meta["cwd"] = cwd
	}
	if summary, _ := p.ToolCall.Args["toolSummary"].(string); strings.TrimSpace(summary) != "" {
		meta["description"] = strings.TrimSpace(summary)
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
			"callID":    fmt.Sprintf("call_%s_%d", cred.messageID, p.StepIdx),
		},
	}
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

	switch answer.Reply {
	case "always":
		s.gate.addAlways(cred.sessionID, command)
		return hookDecisionApprove, ""
	case "once":
		return hookDecisionApprove, ""
	}
	reason := "The user rejected this command in Hoxi"
	if msg := strings.TrimSpace(answer.Message); msg != "" {
		reason += ": " + msg
	}
	return hookDecisionDeny, reason
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
		return p
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
