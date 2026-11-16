package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var scriptRedirectRe = regexp.MustCompile(`^(?:[012]?)(>>?|<)(.*)$`)

func scriptScopeContext(worktree, cwd string) (string, string) {
	for _, value := range []*string{&worktree, &cwd} {
		if isAbsOrRootedPath(*value) && !filepath.IsAbs(*value) {
			absolute, err := filepath.Abs(*value)
			if err != nil {
				return "", ""
			}
			*value = absolute
		}
	}
	return worktree, cwd
}

func isScopedProjectScript(script, worktree, cwd string) bool {
	worktree, cwd = scriptScopeContext(worktree, cwd)
	if safeChmodDir(worktree) == "" || cwd == "" {
		return false
	}
	target := script
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	return pathWithin(worktree, target) && !isProtectedWorktreePath(worktree, target) && !isSecretFilePath(target)
}

func isSafeScriptOutput(target, worktree, cwd string) bool {
	worktree, cwd = scriptScopeContext(worktree, cwd)
	if target == "/dev/null" {
		return true
	}
	if target == "" || strings.ContainsAny(target, "$`*?[]{}<>&|!\r\n") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(target), "/") {
		if part == ".." {
			return false
		}
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		if cwd == "" || !filepath.IsAbs(cwd) {
			return false
		}
		resolved = filepath.Join(cwd, target)
	}
	if isSecretFilePath(resolved) || isSensitiveExternalPath(resolved) || isProtectedRelPath(resolved) {
		return false
	}
	real, ok := resolvePathWithSymlinks(resolved, 0)
	if !ok || isSecretFilePath(real) || isSensitiveExternalPath(real) || isProtectedRelPath(real) {
		return false
	}
	if isTempPathArg(filepath.ToSlash(resolved)) {
		for _, root := range []string{"/tmp", "/private/tmp", os.TempDir()} {
			if !samePath(root, resolved) && pathWithin(root, resolved) {
				return true
			}
		}
		if isMacOSTempPath(filepath.ToSlash(resolved)) {
			for root := filepath.Dir(resolved); filepath.Dir(root) != root; root = filepath.Dir(root) {
				if filepath.Base(root) == "T" && pathWithin(root, resolved) {
					return true
				}
			}
		}
	}
	return safeChmodDir(worktree) != "" && pathWithin(worktree, resolved) && !isProtectedWorktreePath(worktree, resolved)
}

func stripScriptRedirects(args []string, worktree, cwd string) ([]string, bool) {
	var out []string
	for i := 0; i < len(args); i++ {
		word := args[i]
		if dupStreamWordRe.MatchString(word) {
			continue
		}
		m := scriptRedirectRe.FindStringSubmatch(word)
		if m == nil {
			if strings.ContainsAny(word, "<>") {
				return nil, false
			}
			out = append(out, word)
			continue
		}
		target := m[2]
		if target == "" && i+1 < len(args) {
			i++
			target = args[i]
		}
		if (m[1] == "<" && target != "/dev/null") || !isSafeScriptOutput(target, worktree, cwd) {
			return nil, false
		}
	}
	return out, true
}
