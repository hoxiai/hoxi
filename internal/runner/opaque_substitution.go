package runner

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sshHostSpecRe             = regexp.MustCompile(`^(?:[A-Za-z0-9_.-]+@)?([A-Za-z0-9_.-]+)$`)
	sshNumericOrZeroOctetIPRe = regexp.MustCompile(`(?i)^(?:\d+|0x[0-9a-f]+|0\d+(?:\.[0-9a-fx]+)+)$`)
	sshSafeShortFlagRe        = regexp.MustCompile(`^-[TtnNqv46AaCxXYy]+$`)
	scpSafeShortFlagRe        = regexp.MustCompile(`^-[rpfqvBC46OT]+$`)
	sshUnsafeOptRe            = regexp.MustCompile(`(?i)(?:proxycommand|localcommand|permitlocalcommand|knownhostscommand|identityfile|include|identityagent)`)
	sshDurationRe             = regexp.MustCompile(`^\d+[smhd]?$`)
	gitCommitHeredocPrefixRe  = regexp.MustCompile(`(?s)\bgit\s+commit\b.*-m\s*"?\$\(\s*cat\s*$`)

	benignTempFileConsumers = setOf("scp", "ls", "wc", "head", "tail", "stat", "file", "md5", "shasum", "sha256sum", "rm", "echo", "printf", "grep", "rg", "sed")
	sshBlockedKeyBasenames  = setOf("authorized_keys", "known_hosts", "config", "environment", "rc", ".ssh")
)

// extractProcessSubstitutions rewrites each `<(cmd)` into a placeholder
// argument and each `$(cmd)` into the $__hoxi_cmdsub__ variable, and appends
// cmd as its own line, so it is judged like any other command. Nested
// parentheses or an unterminated form leave the command unchanged.
func extractProcessSubstitutions(s string) string {
	var out strings.Builder
	var inner []string
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '$' && quote != '\'' && i+1 < len(s) && s[i+1] == '(':
			end := processSubstitutionEnd(s, i+2)
			if end < 0 {
				return s // `$((…))` arithmetic or nesting stays opaque
			}
			inner = append(inner, s[i+2:end])
			out.WriteString("$" + cmdSubstVar)
			i = end
			continue
		case quote == '"':
			if c == '\\' && i+1 < len(s) {
				out.WriteByte(c)
				i++
				c = s[i]
			} else if c == '"' {
				quote = 0
			}
		case c == '\\' && i+1 < len(s):
			out.WriteByte(c)
			i++
			c = s[i]
		case c == '\'' || c == '"':
			quote = c
		case c == '<' && i+1 < len(s) && s[i+1] == '(' && (i == 0 || isWordBoundary(s[i-1])):
			end := processSubstitutionEnd(s, i+2)
			if end < 0 {
				return s
			}
			inner = append(inner, s[i+2:end])
			out.WriteString(processSubstitutionPlaceholder)
			i = end
			continue
		}
		out.WriteByte(c)
	}
	if len(inner) == 0 {
		return s
	}
	return out.String() + "\n" + strings.Join(inner, "\n")
}

// processSubstitutionEnd returns the index of the `)` closing a `<(` whose
// body starts at start, or -1 for nested parentheses or no closing.
func processSubstitutionEnd(s string, start int) int {
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"':
			if c == '\\' {
				i++
			} else if c == '"' {
				quote = 0
			}
		case c == '\\':
			i++
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			return -1
		case c == ')':
			return i
		}
	}
	return -1
}

// opacityCandidates returns the commands judged for opacity.
func opacityCandidates(item map[string]interface{}) []string {
	meta := itemMetadata(item)
	if cmd := strings.TrimSpace(firstString(meta["command"])); cmd != "" {
		return []string{unwrapSandboxCommand(cmd)}
	}
	return commandCandidates(item)
}

