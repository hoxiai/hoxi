package shellpolicy

import (
	"path/filepath"
	"strings"
)

// Narrow support for monorepo subdirectory glob loops such as:
//
//	for mod_file in work/go/*/go.mod; do
//	  mod_dir="$(dirname "$mod_file")"
//	  echo "=== $mod_dir ==="
//	  (cd "$mod_dir" && go vet ./... && go test -race ./...)
//	done
//
// Keeps arbitrary glob loops opaque for destructive/parameter-sensitive tools
// (`for f in *.go; do rm "$f"; done`, `for d in plugins/*; do git -c "$d" ...`)
// and never feeds glob-derived placeholders into trusted.go recoverability.
// Mirrors the helpers of the same names in web/src/policy/shell-loops.js.

func exactSimpleVarRef(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if !strings.HasPrefix(s, "$") || len(s) < 2 {
		return "", false
	}
	name := s[1:]
	if strings.HasPrefix(name, "{") {
		if !strings.HasSuffix(name, "}") || len(name) < 3 {
			return "", false
		}
		name = name[1 : len(name)-1]
	}
	if !shellVarNameRe.MatchString(name) || harmlessEnvVars[name] {
		return "", false
	}
	return name, true
}

func parseLoopPathTransformSub(inner string) (tool, varName string, ok bool) {
	s := strings.TrimSpace(inner)
	if s == "" || strings.ContainsAny(s, ";|&\n\r`<>()\\'") {
		return "", "", false
	}
	spans := scanShellWordSpans(s)
	if len(spans) < 2 || len(spans) > 3 {
		return "", "", false
	}
	cmd := s[spans[0].start:spans[0].end]
	if cmd != "dirname" && cmd != "basename" && cmd != "/usr/bin/dirname" && cmd != "/usr/bin/basename" {
		return "", "", false
	}
	argIdx := 1
	if len(spans) == 3 {
		if s[spans[1].start:spans[1].end] != "--" {
			return "", "", false
		}
		argIdx = 2
	}
	name, ok := exactSimpleVarRef(s[spans[argIdx].start:spans[argIdx].end])
	if !ok {
		return "", "", false
	}
	return filepath.Base(cmd), name, true
}

func forLoopItemSafe(item string) bool {
	if item == "" || item == seqLoopPlaceholder {
		return item == seqLoopPlaceholder
	}
	if strings.ContainsAny(item, "$`[") || dotGlobRe.MatchString(item) {
		return false
	}
	return !isSecretFilePath(item) && !isSensitiveExternalPath(item) && !isProtectedShellReadPath(item)
}

func isLoopPathTransformValueSub(s string, cmdSubStart int, inner string) bool {
	_, varName, ok := parseLoopPathTransformSub(inner)
	if !ok {
		return false
	}
	matched := false
	scanForLoops(s, func(start, end int, header, body string, items []string) (string, bool) {
		if matched || cmdSubStart < start || cmdSubStart >= end {
			return "", false
		}
		bodyStart := end - len("done") - len(body)
		if cmdSubStart < bodyStart || cmdSubStart >= end-len("done") {
			return "", false
		}
		if forNameAt(header) != varName || len(items) == 0 || len(items) > maxLiteralLoopItems {
			return "", false
		}
		for _, item := range items {
			if !forLoopItemSafe(item) {
				return "", false
			}
		}
		if !literalLoopBindingStable(body, varName) {
			return "", false
		}
		matched = true
		return "", false
	})
	return matched
}

func isSafeScopedWorktreeDirSource(item string) bool {
	if item == "" || strings.HasPrefix(item, "/") || strings.HasPrefix(item, `\`) ||
		strings.HasPrefix(item, "~") || strings.HasPrefix(item, "-") || strings.HasPrefix(item, ".") ||
		isWindowsDriveAbs(item) || strings.ContainsAny(item, "$?[{`\\'\" \t\r\n") || strings.Contains(item, "..") {
		return false
	}
	if !strings.Contains(item, "/") {
		return false
	}
	trimmed := strings.TrimSuffix(item, "/")
	if trimmed == "" || strings.HasSuffix(trimmed, "/") {
		return false
	}
	parts := strings.Split(trimmed, "/")
	globCount := 0
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasPrefix(part, "-") {
			return false
		}
		if strings.Contains(part, "*") {
			globCount++
			if globCount > 1 || !quotedDirGlobSuffixRe.MatchString(part) || quotedDirSecretSuffixRe.MatchString(part) {
				return false
			}
			continue
		}
		if !quotedDirPathSegmentRe.MatchString(part) {
			return false
		}
	}
	concrete := strings.ReplaceAll(trimmed, "*", "x")
	return !isSecretFilePath(concrete) && !isSensitiveExternalPath(concrete) && !isProtectedShellReadPath(concrete) && !isProtectedRelPath(concrete)
}

func isDirLikeGlobItem(item string) bool {
	if strings.HasSuffix(item, "/") {
		return true
	}
	parts := strings.Split(item, "/")
	return len(parts) > 0 && strings.Contains(parts[len(parts)-1], "*")
}

