package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/runner/shellpolicy"
)

// PreToolUse covers every agy tool since the hooks.json matcher is "*".
// Hoxi classifies them here so new agy releases cannot silently bypass the
// gate (1.4.3 introduced read_file after view_file).

const (
	hookActionCommand     = "command"
	hookActionRead        = "read"
	hookActionEdit        = "edit"
	hookActionAsk         = "ask"
	hookActionQuestion    = "question"
	hookActionPassthrough = "passthrough"
	hookActionUnknown     = "unknown"
)

var (
	hookPathScopedReadTools = map[string]bool{
		"read_file":           true,
		"read_many_files":     true,
		"view_file":           true,
		"list_dir":            true,
		"list_directory":      true,
		"find_by_name":        true,
		"grep_search":         true,
		"search_file_content": true,
	}
	hookAskTools = map[string]bool{
		"call_mcp_tool":              true,
		"mcp":                        true,
		"execute_browser_javascript": true,
		"execute_url":                true,
		"unsandboxed":                true,
		"run_workflow":               true,
		"read_url_content":           true,
		"web_fetch":                  true,
		"search_web":                 true,
		"google_web_search":          true,
		"save_memory":                true,
	}
	// Control tools never act on the workspace, so gating them would only
	// block the end of a turn on a question the user does not need to answer.
	hookPassthroughTools = map[string]bool{
		"finish":        true,
		"task_boundary": true,
	}
)

// hookToolAction classifies the tools agy sends through PreToolUse using an
// exact-name allowlist. Unknown tools go through Hoxi's approval gate instead
// of prefix-based auto-approval or agy's headless soft-deny.
func hookToolAction(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == hookToolRunCommand {
		return hookActionCommand
	}
	if isQuestionToolName(lower) {
		return hookActionQuestion
	}
	if hookReadTools[lower] {
		return hookActionRead
	}
	if hookEditTools[lower] {
		return hookActionEdit
	}
	if hookAskTools[lower] {
		return hookActionAsk
	}
	if hookPassthroughTools[lower] {
		return hookActionPassthrough
	}
	return hookActionUnknown
}

func hookToolActionWithArgs(name string, args map[string]interface{}) string {
	if act := hookToolAction(name); act != hookActionUnknown {
		return act
	}
	if isReadOnlyTaskControlCall(name, args) {
		return hookActionRead
	}
	return hookActionUnknown
}

// Refuse the opaque script and let agy retry using its scoped file tools.
// Repeating the same script in a new tool call requests normal human review.
func (s *Service) discoveryReadHint(cred hookCredential, rec SessionRecord, ask approvalAsk, guardKey string) string {
	if cred.discoveryReadHints == nil || ask.callID == "" || !strings.Contains(ask.command, "readdirSync") {
		return ""
	}
	policy := s.ApprovalPolicy()
	if policy.Mode != ApprovalModeAuto || policy.EffectiveRule(permissionCategoryBash) != "allow" {
		return ""
	}
	dir := shellpolicy.InlineDiscoveryDirectory(ask.command, s.currentPermissionRules()[permissionCategoryBash])
	if dir == "" || s.gate.hasAlways(cred.sessionID, ask.command) || isDestructivePermissionRequest(openCodeBashItem(cred.sessionID, ask.command)) {
		return ""
	}
	root := rec.WorktreePath
	cwd := firstNonEmptyStr(ask.cwd, root)
	target := filepath.Join(cwd, dir)
	if !pathWithin(root, cwd) || !pathWithin(root, target) || isProtectedWorktreePath(root, target) || isSecretFilePath(target) {
		return ""
	}
	hash := sha256.Sum256([]byte(guardKey + "\x00" + target + "\x00" + ask.command))
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	firstCall, seen := cred.discoveryReadHints[hash]
	// Keep redelivery idempotent and parallel lookups independent. At capacity
	// subsequent lookups take the existing human approval path.
	if (seen && firstCall != ask.callID) || (!seen && len(cred.discoveryReadHints) >= 64) {
		return ""
	}
	cred.discoveryReadHints[hash] = ask.callID
	return "Hoxi did not execute this script because the file to read is selected at runtime. " +
		"Use list_dir or find_by_name on the directory from the command, then search or read the selected file with its exact path using grep_search or view_file. " +
		"If these tools cannot produce the required result or the original script is required, repeat the identical command to request user approval."
}

