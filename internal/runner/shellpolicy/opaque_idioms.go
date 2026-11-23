package shellpolicy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Safe shell idioms that reduce false-positive prompts without weakening hard
// security floors. Mirrors web/src/policy/shell-idioms.js.

const mktempPlaceholderPrefix = "__hoxi_mktemp_"

var (
	mktempPlaceholderExactRe = regexp.MustCompile(`^/tmp/__hoxi_mktemp_\d+__$`)
	mktempPrefixRe           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	mktempTemplateRe         = regexp.MustCompile(`^/(?:private/)?tmp/[A-Za-z0-9][A-Za-z0-9._-]*XXX+$`)
	tmpdirAssignRe           = regexp.MustCompile(`\bTMPDIR\+?=`)
	trapVarRefRe             = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)
	pathBracePrefixRe        = regexp.MustCompile(`^[A-Za-z0-9_./-]+/$`)
	pathBraceItemRe          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	pathBraceSuffixRe        = regexp.MustCompile(`^(?:/[A-Za-z0-9][A-Za-z0-9._-]*)+$`)
	gofmtReadOnlyFlagRe      = regexp.MustCompile(`^-[ls]+$`)
	shasumAlgoRe             = regexp.MustCompile(`^(?:1|224|256|384|512|512224|512256)$`)

	doubleBracketArithOps = setOf("-eq", "-ne", "-lt", "-le", "-gt", "-ge")
	trapCleanupSignals    = setOf("EXIT", "0", "INT", "TERM", "HUP")
	realpathSafeFlags     = setOf("-q", "--quiet", "-m", "--canonicalize-missing", "-e", "--canonicalize-existing", "-s", "--no-symlinks")
	checksumSafeFlags     = setOf("-b", "-t", "--tag")
	xcrunValueQueryFlags  = setOf("--show-sdk-path", "--show-sdk-version", "--show-sdk-build-version", "--show-sdk-platform-path", "--show-sdk-platform-version")
	xcrunSDKNameRe        = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

func containsMktempPlaceholder(s string) bool {
	return strings.Contains(s, mktempPlaceholderPrefix)
}

func mktempPlaceholderPath(idx int) string {
	return fmt.Sprintf("/tmp/%s%d__", mktempPlaceholderPrefix, idx)
}

func isExactMktempPlaceholderPath(p string) bool {
	return mktempPlaceholderExactRe.MatchString(p)
}

