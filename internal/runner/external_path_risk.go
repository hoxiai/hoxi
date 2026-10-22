package runner

import (
	"encoding/json"
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
		if isSensitiveExternalPath(target) {
			return false
		}
	}
	return true
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
