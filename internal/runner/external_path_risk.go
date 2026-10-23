package runner

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// externalDirectoryTargetsAreRoutine reports whether every path an
// external_directory request touches is an ordinary workspace/sibling path.
// OpenCode raises this permission for any read or write outside the session
// directory, so treating it as unconditionally destructive made "auto approve"
// prompt on nearly every cross-folder command. Credential files and
// execution-controlling paths still count as destructive.
func externalDirectoryTargetsAreRoutine(item map[string]interface{}) bool {
	targets := externalDirectoryTargets(item)
	if len(targets) == 0 {
		return false
	}
	for _, target := range targets {
		if isSensitiveExternalPath(target) && !isOwnSessionRecordRead(item, target) {
			return false
		}
	}
	return true
}

// isOwnSessionRecordRead reports a read (or OpenCode external_directory) whose
// target is exactly the requesting session's own record directory
// (~/.hoxi/sessions/<id>/). Edits stay sensitive. With no session id, or for
// any other session, it returns false. Both the Hoxi id and the runtime
// ExternalID match.
func isOwnSessionRecordRead(item map[string]interface{}, rawPath string) bool {
	if !pathUnderOwnSession(rawPath, requestSessionIDs(item)) {
		return false
	}
	perm := permissionName(item)
	meta := itemMetadata(item)
	tool := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], firstPattern(item))))
	if editPermissions[perm] || editPermissions[tool] || editToolNames[perm] || editToolNames[tool] || hookEditTools[tool] {
		return false
	}
	if perm == "external_directory" {
		return true
	}
	return perm == "read" || readPermissions[perm] || readToolNames[perm] || readToolNames[tool] || hookReadTools[tool]
}

func requestSessionIDs(item map[string]interface{}) []string {
	meta := itemMetadata(item)
	var ids []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if !validSessionID(v) {
			return
		}
		for _, have := range ids {
			if have == v {
				return
			}
		}
		ids = append(ids, v)
	}
	add(firstString(item["sessionID"]))
	add(firstString(item["externalID"]))
	add(firstString(meta["sessionID"]))
	add(firstString(meta["externalID"]))
	add(firstString(meta["externalId"]))
	return ids
}

func validSessionID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 200 {
		return false
	}
	return !strings.ContainsAny(id, "/\\:*?")
}

// pathUnderOwnSession reports whether raw is the session directory itself or
// a file inside it. `..` is rejected rather than cleaned away.
func pathUnderOwnSession(raw string, ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	for _, cand := range sessionPathCandidates(raw) {
		if sessionPathMatches(cand, ids) {
			return true
		}
	}
	return false
}

func sessionPathCandidates(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	out := []string{trimmed}
	stripped := strings.TrimRight(strings.TrimRight(trimmed, "*"), "/")
	if stripped != "" && stripped != trimmed {
		out = append(out, stripped)
	}
	return out
}

func sessionPathMatches(raw string, ids []string) bool {
	slash := filepath.ToSlash(strings.TrimSpace(raw))
	if strings.HasPrefix(slash, "~/") {
		slash = "HOME/" + strings.TrimPrefix(slash, "~/")
	}
	if strings.Contains(slash, "..") {
		return false
	}
	cleaned := path.Clean(slash)
	parts := strings.Split(cleaned, "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != ".hoxi" || parts[i+1] != "sessions" {
			continue
		}
		id := parts[i+2]
		for _, want := range ids {
			if id == want {
				return true
			}
		}
	}
	return false
}

func externalDirectoryTargets(item map[string]interface{}) []string {
	meta, _ := item["metadata"].(map[string]interface{})
	out := make([]string, 0, 4)
	add := func(v interface{}) {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	for _, raw := range []interface{}{
		item["patterns"], item["pattern"], item["always"],
		meta["filepath"], meta["filePath"], meta["path"], meta["target"],
		meta["parentDir"], meta["dir"], meta["directories"],
	} {
		switch v := raw.(type) {
		case string:
			add(v)
		case []string:
			for _, s := range v {
				add(s)
			}
		case []interface{}:
			for _, s := range v {
				add(s)
			}
		}
	}
	for _, f := range metadataFilePaths(meta) {
		add(f)
	}
	if rawArgs := firstString(meta["args"]); strings.HasPrefix(strings.TrimSpace(rawArgs), "{") {
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(rawArgs), &parsed) == nil {
			if p := rawHookToolPath(parsed); p != "" {
				add(p)
			}
		}
	}
	return out
}

