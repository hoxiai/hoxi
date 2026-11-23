package shellpolicy

import (
	"regexp"
	"strings"
)

var (
	quotedDirPathSegmentRe  = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9._@-]*$`)
	quotedDirGlobSuffixRe   = regexp.MustCompile(`^(?:[A-Za-z0-9_@-][A-Za-z0-9._@-]*)?\*[A-Za-z0-9._@*-]*$`)
	quotedDirSecretSuffixRe = regexp.MustCompile(`(?i)(?:\.(?:env(?:\.|$)|pem$|key$|p12$|pfx$|jks$|keystore$|ppk$|asc$|gpg$)|(?:^|[._@*-])id_(?:rsa|ed25519|ecdsa|dsa)(?:$|[._@*-])|authorized_keys|credentials\.json|oauth_creds\.json)`)
	quotedDirWrapperPrev    = setOf("nohup", "time", "exec", "command", "env", "sudo", "xargs", "timeout", "gtimeout")
)

func isGlobCommandWord(w string) bool {
	if strings.ContainsAny(w, "*?") {
		return true
	}
	return w != "[" && w != "[[" && strings.Contains(w, "[")
}

func isQuotedDirGlobSuffix(s string, openIdx, closeIdx int) bool {
	if openIdx <= 0 || closeIdx <= openIdx+1 || (s[openIdx-1] != ' ' && s[openIdx-1] != '\t') {
		return false
	}
	quoted := s[openIdx+1 : closeIdx]
	if strings.Trim(quoted, "/") == "" || strings.HasPrefix(quoted, "-") || strings.Contains(quoted, "..") || strings.ContainsAny(quoted, "\r\n\\") || containsMktempPlaceholder(quoted) {
		return false
	}
	p := openIdx
	for p > 0 && (s[p-1] == ' ' || s[p-1] == '\t') {
		p--
	}
	if p == 0 || strings.IndexByte(";&|\n\r(`", s[p-1]) >= 0 {
		return false
	}
	prevStart := p
	for prevStart > 0 && !isWordBoundary(s[prevStart-1]) {
		prevStart--
	}
	prevWord := s[prevStart:p]
	if prevWord == "" || shellKeywords[prevWord] || quotedDirWrapperPrev[prevWord] || envVarAssignmentWordRe.MatchString(prevWord) {
		return false
	}
	k := closeIdx + 1
	for k < len(s) && !isWordBoundary(s[k]) {
		k++
	}
	suf := s[closeIdx+1 : k]
	if suf == "" || strings.Contains(suf, "..") {
		return false
	}
	lastSeg := suf
	if s[closeIdx-1] == '/' {
		if strings.Contains(suf, "/") {
			return false
		}
	} else {
		if !strings.HasPrefix(suf, "/") || suf == "/*" {
			return false
		}
		parts := strings.Split(suf[1:], "/")
		for _, dir := range parts[:len(parts)-1] {
			if !quotedDirPathSegmentRe.MatchString(dir) {
				return false
			}
		}
		lastSeg = parts[len(parts)-1]
	}
	if !quotedDirGlobSuffixRe.MatchString(lastSeg) || quotedDirSecretSuffixRe.MatchString(lastSeg) {
		return false
	}
	if strings.HasPrefix(suf, "/") && (isSecretFilePath(suf) || isSensitiveExternalPath(suf) || isProtectedShellReadPath(suf)) {
		return false
	}
	combined := quoted + suf
	if synth, ok := normalizeHomePrefixPath(combined); ok {
		combined = synth
	}
	return !isSecretFilePath(combined) && !isSensitiveExternalPath(combined) && !isProtectedShellReadPath(combined)
}

