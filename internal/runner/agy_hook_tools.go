package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PreToolUse covers every agy tool since the hooks.json matcher is "*".
// Hoxi classifies them here so new agy releases cannot silently bypass the
// gate (1.4.3 introduced read_file after view_file).

const (
	hookActionCommand     = "command"
	hookActionRead        = "read"
	hookActionEdit        = "edit"
	hookActionAsk         = "ask"
	hookActionPassthrough = "passthrough"
	hookActionUnknown     = "unknown"
)

var (
	hookReadTools = map[string]bool{
		"read_file":                     true,
		"view_file":                     true,
		"list_dir":                      true,
		"find_by_name":                  true,
		"grep_search":                   true,
		"read_resource":                 true,
		"list_resources":                true,
		"read_browser_page":             true,
		"browser_get_dom":               true,
		"browser_get_network_request":   true,
		"browser_list_network_requests": true,
		"list_browser_pages":            true,
		"capture_browser_console_logs":  true,
		"capture_browser_screenshot":    true,
		"list_permissions":              true,
		"list_plugin_accounts":          true,
		"search_marketplace":            true,
		"command_status":                true,
	}
	hookPathScopedReadTools = map[string]bool{
		"read_file":    true,
		"view_file":    true,
		"list_dir":     true,
		"find_by_name": true,
		"grep_search":  true,
	}
	hookEditTools = map[string]bool{
		"write_to_file":              true,
		"write_file":                 true,
		"replace_file_content":       true,
		"multi_replace_file_content": true,
		"sed_file":                   true,
		"notebook_edit":              true,
	}
	hookAskTools = map[string]bool{
		"call_mcp_tool":              true,
		"mcp":                        true,
		"execute_browser_javascript": true,
		"execute_url":                true,
		"unsandboxed":                true,
		"run_workflow":               true,
		"read_url_content":           true,
		"search_web":                 true,
	}
	// Control tools never act on the workspace, so gating them would only
	// block the end of a turn on a question the user does not need to answer.
	hookPassthroughTools = map[string]bool{
		"finish":        true,
		"task_boundary": true,
		"ask_question":  true,
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

// isReadToolAllowedInWorktree reports whether a read-class tool may be
// auto-approved without prompting. Path-scoped filesystem inspection tools
// must carry a path inside the session worktree; any other read tool that
// carries a path argument must also stay inside the worktree.
func isReadToolAllowedInWorktree(worktree, tool string, args map[string]interface{}) bool {
	if hasEscapingSearchPattern(args) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(tool))
	path := resolveHookToolPath(worktree, args)
	if hookPathScopedReadTools[lower] {
		return path != "" && pathWithin(worktree, path)
	}
	return path == "" || pathWithin(worktree, path)
}

func hasEscapingSearchPattern(args map[string]interface{}) bool {
	for _, k := range []string{"pattern", "Pattern", "glob", "Glob", "Include", "include"} {
		p := strings.TrimSpace(firstString(args[k]))
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			if unquoted, err := strconv.Unquote(p); err == nil {
				p = strings.TrimSpace(unquoted)
			}
		}
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || p == ".." ||
			strings.HasPrefix(p, "../") || strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") {
			return true
		}
	}
	return false
}