// isHarmlessSet reports set options that only change fail-fast exit, unset
// variable error, or trace behavior (`set -e`, `set -u`, `set -x`,
// `set -o pipefail`, `set -euo pipefail`, ...). Bare `set`, `set --`,
// `set -f`/`-a`/`-o noglob`, and trailing positional arguments stay opaque.
func isHarmlessSet(words []string) bool {
	if len(words) < 2 || filepath.Base(words[0]) != "set" {
		return false
	}
	args := words[1:]
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if len(flag) < 2 || (flag[0] != '-' && flag[0] != '+') || strings.HasPrefix(flag, "--") {
			return false
		}
		chars := flag[1:]
		for j := 0; j < len(chars); j++ {
			switch chars[j] {
			case 'e', 'u', 'x':
			case 'o':
				if j != len(chars)-1 || i+1 >= len(args) {
					return false
				}
				i++
				switch args[i] {
				case "pipefail", "errexit", "nounset", "xtrace":
				default:
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// isSafeDoubleBracketArgs reports bash/zsh `[[ ... ]]` arguments that avoid
// arithmetic operators (`-eq`, `-ne`, `-lt`, `-le`, `-gt`, `-ge`), which
// evaluate array-subscript expressions in bash.
func isSafeDoubleBracketArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, a := range args {
		if doubleBracketArithOps[a] {
			return false
		}
	}
	return true
}

// isPathBraceList reports a literal brace list on a non-command path argument
// (`site/pages/{a,b}` or `site/pages/{a,b}/index.vue`). Every expanded path
// must stay outside secret, sensitive external, and protected locations.
func isPathBraceList(s string, open, close int) bool {
	start := open
	for start > 0 && !isWordBoundary(s[start-1]) {
		start--
	}
	prefix := s[start:open]
	if !pathBracePrefixRe.MatchString(prefix) || strings.Trim(prefix, "/") == "" || strings.HasPrefix(prefix, "-") {
		return false
	}
	for _, part := range strings.Split(prefix, "/") {
		if part == ".." {
			return false
		}
	}
	// Refuse command position (`bin/{sh,bash}`, `ls; /bin/{a,b}`, `FOO=1 bin/{a,b}`).
	p := start
	for p > 0 && (s[p-1] == ' ' || s[p-1] == '\t') {
		p--
	}
	if p == 0 {
		return false
	}
	if strings.IndexByte(";&|\n\r(`", s[p-1]) >= 0 {
		return false
	}
	prevStart := p
	for prevStart > 0 && !isWordBoundary(s[prevStart-1]) {
		prevStart--
	}
	prevWord := s[prevStart:p]
	if prevWord == "" || shellKeywords[prevWord] || envVarAssignmentWordRe.MatchString(prevWord) {
		return false
	}
	end := close + 1
	for end < len(s) && !isWordBoundary(s[end]) {
		end++
	}
	suffix := s[close+1 : end]
	if suffix != "" {
		if !pathBraceSuffixRe.MatchString(suffix) || strings.Contains(suffix, "..") {
			return false
		}
	}
	items := strings.Split(s[open+1:close], ",")
	if len(items) < 2 || len(items) > maxLiteralLoopItems {
		return false
	}
	if isDisallowedPathBraceCandidate(prefix) {
		return false
	}
	for _, item := range items {
		if !pathBraceItemRe.MatchString(item) || strings.Contains(item, "..") {
			return false
		}
		if isDisallowedPathBraceCandidate(prefix + item + suffix) {
			return false
		}
	}
	return true
}

func isDisallowedPathBraceCandidate(p string) bool {
	return isSecretFilePath(p) || isSensitiveExternalPath(p) || isProtectedShellReadPath(p) || isProtectedRelPath(p)
}

// isFilterFlagBraceList reports a literal brace list that is the whole value
// of a filter flag: `--exclude-dir={node_modules,dist,.git}` is the same as
// repeating the flag once per item. These flags only narrow what a tool reads.
// An item that names a secret file is refused, as the expanded
// `--include=.env` would be.
func isFilterFlagBraceList(s string, open, close int) bool {
	start := open
	for start > 0 && !isWordBoundary(s[start-1]) {
		start--
	}
	if !filterFlagPrefixRe.MatchString(s[start:open]) {
		return false
	}
	if close+1 < len(s) && !isWordBoundary(s[close+1]) {
		return false
	}
	items := strings.Split(s[open+1:close], ",")
	if len(items) < 2 {
		return false
	}
	for _, item := range items {
		if !braceFilterItemRe.MatchString(item) || strings.HasPrefix(strings.ToLower(item), ".en") || isSecretFilePath(item) {
			return false
		}
	}
	return true
}

func replaceValueSubAssignments(command string, segs []constSeg, bindings map[string]constVal) string {
	if len(bindings) == 0 {
		return command
	}
	hasMktemp := false
	for _, v := range bindings {
		if v.mktemp {
			hasMktemp = true
			break
		}
	}
	var b strings.Builder
	prev := 0
	for _, seg := range segs {
		text := strings.TrimSpace(seg.text(command))
		name, _, pure := pureAssignment(text)
		v, ok := bindings[name]
		repl := ""
		if pure && ok {
			if v.valueSub || v.mktemp {
				repl = v.inner
			} else if strings.Contains(v.text, "$") {
				repl = ":"
			} else {
				continue
			}
		} else if hasMktemp && seg.before != sepPipe && seg.next != sepPipe {
			trapRepl, trapOK := parseSafeMktempTrapSegment(text, seg.start, bindings)
			if !trapOK {
				continue
			}
			repl = trapRepl
		} else {
			continue
		}
		b.WriteString(command[prev:seg.start])
		b.WriteString(repl)
		prev = seg.end
	}
	b.WriteString(command[prev:])
	return b.String()
}

func isSafeXargsTarget(targetBase string, targetArgs []string, replToken string) bool {
	switch targetBase {
	case "basename", "dirname", "echo":
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
	case "wc":
		for _, ta := range targetArgs {
			if replToken != "" && ta == replToken {
				continue
			}
			if strings.HasPrefix(ta, "-") {
				if !wcCountFlagRe.MatchString(ta) {
					return false
				}
				continue
			}
			if isSecretFilePath(ta) || isSensitiveExternalPath(ta) || isProtectedShellReadPath(ta) {
				return false
			}
		}
		return true
	case "gofmt":
		hasList := false
		for _, ta := range targetArgs {
			if replToken != "" && ta == replToken {
				continue
			}
			if strings.HasPrefix(ta, "-") {
				if !gofmtReadOnlyFlagRe.MatchString(ta) {
					return false
				}
				if strings.Contains(ta, "l") {
					hasList = true
				}
				continue
			}
			if isSecretFilePath(ta) || isSensitiveExternalPath(ta) || isProtectedShellReadPath(ta) {
				return false
			}
		}
		return hasList
	}
	return false
}

func isValueClassGofmt(args []string) bool {
	hasList := false
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			if !gofmtReadOnlyFlagRe.MatchString(a) {
				return false
			}
			if strings.Contains(a, "l") {
				hasList = true
			}
			continue
		}
		if hasUnsafeValuePath(a) {
			return false
		}
	}
	return hasList
}

func isValueClassRealpath(args []string) bool {
	hasPath := false
	endFlags := false
	for _, a := range args {
		if !endFlags && a == "--" {
			endFlags = true
			continue
		}
		if !endFlags && strings.HasPrefix(a, "-") {
			if !realpathSafeFlags[a] {
				return false
			}
			continue
		}
		if hasUnsafeValuePath(a) {
			return false
		}
		hasPath = true
	}
	return hasPath
}

func isValueClassChecksum(base string, args []string) bool {
	hasFile := false
	endFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endFlags && a == "--" {
			endFlags = true
			continue
		}
		if !endFlags && strings.HasPrefix(a, "-") {
			switch base {
			case "md5":
				if a != "-q" && a != "-r" && a != "-qr" && a != "-rq" {
					return false
				}
			case "shasum":
				if a == "-a" {
					if i+1 >= len(args) || !shasumAlgoRe.MatchString(args[i+1]) {
						return false
					}
					i++
					continue
				}
				if a != "-b" && a != "-t" && a != "-U" {
					return false
				}
			case "sha256sum", "md5sum":
				if !checksumSafeFlags[a] {
					return false
				}
			default:
				return false
			}
			continue
		}
		if hasUnsafeValuePath(a) {
			return false
		}
		hasFile = true
	}
	return hasFile
}

