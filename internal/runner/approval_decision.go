package runner

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// The single place that decides whether a permission request may run without
// asking the user. Every runtime goes through it: OpenCode's permission.asked
// events, the Claude Code / Antigravity PreToolUse gates, and the pending list
// served to the desktop, CLI and phone. The web UI only displays the result.
//
// isDestructivePermissionRequest mirrors the function of the same name in
// .opencode/lib/permission-policy.js, which the UI uses to mark approval cards;
// .opencode/lib/permission-request.cases.json keeps both ports aligned.

const (
	ApprovalModeAuto   = "auto"
	ApprovalModeSmart  = "smart"
	ApprovalModeManual = "manual"

	permissionCategoryRead     = "read"
	permissionCategoryEdit     = "edit"
	permissionCategoryWebfetch = "webfetch"
	permissionCategoryBash     = "bash"
	permissionCategoryOther    = "other"
)

var (
	readPermissions = setOf("read", "glob", "grep", "list", "lsp", "todoread")

	readToolNames = setOf("read", "glob", "grep", "list", "lsp", "todoread", "hoxi_code_index",
		"ls", "notebookread", "notebook_read", "read_file", "view_file",
		"list_dir", "find_by_name", "grep_search", "read_resource", "list_resources",
		"read_browser_page", "browser_get_dom", "browser_get_network_request",
		"browser_list_network_requests", "list_browser_pages", "capture_browser_console_logs",
		"capture_browser_screenshot", "list_permissions", "list_plugin_accounts",
		"search_marketplace", "command_status")

	editToolNames = setOf("edit", "write", "patch", "multiedit", "apply_patch", "notebookedit",
		"notebook_edit", "write_to_file", "write_file", "create_file", "edit_file",
		"replace_file_content", "multi_replace_file_content", "sed_file",
		"insert_edit_into_file", "str_replace_editor", "str_replace_based_edit_tool")

	// editPermissions are the permission names whose targets are file paths.
	editPermissions = setOf("edit", "write", "patch", "multiedit", "apply_patch", "notebookedit",
		"notebook_edit", "write_to_file", "write_file", "create_file", "edit_file",
		"replace_file_content", "multi_replace_file_content", "sed_file")

	webfetchToolNames = setOf("webfetch", "websearch", "codesearch", "web_fetch", "web_search",
		"fetch_url", "read_url_content", "search_web", "execute_url")

	bashToolNames = setOf("bash", "shell", "run_command", "execute_command", "run_terminal_cmd",
		"sh", "zsh", "cmd")

	// smartControlPermissions run without asking in smart mode: they only
	// steer the agent and never touch the workspace on their own.
	smartControlPermissions = setOf("todowrite", "task", "skill", "external_directory")

	toolArgPathKeys = []string{"Path", "AbsolutePath", "TargetFile", "target_file", "file_path", "filePath",
		"filepath", "File", "file", "path", "target", "DirectoryPath", "directory_path",
		"SearchPath", "search_path", "SearchDirectory", "search_directory",
		"Directory", "directory", "NotebookPath", "notebook_path"}

	editTargetKeys = []string{"filepath", "filePath", "path", "target", "Path", "AbsolutePath",
		"TargetFile", "target_file", "file_path", "File", "file", "NotebookPath", "notebook_path"}

	webfetchTargetKeys = []string{"url", "Url", "URL", "uri", "URI", "domain", "Domain", "query", "Query"}

	// The OpenCode sandbox plugin rewrites bash commands into a sandbox-exec
	// wrapper; the wrapped command is what the policy judges.
	sandboxWrapRe       = regexp.MustCompile(`^'?/usr/bin/sandbox-exec'?\s+-f\s+(?:'[^']*'|\S+)\s+--\s+/bin/sh\s+-c\s+'((?:[^']|'\\'')*)'$`)
	sandboxWrapPrefixRe = regexp.MustCompile(`^'?/usr/bin/sandbox-exec'?\s+-f\s+(?:'[^']*'|\S+)\s+--\s+(?:/bin/sh|sh)\s+-c\s+'`)

	// A `# hoxi-stop:` tag line, the plugin's stop trap line (openCodeStopTrap),
	// its USR1 half when OpenCode splits `trap ... USR1; trap : EXIT` at `;`
	// into `patterns`, or the older bare `trap : EXIT`.
	openCodeStopMarkerRe = regexp.MustCompile(`^(?:[ \t]*(?:# hoxi-stop:[^\r\n]*(?:\r?\n(?:[ \t]*(?:` +
		regexp.QuoteMeta(openCodeStopTrap) + `|` + regexp.QuoteMeta(openCodeStopTrapUSR1) + `|trap : (?:EXIT|\S+))(?:[ \t]*\r?\n|$))?|$)|(?:` +
		regexp.QuoteMeta(openCodeStopTrap) + `|` + regexp.QuoteMeta(openCodeStopTrapUSR1) + `|trap : EXIT)(?:[ \t]*\r?\n|[ \t]+|$)))+`)
)