func rawHookToolPath(args map[string]interface{}) string {
	raw := firstString(args["Path"], args["AbsolutePath"], args["TargetFile"],
		args["target_file"], args["file_path"], args["filePath"], args["filepath"], args["File"],
		args["file"], args["path"], args[" target"], args["target"], args["DirectoryPath"], args["directory_path"],
		args["SearchPath"], args["search_path"], args["SearchDirectory"],
		args["search_directory"], args["Directory"], args["directory"],
		args["NotebookPath"], args["notebook_path"])
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = strings.TrimSpace(unquoted)
		}
	}
	return raw
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
	if !filepath.IsAbs(raw) {
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

func (s *Service) inspectHookToolRisk(sessionID, tool string, args map[string]interface{}) (filepathOut, worktree string, destructive, externalDir bool) {
	return s.inspectHookToolRiskIn(sessionID, tool, "", args)
}

func (s *Service) inspectHookToolRiskIn(sessionID, tool, cwd string, args map[string]interface{}) (filepathOut, worktree string, destructive, externalDir bool) {
	worktree = ""
	if rec, ok := s.store.Get(sessionID); ok {
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
	if hasEscapingSearchPattern(args) {
		externalDir = true
		destructive = true
	}
	if filepathOut != "" {
		if isProtectedRelPath(filepathOut) || (rawPath != "" && isProtectedRelPath(rawPath)) {
			destructive = true
		}
		if isSensitiveExternalPath(filepathOut) || (rawPath != "" && isSensitiveExternalPath(rawPath)) {
			destructive = true
		}
		if isSecretFilePath(filepathOut) || (rawPath != "" && isSecretFilePath(rawPath)) {
			destructive = true
		}
		if worktree != "" && resolved != "" {
			if !pathWithin(worktree, resolved) {
				// Crossing into a sibling workspace folder or an SDK cache is
				// routine; only credential and execution-controlling paths
				// must keep asking (same rule as the shared policy port).
				externalDir = true
				if isSensitiveExternalPath(resolved) || isSecretFilePath(resolved) {
					destructive = true
				}
			} else if (hookEditTools[lowerTool] || lowerTool == "edit" || lowerTool == "multiedit" || lowerTool == "write" || lowerTool == "notebookedit") &&
				isProtectedWorktreePath(worktree, resolved) {
				destructive = true
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
	if desc := firstString(args["toolSummary"], args["description"], args["Action"]); desc != "" {
		meta["description"] = desc
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
	if s.policyApproves(req, worktree) {
		s.recordPolicyApproval(cred, req)
		return true, ""
	}
	answer := s.awaitReply(r, cred, id, req)
	switch answer.Reply {
	case "always":
		if !destructive {
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

var protectedWorktreeDirs = map[string]bool{
	".git":      true,
	".agents":   true,
	".opencode": true,
	".claude":   true,
	".hoxi":     true,
	".envrc":    true,
}

func stripWorktreePrefix(slash string) string {
	const marker = "/.hoxi/worktrees/"
	if idx := strings.LastIndex(slash, marker); idx != -1 {
		rest := slash[idx+len(marker):]
		if nextSlash := strings.IndexByte(rest, '/'); nextSlash != -1 {
			return rest[nextSlash+1:]
		}
		return ""
	}
	const relMarker = ".hoxi/worktrees/"
	if strings.HasPrefix(slash, relMarker) {
		rest := slash[len(relMarker):]
		if nextSlash := strings.IndexByte(rest, '/'); nextSlash != -1 {
			return rest[nextSlash+1:]
		}
		return ""
	}
	return slash
}

func isProtectedRelPath(rel string) bool {
	rawSlash := strings.ToLower(filepath.ToSlash(filepath.Clean(rel)))
	slash := stripWorktreePrefix(rawSlash)
	if slash == "." || slash == "" {
		return false
	}
	if strings.Contains(slash, "/.hoxi/skills/") ||
		strings.Contains(slash, "/.hoxi/attachments/") ||
		strings.HasPrefix(slash, ".hoxi/skills/") ||
		strings.HasPrefix(slash, ".hoxi/attachments/") {
		return false
	}
	if slash == ".vscode/settings.json" || strings.HasSuffix(slash, "/.vscode/settings.json") {
		return true
	}
	for _, part := range strings.Split(slash, "/") {
		if protectedWorktreeDirs[part] {
			return true
		}
	}
	return false
}

// isProtectedWorktreePath reports whether an edit target inside the worktree
// touches execution-controlling paths (.git/**, .agents/**, .opencode/**,
// .claude/**, .hoxi/**, .vscode/settings.json, .envrc) that must ask the user.
func isProtectedWorktreePath(base, target string) bool {
	cleanBase := filepath.Clean(strings.TrimSpace(base))
	cleanTarget := filepath.Clean(strings.TrimSpace(target))
	if rel, err := filepath.Rel(cleanBase, cleanTarget); err == nil && isProtectedRelPath(rel) {
		return true
	}
	realBase, okBase := resolvePathWithSymlinks(base, 0)
	realTarget, okTarget := resolvePathWithSymlinks(target, 0)
	if !okBase || !okTarget {
		return true
	}
	rel, err := filepath.Rel(realBase, realTarget)
	return err != nil || isProtectedRelPath(rel)
}

func resolvePathWithSymlinks(p string, depth int) (string, bool) {
	if depth > 40 {
		return "", false
	}
	p = filepath.Clean(strings.TrimSpace(p))
	if p == "" {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real, true
	}
	suffix := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, suffix), true
		}
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return "", false
			}
			link, err := os.Readlink(cur)
			if err != nil {
				return "", false
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(filepath.Dir(cur), link)
			}
			return resolvePathWithSymlinks(filepath.Join(link, suffix), depth+1)
		}
		if !os.IsNotExist(err) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, true
		}
		suffix = filepath.Join(filepath.Base(cur), suffix)
		cur = parent
	}
}

func pathWithin(base, target string) bool {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(target) == "" {
		return false
	}
	realBase, ok := resolvePathWithSymlinks(base, 0)
	if !ok {
		return false
	}
	realTarget, ok := resolvePathWithSymlinks(target, 0)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(realBase, realTarget)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
