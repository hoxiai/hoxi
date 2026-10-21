package gateway

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Credential stores under the user's home directory. Directories deny the
// whole subtree; files deny one path. Applies to desktop and tunnel reads.
var (
	sensitiveHomeDirs = []string{
		".ssh", ".aws", ".gnupg", ".kube", ".codex", ".gemini",
		filepath.Join(".config", "gh"),
	}
	// Antigravity artifacts are previewed from agy transcripts; the
	// credentials in ~/.gemini stay denied.
	sensitiveHomeDirExceptions = []string{
		filepath.Join(".gemini", "antigravity", "brain"),
	}
	sensitiveHomeFiles = []string{
		filepath.Join(".claude", ".credentials.json"),
		filepath.Join(".docker", "config.json"),
		".netrc", ".npmrc", ".git-credentials",
	}
)

// canonicalPath resolves symlinks (macOS /var → /private/var) so comparisons do
// not depend on which spelling the caller used. For paths that do not exist
// yet, the deepest existing ancestor is resolved and the rest re-joined.
func canonicalPath(p string) string {
	cleaned := filepath.Clean(p)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	rest := ""
	for dir := cleaned; ; {
		if sym, err := filepath.EvalSymlinks(dir); err == nil && sym != "" {
			return filepath.Join(filepath.Clean(sym), rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cleaned
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.ToSlash(a), filepath.ToSlash(b))
}

func pathWithin(base, target string) bool {
	if base == "" {
		return false
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		// Default macOS/Windows volumes are case-insensitive.
		base, target = strings.ToLower(base), strings.ToLower(target)
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel))
}

func isSensitiveCredentialPath(targetPath string) bool {
	resolved := canonicalPath(targetPath)
	slashPath := strings.ToLower(filepath.ToSlash(resolved))
	base := strings.ToLower(filepath.Base(resolved))

	// Hoxi state: protected files directly under hoxipaths.Home() (which may
	// be $HOXI_HOME) and the engine database, plus the same names inside any
	// .hoxi directory (project-level .hoxi/models.json also carries keys).
	if hoxipaths.IsProtectedStateFile(base) {
		if home := hoxipaths.Home(); home != "" && samePath(filepath.Dir(resolved), canonicalPath(home)) {
			return true
		}
		if strings.Contains(slashPath, "/.hoxi/") {
			return true
		}
	}
	for _, db := range hoxipaths.DBFiles() {
		if samePath(resolved, canonicalPath(db)) {
			return true
		}
	}
	if strings.Contains(slashPath, "/opencode/") && (base == "auth.json" || base == "config.json" || base == "opencode.json" || base == "opencode.jsonc") {
		return true
	}
	for _, dirToken := range []string{"/.ssh/", "/.aws/", "/.gnupg/", "/.kube/"} {
		if strings.Contains(slashPath, dirToken) {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		home = canonicalPath(home)
		for _, f := range sensitiveHomeFiles {
			if samePath(resolved, filepath.Join(home, f)) {
				return true
			}
		}
		for _, d := range sensitiveHomeDirs {
			if !pathWithin(filepath.Join(home, d), resolved) {
				continue
			}
			excepted := false
			for _, ex := range sensitiveHomeDirExceptions {
				if pathWithin(filepath.Join(home, ex), resolved) {
					excepted = true
					break
				}
			}
			if !excepted {
				return true
			}
		}
	}
	return false
}
