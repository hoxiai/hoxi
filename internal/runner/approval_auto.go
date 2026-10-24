package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// approvalSourcePolicy marks decisions the approval policy made without the
// user, in audit entries and permission.replied events.
const (
	approvalSourcePolicy     = "policy"
	approvalSourceUnattended = "unattended"
	approvalSourceTimeout    = "timeout"
)

const (
	openCodeAutoReplyInflight = 1
	openCodeAutoReplyFailed   = 2
	// openCodeAutoReplySettled: OpenCode accepted an answer to the request or
	// no longer knows it.
	openCodeAutoReplySettled = 3
)

// openCodeSettledFor is how long a settled request stays hidden. A listing
// that still returns it (one that lags behind the reply, or a reply that
// reached an instance without the request) is ignored for that long instead
// of being shown, timed out and audited again on every poll.
const openCodeSettledFor = 15 * time.Minute

type openCodeSettledMark struct{ until time.Time }

func (s *Service) currentPermissionRules() map[string]interface{} {
	s.mu.Lock()
	fn := s.permissionRules
	s.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn()
}

// policyApproves reports whether item may run without asking the user.
func (s *Service) policyApproves(item map[string]interface{}, worktree string) bool {
	return s.policyApprovesIsolated(item, worktree, false)
}

// policyApprovesIsolated is policyApproves once the caller knows whether the
// session is an isolated, command-sandboxed worktree.
func (s *Service) policyApprovesIsolated(item map[string]interface{}, worktree string, isolated bool) bool {
	return approvalAutoApprovesIsolated(item, s.ApprovalPolicy(), s.currentPermissionRules(), worktree, isolated)
}

func (s *Service) sessionIsolated(sessionID string) (worktree string, isolated bool) {
	rec, ok := s.store.Get(sessionID)
	if !ok {
		return "", false
	}
	return rec.WorktreePath, unattendedIsolated(rec)
}

// policyApprovesWorktreeEdit reports whether an in-worktree, non-protected
// edit may run without asking (it may not in manual mode or with edit=ask).
func (s *Service) policyApprovesWorktreeEdit(rec SessionRecord, path string) bool {
	target := path
	if rel, err := filepath.Rel(rec.WorktreePath, path); err == nil && rec.WorktreePath != "" {
		target = filepath.ToSlash(rel)
	}
	return s.policyApproves(map[string]interface{}{"permission": "edit", "patterns": []string{target}}, rec.WorktreePath)
}

func (s *Service) sessionWorktree(sessionID string) string {
	if rec, ok := s.store.Get(sessionID); ok {
		return rec.WorktreePath
	}
	return ""
}

// recordPolicyApproval audits a request the policy let through, so the
// approval history still shows what ran without a prompt.
func (s *Service) recordPolicyApproval(cred hookCredential, req map[string]interface{}) {
	if _, ok := req["id"].(string); !ok {
		req["id"] = runnerPermissionID + randomHex(12)
	}
	s.recordRunnerApprovalAudit(cred, req, "once", approvalReply{Reply: "once", Source: approvalSourcePolicy})
}

// autoReplyOpenCode answers an OpenCode permission request on the policy's
// behalf. Each request is answered once; a failed reply is never retried
// and stays visible as a normal pending request.
func (s *Service) autoReplyOpenCode(id, sessionID, directory, source string, onFail func()) {
	s.autoReplyOpenCodeMessage(id, sessionID, directory, "once", "", source, nil, onFail)
}

// autoReplyOpenCodeMessage answers an OpenCode permission once. reply is
// "once" or "reject"; message is the reason OpenCode shows the model
// (PermissionCorrectedError) and must not contain the command.
func (s *Service) autoReplyOpenCodeMessage(id, sessionID, directory, reply, message, source string, onAccept, onFail func()) {
	if id == "" {
		return
	}
	if !s.claimOpenCodeReply(id) {
		return
	}
	body, err := json.Marshal(map[string]string{"reply": reply, "message": message})
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, _, err := s.replyOpenCodePermission(ctx, id, directory, body)
		switch {
		case err == nil && status >= 200 && status < 300:
			// Audit and count only the reply OpenCode actually accepted, so a
			// hard deny is never logged as "once" and a failed reply never
			// inflates the turn summary or deferred list.
			s.recordOpenCodeAudit(id, sessionID, directory, reply, message, source)
			if onAccept != nil {
				onAccept()
			}
			s.settleOpenCodePermission(id)
		case err == nil && status == http.StatusNotFound:
			// Already answered elsewhere.
			s.settleOpenCodePermission(id)
		default:
			s.openCodeAutoReplies.Store(id, openCodeAutoReplyFailed)
			if onFail != nil {
				onFail()
			}
		}
	}()
}