// destructiveScanForms returns the texts the destructive patterns scan.
func destructiveScanForms(cmd string) []string {
	baseCmd := joinLineContinuations(strings.TrimSpace(unwrapSandboxCommand(cmd)))
	prepared := baseCmd
	hadBenignHeredoc := false
	if strings.Contains(prepared, "<<") {
		if stripped, ok := stripBenignHeredocs(prepared); ok {
			prepared = stripped
			baseCmd = stripped
			hadBenignHeredoc = true
		} else if stripped, ok := stripLiteralHeredocs(prepared); ok {
			prepared = stripped
		}
	}
	if !strings.Contains(baseCmd, "<<") || hadBenignHeredoc {
		baseCmd = maskRemoteSSHAndSCP(baseCmd)
		prepared = maskRemoteSSHAndSCP(prepared)
	}
	forms := []string{maskCommandDataArguments(baseCmd)}
	if unpacked := extractProcessSubstitutions(baseCmd); unpacked != baseCmd {
		forms = append(forms, maskCommandDataArguments(unpacked))
	}
	if spans, ok := findSubshellSpans(prepared); ok && len(spans) > 0 {
		prepared = eraseSubshells(prepared)
	}
	if expanded, status := concretizeLiterals(prepared); status == constApplied || status == constSecret {
		maskedExp := maskRemoteSSHAndSCP(expanded)
		forms = append(forms, maskCommandDataArguments(maskedExp))
		if unpacked := extractProcessSubstitutions(maskedExp); unpacked != maskedExp {
			forms = append(forms, maskCommandDataArguments(unpacked))
		}
	}
	return forms
}

func stripBenignHeredocs(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	state := &heredocScan{}
	var tempTargets []string
	for i := 0; i < len(lines); i++ {
		line, markers, ok := takeHeredocMarkers(lines[i], state)
		if !ok {
			return s, false
		}
		out = append(out, line)
		if len(markers) > 0 {
			if len(state.outer) == 1 {
				if !gitCommitHeredocPrefixRe.MatchString(line) {
					return s, false
				}
			} else if len(state.outer) == 0 {
				segs, pipedIn := splitCommandChainWithPipes(line)
				if len(segs) == 0 {
					return s, false
				}
				lastIdx := len(segs) - 1
				lastSeg := segs[lastIdx]
				if !pipedIn[lastIdx] && isSafeRemoteSSHSegment(lastSeg) {
					// remote ssh heredoc
				} else if !pipedIn[lastIdx] {
					target := tempCatRedirectTarget(lastSeg)
					if target == "" {
						return s, false
					}
					tempTargets = append(tempTargets, target)
				} else {
					return s, false
				}
			} else {
				return s, false
			}
		}
		for _, m := range markers {
			var body []string
			found := false
			for i+1 < len(lines) {
				i++
				cmp := lines[i]
				if m.dash {
					cmp = strings.TrimLeft(cmp, "\t")
				}
				if cmp == m.word {
					found = true
					break
				}
				body = append(body, lines[i])
			}
			if !found {
				return s, false
			}
			if !m.quoted && strings.ContainsAny(strings.Join(body, "\n"), "$`\\") {
				return s, false
			}
		}
	}
	stripped := strings.Join(out, "\n")
	if len(tempTargets) > 0 {
		allSegs, _ := splitCommandChainWithPipes(stripped)
		for _, t := range tempTargets {
			consumed := false
			for _, seg := range allSegs {
				if tempCatRedirectTarget(seg) != "" {
					continue
				}
				if strings.Contains(seg, t) || strings.Contains(seg, filepath.Base(t)) {
					words := stripShellKeywords(shellWords(seg))
					j := 0
					for j < len(words) && envVarAssignmentWordRe.MatchString(words[j]) {
						j++
					}
					if j >= len(words) || !benignTempFileConsumers[filepath.Base(normalizeAbsoluteTool(words[j]))] {
						return s, false
					}
					consumed = true
				}
			}
			if !consumed {
				return s, false
			}
		}
	}
	return stripped, true
}

func tempCatRedirectTarget(seg string) string {
	if hasDollarExpansion(seg) || strings.Contains(seg, "`") {
		return ""
	}
	rawWords := shellWords(seg)
	var expanded []string
	for _, w := range rawWords {
		if (strings.HasPrefix(w, ">>") || strings.HasPrefix(w, ">")) && len(w) > 1 && !strings.HasPrefix(w, ">&") {
			op := ">"
			if strings.HasPrefix(w, ">>") {
				op = ">>"
			}
			expanded = append(expanded, op, w[len(op):])
		} else {
			expanded = append(expanded, w)
		}
	}
	var words []string
	for i := 0; i < len(expanded); i++ {
		w := expanded[i]
		if devNullWordRe.MatchString(w) || dupStreamWordRe.MatchString(w) {
			continue
		}
		if redirectWordRe.MatchString(w) && i+1 < len(expanded) && expanded[i+1] == "/dev/null" {
			i++
			continue
		}
		words = append(words, w)
	}
	if len(words) != 3 || filepath.Base(normalizeAbsoluteTool(words[0])) != "cat" || (words[1] != ">" && words[1] != ">>") {
		return ""
	}
	target := words[2]
	if target == "/dev/null" || !isSafeCurlOutputTarget(target) {
		return ""
	}
	return target
}

