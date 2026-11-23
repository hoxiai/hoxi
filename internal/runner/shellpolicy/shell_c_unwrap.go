package shellpolicy

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Single-quoted top-level `sh/bash/zsh -c '<literal>'` unwrapping and scoped
// toolchain environment assignment checks (`PATH=`, `NODE_PATH=`, `GOCACHE=`,
// `GOMODCACHE=`, `GOPATH=`). Mirrors web/src/policy/shell-idioms.js.

var (
	benignPathEnvVars     = setOf("PATH", "NODE_PATH", "GOCACHE", "GOMODCACHE", "GOPATH")
	safeShellCLiteralVars = setOf("GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "NODE_ENV", "CI", "TZ", "LC_ALL", "LANG", "NO_COLOR", "FORCE_COLOR", "TERM", "GOWORK", "HOXI_ALLOW_DIRTY_EXPORT")
	safeShellCBinaries    = setOf("sh", "bash", "zsh", "/bin/sh", "/bin/bash", "/usr/bin/bash", "/bin/zsh", "/usr/bin/zsh")
	safeShellCFlagRe      = regexp.MustCompile(`^-[eul]*c$`)
	safeShellCOptRe       = regexp.MustCompile(`^-[eul]+$`)
	safeShellCValRe       = regexp.MustCompile(`^[A-Za-z0-9_.,:/=+-]*$`)
)

// isBenignToolchainEnvAssignment accepts PATH= and scoped toolchain path
// assignments (NODE_PATH, GOCACHE, GOMODCACHE, GOPATH) whose expansions are
// harmless env vars and whose path entries stay out of secret/sensitive dirs.
func isBenignToolchainEnvAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name, value := word[:eq], word[eq+1:]
	if !benignPathEnvVars[name] || strings.ContainsAny(value, "`;&|<>()") {
		return false
	}
	names, ok := wordExpansionNames(value)
	if !ok {
		return false
	}
	for _, n := range names {
		if !harmlessEnvVars[n] {
			return false
		}
	}
	if name == "PATH" {
		return true
	}
	if value == "" {
		return false
	}
	for _, entry := range strings.Split(value, ":") {
		if entry == "" || strings.ContainsAny(entry, "*?[") {
			return false
		}
		check := entry
		if synth, ok := normalizeHomePrefixPath(check); ok {
			check = synth
		} else if strings.HasPrefix(check, "$PWD/") {
			check = "/__pwd__/" + check[len("$PWD/"):]
		} else if strings.HasPrefix(check, "${PWD}/") {
			check = "/__pwd__/" + check[len("${PWD}/"):]
		} else if check == "$PWD" || check == "${PWD}" {
			check = "/__pwd__"
		}
		if strings.Contains(check, "$") {
			return false
		}
		for _, part := range strings.Split(filepath.ToSlash(check), "/") {
			if part == ".." {
				return false
			}
		}
		if isSecretFilePath(check) || isSensitiveExternalPath(check) || isProtectedShellReadPath(check) {
			return false
		}
	}
	return true
}

func isSafeShellCPrefixEnv(word string) bool {
	if isBenignToolchainEnvAssignment(word) {
		return true
	}
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name, value := word[:eq], word[eq+1:]
	if name == "TMPDIR" {
		return value == "/tmp" || value == "/private/tmp"
	}
	return safeShellCLiteralVars[name] && safeShellCValRe.MatchString(value) && !strings.Contains(value, "..")
}

// extractSafeSingleQuotedShellC unwraps a top-level single-quoted literal
// `sh/bash/zsh -c '<script>'` (with only safe toolchain/build env prefixes,
// harmless flags, no positional args, and at most /dev/null or 2>&1 redirects).
func extractSafeSingleQuotedShellC(seg string) (string, bool) {
	s := strings.TrimSpace(seg)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	spans := scanShellWordSpans(s)
	if len(spans) < 3 {
		return "", false
	}
	idx := 0
	for idx < len(spans) && envVarAssignmentWordRe.MatchString(spans[idx].word) {
		if !isSafeShellCPrefixEnv(spans[idx].word) {
			return "", false
		}
		idx++
	}
	if idx >= len(spans) {
		return "", false
	}
	rawCmd := s[spans[idx].start:spans[idx].end]
	if strings.ContainsAny(rawCmd, "'\"$`\\") || !safeShellCBinaries[rawCmd] {
		return "", false
	}
	idx++
	foundC := false
	for idx < len(spans) {
		w := spans[idx].word
		if w == "-o" && idx+1 < len(spans) && (spans[idx+1].word == "pipefail" || spans[idx+1].word == "errexit") {
			idx += 2
			continue
		}
		if safeShellCFlagRe.MatchString(w) {
			foundC = true
			idx++
			break
		}
		if safeShellCOptRe.MatchString(w) {
			idx++
			continue
		}
		return "", false
	}
	if !foundC || idx >= len(spans) {
		return "", false
	}
	rawScript := s[spans[idx].start:spans[idx].end]
	if len(rawScript) < 2 || rawScript[0] != '\'' || rawScript[len(rawScript)-1] != '\'' {
		return "", false
	}
	inner := rawScript[1 : len(rawScript)-1]
	if strings.ContainsRune(inner, '\'') || strings.TrimSpace(inner) == "" {
		return "", false
	}
	idx++
	for idx < len(spans) {
		w := spans[idx].word
		if devNullWordRe.MatchString(w) || dupStreamWordRe.MatchString(w) {
			idx++
			continue
		}
		if redirectWordRe.MatchString(w) && idx+1 < len(spans) && spans[idx+1].word == "/dev/null" {
			idx += 2
			continue
		}
		return "", false
	}
	return inner, true
}

// unwrapTopLevelSingleQuotedShellC replaces each non-piped top-level
// single-quoted `sh/bash/zsh -c '<literal>'` segment with `:` once allow(inner)
// accepts its body.
func unwrapTopLevelSingleQuotedShellC(s string, allow func(inner string) bool) string {
	var out strings.Builder
	var quote byte
	parenDepth := 0
	segStart := 0
	pipedIn := false
	flush := func(segEnd int, nextPiped bool) {
		seg := s[segStart:segEnd]
		if !pipedIn {
			if inner, ok := extractSafeSingleQuotedShellC(seg); ok && allow(inner) {
				out.WriteString(" : ")
			} else {
				out.WriteString(seg)
			}
		} else {
			out.WriteString(seg)
		}
		pipedIn = nextPiped
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '(' {
			parenDepth++
			continue
		}
		if c == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}
		if parenDepth > 0 {
			continue
		}
		sepLen, nextPipe := 0, false
		if (c == '&' && next == '&') || (c == '|' && next == '|') || (c == '|' && next == '&') {
			sepLen = 2
			nextPipe = c == '|' && next == '&'
		} else if c == ';' || c == '\n' {
			sepLen = 1
		} else if c == '|' {
			sepLen = 1
			nextPipe = true
		} else if c == '&' && (i == 0 || s[i-1] != '>') && next != '>' {
			sepLen = 1
		}
		if sepLen > 0 {
			flush(i, nextPipe)
			out.WriteString(s[i : i+sepLen])
			i += sepLen - 1
			segStart = i + 1
		}
	}
	if quote != 0 || parenDepth != 0 {
		return s
	}
	flush(len(s), false)
	return out.String()
}
