package shellpolicy

import (
	"path"
	"regexp"
	"strings"
)

var windowsTempPathRe = regexp.MustCompile(`(?i)^[a-z]:/(?:users/[^/]+/appdata/local/temp|windows/temp)/[^/]+(?:/.*)?$`)

func isWindowsDriveAbs(p string) bool {
	return len(p) >= 3 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) &&
		p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

func isWindowsDriveRoot(p string) bool {
	s := strings.TrimSpace(strings.ReplaceAll(p, `\`, `/`))
	if len(s) < 2 || !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) || s[1] != ':' {
		return false
	}
	return strings.TrimLeft(s[2:], "/") == ""
}

func isWindowsTempPath(slash string) bool {
	s := cleanSlashPath(slash)
	if !windowsTempPathRe.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s[3:], "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "*?[") {
			return false
		}
	}
	return true
}

func cleanSlashPath(p string) string {
	s := strings.TrimSpace(strings.ReplaceAll(p, `\`, `/`))
	if s == "" {
		return ""
	}
	if isWindowsDriveAbs(s) {
		drive := strings.ToUpper(s[:1]) + ":"
		cleaned := path.Clean("/" + s[3:])
		if cleaned == "/" {
			return drive + "/"
		}
		return drive + cleaned
	}
	return path.Clean(s)
}

func joinSlashPath(base, target string) string {
	t := strings.TrimSpace(strings.ReplaceAll(target, `\`, `/`))
	if strings.HasPrefix(t, "/") || isWindowsDriveAbs(t) {
		return cleanSlashPath(t)
	}
	b := cleanSlashPath(base)
	if isWindowsDriveAbs(b) {
		drive := b[:2]
		joined := path.Clean("/" + strings.TrimPrefix(b[2:], "/") + "/" + t)
		if joined == "/" {
			return drive + "/"
		}
		return drive + joined
	}
	return path.Clean(b + "/" + t)
}

func slashPathWithin(base, target string) bool {
	b := cleanSlashPath(base)
	t := cleanSlashPath(target)
	if b == "" || t == "" || b == "/" || isWindowsDriveRoot(b) {
		return false
	}
	bWin := isWindowsDriveAbs(b)
	tWin := isWindowsDriveAbs(t)
	if bWin != tWin {
		return false
	}
	if bWin {
		bl := strings.TrimSuffix(strings.ToLower(b), "/")
		tl := strings.TrimSuffix(strings.ToLower(t), "/")
		return tl == bl || strings.HasPrefix(tl, bl+"/")
	}
	b = strings.TrimSuffix(b, "/")
	return t == b || strings.HasPrefix(t, b+"/")
}

func windowsRelPath(base, target string) (string, bool) {
	if !slashPathWithin(base, target) {
		return "", false
	}
	b := strings.TrimSuffix(cleanSlashPath(base), "/")
	t := strings.TrimSuffix(cleanSlashPath(target), "/")
	if strings.EqualFold(b, t) {
		return ".", true
	}
	// Slice by path segments rather than byte length so variable-width Unicode
	// case-folding (e.g. U+212A 'K' <-> 'k' or 'İ' <-> "i\u0307") never panics
	// or truncates the relative path.
	bParts := strings.Split(b, "/")
	tParts := strings.Split(t, "/")
	if len(tParts) < len(bParts) {
		return "", false
	}
	if len(tParts) == len(bParts) {
		return ".", true
	}
	return strings.Join(tParts[len(bParts):], "/"), true
}

// literalCDTarget reads one path argument without rebuilding or reparsing a
// shell command. Whole-word quotes may contain spaces; glued quotes, options,
// substitutions and globs stay unresolved. Mirrors shell-paths.js.
func literalCDTarget(segment string) (string, bool) {
	segment = stripCDNullRedirects(strings.TrimSpace(segment))
	spans := scanShellWordSpans(segment)
	if len(spans) != 2 || segment[spans[0].start:spans[0].end] != "cd" {
		return "", false
	}
	raw := segment[spans[1].start:spans[1].end]
	target := raw
	if raw[0] == '\'' || raw[0] == '"' {
		var ok bool
		target, ok = unquoteLiteralShellScriptArg(raw)
		if !ok {
			return "", false
		}
	} else if strings.ContainsAny(raw, "'\"\\") {
		return "", false
	}
	if raw[0] != '\'' && (raw[0] != '"' || strings.HasPrefix(target, "$")) {
		if home, ok := normalizeHomePrefixPath(target); ok {
			target = home
		}
	}
	if target == "" || strings.HasPrefix(target, "-") || strings.HasPrefix(target, "~") ||
		strings.ContainsAny(target, "\x00\r\n\t$`\\*?[]{}") {
		return "", false
	}
	return target, true
}
