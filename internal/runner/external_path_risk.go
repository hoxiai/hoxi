package runner

import (
	"encoding/json"
	"path/filepath"
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
	for _, raw := range []interface{}{item["patterns"], item["pattern"], meta["filepath"], meta["filePath"], meta["path"], meta["target"], meta["directories"]} {
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
	".curlrc": true, ".wgetrc": true, ".envrc": true, "authorized_keys": true,
}

var sensitiveExternalFragments = []string{
	"/.config/opencode/",
	"/.config/hoxi/",
	"/.config/claude/",
	"/.ssh/",
	"/.aws/",
	"/.gnupg/",
	"/.kube/",
	"/library/keychains/",
}

var sensitiveExternalPrefixes = []string{
	"/etc/",
	"/private/etc/",
	"/library/launchagents/",
	"/library/launchdaemons/",
	"/library/startupitems/",
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
		slash := strings.ToLower(filepath.ToSlash(cand))
		base := slash[strings.LastIndex(slash, "/")+1:]
		if sensitiveExternalBasenames[base] {
			return true
		}
		for _, prefix := range sensitiveExternalPrefixes {
			if strings.HasPrefix(slash, prefix) {
				return true
			}
		}
		for _, fragment := range sensitiveExternalFragments {
			if strings.Contains(slash, fragment) {
				return true
			}
		}
		if isDestructiveCommand(cand) {
			return true
		}
	}
	return false
}