// isReadToolAllowedInWorktree reports whether a read-class tool may be
// auto-approved without prompting. Path-scoped filesystem inspection tools
// must carry a path inside the session worktree; any other read tool that
// carries a path argument must also stay inside the worktree.
func isReadToolAllowedInWorktree(worktree, tool string, args map[string]interface{}) bool {
	if hasRiskySearchSelector(tool, args) {
		return false
	}
	if multi := stringList(args["paths"]); len(multi) > 0 {
		for _, p := range multi {
			resolved := resolveHookToolPath(worktree, map[string]interface{}{"path": p})
			if resolved == "" || !pathWithin(worktree, resolved) {
				return false
			}
		}
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(tool))
	path := resolveHookToolPath(worktree, args)
	if hookPathScopedReadTools[lower] {
		return path != "" && pathWithin(worktree, path)
	}
	return path == "" || pathWithin(worktree, path)
}

// resolveHookToolPath normalizes the file args agy uses across read/write
// tools. agy sometimes double-encodes string args, so quoted values are
// unquoted before resolving relative paths against the worktree.
func resolveHookToolPath(base string, args map[string]interface{}) string {
	raw := rawHookToolPath(args)
	if raw == "" {
		return ""
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~"))
	} else if strings.HasPrefix(raw, "~") {
		return ""
	}
	if !isAbsOrRootedPath(raw) {
		if strings.TrimSpace(base) == "" {
			return ""
		}
		raw = filepath.Join(base, raw)
	}
	cleaned := filepath.Clean(raw)
	resolveInBase := func(resolved string) string {
		if strings.TrimSpace(base) != "" {
			if realBase, ok := resolvePathWithSymlinks(base, 0); ok && realBase != "" {
				if rel, err := filepath.Rel(realBase, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return filepath.Join(filepath.Clean(base), rel)
				}
			}
		}
		return resolved
	}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && resolved != "" {
		return resolveInBase(resolved)
	}
	if info, err := os.Lstat(cleaned); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if resolved, ok := resolvePathWithSymlinks(cleaned, 0); ok && resolved != "" {
			return resolveInBase(resolved)
		}
	}
	return cleaned
}

// hookOwnSessionRead is a read of this session's own runner record. Edits of
// that directory stay destructive.
func hookOwnSessionRead(s *Service, sessionID, tool, filepathOut, rawPath string) bool {
	if hookEditTools[tool] || editToolNames[tool] {
		return false
	}
	if !hookReadTools[tool] && !readToolNames[tool] {
		return false
	}
	var ids []string
	if validSessionID(sessionID) {
		ids = append(ids, sessionID)
	}
	if rec, ok := s.store.Meta(sessionID); ok && validSessionID(rec.ExternalID) {
		ids = append(ids, rec.ExternalID)
	}
	return pathUnderOwnSession(filepathOut, ids) || pathUnderOwnSession(rawPath, ids)
}

// isAgyBrainPath reports whether rawPath points inside an Antigravity
// per-conversation brain directory (~/.gemini/antigravity-cli/brain/<id> or
// ~/.gemini/antigravity/brain/<id>).
func isAgyBrainPath(rawPath string) bool {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "file://") {
		trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "file://"), "localhost")
	}
	for _, root := range agyBrainRoots() {
		if pathWithin(root, trimmed) {
			return true
		}
	}
	slash := strings.ToLower(filepath.ToSlash(filepath.Clean(trimmed)))
	return strings.Contains(slash, "/.gemini/antigravity-cli/brain/") ||
		strings.Contains(slash, "/.gemini/antigravity/brain/")
}

