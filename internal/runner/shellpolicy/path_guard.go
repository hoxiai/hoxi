package shellpolicy

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	hookReadTools = map[string]bool{
		"read_file":                     true,
		"read_many_files":               true,
		"view_file":                     true,
		"list_dir":                      true,
		"list_directory":                true,
		"find_by_name":                  true,
		"grep_search":                   true,
		"search_file_content":           true,
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
		"wait":                          true,
		"wait_5_seconds":                true,
	}
	hookEditTools = map[string]bool{
		"edit_file":                  true,
		"write_to_file":              true,
		"write_file":                 true,
		"replace":                    true,
		"replace_file_content":       true,
		"multi_replace_file_content": true,
		"sed_file":                   true,
		"notebook_edit":              true,
	}
	protectedWorktreeDirs = map[string]bool{
		".git":      true,
		".agents":   true,
		".opencode": true,
		".claude":   true,
		".hoxi":     true,
		".envrc":    true,
	}
)

func isReadOnlyTaskControlCall(name string, args map[string]interface{}) bool {
	if len(args) == 0 {
		return false
	}
	raw := strings.TrimSpace(firstString(args["Action"], args["action"]))
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = strings.TrimSpace(unquoted)
		}
	}
	act := strings.ToLower(raw)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "manage_task":
		return act == "status" || act == "list"
	case "manage_subagents":
		return act == "list"
	}
	return false
}

func rawHookToolPath(args map[string]interface{}) string {
	raw := firstString(args["Path"], args["AbsolutePath"], args["absolute_path"], args["TargetFile"],
		args["target_file"], args["file_path"], args["filePath"], args["filepath"], args["File"],
		args["file"], args["path"], args[" target"], args["target"], args["DirectoryPath"], args["directory_path"],
		args["dir_path"], args["dirPath"], args["SearchPath"], args["search_path"], args["SearchDirectory"],
		args["search_directory"], args["Directory"], args["directory"],
		args["NotebookPath"], args["notebook_path"])
	if raw == "" {
		if multi := stringList(args["paths"]); len(multi) > 0 {
			raw = multi[0]
		}
	}
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = strings.TrimSpace(unquoted)
		}
	}
	return raw
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
	if isRoutineAttachmentSubpath(slash) {
		return false
	}
	if slash == "hoxi.json" || strings.HasSuffix(slash, "/hoxi.json") ||
		slash == ".vscode/settings.json" || strings.HasSuffix(slash, "/.vscode/settings.json") ||
		slash == ".gemini/settings.json" || strings.HasSuffix(slash, "/.gemini/settings.json") ||
		slash == ".gemini/config/hooks.json" || strings.HasSuffix(slash, "/.gemini/config/hooks.json") {
		return true
	}
	for _, part := range strings.Split(slash, "/") {
		if protectedWorktreeDirs[part] {
			return true
		}
	}
	return false
}

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
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(cur)
				if err != nil {
					return "", false
				}
				if !filepath.IsAbs(link) {
					link = filepath.Join(filepath.Dir(cur), link)
				}
				return resolvePathWithSymlinks(filepath.Join(link, suffix), depth+1)
			}
		} else if !os.IsNotExist(err) {
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
	if filepath.IsAbs(base) != filepath.IsAbs(target) && isAbsOrRootedPath(base) && isAbsOrRootedPath(target) {
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