// approvalAutoApproves reports whether item may run without asking the user
// under pol. rules is the OpenCode `permission` block, whose pattern objects
// (written by "always allow" on an approval card) apply to every runtime.
// worktree scopes a leading `cd` in smart-mode command checks.
func approvalAutoApproves(item map[string]interface{}, pol ApprovalPolicy, rules map[string]interface{}, worktree string) bool {
	return approvalAutoApprovesIsolated(item, pol, rules, worktree, false)
}

// approvalAutoApprovesIsolated is approvalAutoApproves for a session whose
// confinement is already known. isolated is true only when the session is a
// Hoxi worktree and its runtime commands are OS-sandboxed; unattended mode
// then allows everything the hard-refuse list does not name.
func approvalAutoApprovesIsolated(item map[string]interface{}, pol ApprovalPolicy, rules map[string]interface{}, worktree string, isolated bool) bool {
	if pol.Mode == ApprovalModeUnattended && isolated {
		allow, _ := unattendedDecision(item, worktree)
		return allow
	}
	// Full trust also lets interpreter scripts through (see isOpaqueShellCommandWith).
	if isOpaquePermissionRequestWith(item, worktree, pol.Mode == ApprovalModeTrusted || pol.Mode == ApprovalModeUnattended) {
		return false
	}
	if isDestructivePermissionRequest(item) &&
		!((pol.Mode == ApprovalModeTrusted || pol.Mode == ApprovalModeUnattended) && trustedRecoverableRequest(item, worktree)) {
		return false
	}
	perm := permissionName(item)
	if readPermissions[perm] {
		return true
	}
	meta := itemMetadata(item)
	external, _ := meta["externalDirectory"].(bool)
	category := permissionCategory(item)
	if category == permissionCategoryRead && !external {
		return true
	}
	switch category {
	case permissionCategoryEdit, permissionCategoryWebfetch:
		targets := categoryTargets(item, category)
		if category == permissionCategoryEdit && len(targets) == 0 {
			// Without a path the edit's reach cannot be checked.
			return false
		}
		if decision, ok := ruleDecision(rules[category], targets, category); ok {
			return decision == "allow"
		}
		return pol.effectiveRule(category) == "allow"
	case permissionCategoryBash:
		if decision, ok := ruleDecision(rules[category], categoryTargets(item, category), category); ok {
			return decision == "allow"
		}
		switch pol.effectiveRule(category) {
		case "allow":
			return true
		case "smart":
			return isSafeCommandPermissionRequest(item, worktree)
		}
		return false
	}
	switch pol.Mode {
	case ApprovalModeManual:
		return false
	case ApprovalModeSmart:
		if category == permissionCategoryRead || smartControlPermissions[perm] {
			return true
		}
		if perm == "tool" {
			tool := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], firstPattern(item))))
			return readToolNames[tool] || smartControlPermissions[tool]
		}
		return false
	}
	return true
}

func permissionName(item map[string]interface{}) string {
	return strings.ToLower(strings.TrimSpace(firstString(item["permission"], item["type"])))
}

func itemMetadata(item map[string]interface{}) map[string]interface{} {
	meta, _ := item["metadata"].(map[string]interface{})
	if meta == nil {
		return map[string]interface{}{}
	}
	return meta
}

func firstPattern(item map[string]interface{}) string {
	if patterns := stringList(item["patterns"]); len(patterns) > 0 {
		return patterns[0]
	}
	return ""
}

func stringList(v interface{}) []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	switch list := v.(type) {
	case string:
		add(list)
	case []string:
		for _, s := range list {
			add(s)
		}
	case []interface{}:
		for _, raw := range list {
			if s, ok := raw.(string); ok {
				add(s)
			}
		}
	}
	return out
}

func classifyToolName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case lower == "":
		return ""
	case readToolNames[lower]:
		return permissionCategoryRead
	case editToolNames[lower]:
		return permissionCategoryEdit
	case webfetchToolNames[lower]:
		return permissionCategoryWebfetch
	case bashToolNames[lower]:
		return permissionCategoryBash
	}
	return ""
}