// agyForeignBrainDir extracts the conversation ID when rawPath targets an
// Antigravity brain/<id> directory that does not belong to activeConvID.
// Antigravity's built-in file tools hard-deny cross-conversation brain paths in
// headless mode even when PreToolUse returns auto_approve, aborting the turn.
func agyForeignBrainDir(worktree, activeConvID, rawPath string) string {
	activeConvID = strings.TrimSpace(activeConvID)
	resolved := resolveHookToolPath(worktree, map[string]interface{}{"path": rawPath})
	if resolved == "" {
		resolved = strings.TrimSpace(rawPath)
	}
	if resolved == "" {
		return ""
	}
	if worktree != "" && pathWithin(worktree, resolved) {
		return ""
	}
	for _, root := range agyBrainRoots() {
		if !pathWithin(root, resolved) {
			continue
		}
		realRoot, okRoot := resolvePathWithSymlinks(root, 0)
		realTarget, okTarget := resolvePathWithSymlinks(resolved, 0)
		if !okRoot || !okTarget {
			continue
		}
		rel, err := filepath.Rel(realRoot, realTarget)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		id := strings.Split(filepath.ToSlash(rel), "/")[0]
		if id != "" && id != "." && id != activeConvID {
			return id
		}
	}
	slash := filepath.ToSlash(filepath.Clean(resolved))
	for _, marker := range []string{"/.gemini/antigravity-cli/brain/", "/.gemini/antigravity/brain/"} {
		if idx := strings.Index(slash, marker); idx >= 0 {
			rest := slash[idx+len(marker):]
			id := strings.Split(rest, "/")[0]
			if id != "" && id != "." && id != ".." && id != activeConvID {
				return id
			}
		}
	}
	return ""
}

func agyHookForeignBrainReason(worktree, activeConvID string, args map[string]interface{}) string {
	var candidates []string
	if p := rawHookToolPath(args); p != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, stringList(args["paths"])...)
	for _, cand := range candidates {
		if otherID := agyForeignBrainDir(worktree, activeConvID, cand); otherID != "" {
			return "Antigravity only allows file tools to access the current conversation's own brain directory; " + otherID + " belongs to an earlier or different conversation"
		}
	}
	return ""
}

func pathWithinAgyOwnBrain(activeConvID, target string) bool {
	activeConvID = strings.TrimSpace(activeConvID)
	if activeConvID == "" || strings.TrimSpace(target) == "" {
		return false
	}
	for _, root := range agyBrainRoots() {
		if pathWithin(filepath.Join(root, activeConvID), target) {
			return true
		}
	}
	return false
}

func isReadToolAllowedInAgyOwnBrain(worktree, activeConvID, tool string, args map[string]interface{}) bool {
	if activeConvID == "" || hasRiskySearchSelector(tool, args) {
		return false
	}
	if multi := stringList(args["paths"]); len(multi) > 0 {
		for _, p := range multi {
			resolved := resolveHookToolPath(worktree, map[string]interface{}{"path": p})
			if resolved == "" || !pathWithinAgyOwnBrain(activeConvID, resolved) || isSecretFilePath(resolved) {
				return false
			}
		}
		return true
	}
	if !hookPathScopedReadTools[strings.ToLower(strings.TrimSpace(tool))] {
		return false
	}
	path := resolveHookToolPath(worktree, args)
	return path != "" && pathWithinAgyOwnBrain(activeConvID, path) && !isSecretFilePath(path)
}

func (s *Service) inspectHookToolRisk(sessionID, tool string, args map[string]interface{}) (filepathOut, worktree string, destructive, externalDir bool) {
	return s.inspectHookToolRiskIn(sessionID, tool, "", args)
}