func hasOpaqueShellSyntax(s string) bool {
	var quote byte
	openIdx := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		prev, next := byte(0), byte(0)
		if i > 0 {
			prev = s[i-1]
		}
		if i+1 < len(s) {
			next = s[i+1]
		}
		if quote == '\'' {
			if c == '\'' {
				if i+3 < len(s) && s[i+1:i+4] == `\''` {
					i += 3
					continue
				}
				quote = 0
				if next != 0 && !isWordBoundary(next) && !isQuotedDirGlobSuffix(s, openIdx, i) {
					return true
				}
			}
			continue
		}
		if c == '`' || (c == '$' && next == '(') {
			return true
		}
		if quote == '"' {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == '"' {
				quote = 0
				if next != 0 && !isWordBoundary(next) && !isQuotedDirGlobSuffix(s, openIdx, i) {
					return true
				}
			}
			continue
		}
		if c == '\\' && (next == '[' || next == ']' || next == '(' || next == ')') {
			i++
			continue
		}
		if c == '{' {
			if nextIdx, ok := skipSafeBraceSpan(s, i); ok {
				i = nextIdx
				continue
			}
			return true
		}
		if c == '\\' || c == '(' || c == ')' || c == '}' {
			return true
		}
		if c == '<' && next == '<' {
			return true
		}
		if c == '\'' || c == '"' {
			shortFlag := i >= 2 && s[i-2] == '-' && (i == 2 || isWordBoundary(s[i-3])) &&
				((prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z'))
			if prev != 0 && !isWordBoundary(prev) && prev != '=' && !(prev == '/' && next == '[') && !shortFlag {
				return true
			}
			quote = c
			openIdx = i
		}
	}
	return quote != 0
}

func hasDollarExpansion(s string) bool {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"':
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == '"' {
				quote = 0
			} else if c == '$' && next != 0 && next != '"' && dollarExpandRe.MatchString(string(next)) {
				return true
			}
		default:
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
			} else if c == '$' && next != 0 && dollarExpandRe.MatchString(string(next)) {
				return true
			}
		}
	}
	return false
}

func isBenignPathExport(seg string) bool {
	trimmed := strings.TrimSpace(seg)
	if !benignPathExportRe.MatchString(trimmed) {
		return false
	}
	rest := strings.ReplaceAll(strings.ReplaceAll(trimmed, "${PATH}", ""), "$PATH", "")
	return !strings.Contains(rest, "$")
}

func hasInlineScriptFlag(base string, args []string) bool {
	if len(args) == 0 {
		return true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if _, _, attached := attachedInlineCode(base, a); attached || unattendedInlineFlag(base, a) {
			return true
		}
		if a == "--" {
			return false
		}
		if a == "-" || inlineScriptFlags[a] ||
			strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "-e") ||
			strings.HasPrefix(a, "--eval=") || strings.HasPrefix(a, "--print=") {
			return true
		}
		if (base == "perl" || base == "ruby") && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "ep") {
			return true
		}
		if (base == "php") && (a == "-r" || strings.HasPrefix(a, "-r")) {
			return true
		}
		if interpValueFlags[a] {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return false
}

func splitCommandChainWithPipes(command string) ([]string, []bool) {
	var segments []string
	var pipedIn []bool
	var cur strings.Builder
	var quote byte
	lastPipe := false
	flush := func(nextPipe bool) {
		seg := strings.TrimSpace(trailingParenRe.ReplaceAllString(cur.String(), ""))
		if seg != "" {
			segments = append(segments, seg)
			pipedIn = append(pipedIn, lastPipe)
		}
		cur.Reset()
		lastPipe = nextPipe
	}
	s := stripOpenCodeStopMarkerText(command)
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
			cur.WriteByte(c)
			continue
		}
		if c == '\\' && i+1 < len(s) {
			cur.WriteByte(c)
			cur.WriteByte(next)
			i++
			continue
		}
		if quote == '"' {
			switch {
			case c == '"':
				quote = 0
				cur.WriteByte(c)
			case c == '$' && next == '(':
				flush(false)
				i++
			case c == '`':
				flush(false)
			default:
				cur.WriteByte(c)
			}
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == '&' && next == '&', c == '|' && next == '|', c == '$' && next == '(':
			flush(false)
			i++
		case c == '|':
			flush(true)
		case c == ';', c == '\n', c == '`':
			flush(false)
		case c == '&' && (i == 0 || s[i-1] != '>') && next != '>':
			flush(false)
		default:
			cur.WriteByte(c)
		}
	}
	flush(false)
	return segments, pipedIn
}