func (s *Service) openCodeAutoReplyState(id string) int {
	v, ok := s.openCodeAutoReplies.Load(id)
	if !ok {
		return 0
	}
	if mark, settled := v.(openCodeSettledMark); settled {
		if time.Now().Before(mark.until) {
			return openCodeAutoReplySettled
		}
		s.openCodeAutoReplies.CompareAndDelete(id, v)
		return 0
	}
	state, _ := v.(int)
	return state
}

// claimOpenCodeReply takes the policy's single right to answer id. It fails
// while another answer is in flight, after a failed one (the policy never
// retries), and while id is settled.
func (s *Service) claimOpenCodeReply(id string) bool {
	s.openCodeAutoReplyState(id) // drops an expired settle mark
	_, loaded := s.openCodeAutoReplies.LoadOrStore(id, openCodeAutoReplyInflight)
	return !loaded
}

// claimOpenCodeAnswer takes the right to answer id for the user or the
// timeout. Unlike the policy they may take over a request whose policy reply
// failed, since that request is shown as pending. release undoes a claim
// whose reply OpenCode did not accept and restores a failed request as
// failed, so the policy still never retries it.
func (s *Service) claimOpenCodeAnswer(id string) (release func(), ok bool) {
	if s.claimOpenCodeReply(id) {
		return func() { s.openCodeAutoReplies.CompareAndDelete(id, openCodeAutoReplyInflight) }, true
	}
	if s.openCodeAutoReplies.CompareAndSwap(id, openCodeAutoReplyFailed, openCodeAutoReplyInflight) {
		return func() {
			s.openCodeAutoReplies.CompareAndSwap(id, openCodeAutoReplyInflight, openCodeAutoReplyFailed)
		}, true
	}
	return nil, false
}

// settleOpenCodePermission ends a request once OpenCode accepted an answer
// or reported it unknown: it is no longer claimed, shown, timed out or
// audited, even if a stale listing still returns it.
func (s *Service) settleOpenCodePermission(id string) {
	s.gate.forgetOpenCodeAsk(id)
	now := time.Now()
	s.openCodeAutoReplies.Range(func(key, v interface{}) bool {
		if mark, ok := v.(openCodeSettledMark); ok && !now.Before(mark.until) {
			s.openCodeAutoReplies.CompareAndDelete(key, v)
		}
		return true
	})
	s.openCodeAutoReplies.Store(id, openCodeSettledMark{until: now.Add(openCodeSettledFor)})
}

// claimOpenCodePermission decides a pending OpenCode request. It returns true
// when the request is handled by the policy (or already being handled) and
// must not reach the user.
func (s *Service) claimOpenCodePermission(item map[string]interface{}, sessionID, directory string, onFail func()) bool {
	id, _ := item["id"].(string)
	if id == "" || !s.openCode.Available() {
		return false
	}
	switch s.openCodeAutoReplyState(id) {
	case openCodeAutoReplyInflight, openCodeAutoReplySettled:
		return true
	case openCodeAutoReplyFailed:
		// The policy already tried once; the user or the timeout answers.
		annotateGrantClasses(item, directory)
		s.askOpenCodeUser(item, sessionID, directory)
		return false
	}
	// A failed policy reply leaves the request to the user, and to the
	// timeout even if no listing shows it again.
	userFallback := func() {
		s.askOpenCodeUser(item, sessionID, directory)
		if onFail != nil {
			onFail()
		}
	}
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		if allow, category := unattendedDecision(item, directory); !allow && category != "" {
			summary := permissionSummaryText(item)
			s.autoReplyOpenCodeMessage(id, sessionID, directory, "reject", unattendedDenyReason(category), approvalSourceUnattended, func() {
				s.rememberDeferred(sessionID, id, category, summary)
				s.noteTurn(sessionID, "deny")
			}, userFallback)
			return true
		}
		_, isolated := s.sessionIsolated(sessionID)
		if isolated && s.policyApprovesIsolated(item, directory, true) {
			s.autoReplyOpenCodeMessage(id, sessionID, directory, "once", "", approvalSourcePolicy, func() {
				s.noteTurn(sessionID, "allow")
			}, userFallback)
			return true
		}
	}
	if s.policyApproves(item, directory) {
		s.autoReplyOpenCode(id, sessionID, directory, approvalSourcePolicy, userFallback)
		return true
	}
	if s.classGrantApproves(item, sessionID, directory) {
		s.autoReplyOpenCode(id, sessionID, directory, grantSourceSession, userFallback)
		return true
	}
	// Shown to the user: offer a session grant when Go would honour one.
	annotateGrantClasses(item, directory)
	s.askOpenCodeUser(item, sessionID, directory)
	return false
}

