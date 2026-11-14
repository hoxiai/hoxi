package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func attributionPreferencePath() string {
	path := hoxipaths.Join(worktree.AttributionFile)
	if path == "" || !isAbsOrRootedPath(path) {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return absolute
}

// The target remains protected for writes, including under a custom HOXI_HOME.
// Only the separate canonical read proof below may exempt an operation.
func isAttributionPreferenceTarget(raw string) bool {
	if filepath.Base(strings.TrimSpace(raw)) != worktree.AttributionFile || !isAbsOrRootedPath(raw) {
		return false
	}
	expected := attributionPreferencePath()
	actual, err := filepath.Abs(strings.TrimSpace(raw))
	return err == nil && expected != "" && preferencePathEqual(actual, expected)
}

func preferencePathEqual(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isAttributionPreferenceReadPath(raw string) bool {
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\x00\r\n\t$`*?[]{}") ||
		(filepath.Separator != '\\' && strings.Contains(raw, "\\")) || !isAttributionPreferenceTarget(raw) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(raw), "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	expected := attributionPreferencePath()
	realRoot, rootOK := resolvePathWithSymlinks(filepath.Dir(expected), 0)
	realFile, fileOK := resolvePathWithSymlinks(expected, 0)
	if !rootOK || !fileOK || !preferencePathEqual(realFile, filepath.Join(realRoot, worktree.AttributionFile)) {
		return false
	}
	if info, err := os.Lstat(expected); err == nil {
		if !info.Mode().IsRegular() {
			return false
		}
	} else if !os.IsNotExist(err) {
		return false
	}
	rootSlash := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(realRoot), "/")) + "/"
	for _, prefix := range sensitiveExternalPrefixes {
		if strings.HasPrefix(rootSlash, prefix) {
			return false
		}
	}
	for _, fragment := range sensitiveExternalFragments {
		if fragment != "/.hoxi/" && strings.Contains(rootSlash, fragment) {
			return false
		}
	}
	for _, part := range strings.Split(rootSlash, "/") {
		if part != ".hoxi" && protectedWorktreeDirs[part] {
			return false
		}
	}
	return true
}

func hasAmbiguousAttributionPreferenceInput(item map[string]interface{}) bool {
	return hasAmbiguousPermissionPathInput(item, func(raw string) bool {
		return raw != "" && !isAttributionPreferenceReadPath(raw)
	})
}

func isAttributionPreferenceReadRequest(item map[string]interface{}, target string) bool {
	if !isAttributionPreferenceReadPath(target) || hasAmbiguousAttributionPreferenceInput(item) {
		return false
	}
	req := parsePermissionRequest(item)
	toolObj, _ := item["tool"].(map[string]interface{})
	tool := strings.ToLower(firstString(req.meta["tool"], toolObj["name"]))
	if req.permission == "tool" && tool == "" {
		tool = strings.ToLower(req.firstPattern)
	}
	if req.destructive || req.hasCommandField || editPermissions[req.permission] || editPermissions[tool] || editToolNames[tool] || hookEditTools[tool] {
		return false
	}
	if _, commands := req.meta["commands"].([]string); commands {
		return false
	}
	readTool := readPermissions[tool] || readToolNames[tool] || hookReadTools[tool]
	if tool != "" && !readTool {
		return false
	}
	if req.permission == "external_directory" {
		return readTool // unknown directory scope must not stand in for a file read
	}
	return readPermissions[req.permission] || (req.permission == "tool" && readTool)
}
