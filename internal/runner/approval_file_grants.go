package runner

import (
	"path/filepath"
	"sort"
	"strings"
)

// Session file grants let the approval card's session button cover an
// OpenCode edit that asks in every mode only because it touches a protected
// file (.agents/**, a skill script, .vscode/settings.json). The grant
// is for that exact file, resolved through symlinks, in one session: the
// next edit of the same file runs unasked and any other file still asks.
// OpenCode itself only ever gets a one-time approval, since its own "always"
// for an edit is `*` and would stop asking for every file.
//
// Files that hold secrets or steer the approval and sandbox themselves are
// never granted, so a session cannot quietly switch its own confirmation
// off: secrets and credential stores, .git (hooks, config), .opencode
// (plugins, permission rules), .hoxi except skills, .claude, .gemini, hook
// configs and hook scripts, and system paths. Those keep asking once per edit.

const (
	fileGrantPrefix = "file:"
	grantKindFile   = "file"
)

// fileGrantRefusedDirs are path parts that make a file ungrantable.
var fileGrantRefusedDirs = map[string]bool{
	".git":      true,
	".opencode": true,
	".hoxi":     true,
	".claude":   true,
	".gemini":   true,
}

func isFileGrantClass(class string) bool {
	return strings.HasPrefix(class, fileGrantPrefix) && len(class) > len(fileGrantPrefix)
}

// grantableEditFiles returns the sorted file grants an OpenCode edit request
// needs within dir, or nil when the request is not a protected edit or any
// of its files may not be granted.
func grantableEditFiles(item map[string]interface{}, dir string) []string {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) || !editPermissions[permissionName(item)] {
		return nil
	}
	req := parsePermissionRequest(item)
	if req.destructive || req.externalDir || !isDestructivePermissionRequest(item) {
		return nil
	}
	var raw []string
	raw = append(raw, req.patterns...)
	raw = append(raw, req.pattern...)
	if _, multi := req.meta["files"].([]interface{}); multi {
		// apply_patch joins its files into filepath ("a, b"); files lists them.
		raw = append(raw, metadataFilePaths(req.meta)...)
	} else {
		for _, key := range []string{"filepath", "filePath", "path", "target"} {
			raw = append(raw, stringList(req.meta[key])...)
		}
	}
	raw = append(raw, toolArgPaths(req.meta["args"])...)
	if len(raw) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range raw {
		target, ok := fileGrantTarget(p, dir)
		if !ok {
			return nil
		}
		if class := fileGrantPrefix + target; !seen[class] {
			seen[class] = true
			out = append(out, class)
		}
	}
	sort.Strings(out)
	return out
}

// fileGrantTarget resolves one edit target against dir and through symlinks.
// It fails for globs, unresolvable paths and files that may never be granted,
// judged as written, as joined and as resolved.
func fileGrantTarget(raw, dir string) (string, bool) {
	p := strings.TrimSpace(raw)
	if p == "" || strings.HasPrefix(p, "~") || strings.ContainsAny(p, "*?[\x00\n") {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)
	resolved, ok := resolvePathWithSymlinks(p, 0)
	if !ok || !filepath.IsAbs(resolved) {
		return "", false
	}
	for _, cand := range []string{raw, p, resolved} {
		if fileGrantRefused(cand) {
			return "", false
		}
	}
	return resolved, true
}

func fileGrantRefused(p string) bool {
	if isSecretFilePath(p) {
		return true
	}
	slash := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(p))))
	dirSlash := "/" + strings.TrimLeft(slash, "/") + "/"
	for _, prefix := range sensitiveExternalPrefixes {
		if strings.HasPrefix(dirSlash, prefix) {
			return true
		}
	}
	parts := strings.Split(stripWorktreePrefix(slash), "/")
	for i, part := range parts {
		// Hook configs and hook scripts decide what an agent runs unasked.
		if part == "hooks" || (part == "hooks.json" && i == len(parts)-1) {
			return true
		}
		if !fileGrantRefusedDirs[part] {
			continue
		}
		// .hoxi/skills holds skills, not approval state.
		if part == ".hoxi" && i+1 < len(parts) && parts[i+1] == "skills" {
			continue
		}
		return true
	}
	return false
}