// askOpenCodeUser keeps a request shown to the user, so a grant reply is
// judged against it, and arms its timeout the first time it is shown.
func (s *Service) askOpenCodeUser(item map[string]interface{}, sessionID, directory string) {
	id, _ := item["id"].(string)
	if s.gate.rememberOpenCodeAsk(id, openCodeAsk{item: item, sessionID: sessionID, dir: directory}) {
		s.armOpenCodeTimeout(id, sessionID, directory, item)
	}
}

func openCodePermissionCreatedAt(item map[string]interface{}) time.Time {
	parseMillis := func(v interface{}) (time.Time, bool) {
		switch n := v.(type) {
		case float64:
			if n > 0 {
				return time.UnixMilli(int64(n)), true
			}
		case int64:
			if n > 0 {
				return time.UnixMilli(n), true
			}
		case int:
			if n > 0 {
				return time.UnixMilli(int64(n)), true
			}
		case json.Number:
			if i, err := n.Int64(); err == nil && i > 0 {
				return time.UnixMilli(i), true
			}
			if f, err := n.Float64(); err == nil && f > 0 {
				return time.UnixMilli(int64(f)), true
			}
		}
		return time.Time{}, false
	}
	if t, ok := parseMillis(item["createdAt"]); ok {
		return t
	}
	if tm, ok := item["time"].(map[string]interface{}); ok {
		if t, ok := parseMillis(tm["created"]); ok {
			return t
		}
	}
	return time.Now()
}

// armOpenCodeTimeout rejects an OpenCode request that nobody answers before
// the policy timeout. The timer and a user reply share one claim, so only one
// of them answers the request. A reject OpenCode refuses is sent again after
// each of openCodeTimeoutRetryDelays until the request is settled.
func (s *Service) armOpenCodeTimeout(id, sessionID, directory string, item map[string]interface{}) {
	created := openCodePermissionCreatedAt(item)
	wait := s.ApprovalPolicy().approvalWaitLimit()
	deadline := created.Add(wait)
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	summary := permissionSummaryText(item)
	retries := s.openCodeTimeoutRetries
	if retries == nil {
		retries = openCodeTimeoutRetryDelays
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for attempt := 0; ; attempt++ {
			<-timer.C
			if s.timeoutRejectOpenCode(id, sessionID, directory, summary) || attempt >= len(retries) {
				return
			}
			timer.Reset(retries[attempt])
		}
	}()
}

// openCodeTimeoutRetryDelays spaces the timeout's further rejects after
// OpenCode refused one or another answer was in flight. Once they run out
// the request stays shown to the user.
var openCodeTimeoutRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute}

// timeoutRejectOpenCode sends the timeout's reject. It reports true when the
// request needs no further attempt: it was answered, settled or forgotten.
// The timeout only ever rejects; it never approves.
func (s *Service) timeoutRejectOpenCode(id, sessionID, directory, summary string) bool {
	if _, pending := s.gate.openCodeAskFor(id); !pending {
		return true
	}
	release, ok := s.claimOpenCodeAnswer(id)
	if !ok {
		// Another answer is in flight; check again after the next delay.
		return false
	}
	reason := approvalTimeoutReason(s.ApprovalPolicy())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"reply": "reject", "message": reason})
	status, _, err := s.replyOpenCodePermission(ctx, id, directory, body)
	if err == nil && status == http.StatusNotFound {
		s.settleOpenCodePermission(id)
		return true
	}
	if err != nil || status < 200 || status >= 300 {
		release()
		return false
	}
	s.recordOpenCodeAudit(id, sessionID, directory, "reject", reason, approvalSourceTimeout)
	s.rememberDeferred(sessionID, id, "timeout", summary)
	s.noteTurn(sessionID, "timeout")
	s.settleOpenCodePermission(id)
	return true
}