var sensitiveExternalBasenames = map[string]bool{
	".zshrc": true, ".zshenv": true, ".zprofile": true, ".zlogin": true,
	".bashrc": true, ".bash_profile": true, ".bash_login": true, ".profile": true,
	".gitconfig": true, ".git-credentials": true, ".netrc": true, ".npmrc": true,
	".pypirc": true, ".curlrc": true, ".wgetrc": true, ".envrc": true, "authorized_keys": true,
	"id_rsa": true, "id_ed25519": true, "id_ecdsa": true, "id_dsa": true,
	".credentials.json": true,
}

var sensitiveEnvBasenameRe = regexp.MustCompile(`(?i)^\.en(?:v(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?|stag(?:e|ing)|test(?:ing)?|ci|preview|secret\w*|key\w*|private\w*|bak|backup)(?:\.local)?)?|\?|\*)$`)

var sensitiveExternalFragments = []string{
	"/.config/opencode/",
	"/.config/hoxi/",
	"/.config/claude/",
	"/.config/gh/",
	"/.config/gcloud/",
	"/.hoxi/",
	"/.claude/",
	"/.ssh/",
	"/.aws/",
	"/.gnupg/",
	"/.kube/",
	"/.docker/",
	"/.azure/",
	"/library/keychains/",
	"/library/launchagents/",
	"/library/launchdaemons/",
	"/library/startupitems/",
}

var sensitiveExternalPrefixes = []string{
	"/etc/",
	"/private/etc/",
	"/root/",
	"/var/root/",
	"/private/var/root/",
}

func isRoutineHoxiSubpath(slash string) bool {
	return strings.Contains(slash, "/.hoxi/skills/") ||
		strings.Contains(slash, "/.hoxi/attachments/") ||
		strings.HasPrefix(slash, ".hoxi/skills/") ||
		strings.HasPrefix(slash, ".hoxi/attachments/")
}

func isSensitiveExternalPath(rawPath string) bool {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return false
	}
	candidates := []string{trimmed}
	stripped := strings.TrimRight(strings.TrimRight(trimmed, "*"), "/")
	if stripped != "" && stripped != trimmed {
		candidates = append(candidates, stripped)
	}
	for _, cand := range candidates {
		if isProtectedRelPath(cand) {
			return true
		}
		rawSlash := strings.TrimRight(strings.ToLower(filepath.ToSlash(cand)), "/")
		slash := strings.TrimRight(stripWorktreePrefix(rawSlash), "/")
		if slash == "" {
			slash = "/"
		}
		slashDir := slash + "/"
		if slash == "/" {
			slashDir = "/"
		}
		base := slash[strings.LastIndex(slash, "/")+1:]
		if sensitiveExternalBasenames[base] || sensitiveEnvBasenameRe.MatchString(base) {
			return true
		}
		if isRoutineHoxiSubpath(slash) {
			continue
		}
		for _, prefix := range sensitiveExternalPrefixes {
			if strings.HasPrefix(slashDir, prefix) {
				return true
			}
		}
		normDir := "/" + strings.TrimLeft(slashDir, "/")
		for _, fragment := range sensitiveExternalFragments {
			if strings.Contains(normDir, fragment) {
				return true
			}
		}
	}
	return false
}

// isSecretFilePath reports whether reading p could expose credentials: .env
// variants, shell/credential dotfiles and key directories. It is narrower than
// isSensitiveExternalPath, so reading .git or /etc/hosts inside a session
// stays routine while `.env` asks in every mode, matching OpenCode's own
// read defaults (`*.env`, `*.env.*` ask; `*.env.example` allowed).
func isSecretFilePath(p string) bool {
	if isSecretFilePathLexical(p) {
		return true
	}
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if filepath.IsAbs(cleaned) {
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && resolved != "" && resolved != cleaned {
			if isSecretFilePathLexical(resolved) {
				return true
			}
		}
		if resolved, ok := resolvePathWithSymlinks(cleaned, 0); ok && resolved != "" && resolved != cleaned {
			if isSecretFilePathLexical(resolved) {
				return true
			}
		}
	}
	return false
}

func isSecretFilePathLexical(p string) bool {
	rawSlash := strings.TrimRight(strings.ToLower(filepath.ToSlash(strings.TrimSpace(p))), "/")
	slash := strings.TrimRight(stripWorktreePrefix(rawSlash), "/")
	if slash == "" {
		return false
	}
	base := slash[strings.LastIndex(slash, "/")+1:]
	if sensitiveExternalBasenames[base] || sensitiveEnvBasenameRe.MatchString(base) {
		return true
	}
	if isRoutineHoxiSubpath(slash) {
		return false
	}
	norm := "/" + strings.TrimLeft(slash, "/") + "/"
	for _, fragment := range sensitiveExternalFragments {
		if strings.Contains(norm, fragment) {
			return true
		}
	}
	return false
}
