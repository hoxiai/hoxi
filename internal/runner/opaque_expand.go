package runner

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Literal for-loop expansion, value-class command substitutions and the
// macOS per-user temp path. Judgment only: the shell still runs the original
// command. Mirrors .opencode/lib/shell-constants.js and shell-loops.js.

const seqLoopPlaceholder = "__hoxi_seq__"

var (
	numericForHeaderRe  = regexp.MustCompile(`(?m)(^|[;&|()\n\r\t ])(for[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]+in[ \t]+)(?:\$\(seq(?:[ \t]+\d+){1,3}\)|\{\d+\.\.\d+(?:\.\.\d+)?\})([ \t]*(?:;|\r?\n))`)
	urlQueryItemRe      = regexp.MustCompile(`^(?:https?://[A-Za-z0-9_./:@%+=~?&,-]+|/[A-Za-z0-9_./%+-]*\?[A-Za-z0-9_=&.%+-]+)$`)
	wcCountFlagRe       = regexp.MustCompile(`^(?:-[lcmwL]+|--(?:lines|bytes|chars|words|max-line-length))$`)
	curlSilentFlagRe    = regexp.MustCompile(`^-(?:[sSLkIf46]+|-silent|-show-error|-location|-insecure|-fail|-head)$`)
	curlWriteOutRe      = regexp.MustCompile(`^[A-Za-z0-9_.:;,/ \-%{}()\\\n\r\t]+$`)
	positiveIntRe       = regexp.MustCompile(`^[1-9]\d*$`)
	xargsReplRe         = regexp.MustCompile(`^(?:\{\}|%|[A-Za-z_][A-Za-z0-9_]*)$`)
	xargsAttachedFlagRe = regexp.MustCompile(`^-(?:[nLPs][1-9]\d*|I(?:\{\}|%|[A-Za-z_][A-Za-z0-9_]*)|-max-(?:args|lines|procs)=[1-9]\d*)$`)
)

func normalizeNumericForHeaders(s string) string {
	return numericForHeaderRe.ReplaceAllString(s, "${1}${2}"+seqLoopPlaceholder+"${3}")
}

func forLoopRanges(s string) [][2]int {
	var ranges [][2]int
	scanForLoops(s, func(start, end int, header, body string, items []string) (string, bool) {
		ranges = append(ranges, [2]int{start, end})
		return "", false // keep the original text; expansion is a separate pass
	})
	return ranges
}

func expandLiteralForLoops(s string, bindings map[string]constVal) (string, bool) {
	changed := false
	out := scanForLoops(s, func(start, end int, header, body string, items []string) (string, bool) {
		if len(items) == 0 || len(items) > maxLiteralLoopItems || !literalForItems(items) {
			return "", false
		}
		name := forNameAt(header)
		if name == "" {
			return "", false
		}
		if _, shadowed := bindings[name]; shadowed {
			return "", false
		}
		var copies []string
		for _, item := range items {
			one, ok := substituteVars(body, map[string]constVal{name: {text: item}})
			if !ok {
				return "", false
			}
			copies = append(copies, strings.TrimSpace(one))
		}
		changed = true
		return strings.Join(copies, "\n"), true
	})
	return out, changed
}

func forNameAt(header string) string {
	name, ok := forLoopVar(header)
	if !ok {
		return ""
	}
	return name
}

func literalForItems(items []string) bool {
	for _, item := range items {
		if item == seqLoopPlaceholder {
			return false
		}
		if urlQueryItemRe.MatchString(item) && !strings.Contains(item, "..") {
			continue
		}
		if strings.ContainsAny(item, "$*?[`{") || strings.Contains(item, "${") {
			return false
		}
		if isSecretFilePath(item) || isSensitiveExternalPath(item) || isProtectedRelPath(item) {
			return false
		}
	}
	return true
}

// scanForLoops walks outermost for/do/done loops. visit returns the
// replacement and true when it rewrote the loop and the scan should restart.
func scanForLoops(s string, visit func(start, end int, header, body string, items []string) (string, bool)) string {
	for {
		var quote byte
		rewrote := false
		for i := 0; i < len(s); i++ {
			c := s[i]
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
			if c == '"' {
				if quote == '"' {
					quote = 0
				} else if quote == 0 {
					quote = '"'
				}
				continue
			}
			if c == '\'' && quote == 0 {
				quote = '\''
				continue
			}
			if !wordAt(s, i, "for") {
				continue
			}
			items, bodyStart, ok := parseForHeader(s, i)
			if !ok {
				continue
			}
			bodyEnd, doneEnd, ok := findLoopDone(s, bodyStart)
			if !ok {
				continue
			}
			if repl, ok := visit(i, doneEnd, s[i:doneEnd], s[bodyStart:bodyEnd], items); ok {
				s = s[:i] + repl + s[doneEnd:]
				rewrote = true
				break
			}
			i = doneEnd - 1
		}
		if !rewrote {
			return s
		}
	}
}

