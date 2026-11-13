package runner

import "strings"

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