func (s *Service) inspectHookToolRiskIn(sessionID, tool, cwd string, args map[string]interface{}) (filepathOut, worktree string, destructive, externalDir bool) {
	worktree = ""
	if rec, ok := s.store.Meta(sessionID); ok {
		worktree = strings.TrimSpace(rec.WorktreePath)
	}
	baseDir := firstNonEmptyStr(strings.TrimSpace(cwd), worktree)
	rawPath := rawHookToolPath(args)
	resolved := resolveHookToolPath(baseDir, args)
	if resolved == "" && strings.TrimSpace(cwd) != "" && (worktree == "" || !pathWithin(worktree, cwd)) {
		resolved = filepath.Clean(strings.TrimSpace(cwd))
	}
	if resolved != "" {
		filepathOut = resolved
	} else {
		filepathOut = rawPath
	}
	lowerTool := strings.ToLower(strings.TrimSpace(tool))
	if lowerTool == "unsandboxed" {
		destructive = true
	}
	if hasRiskySearchSelector(lowerTool, args) {
		externalDir = true
		destructive = true
	}
	isReadTool := (hookToolActionWithArgs(lowerTool, args) == hookActionRead || readToolNames[lowerTool]) && !hookEditTools[lowerTool] && !editToolNames[lowerTool]
	read := map[string]interface{}{"permission": "tool", "metadata": map[string]interface{}{"tool": lowerTool, "args": args}}
	preferenceRead := isReadTool && isAttributionPreferenceReadRequest(read, rawPath)
	if preferenceRead {
		// Keep the verified configured spelling for the downstream request. The
		// resolved path can differ through a legitimate parent alias (/var on
		// macOS), but the raw args and real root/file were already checked.
		filepathOut = rawPath
	}
	isRiskyPath := func(p string) bool {
		if p == "" {
			return false
		}
		if isReadTool {
			if preferenceRead && (preferencePathEqual(p, rawPath) || preferencePathEqual(p, resolved)) {
				return false
			}
			return isSensitiveExternalReadPath(p) || isSecretFilePath(p)
		}
		return isProtectedRelPath(p) || isSensitiveExternalPath(p) || isSecretFilePath(p)
	}
	ownRead := hookOwnSessionRead(s, sessionID, lowerTool, filepathOut, rawPath)
	if filepathOut != "" {
		if !ownRead && (isRiskyPath(filepathOut) || isRiskyPath(rawPath)) {
			destructive = true
		}
		if worktree != "" {
			if resolved == "" {
				// A non-empty raw path that resolveHookToolPath refused to expand
				// (e.g. ~otheruser/... or ~\...) cannot be proven inside the worktree:
				// fail closed as external + destructive so no approval mode auto-allows it.
				externalDir = true
				destructive = true
			} else if !pathWithin(worktree, resolved) {
				// Crossing into a sibling workspace folder or an SDK cache is
				// routine; only credential and execution-controlling paths
				// must keep asking (same rule as the shared policy port).
				externalDir = true
				if !ownRead && isRiskyPath(resolved) {
					destructive = true
				}
			} else if (hookEditTools[lowerTool] || lowerTool == "edit" || lowerTool == "multiedit" || lowerTool == "write" || lowerTool == "notebookedit") &&
				isProtectedWorktreePath(worktree, resolved) {
				destructive = true
			}
		}
	}
	for _, p := range stringList(args["paths"]) {
		if p == rawPath {
			continue // already checked as the primary path above
		}
		subResolved := resolveHookToolPath(baseDir, map[string]interface{}{"path": p})
		subOut := subResolved
		if subOut == "" {
			subOut = p
		}
		if !hookOwnSessionRead(s, sessionID, lowerTool, subOut, p) {
			if isRiskyPath(subOut) || isRiskyPath(p) {
				destructive = true
				filepathOut = subOut
			}
		}
		if worktree != "" {
			if subResolved == "" {
				externalDir = true
				destructive = true
				if filepathOut == resolved {
					filepathOut = subOut
				}
			} else if !pathWithin(worktree, subResolved) {
				externalDir = true
				if filepathOut == resolved {
					filepathOut = subResolved
				}
			}
		}
	}
	return filepathOut, worktree, destructive, externalDir
}

// gateToolCall decides a tool that can reach outside the worktree (MCP,
// browser JavaScript, network egress, explicit unsandboxed execution, or
// protected/out-of-worktree file access): the approval policy lets it run or
// the user is asked. Destructive calls always reach the user.
func (s *Service) gateToolCall(r *http.Request, cred hookCredential, tool string, args map[string]interface{}, callID string) (bool, string) {
	return s.gateToolCallIn(r, cred, tool, args, callID, "")
}