func parseForHeader(s string, i int) (items []string, bodyStart int, ok bool) {
	i += len("for")
	name, next, ok := readShellWord(s, i)
	if !ok || !shellVarNameRe.MatchString(name) {
		return nil, 0, false
	}
	inWord, next, ok := readShellWord(s, next)
	if !ok || inWord != "in" {
		return nil, 0, false
	}
	i = next
	for {
		i = skipShellSpace(s, i)
		if i >= len(s) {
			return nil, 0, false
		}
		if s[i] == ';' {
			i++
			continue
		}
		w, n, ok := readShellWord(s, i)
		if !ok {
			return nil, 0, false
		}
		if w == "do" {
			return items, n, true
		}
		items = append(items, w)
		i = n
	}
}

func findLoopDone(s string, i int) (bodyEnd, doneEnd int, ok bool) {
	depth := 0
	var quote byte
	for j := i; j < len(s); j++ {
		c := s[j]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && j+1 < len(s) {
			j++
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else if quote == 0 {
				quote = '"'
			}
			continue
		}
		if c == '\'' && quote == 0 {
			quote = '\''
			continue
		}
		if wordAt(s, j, "for") {
			depth++
			j += len("for") - 1
			continue
		}
		if wordAt(s, j, "done") {
			if depth == 0 {
				return j, j + len("done"), true
			}
			depth--
			j += len("done") - 1
		}
	}
	return 0, 0, false
}

func skipShellSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

func readShellWord(s string, i int) (string, int, bool) {
	i = skipShellSpace(s, i)
	if i >= len(s) {
		return "", i, false
	}
	if s[i] == '\'' || s[i] == '"' {
		q := s[i]
		j := i + 1
		for j < len(s) && s[j] != q {
			j++
		}
		if j >= len(s) {
			return "", i, false
		}
		return s[i+1 : j], j + 1, true
	}
	j := i
	for j < len(s) && !isWordBoundary(s[j]) {
		j++
	}
	if j == i {
		return "", i, false
	}
	return s[i:j], j, true
}

// isValueClassSegment reports a command whose stdout is a single derived
// value (a path, hash, pid or count), not an arbitrary file or network body.
func isValueClassSegment(seg string) bool {
	seg = strings.TrimSpace(seg)
	if seg == "" || hasDollarExpansion(seg) || strings.Contains(seg, "`") {
		return false
	}
	if isSafeListingPipeline(seg) {
		return true
	}
	words := stripShellKeywords(shellWords(seg))
	if len(words) == 0 {
		return false
	}
	words, _, ok := unwrapCommandPrefixes(words)
	if !ok || len(words) == 0 {
		return false
	}
	base := filepath.Base(normalizeAbsoluteTool(words[0]))
	args := words[1:]
	switch base {
	case "pwd", "date", "which", "dirname", "basename":
		return true
	case "command":
		return len(args) >= 1 && (args[0] == "-v" || args[0] == "-V")
	case "go":
		if len(args) == 0 || args[0] != "env" {
			return false
		}
		for _, a := range args[1:] {
			if a == "-w" || a == "-u" || strings.HasPrefix(a, "-w=") || strings.HasPrefix(a, "-u=") {
				return false
			}
		}
		return true
	case "wc":
		counted := false
		for _, a := range args {
			switch {
			case a == "<":
				continue
			case strings.HasPrefix(a, "--files0-from"):
				return false
			case wcCountFlagRe.MatchString(a):
				counted = true
			case strings.HasPrefix(a, "-"):
				return false
			default:
				p := strings.TrimPrefix(a, "<")
				if isSecretFilePath(p) || isSensitiveExternalPath(p) || isProtectedRelPath(p) {
					return false
				}
			}
		}
		return counted
	case "curl":
		return isValueClassCurl(args)
	case "grep", "rg":
		count := false
		for _, a := range args {
			switch {
			case a == "-c" || a == "--count":
				count = true
			case a == "-f" || a == "--file" || strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "--file=") ||
				strings.HasPrefix(a, "--pre") || strings.HasPrefix(a, "--hostname-bin"):
				return false
			}
		}
		return count
	case "ps":
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "-o" || a == "--format" || a == "-O" {
				if i+1 < len(args) && strings.HasSuffix(args[i+1], "=") {
					return true
				}
			}
			if strings.HasPrefix(a, "-o") && strings.HasSuffix(a, "=") && len(a) > 2 {
				return true
			}
			if strings.HasPrefix(a, "--format=") && strings.HasSuffix(a, "=") {
				return true
			}
		}
		return false
	case "git":
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return false
		}
		switch args[0] {
		case "rev-parse", "write-tree", "merge-base", "symbolic-ref", "describe", "hash-object":
			return true
		}
	}
	return false
}