// claimOpenCodeSSELine handles permission.asked frames on the raw OpenCode
// stream (sessions the runner does not drive). It reports whether the frame
// was answered by the policy and must be dropped.
func (s *Service) claimOpenCodeSSELine(line, directory string) bool {
	if !strings.Contains(line, `"permission.asked"`) {
		return false
	}
	var envelope struct {
		Type       string                 `json:"type"`
		Properties map[string]interface{} `json:"properties"`
		Directory  string                 `json:"directory"`
		Payload    *struct {
			Type       string                 `json:"type"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"payload"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &envelope) != nil {
		return false
	}
	if envelope.Payload != nil {
		envelope.Type, envelope.Properties = envelope.Payload.Type, envelope.Payload.Properties
	}
	if envelope.Type != "permission.asked" || envelope.Properties == nil {
		return false
	}
	sanitizeOpenCodePermissionItem(envelope.Properties)
	dir := firstNonEmptyStr(envelope.Directory, directory)
	rawSid, _ := envelope.Properties["sessionID"].(string)
	sessionID, dir := s.resolveOpenCodePermissionTarget(rawSid, dir, envelope.Properties)
	if s.audit != nil {
		s.audit.rememberOpenCodePerm(envelope.Properties, dir)
	}
	return s.claimOpenCodePermission(envelope.Properties, sessionID, dir, nil)
}

func (s *Service) resolveOpenCodePermissionTarget(rawSessionID, fallbackDir string, item map[string]interface{}) (string, string) {
	sid := strings.TrimSpace(rawSessionID)
	sessionDir := ""
	if sid != "" {
		s.mu.Lock()
		for hoxiID, ap := range s.procs {
			if ap != nil && ap.isOpenCodeChild(sid) {
				sid = hoxiID
				break
			}
		}
		s.mu.Unlock()
		if rec, ok := s.store.Meta(sid); ok {
			sid = rec.ID
			sessionDir = firstNonEmptyStr(strings.TrimSpace(rec.WorktreePath), strings.TrimSpace(rec.ProjectDir))
		} else {
			for _, info := range s.store.List("") {
				rec, ok := s.store.Meta(info.ID)
				if !ok {
					continue
				}
				recDir := firstNonEmptyStr(strings.TrimSpace(rec.WorktreePath), strings.TrimSpace(rec.ProjectDir))
				if rec.ExternalID == sid {
					sid = rec.ID
					sessionDir = recDir
					break
				}
				matched := false
				for _, ocID := range rec.OpenCodeIDs {
					if ocID == sid {
						sid = rec.ID
						sessionDir = firstNonEmptyStr(strings.TrimSpace(rec.OpenCodeDirectories[ocID]), recDir)
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
		}
	}
	meta, _ := item["metadata"].(map[string]interface{})
	resolvedDir := firstNonEmptyStr(sessionDir, firstString(meta["directory"], meta["cwd"]), fallbackDir)
	if item != nil {
		if sid != "" {
			item["sessionID"] = sid
		}
		if resolvedDir != "" {
			if meta == nil {
				meta = make(map[string]interface{})
				item["metadata"] = meta
			}
			if firstString(meta["directory"], meta["cwd"]) == "" {
				meta["directory"] = resolvedDir
			}
		}
	}
	return sid, resolvedDir
}

// reconcilePendingApprovals lets requests that are already waiting through
// when a policy change now allows them, as the desktop used to do locally.
func (s *Service) reconcilePendingApprovals() {
	s.gate.mu.Lock()
	pending := make([]*pendingApproval, 0, len(s.gate.pending))
	for _, pa := range s.gate.pending {
		pending = append(pending, pa)
	}
	s.gate.mu.Unlock()
	for _, pa := range pending {
		dir := s.sessionWorktree(pa.sid)
		source := ""
		switch {
		case s.policyApproves(pa.req, dir):
			source = approvalSourcePolicy
		case s.classGrantApproves(pa.req, pa.sid, dir):
			source = grantSourceSession
		default:
			continue
		}
		select {
		case pa.reply <- approvalReply{Reply: "once", Source: source}:
		default:
		}
	}
	if s.openCode == nil || !s.openCode.Available() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.listUnifiedPermissions(ctx, "")
	}()
}

func (s *Service) replyOpenCodePermission(ctx context.Context, id, directory string, raw []byte) (int, []byte, error) {
	path := "/permission/" + url.PathEscape(id) + "/reply"
	requestedDir := strings.TrimSpace(directory)
	rememberedDir := ""
	if ask, ok := s.gate.openCodeAskFor(id); ok {
		rememberedDir = strings.TrimSpace(ask.dir)
	}
	if s.audit != nil {
		if snap, ok := s.audit.lookupOpenCodePerm(id); ok {
			if rememberedDir == "" {
				rememberedDir = strings.TrimSpace(snap.Directory)
			}
			if wt := strings.TrimSpace(s.sessionWorktree(snap.SessionID)); wt != "" {
				rememberedDir = wt
			}
		}
	}
	primary := firstNonEmptyStr(rememberedDir, requestedDir)
	status, respRaw, err := s.openCodeRaw(ctx, http.MethodPost, path, directoryQuery(primary, nil), raw)
	if err != nil || status != http.StatusNotFound {
		return status, respRaw, err
	}
	candidates := s.openCodeCandidateDirs("")
	if requestedDir != "" {
		candidates = append([]string{requestedDir}, candidates...)
	}
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
		if trimmed == "" {
			return
		}
		key := normalizeComparePath(trimmed)
		if !seen[key] {
			seen[key] = true
			out = append(out, trimmed)
		}
	}
	normFilter := normalizeComparePath(directory)
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
	for _, info := range s.store.List("") {
		addRec(s.store.Meta(info.ID))
	}
	add(directory)
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}