func hasUnsafeValuePath(p string) bool {
	if strings.TrimSpace(p) == "" {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return true
		}
	}
	return isSecretFilePath(p) || isSensitiveExternalPath(p) || isProtectedShellReadPath(p)
}

func commandRebindsTMPDIR(command string, segs []constSeg) bool {
	if !strings.Contains(command, "TMPDIR") {
		return false
	}
	if tmpdirAssignRe.MatchString(command) {
		return true
	}
	for _, seg := range segs {
		text := strings.TrimSpace(seg.text(command))
		if name, ok := forLoopVar(text); ok && name == "TMPDIR" {
			return true
		}
		for _, rebound := range rebindNames(text) {
			if rebound == "TMPDIR" {
				return true
			}
		}
	}
	return false
}

// isSafeMktempSegment reports a plain `mktemp` / `mktemp -d` call guaranteed
// to create a fresh entry directly under /tmp or the inherited $TMPDIR.
func isSafeMktempSegment(seg string) bool {
	seg = strings.TrimSpace(seg)
	if seg == "" || hasDollarExpansion(seg) || strings.ContainsAny(seg, "`;&|\n\r()") {
		return false
	}
	words, ok := stripNullRedirects(stripShellKeywords(shellWords(seg)))
	if !ok || len(words) == 0 || envVarAssignmentWordRe.MatchString(words[0]) {
		return false
	}
	cmdWords, lookupOnly, ok := unwrapCommandPrefixes(words)
	if !ok || lookupOnly || len(cmdWords) == 0 {
		return false
	}
	if cmdWords[0] != "mktemp" && cmdWords[0] != "/usr/bin/mktemp" && cmdWords[0] != "/bin/mktemp" {
		return false
	}
	args := cmdWords[1:]
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		switch a {
		case "-d", "--directory", "-q", "--quiet", "-dq", "-qd":
			i++
		case "-t":
			if i+1 >= len(args) || !mktempPrefixRe.MatchString(args[i+1]) || strings.Contains(args[i+1], "..") {
				return false
			}
			i += 2
		default:
			if strings.HasPrefix(a, "-") {
				return false
			}
			goto doneFlags
		}
	}
doneFlags:
	pos := args[i:]
	if len(pos) == 0 {
		return true
	}
	if len(pos) != 1 {
		return false
	}
	return mktempTemplateRe.MatchString(pos[0]) && !strings.Contains(pos[0], "..") && !isSecretFilePath(pos[0])
}