func maskRemoteSSHSegments(s string) string {
	return mapTopLevelSegments(s, func(seg string) string {
		if isSafeRemoteSSHSegment(seg) {
			return "ssh " + maskedDataArgument
		}
		return seg
	})
}

func maskRemoteSSHAndSCP(s string) string {
	return mapTopLevelSegments(s, func(seg string) string {
		if isSafeRemoteSSHSegment(seg) {
			return "ssh " + maskedDataArgument
		}
		if rewritten, ok := rewriteSafeRemoteSCPSegment(seg); ok {
			return rewritten
		}
		return seg
	})
}

func mapTopLevelSegments(s string, fn func(seg string) string) string {
	var out strings.Builder
	var quote byte
	parenDepth := 0
	segStart := 0
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
		sepLen := 0
		if (c == '&' && next == '&') || (c == '|' && next == '|') || (c == '|' && next == '&') {
			sepLen = 2
		} else if c == ';' || c == '\n' || c == '|' {
			sepLen = 1
		} else if c == '&' && (i == 0 || s[i-1] != '>') && next != '>' {
			sepLen = 1
		}
		if sepLen > 0 {
			out.WriteString(fn(s[segStart:i]))
			out.WriteString(s[i : i+sepLen])
			i += sepLen - 1
			segStart = i + 1
		}
	}
	if quote != 0 || parenDepth != 0 {
		return s
	}
	out.WriteString(fn(s[segStart:]))
	return out.String()
}

func hasUnsafeLocalSSHExpansion(seg string) bool {
	var quote byte
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			if c == '\\' && i+1 < len(seg) {
				i++
				continue
			}
			if c == '"' {
				quote = 0
				continue
			}
			if c == '`' {
				return true
			}
			if c == '$' && !strings.HasPrefix(seg[i:], "$HOME/") && !strings.HasPrefix(seg[i:], "${HOME}/") {
				return true
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '\\' || c == '`' {
			return true
		}
		if c == '$' && !strings.HasPrefix(seg[i:], "$HOME/") && !strings.HasPrefix(seg[i:], "${HOME}/") {
			return true
		}
	}
	return quote != 0
}

func isSSHIdentityKeyArg(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "..") || strings.ContainsAny(p, "*?[`;&|<>()") {
		return false
	}
	for _, pfx := range []string{"~/", "$HOME/", "${HOME}/"} {
		if strings.HasPrefix(p, pfx) && len(p) > len(pfx) {
			p = "/Users/u/" + p[len(pfx):]
			break
		}
	}
	if strings.Contains(p, "$") {
		return false
	}
	slash := strings.ToLower(filepath.ToSlash(p))
	if !strings.Contains(slash, "/.ssh/") {
		return false
	}
	base := slash[strings.LastIndex(slash, "/")+1:]
	return base != "" && !sshBlockedKeyBasenames[base]
}

func isNonLoopbackSSHHost(target string) bool {
	if strings.HasPrefix(target, "-") {
		return false
	}
	m := sshHostSpecRe.FindStringSubmatch(target)
	if m == nil {
		return false
	}
	host := strings.TrimRight(strings.ToLower(m[1]), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "localhost.localdomain" {
		return false
	}
	if host == "0" || host == "0.0.0.0" || host == "::1" || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "0.") {
		return false
	}
	return !sshNumericOrZeroOctetIPRe.MatchString(host)
}