func prefixHasUnsafeCD(prefix string) bool {
	erased := eraseSubshells(prefix)
	for _, seg := range splitConstSegments(erased) {
		text := strings.TrimSpace(seg.text(erased))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		words := stripShellKeywords(shellWords(text))
		for len(words) > 0 && envVarAssignmentWordRe.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		base := filepath.Base(normalizeAbsoluteTool(words[0]))
		if base == "pushd" || base == "popd" {
			return true
		}
		if base != "cd" {
			continue
		}
		target, ok := literalCDTarget(text)
		if !ok || target == "" || isAbsOrRootedPath(target) || strings.HasPrefix(target, "~") || strings.HasPrefix(target, "-") || strings.Contains(target, "..") {
			return true
		}
		for _, part := range strings.Split(strings.TrimSuffix(filepath.ToSlash(target), "/"), "/") {
			if part == "" || strings.HasPrefix(part, ".") {
				return true
			}
		}
		if isSecretFilePath(target) || isSensitiveExternalPath(target) || isProtectedShellReadPath(target) || isProtectedRelPath(target) {
			return true
		}
	}
	return false
}

func stripLeadingControlKeywordPrefix(seg string) string {
	s := strings.TrimSpace(seg)
	for {
		matched := false
		for _, kw := range []string{"do", "then", "else", "elif"} {
			if strings.HasPrefix(s, kw) && len(s) > len(kw) && (s[len(kw)] == ' ' || s[len(kw)] == '\t' || s[len(kw)] == '\n' || s[len(kw)] == '\r') {
				s = strings.TrimSpace(s[len(kw):])
				matched = true
				break
			}
		}
		if !matched {
			return s
		}
	}
}

func parseDirnameAssignOf(seg, loopVar string) (string, bool) {
	s := stripLeadingControlKeywordPrefix(seg)
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return "", false
	}
	name := s[:eq]
	if name == loopVar || !shellVarNameRe.MatchString(name) || harmlessEnvVars[name] {
		return "", false
	}
	val := strings.TrimSpace(s[eq+1:])
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	if !strings.HasPrefix(val, "$(") || !strings.HasSuffix(val, ")") || processSubstitutionEnd(val, 2) != len(val)-1 {
		return "", false
	}
	tool, refVar, ok := parseLoopPathTransformSub(val[2 : len(val)-1])
	if !ok || tool != "dirname" || refVar != loopVar {
		return "", false
	}
	return name, true
}

func countVarBindings(body, name string) int {
	count := 0
	for _, seg := range splitConstSegments(body) {
		words := stripShellKeywords(shellWords(seg.text(body)))
		if len(words) > 0 && strings.HasPrefix(words[0], name+"[") {
			count += 2
			continue
		}
		for len(words) > 0 && (envVarAssignmentWordRe.MatchString(words[0]) || strings.HasPrefix(words[0], name+"+=")) {
			if strings.HasPrefix(words[0], name+"=") {
				count++
			} else if strings.HasPrefix(words[0], name+"+=") {
				count += 2
			}
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		base := filepath.Base(words[0])
		if base == "read" || loopVarRebinders[base] || (base == "for" && len(words) > 1 && words[1] == name) {
			count += 2
		}
		if base == "printf" {
			for _, arg := range words[1:] {
				if arg == "-v" {
					count += 2
				}
			}
		}
	}
	return count
}

func scopedDirVarsForSubshell(command string, spStart, spEnd int) map[string]bool {
	var result map[string]bool
	subBody := command[spStart+1 : spEnd]
	scanForLoops(command, func(start, end int, header, body string, items []string) (string, bool) {
		if result != nil || spStart < start || spEnd > end {
			return "", false
		}
		bodyStart := end - len("done") - len(body)
		bodyEnd := end - len("done")
		if spStart < bodyStart || spEnd > bodyEnd {
			return "", false
		}
		if prefixHasUnsafeCD(command[:start]) {
			return "", false
		}
		loopVar := forNameAt(header)
		if loopVar == "" || harmlessEnvVars[loopVar] || len(items) == 0 || len(items) > maxLiteralLoopItems {
			return "", false
		}
		allDirLike := true
		for _, item := range items {
			if !isSafeScopedWorktreeDirSource(item) {
				return "", false
			}
			if !isDirLikeGlobItem(item) {
				allDirLike = false
			}
		}
		erasedBody := eraseSubshells(body)
		if !literalLoopBindingStable(erasedBody, loopVar) || !literalLoopBindingStable(subBody, loopVar) {
			return "", false
		}
		dirs := map[string]bool{}
		if allDirLike {
			dirs[loopVar] = true
		}
		relSubStart := spStart - bodyStart
		for _, seg := range splitConstSegments(body) {
			if seg.end > relSubStart || seg.before == sepPipe || seg.before == sepOr || seg.next == sepPipe {
				continue
			}
			dirVar, ok := parseDirnameAssignOf(seg.text(body), loopVar)
			if !ok {
				continue
			}
			if countVarBindings(erasedBody, dirVar) == 1 && literalLoopBindingStable(subBody, dirVar) {
				dirs[dirVar] = true
			}
		}
		if len(dirs) > 0 {
			result = dirs
		}
		return "", false
	})
	return result
}

func isScopedDirCDArg(seg string, scopedDirs map[string]bool) bool {
	if len(scopedDirs) == 0 {
		return false
	}
	s := stripCDNullRedirects(strings.TrimSpace(seg))
	spans := scanShellWordSpans(s)
	if len(spans) != 2 || s[spans[0].start:spans[0].end] != "cd" {
		return false
	}
	name, ok := exactSimpleVarRef(s[spans[1].start:spans[1].end])
	return ok && scopedDirs[name]
}