func isSafeListingPipeline(cmd string) bool {
	segments, pipedIn := splitCommandChainWithPipes(cmd)
	if len(segments) == 0 || !isSafeListingProducer(segments[0]) {
		return false
	}
	for i := 1; i < len(segments); i++ {
		if !pipedIn[i] || hasDollarExpansion(segments[i]) || strings.Contains(segments[i], "`") {
			return false
		}
		words, ok := stripNullRedirects(shellWords(segments[i]))
		if !ok || len(words) == 0 || envVarAssignmentWordRe.MatchString(words[0]) {
			return false
		}
		base := filepath.Base(normalizeAbsoluteTool(words[0]))
		if (base != "head" && base != "tail") || !isStdinOnlyHeadTail(words[1:]) {
			return false
		}
	}
	return true
}

func isStdinOnlyHeadTail(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-n" || a == "-c" || a == "--lines" || a == "--bytes" {
			if i+1 >= len(args) || !headTailCountRe.MatchString(args[i+1]) {
				return false
			}
			i++
			continue
		}
		if !headTailFlagRe.MatchString(a) {
			return false
		}
	}
	return true
}

func isValueClassCurl(args []string) bool {
	hasWriteOut, hasSafeOutput := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-w" || a == "--write-out":
			if i+1 >= len(args) || !isSafeCurlWriteOut(args[i+1]) {
				return false
			}
			hasWriteOut = true
			i++
		case strings.HasPrefix(a, "-w") && len(a) > 2:
			if !isSafeCurlWriteOut(a[2:]) {
				return false
			}
			hasWriteOut = true
		case strings.HasPrefix(a, "--write-out="):
			if !isSafeCurlWriteOut(a[len("--write-out="):]) {
				return false
			}
			hasWriteOut = true
		case a == "-o" || a == "--output":
			if i+1 >= len(args) || !isSafeCurlOutputTarget(args[i+1]) {
				return false
			}
			hasSafeOutput = true
			i++
		case strings.HasPrefix(a, "-o") && len(a) > 2:
			if !isSafeCurlOutputTarget(a[2:]) {
				return false
			}
			hasSafeOutput = true
		case strings.HasPrefix(a, "--output="):
			if !isSafeCurlOutputTarget(a[len("--output="):]) {
				return false
			}
			hasSafeOutput = true
		case a == "--max-time" || a == "-m" || a == "--connect-timeout" || a == "-H" || a == "--header" || a == "-A" || a == "--user-agent" || a == "-X" || a == "--request":
			if i+1 >= len(args) {
				return false
			}
			i++
		case curlSilentFlagRe.MatchString(a):
		case strings.HasPrefix(a, "-"):
			return false
		}
	}
	return hasWriteOut && hasSafeOutput
}

func isSafeCurlWriteOut(fmtStr string) bool {
	lower := strings.ToLower(fmtStr)
	return fmtStr != "" && !strings.Contains(fmtStr, "@") && !strings.Contains(lower, "header") && !strings.Contains(lower, "json") && curlWriteOutRe.MatchString(fmtStr)
}

func isSafeCurlOutputTarget(p string) bool {
	if p == "/dev/null" {
		return true
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, "$*?[`~") {
		return false
	}
	return strings.HasPrefix(p, "/tmp/") || strings.HasPrefix(p, "/private/tmp/") || isMacOSTempPath(p)
}