func unwrapSSHCommandWords(seg string) ([]string, bool) {
	if hasUnsafeLocalSSHExpansion(seg) {
		return nil, false
	}
	words, ok := stripNullRedirects(stripShellKeywords(shellWords(seg)))
	if !ok || len(words) == 0 {
		return nil, false
	}
	j := 0
	for j < len(words) && envVarAssignmentWordRe.MatchString(words[j]) {
		if strings.Contains(words[j], "$") {
			return nil, false
		}
		j++
	}
	if j >= len(words) {
		return nil, false
	}
	unwrapped, lookupOnly, ok := unwrapCommandPrefixes(words[j:])
	if !ok || lookupOnly || len(unwrapped) == 0 {
		return nil, false
	}
	if filepath.Base(normalizeAbsoluteTool(unwrapped[0])) == "timeout" {
		if len(unwrapped) < 3 || !sshDurationRe.MatchString(unwrapped[1]) {
			return nil, false
		}
		unwrapped = unwrapped[2:]
	}
	return unwrapped, true
}

func isSafeRemoteSSHSegment(seg string) bool {
	words, ok := unwrapSSHCommandWords(seg)
	if !ok || filepath.Base(normalizeAbsoluteTool(words[0])) != "ssh" {
		return false
	}
	args := words[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i":
			if i+1 >= len(args) || !isSSHIdentityKeyArg(args[i+1]) {
				return false
			}
			i++
		case strings.HasPrefix(a, "-i") && len(a) > 2:
			if !isSSHIdentityKeyArg(a[2:]) {
				return false
			}
		case a == "-o":
			if i+1 >= len(args) || sshUnsafeOptRe.MatchString(args[i+1]) {
				return false
			}
			i++
		case strings.HasPrefix(a, "-o") && len(a) > 2:
			if sshUnsafeOptRe.MatchString(a[2:]) {
				return false
			}
		case a == "-p" || a == "-l" || a == "-J":
			if i+1 >= len(args) {
				return false
			}
			i++
		case strings.HasPrefix(a, "-p") && len(a) > 2:
		case sshSafeShortFlagRe.MatchString(a):
		case strings.HasPrefix(a, "-"):
			return false
		default:
			return i+1 < len(args) && isNonLoopbackSSHHost(a)
		}
	}
	return false
}

func rewriteSafeRemoteSCPSegment(seg string) (string, bool) {
	words, ok := unwrapSSHCommandWords(seg)
	if !ok || filepath.Base(normalizeAbsoluteTool(words[0])) != "scp" {
		return "", false
	}
	args := words[1:]
	out := []string{"scp"}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i":
			if i+1 >= len(args) || !isSSHIdentityKeyArg(args[i+1]) {
				return "", false
			}
			out = append(out, "-i", maskedDataArgument)
			i++
		case strings.HasPrefix(a, "-i") && len(a) > 2:
			if !isSSHIdentityKeyArg(a[2:]) {
				return "", false
			}
			out = append(out, "-i", maskedDataArgument)
		case a == "-o":
			if i+1 >= len(args) || sshUnsafeOptRe.MatchString(args[i+1]) {
				return "", false
			}
			out = append(out, a, args[i+1])
			i++
		case strings.HasPrefix(a, "-o") && len(a) > 2:
			if sshUnsafeOptRe.MatchString(a[2:]) {
				return "", false
			}
			out = append(out, a)
		case a == "-P" || a == "-l":
			if i+1 >= len(args) {
				return "", false
			}
			out = append(out, a, args[i+1])
			i++
		case strings.HasPrefix(a, "-P") && len(a) > 2:
			out = append(out, a)
		case scpSafeShortFlagRe.MatchString(a):
			out = append(out, a)
		case strings.HasPrefix(a, "-"):
			return "", false
		default:
			pos = append(pos, a)
			out = append(out, a)
		}
	}
	if len(pos) < 2 {
		return "", false
	}
	hasRemote := false
	for _, p := range pos {
		if colon := strings.IndexByte(p, ':'); colon > 0 && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, ".") && isNonLoopbackSSHHost(p[:colon]) {
			hasRemote = true
			continue
		}
		check := p
		if synth, ok := normalizeHomePrefixPath(check); ok {
			check = synth
		}
		if strings.Contains(check, "$") || isSecretFilePath(check) || isSensitiveExternalPath(check) || isProtectedRelPath(check) {
			return "", false
		}
	}
	if !hasRemote {
		return "", false
	}
	return strings.Join(out, " "), true
}