// permissionCategory maps OpenCode permission names and every runtime's tool
// names onto the four categories the approval policy configures.
func permissionCategory(item map[string]interface{}) string {
	if c := classifyToolName(permissionName(item)); c != "" {
		return c
	}
	meta := itemMetadata(item)
	tool, _ := item["tool"].(map[string]interface{})
	for _, name := range []string{firstString(meta["tool"]), firstString(tool["name"]), firstPattern(item), firstString(item["pattern"])} {
		if c := classifyToolName(name); c != "" {
			return c
		}
	}
	return permissionCategoryOther
}

func stripOpenCodeStopMarkerText(raw string) string {
	if !strings.Contains(raw, stopMarkerPrefix) && !strings.Contains(raw, "trap : EXIT") && !strings.Contains(raw, openCodeStopTrapUSR1) {
		return raw
	}
	return openCodeStopMarkerRe.ReplaceAllString(raw, "")
}

func stripStopMarkerValue(v interface{}) (interface{}, bool) {
	switch val := v.(type) {
	case string:
		cleaned := unwrapSandboxCommand(val)
		return cleaned, cleaned != val
	case []string:
		changed := false
		out := make([]string, 0, len(val))
		for _, s := range val {
			cleaned := unwrapSandboxCommand(s)
			if cleaned != s {
				changed = true
			}
			if strings.TrimSpace(cleaned) != "" || strings.TrimSpace(s) == "" {
				out = append(out, cleaned)
			}
		}
		if !changed {
			return val, false
		}
		return out, true
	case []interface{}:
		changed := false
		out := make([]interface{}, 0, len(val))
		for _, elem := range val {
			if s, ok := elem.(string); ok {
				cleaned := unwrapSandboxCommand(s)
				if cleaned != s {
					changed = true
				}
				if strings.TrimSpace(cleaned) != "" || strings.TrimSpace(s) == "" {
					out = append(out, cleaned)
				}
			} else {
				out = append(out, elem)
			}
		}
		if !changed {
			return val, false
		}
		return out, true
	}
	return v, false
}

func sanitizeOpenCodePermissionItem(item map[string]interface{}) bool {
	if item == nil {
		return false
	}
	changed := false
	for _, key := range []string{"patterns", "pattern", "always", "title"} {
		if v, exists := item[key]; exists {
			if cleaned, ok := stripStopMarkerValue(v); ok {
				item[key] = cleaned
				changed = true
			}
		}
	}
	if meta, ok := item["metadata"].(map[string]interface{}); ok && meta != nil {
		for _, key := range []string{"command", "cmd", "commands"} {
			if v, exists := meta[key]; exists {
				if cleaned, ok := stripStopMarkerValue(v); ok {
					meta[key] = cleaned
					changed = true
				}
			}
		}
	}
	return changed
}

func unwrapSandboxCommand(command string) string {
	cur := command
	for i := 0; i < 8; i++ {
		next := stripOpenCodeStopMarkerText(cur)
		trimmed := strings.TrimSpace(next)
		if m := sandboxWrapRe.FindStringSubmatch(trimmed); m != nil {
			next = stripOpenCodeStopMarkerText(strings.ReplaceAll(m[1], `'\''`, "'"))
		} else if strings.HasSuffix(trimmed, "…") {
			if loc := sandboxWrapPrefixRe.FindStringIndex(trimmed); loc != nil {
				next = stripOpenCodeStopMarkerText(strings.ReplaceAll(trimmed[loc[1]:], `'\''`, "'"))
			}
		}
		if next == cur {
			break
		}
		cur = next
	}
	return cur
}