func isSafeXargsSegment(rawArgs []string, piped bool, prevSeg string) bool {
	if !piped {
		return false
	}
	args, ok := stripNullRedirects(rawArgs)
	if !ok {
		return false
	}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if a == "-0" || a == "--null" || a == "-r" || a == "--no-run-if-empty" || a == "-t" || a == "--verbose" {
			i++
			continue
		}
		if a == "-n" || a == "-L" || a == "-P" || a == "-s" {
			if i+1 >= len(args) || !positiveIntRe.MatchString(args[i+1]) {
				return false
			}
			i += 2
			continue
		}
		if a == "-I" {
			if i+1 >= len(args) || !xargsReplRe.MatchString(args[i+1]) {
				return false
			}
			i += 2
			continue
		}
		if xargsAttachedFlagRe.MatchString(a) {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			return false
		}
		break
	}
	rest := args[i:]
	if len(rest) == 0 {
		return true
	}
	for _, w := range rest {
		if strings.ContainsAny(w, "$`") {
			return false
		}
	}
	targetBase := filepath.Base(normalizeAbsoluteTool(rest[0]))
	targetArgs := rest[1:]
	if targetBase != "basename" && targetBase != "dirname" && targetBase != "echo" {
		return false
	}
	for j := 0; j < len(targetArgs); j++ {
		ta := targetArgs[j]
		if targetBase == "basename" && (ta == "-a" || strings.HasPrefix(ta, "-s=")) {
			continue
		}
		if targetBase == "basename" && ta == "-s" && j+1 < len(targetArgs) {
			j++
			continue
		}
		if targetBase == "echo" && ta == "-n" {
			continue
		}
		if strings.HasPrefix(ta, "-") {
			return false
		}
	}
	return true
}

func isQuotedDirGlobSuffix(s string, openIdx, closeIdx int) bool {
	if openIdx < 0 || closeIdx <= openIdx+1 || s[closeIdx-1] != '/' {
		return false
	}
	if strings.Trim(s[openIdx+1:closeIdx], "/") == "" {
		return false
	}
	k := closeIdx + 1
	for k < len(s) && !isWordBoundary(s[k]) {
		k++
	}
	suf := s[closeIdx+1 : k]
	return suf == "*" || extGlobRe.MatchString(suf)
}

// commandSubsAreValueClass reports whether every $(...) in s is a value-class
// command that is itself not opaque. Nested substitutions fail closed.
func commandSubsAreValueClass(s string) bool {
	var quote byte
	found := strings.Contains(s, "$"+cmdSubstVar)
	for i := 0; i < len(s); i++ {
		c := s[i]
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
		if quote == 0 && c == '\'' {
			quote = '\''
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else if quote == 0 {
				quote = '"'
			}
			continue
		}
		if c == '$' && i+1 < len(s) && s[i+1] == '(' {
			end := processSubstitutionEnd(s, i+2)
			if end < 0 {
				return false
			}
			inner := s[i+2 : end]
			if !isValueClassSegment(inner) || opaqueShellCore(inner, "", false, false) {
				return false
			}
			found = true
			i = end
		}
	}
	return found
}

// isHarmlessSet reports set options that only change exit and trace behavior.
// Other options (noglob, nounset, allexport, ...) stay opaque.
func isHarmlessSet(words []string) bool {
	if len(words) < 2 || filepath.Base(words[0]) != "set" {
		return false
	}
	args := words[1:]
	if len(args) == 2 && (args[0] == "-o" || args[0] == "+o") {
		switch args[1] {
		case "pipefail", "errexit", "xtrace":
			return true
		}
		return false
	}
	if len(args) != 1 {
		return false
	}
	flag := args[0]
	if len(flag) < 2 || (flag[0] != '-' && flag[0] != '+') || strings.HasPrefix(flag, "--") {
		return false
	}
	for _, c := range flag[1:] {
		if c != 'e' && c != 'x' {
			return false
		}
	}
	return true
}

// isMacOSTempPath reports a concrete entry under the per-user temporary
// directory (/var/folders/<id>/T/<entry> or the three-component form). The T
// directory itself does not qualify.
func isMacOSTempPath(slash string) bool {
	parts := strings.Split(strings.Trim(path.Clean(slash), "/"), "/")
	if strings.HasPrefix(strings.Trim(slash, "/"), "private/") {
		parts = parts[1:]
	}
	if len(parts) < 5 || parts[0] != "var" || parts[1] != "folders" {
		return false
	}
	tAt := -1
	for i := 2; i < len(parts)-1 && i <= 4; i++ {
		if parts[i] == "T" {
			tAt = i
			break
		}
	}
	if tAt < 0 || tAt+1 >= len(parts) {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "*?[") {
			return false
		}
	}
	return !isSecretFilePath(slash)
}
