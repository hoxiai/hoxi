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
		"read_url_content":              true,
		"read_browser_page":             true,
		"browser_get_dom":               true,
		"browser_get_network_request":   true,
		"browser_list_network_requests": true,
		"list_browser_pages":            true,
		"capture_browser_console_logs":  true,
		"capture_browser_screenshot":    true,
		"list_permissions":              true,
		"list_plugin_accounts":          true,
		"search_web":                    true,
		"search_marketplace":            true,
		"command_status":                true,
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
	}
	// Control tools never act on the workspace, so gating them would only
	// block the end of a turn on a question the user does not need to answer.
	hookPassthroughTools = map[string]bool{
		"finish":        true,
		"task_boundary": true,
		"ask_question":  true,
	}
)

// hookToolAction classifies the tools agy sends through PreToolUse. Unknown
// tools go through Hoxi's approval gate instead of agy's headless policy,
// which would soft-deny anything that needs permission and end the turn.
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
	switch {
	case strings.HasPrefix(lower, "read_"), strings.HasPrefix(lower, "view_"),
		strings.HasPrefix(lower, "list_"), strings.HasPrefix(lower, "find_"),
		strings.HasPrefix(lower, "grep_"), strings.HasPrefix(lower, "browser_get_"),
		strings.HasPrefix(lower, "capture_browser_"):
		return hookActionRead
	}
	return hookActionUnknown
}

// resolveHookToolPath normalizes the file args agy uses across read/write
// tools. agy sometimes double-encodes string args, so quoted values are
// unquoted before resolving relative paths against the worktree.
func resolveHookToolPath(base string, args map[string]interface{}) string {
	raw := firstString(args["Path"], args["AbsolutePath"], args["TargetFile"],
		args["file_path"], args["File"], args["path"], args["DirectoryPath"], args["SearchPath"])
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = unquoted
		}
	}
	if raw == "" {
		return ""
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, strings.TrimPrefix(raw, "~"))
		}
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(base, raw)
	}
	return raw
}

// gateToolCall asks the user before a tool that can reach outside the
// worktree (MCP, browser JavaScript, explicit unsandboxed execution) runs.
func (s *Service) gateToolCall(r *http.Request, cred hookCredential, tool string, args map[string]interface{}, callID string) (bool, string) {
	key := hookToolGrantKey(tool, args)
	id := runnerPermissionID + randomHex(12)
	pretty, _ := json.MarshalIndent(args, "", "  ")
	meta := map[string]interface{}{
		"tool":   tool,
		"args":   string(pretty),
		"source": string(RuntimeAntigravity),
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
	answer := s.awaitReply(r, cred, id, req)
	switch answer.Reply {
	case "always":
		s.gate.addToolAlways(cred.sessionID, key)
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