// commandCandidates mirrors commandCandidates in permission-policy.js.
func commandCandidates(item map[string]interface{}) []string {
	meta := itemMetadata(item)
	var raw []string
	for _, v := range []interface{}{item["patterns"], item["pattern"], meta["command"], meta["cmd"], meta["commands"]} {
		raw = append(raw, stringList(v)...)
	}
	if len(raw) == 0 {
		raw = stringList(item["title"])
	}
	out := make([]string, 0, len(raw))
	for _, cmd := range raw {
		if cleaned := strings.TrimSpace(unwrapSandboxCommand(cmd)); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out
}

func parseToolArgs(raw interface{}) map[string]interface{} {
	switch v := raw.(type) {
	case map[string]interface{}:
		return v
	case string:
		if strings.HasPrefix(strings.TrimSpace(v), "{") {
			var parsed map[string]interface{}
			if json.Unmarshal([]byte(v), &parsed) == nil {
				return parsed
			}
		}
	}
	return nil
}

func toolArgPaths(raw interface{}) []string {
	args := parseToolArgs(raw)
	var out []string
	for _, key := range toolArgPathKeys {
		out = append(out, stringList(args[key])...)
	}
	return out
}

func metadataFilePaths(meta map[string]interface{}) []string {
	var out []string
	if files, ok := meta["files"].([]interface{}); ok {
		for _, f := range files {
			if m, ok := f.(map[string]interface{}); ok {
				out = append(out, stringList(firstString(m["filePath"], m["filepath"], m["file"], m["path"]))...)
			}
		}
	}
	return out
}

// isDestructivePermissionRequest mirrors isDestructivePermissionRequest in
// permission-policy.js: requests that must reach the user in every mode.
func isDestructivePermissionRequest(item map[string]interface{}) bool {
	perm := permissionName(item)
	meta := itemMetadata(item)
	if perm == "doom_loop" {
		return true
	}
	if perm == "external_directory" {
		return !externalDirectoryTargetsAreRoutine(item)
	}
	if destructive, _ := meta["destructive"].(bool); destructive {
		return true
	}
	if external, _ := meta["externalDirectory"].(bool); external && !externalDirectoryTargetsAreRoutine(item) {
		return true
	}
	if readPermissions[perm] {
		// Search patterns are not paths; only a file read can expose a secret.
		if perm != "read" {
			return false
		}
		for _, p := range readTargets(item) {
			if isSecretFilePath(p) && !isOwnSessionRecordRead(item, p) {
				return true
			}
		}
		return false
	}
	if editPermissions[perm] || perm == "tool" {
		tool := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], firstPattern(item))))
		if tool == "unsandboxed" {
			return true
		}
		var candidates []string
		if perm != "tool" {
			candidates = append(candidates, stringList(item["patterns"])...)
			candidates = append(candidates, stringList(item["pattern"])...)
		}
		for _, key := range []string{"filepath", "filePath", "path", "target"} {
			candidates = append(candidates, stringList(meta[key])...)
		}
		candidates = append(candidates, metadataFilePaths(meta)...)
		candidates = append(candidates, toolArgPaths(meta["args"])...)
		for _, p := range candidates {
			if (isProtectedRelPath(p) || isSensitiveExternalPath(p)) && !isOwnSessionRecordRead(item, p) {
				return true
			}
		}
		if editPermissions[perm] {
			return false
		}
		_, hasCommand := meta["command"].(string)
		_, hasCmd := meta["cmd"].(string)
		_, hasCommands := meta["commands"].([]interface{})
		if !hasCommand && !hasCmd && !hasCommands {
			return false
		}
	}
	var scanned []string
	for _, cmd := range commandCandidates(item) {
		scanned = append(scanned, destructiveScanForms(cmd)...)
	}
	blob := perm + "\n" + strings.Join(scanned, "\n")
	return matchesDestructive(blob) || matchesDestructive(deobfuscator.Replace(blob))
}

func readTargets(item map[string]interface{}) []string {
	meta := itemMetadata(item)
	var out []string
	for _, v := range []interface{}{item["patterns"], item["pattern"], meta["filepath"], meta["filePath"], meta["path"]} {
		out = append(out, stringList(v)...)
	}
	return out
}

func isOpaquePermissionRequest(item map[string]interface{}, worktree string) bool {
	return isOpaquePermissionRequestWith(item, worktree, false)
}

func isOpaquePermissionRequestWith(item map[string]interface{}, worktree string, allowInlineScripts bool) bool {
	if worktree == "" {
		worktree = firstString(itemMetadata(item)["worktree"])
	}
	category := permissionCategory(item)
	candidates := opacityCandidates(item)
	if category != permissionCategoryBash {
		meta := itemMetadata(item)
		_, hasCommand := meta["command"].(string)
		_, hasCmd := meta["cmd"].(string)
		_, hasCommands := meta["commands"].([]interface{})
		if !hasCommand && !hasCmd && !hasCommands {
			return false
		}
	}
	if len(candidates) == 0 {
		return true
	}
	for _, cmd := range candidates {
		if isOpaqueShellCommandWith(cmd, worktree, allowInlineScripts) {
			return true
		}
	}
	return false
}