// mktempWordSuffixIsSafe guards against an empty $TMP fallback turning
// `"$TMP/etc/passwd"` or `"$TMP/.env"` into a sensitive absolute read.
func mktempWordSuffixIsSafe(s string, afterIdx int, quote byte) bool {
	end := afterIdx
	for end < len(s) {
		c := s[end]
		if quote != 0 {
			if c == quote {
				break
			}
		} else if isWordBoundary(c) || c == '\'' || c == '"' {
			break
		}
		end++
	}
	suffix := s[afterIdx:end]
	if suffix == "" {
		return true
	}
	if !strings.HasPrefix(suffix, "/") || strings.ContainsAny(suffix, "*?[$`\\") {
		return false
	}
	for _, part := range strings.Split(suffix[1:], "/") {
		if part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return !isDisallowedPathBraceCandidate(suffix)
}

// parseSafeMktempTrapSegment recognizes `trap 'rm -rf "$TMP"' EXIT` (and
// equivalent double-quoted or multi-signal cleanup traps) when every target is
// an un-rebound mktemp variable assigned earlier in the same command, and
// returns its judgment-only `rm` equivalent.
func parseSafeMktempTrapSegment(seg string, segStart int, bindings map[string]constVal) (string, bool) {
	seg = strings.TrimSpace(seg)
	if !strings.HasPrefix(seg, "trap ") && !strings.HasPrefix(seg, "trap\t") {
		return "", false
	}
	i := len("trap")
	for i < len(seg) && (seg[i] == ' ' || seg[i] == '\t') {
		i++
	}
	if i >= len(seg) || (seg[i] != '\'' && seg[i] != '"') {
		return "", false
	}
	q := seg[i]
	i++
	var actionBuf strings.Builder
	closed := false
	for i < len(seg) {
		c := seg[i]
		if q == '"' && c == '\\' && i+1 < len(seg) && (seg[i+1] == '"' || seg[i+1] == '\\' || seg[i+1] == '$') {
			actionBuf.WriteByte(seg[i+1])
			i += 2
			continue
		}
		if c == q {
			closed = true
			i++
			break
		}
		actionBuf.WriteByte(c)
		i++
	}
	if !closed || (i < len(seg) && seg[i] != ' ' && seg[i] != '\t') {
		return "", false
	}
	rest := strings.TrimSpace(seg[i:])
	if rest == "" || strings.ContainsAny(rest, "<>|;&()`'\"$") {
		return "", false
	}
	signals := strings.Fields(rest)
	if len(signals) == 0 {
		return "", false
	}
	for _, sig := range signals {
		if !trapCleanupSignals[sig] {
			return "", false
		}
	}
	action := strings.TrimSpace(actionBuf.String())
	if action == "" || strings.ContainsAny(action, ";&|\n\r`<>()") || strings.Contains(action, "$(") {
		return "", false
	}
	rawTokens, ok := splitTrapActionTokens(action)
	if !ok || len(rawTokens) < 2 {
		return "", false
	}
	if rawTokens[0] != "rm" && rawTokens[0] != "/bin/rm" {
		return "", false
	}
	var out []string
	out = append(out, "rm")
	idx := 1
	for idx < len(rawTokens) && rawTokens[idx] != "--" && strings.HasPrefix(rawTokens[idx], "-") {
		if !rmFlagRe.MatchString(rawTokens[idx]) {
			return "", false
		}
		out = append(out, rawTokens[idx])
		idx++
	}
	if idx < len(rawTokens) && rawTokens[idx] == "--" {
		out = append(out, "--")
		idx++
	}
	if idx >= len(rawTokens) {
		return "", false
	}
	for ; idx < len(rawTokens); idx++ {
		tok := rawTokens[idx]
		if len(tok) >= 2 && ((tok[0] == '"' && tok[len(tok)-1] == '"') || (tok[0] == '\'' && tok[len(tok)-1] == '\'')) {
			tok = tok[1 : len(tok)-1]
		}
		m := trapVarRefRe.FindStringSubmatch(tok)
		if m == nil {
			return "", false
		}
		name := m[1]
		if name == "" {
			name = m[2]
		}
		v, ok := bindings[name]
		if !ok || !v.mktemp || segStart <= v.assignedAt {
			return "", false
		}
		out = append(out, v.text)
	}
	return strings.Join(out, " "), true
}

func splitTrapActionTokens(s string) ([]string, bool) {
	var tokens []string
	var cur strings.Builder
	inTok := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
				if i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '\t' {
					return nil, false
				}
			}
			continue
		}
		if c == ' ' || c == '\t' {
			if inTok {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inTok = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			if inTok {
				return nil, false
			}
			quote = c
			cur.WriteByte(c)
			inTok = true
			continue
		}
		if c == '\\' {
			return nil, false
		}
		cur.WriteByte(c)
		inTok = true
	}
	if quote != 0 {
		return nil, false
	}
	if inTok {
		tokens = append(tokens, cur.String())
	}
	return tokens, true
}

func isValueClassXcrun(args []string) bool {
	hasQuery := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--sdk":
			if i+1 >= len(args) || !xcrunSDKNameRe.MatchString(args[i+1]) {
				return false
			}
			i++
		case strings.HasPrefix(a, "--sdk="):
			if !xcrunSDKNameRe.MatchString(strings.TrimPrefix(a, "--sdk=")) {
				return false
			}
		case xcrunValueQueryFlags[a]:
			if hasQuery {
				return false
			}
			hasQuery = true
		default:
			return false
		}
	}
	return hasQuery
}
