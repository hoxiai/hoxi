package runner

import (
	"path/filepath"
	"strings"
)

var claudeSkillCredentialNames = setOf("auth.json", "credentials.json", "token.json", "tokens.json", "secrets.json", "oauth.json")
var claudeSkillShellReadTools = setOf("cat", "head", "tail", "grep", "rg", "sed", "awk", "jq", "diff", "cmp", "stat", "file", "ls", "find", "wc", "nl", "cut")

// claudeSkillReadRoot validates the lexical scope before any exemption. Only a
// terminal /* or /** may describe a directory grant; actual file reads stay
// concrete so a wildcard cannot hide credentials or a symlink destination.
func claudeSkillReadRoot(raw string, directoryScope bool) (root, target string, ok bool) {
	slash := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	driveRoot := len(slash) >= 3 && slash[1] == ':' && slash[2] == '/' && ((slash[0] >= 'a' && slash[0] <= 'z') || (slash[0] >= 'A' && slash[0] <= 'Z'))
	if claudeSkillPathHasAmbiguousInput(raw) || slash == "" || strings.ContainsAny(slash, "\x00\r\n$`") ||
		(!strings.HasPrefix(slash, "/") && !driveRoot) {
		return "", "", false
	}
	parts := []string{}
	for _, part := range strings.Split(strings.TrimRight(slash, "/"), "/") {
		if part == ".." {
			return "", "", false
		}
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	if directoryScope && len(parts) > 0 && (parts[len(parts)-1] == "*" || parts[len(parts)-1] == "**") {
		parts = parts[:len(parts)-1]
	}
	index := -1
	for i, part := range parts {
		if strings.ContainsAny(part, "*?[]") {
			return "", "", false
		}
		if index < 0 && strings.EqualFold(part, ".claude") && i+1 < len(parts) && strings.EqualFold(parts[i+1], "skills") {
			index = i
		}
	}
	if index < 0 {
		return "", "", false
	}
	prefix := ""
	if strings.HasPrefix(slash, "/") {
		prefix = "/"
	}
	parent := prefix + strings.Join(parts[:index], "/")
	if isProtectedRelPath(parent) || isSensitiveExternalPath(parent) {
		return "", "", false
	}
	for _, part := range parts[:index] {
		if protectedWorktreeDirs[strings.ToLower(part)] {
			return "", "", false
		}
	}
	parentSlash := "/" + strings.ToLower(strings.TrimLeft(parent, "/")) + "/"
	for _, fragment := range sensitiveExternalFragments {
		if strings.Contains(parentSlash, fragment) {
			return "", "", false
		}
	}
	for _, prefix := range sensitiveExternalPrefixes {
		if strings.HasPrefix(parentSlash, prefix) {
			return "", "", false
		}
	}
	for _, part := range parts[index+2:] {
		lower := strings.ToLower(part)
		if protectedWorktreeDirs[lower] || sensitiveExternalBasenames[lower] || claudeSkillCredentialNames[lower] ||
			sensitiveEnvBasenameRe.MatchString(lower) || inlineKeyFileRe.MatchString(lower) {
			return "", "", false
		}
	}
	suffix := "/" + strings.ToLower(strings.Join(parts[index+2:], "/")) + "/"
	for _, fragment := range sensitiveExternalFragments {
		if strings.Contains(suffix, fragment) {
			return "", "", false
		}
	}
	if isProtectedRelPath(strings.Join(parts[index+2:], "/")) {
		return "", "", false
	}
	return prefix + strings.Join(parts[:index+2], "/"), prefix + strings.Join(parts, "/"), true
}

// The Go gate additionally resolves symlinks, including existing ancestors of
// wildcard/nonexistent paths. A nested link cannot escape the installed skill
// root. The root itself may be a normal user-installed link, never a secret root.
func isClaudeSkillReadPath(raw string, directoryScope bool) bool {
	root, target, ok := claudeSkillReadRoot(raw, directoryScope)
	if !ok {
		return false
	}
	root, target = filepath.FromSlash(root), filepath.FromSlash(target)
	if !filepath.IsAbs(root) && isAbsOrRootedPath(root) {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return false
		}
		target, err = filepath.Abs(target)
		if err != nil {
			return false
		}
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return false
	}
	if _, _, safe := claudeSkillReadRoot(filepath.ToSlash(target), false); !safe {
		return false
	}
	realRoot, rootOK := resolvePathWithSymlinks(root, 0)
	realTarget, targetOK := resolvePathWithSymlinks(target, 0)
	if !rootOK || !targetOK {
		return false
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if _, _, safe := claudeSkillReadRoot(filepath.Join(root, rel), false); !safe {
		return false
	}
	if realRoot != root || realTarget != target {
		if _, _, safe := claudeSkillReadRoot(realTarget, false); !safe &&
			(isSecretFilePathLexical(realTarget) || isSensitiveExternalReadPath(realTarget) || isProtectedShellReadPath(realTarget) || inlineKeyFileRe.MatchString(filepath.ToSlash(realTarget))) {
			return false
		}
	}
	return true
}

func claudeSkillPathHasAmbiguousInput(raw string) bool {
	if !strings.Contains("/"+strings.ToLower(strings.ReplaceAll(raw, "\\", "/"))+"/", "/.claude/skills/") {
		return false
	}
	wrongSeparator := strings.Contains(raw, "\\") && (filepath.Separator != '\\' || strings.HasPrefix(raw, "/"))
	return raw != strings.TrimSpace(raw) || wrongSeparator
}

// Inspect original path values before generic permission helpers trim them.
// A trailing-space filename can name a different symlink on the filesystem.
func hasNonCanonicalClaudeSkillPath(item map[string]interface{}) bool {
	return hasAmbiguousPermissionPathInput(item, claudeSkillPathHasAmbiguousInput)
}

// external_directory alone grants a range, not a write or execution. Requests
// carrying an edit, shell command or unknown tool cannot use the read exemption.
func isClaudeSkillReadRequest(item map[string]interface{}, target string) bool {
	req := parsePermissionRequest(item)
	toolObj, _ := item["tool"].(map[string]interface{})
	tool := strings.ToLower(strings.TrimSpace(firstString(req.meta["tool"], toolObj["name"])))
	if req.permission == "tool" && tool == "" {
		tool = strings.ToLower(req.firstPattern)
	}
	if _, hasCommands := req.meta["commands"].([]string); hasCommands {
		return false
	}
	if req.destructive || req.hasCommandField || editPermissions[req.permission] ||
		editToolNames[tool] || hookEditTools[tool] || editPermissions[tool] {
		return false
	}
	readTool := readPermissions[tool] || readToolNames[tool] || hookReadTools[tool]
	if req.permission == "external_directory" {
		return (tool == "" || readTool) && isClaudeSkillReadPath(target, true)
	}
	return (readPermissions[req.permission] || (req.permission == "tool" && readTool)) && isClaudeSkillReadPath(target, false)
}

// A range grant must not make `cat references/*` or a disguised credential
// symlink equivalent to a verified concrete read, or authorize executing a skill.
func hasUnsafeClaudeSkillShellAccess(command string) bool {
	isSkillPath := func(arg string) bool {
		if !isAbsOrRootedPath(arg) && !strings.HasPrefix(arg, "~/") {
			return false
		}
		return strings.Contains(strings.ToLower(strings.ReplaceAll(arg, "\\", "/"))+"/", "/.claude/skills/")
	}
	for _, segment := range splitCommandChain(command) {
		words := stripShellKeywords(shellWords(segment))
		for len(words) > 0 && envVarAssignmentWordRe.MatchString(words[0]) {
			words = words[1:]
		}
		words, lookup, ok := unwrapCommandPrefixes(words)
		if !ok || lookup || len(words) == 0 {
			continue
		}
		if isSkillPath(words[0]) {
			return true // a directory read grant never authorizes executing a skill
		}
		readTool := claudeSkillShellReadTools[filepath.Base(words[0])] &&
			(!strings.Contains(words[0], "/") || trustedToolDirs()[filepath.Clean(filepath.Dir(words[0]))])
		for _, arg := range words[1:] {
			if strings.HasPrefix(arg, "-") {
				if _, value, attached := strings.Cut(arg, "="); attached {
					arg = value
				}
			}
			if isSkillPath(arg) && (!readTool || !isClaudeSkillReadPath(arg, false)) {
				return true
			}
		}
	}
	return false
}