func isSafeCommandPermissionRequest(item map[string]interface{}, worktree string) bool {
	if isDestructivePermissionRequest(item) {
		return false
	}
	meta := itemMetadata(item)
	if worktree == "" {
		worktree = firstString(meta["worktree"])
	}
	candidates := commandCandidates(item)
	if cmd := strings.TrimSpace(unwrapSandboxCommand(firstString(meta["command"]))); cmd != "" {
		filtered := []string{cmd}
		for _, piece := range candidates {
			if piece != "" && !strings.Contains(cmd, piece) {
				filtered = append(filtered, piece)
			}
		}
		candidates = filtered
	}
	if len(candidates) == 0 {
		return false
	}
	for _, cmd := range candidates {
		if !isSafeCommand(cmd, worktree) {
			return false
		}
	}
	return true
}

func categoryTargets(item map[string]interface{}, category string) []string {
	if category == permissionCategoryBash {
		return commandCandidates(item)
	}
	perm := permissionName(item)
	meta := itemMetadata(item)
	tool, _ := item["tool"].(map[string]interface{})
	toolName := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], tool["name"])))
	seen := map[string]bool{}
	var out []string
	add := func(values ...string) {
		for _, v := range values {
			if perm == "tool" && (strings.ToLower(v) == toolName || classifyToolName(v) != "") {
				continue
			}
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	add(stringList(item["patterns"])...)
	add(stringList(item["pattern"])...)
	args := parseToolArgs(meta["args"])
	keys := webfetchTargetKeys
	if category == permissionCategoryEdit {
		keys = editTargetKeys
		add(metadataFilePaths(meta)...)
	}
	for _, key := range keys {
		add(stringList(meta[key])...)
		add(stringList(args[key])...)
	}
	return out
}

// ruleDecision applies an OpenCode permission entry to the request targets.
// Only an explicit answer counts: "deny" anywhere (including "*":"deny") or a
// specific pattern match. A rule object that says nothing about these targets
// leaves the decision to the approval mode, so one "always allow" pattern
// never turns every other command into a prompt. String "allow"/"ask" values
// and the "*":"ask" catch-all in pattern objects are Hoxi's own enforcement
// defaults (see provider.normalizeApprovalPermissions), not user choices.
func ruleDecision(entry interface{}, targets []string, category string) (string, bool) {
	switch v := entry.(type) {
	case string:
		if v == "deny" {
			return "deny", true
		}
		return "", false
	case map[string]interface{}:
		if len(targets) == 0 {
			return "", false
		}
		wildcard := firstString(v["*"], v["**"])
		allAllowed := true
		for _, target := range targets {
			matched := ""
			for pattern, raw := range v {
				decision, _ := raw.(string)
				p := strings.TrimSpace(pattern)
				if p == "*" || p == "**" || !matchesRulePattern(p, target, category) {
					continue
				}
				if decision == "deny" || (decision == "ask" && matched != "deny") || (decision == "allow" && matched == "") {
					matched = decision
				}
			}
			if matched == "" && wildcard == "deny" {
				matched = "deny"
			}
			switch matched {
			case "deny", "ask":
				return matched, true
			case "allow":
			default:
				allAllowed = false
			}
		}
		if allAllowed {
			return "allow", true
		}
	}
	return "", false
}

func globToRegexp(pattern string, loose bool) *regexp.Regexp {
	var b strings.Builder
	if loose {
		b.WriteString("(?is)^")
	} else {
		b.WriteString("(?i)^")
	}
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if loose {
				for i+1 < len(pattern) && pattern[i+1] == '*' {
					i++
				}
				b.WriteString(".*")
			} else if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				b.WriteString(".*")
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

func matchesRulePattern(pattern, target, category string) bool {
	p, t := strings.TrimSpace(pattern), strings.TrimSpace(target)
	if p == "" || t == "" {
		return false
	}
	if p == "*" || p == "**" || strings.EqualFold(p, t) {
		return true
	}
	if re := globToRegexp(p, false); re != nil && re.MatchString(t) {
		return true
	}
	loose := globToRegexp(p, true)
	if loose != nil && loose.MatchString(t) {
		return true
	}
	if category != permissionCategoryWebfetch {
		return false
	}
	raw := t
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	lowerP := strings.ToLower(p)
	if host == lowerP {
		return true
	}
	if strings.HasPrefix(lowerP, "*.") && (host == lowerP[2:] || strings.HasSuffix(host, lowerP[1:])) {
		return true
	}
	return loose != nil && (loose.MatchString(host) || loose.MatchString(parsed.Scheme+"://"+parsed.Host))
}