func (s *Service) gateToolCallIn(r *http.Request, cred hookCredential, tool string, args map[string]interface{}, callID, cwd string) (bool, string) {
	key := hookToolGrantKey(tool, args)
	targetPath, worktree, destructive, externalDir := s.inspectHookToolRiskIn(cred.sessionID, tool, cwd, args)
	if !destructive && s.gate.hasToolAlways(cred.sessionID, key) {
		return true, ""
	}
	id := runnerPermissionID + randomHex(12)
	pretty, _ := json.MarshalIndent(args, "", "  ")
	meta := map[string]interface{}{
		"tool":   tool,
		"args":   string(pretty),
		"source": firstNonEmptyStr(cred.runtime, string(RuntimeAntigravity)),
	}
	if worktree != "" {
		meta["cwd"] = worktree
	}
	if targetPath != "" {
		meta["filepath"] = targetPath
	}
	if destructive {
		meta["destructive"] = true
	}
	if externalDir {
		meta["externalDirectory"] = true
	}
	if desc := firstNonEmptyStr(firstString(args["toolSummary"], args["description"], args["Action"]), summarizeHookToolAction(tool, args)); desc != "" {
		meta["description"] = desc
	}
	if rec, ok := s.store.Meta(cred.sessionID); ok && strings.TrimSpace(rec.ExternalID) != "" {
		meta["externalID"] = rec.ExternalID
	}
	req := map[string]interface{}{
		"id":         id,
		"sessionID":  cred.sessionID,
		"permission": "tool",
		"patterns":   []string{tool},
		"metadata":   meta,
		"always":     []string{tool},
		"tool": map[string]interface{}{
			"messageID": cred.messageID,
			"callID":    callID,
		},
	}
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		if allow, category := unattendedDecision(req, worktree); !allow && category != "" {
			s.recordUnattendedDeny(cred, req, category)
			return false, unattendedDenyReason(category)
		}
		if rec, ok := s.store.Meta(cred.sessionID); ok && unattendedIsolated(rec) && s.policyApprovesIsolated(req, worktree, true) {
			s.recordPolicyApproval(cred, req)
			s.noteTurn(cred.sessionID, "allow")
			return true, ""
		}
	}
	if s.policyApproves(req, worktree) {
		s.recordPolicyApproval(cred, req)
		return true, ""
	}
	if s.classGrantApproves(req, cred.sessionID, worktree) {
		s.recordRunnerApprovalAudit(cred, req, "once", approvalReply{Reply: "once", Source: grantSourceSession})
		return true, ""
	}
	annotateGrantClasses(req, worktree)
	answer := s.awaitReply(r, cred, id, req)
	switch answer.Reply {
	case "always":
		if !destructive && !isDestructivePermissionRequest(req) {
			s.gate.addToolAlways(cred.sessionID, key, tool, summarizeToolGrantPattern(tool, args), answer.Source)
		}
		return true, ""
	case "once":
		return true, ""
	}
	reason := "The user rejected the " + tool + " call in Hoxi"
	if msg := strings.TrimSpace(answer.Message); msg != "" {
		reason += ": " + msg
	}
	return false, reason
}

func hookToolGrantKey(tool string, args map[string]interface{}) string {
	raw, _ := json.Marshal(args)
	sum := sha256.Sum256(raw)
	return "tool:" + tool + ":" + hex.EncodeToString(sum[:8])
}

func summarizeToolGrantPattern(tool string, args map[string]interface{}) string {
	if len(args) == 0 {
		return tool
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return tool
	}
	return redactApprovalText(tool+" "+string(raw), 200)
}

func summarizeHookToolAction(tool string, args map[string]interface{}) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "hoxi_browser":
		act := strings.TrimSpace(firstString(args["action"]))
		target := firstNonEmptyStr(firstString(args["url"], args["selector"], args["key"], args["tabID"]))
		if act != "" && target != "" {
			return redactApprovalText(act+" "+target, 120)
		}
		return act
	case "hoxi_tools":
		act := strings.TrimSpace(firstString(args["action"]))
		prov := strings.TrimSpace(firstString(args["provider"]))
		ep := strings.TrimSpace(firstString(args["endpoint"], args["query"]))
		return redactApprovalText(strings.TrimSpace(strings.Join([]string{act, prov, ep}, " ")), 120)
	case "hoxi_data":
		act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
		if act == "http_request" {
			method := firstNonEmptyStr(strings.ToUpper(strings.TrimSpace(firstString(args["method"]))), "GET")
			prof := strings.TrimSpace(firstString(args["profile"]))
			ep := strings.TrimSpace(firstString(args["endpoint"]))
			return redactApprovalText(strings.TrimSpace(strings.Join([]string{method, prof, ep}, " ")), 120)
		}
		return act
	case "hoxi_image", "hoxi_video", "hoxi_speech":
		if out := strings.TrimSpace(firstString(args["outputPath"])); out != "" {
			return redactApprovalText(out, 120)
		}
		return redactApprovalText(strings.TrimSpace(firstNonEmptyStr(firstString(args["prompt"]), firstString(args["text"]))), 80)
	default:
		return ""
	}
}
