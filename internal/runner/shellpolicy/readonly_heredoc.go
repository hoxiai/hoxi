package shellpolicy

import (
	"path/filepath"
	"strings"
)

// Verified read-only quoted interpreter heredoc masking for destructive scans
// and trusted-mode denylist checks. Mirrors web/src/policy/readonly-heredoc.js.
//
// Only top-level quoted heredocs (`<<'EOF'`, `<<"EOF"`, `<<-'EOF'`, `<<-"EOF"`)
// attached to a safe non-piped Python/Node invocation (optionally preceded by
// other shell segments such as `cd <dir> &&`, and allowing safe flags such as
// `-u`, `-B`, `--no-warnings` plus `/dev/null` / `2>&1` redirects) whose
// verbatim body passes `readOnlyScriptCategory` are stripped. Every surrounding
// shell segment (`cd`, pipes, `&&`, subsequent lines) remains intact so
// destructive commands, sensitive `cd` targets, and credential reads outside
// the verified script body are still caught.

var readOnlyHeredocSafeEnvVars = setOf(
	"GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "NODE_ENV", "CI", "TZ",
	"LC_ALL", "LANG", "NO_COLOR", "FORCE_COLOR", "TERM", "GOWORK",
	"HOXI_ALLOW_DIRTY_EXPORT", "PYTHONIOENCODING", "PYTHONUTF8", "PYTHONDONTWRITEBYTECODE",
)

type roHeredocMarker struct {
	word   string
	dash   bool
	quoted bool
	outPos int
}

func stripReadOnlyInterpreterHeredocs(s string) (string, bool) {
	if !strings.Contains(s, "<<") {
		return s, false
	}
	norm := strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	out := make([]string, 0, len(lines))
	state := &heredocScan{}
	strippedAny := false
	for i := 0; i < len(lines); i++ {
		rawLine := lines[i]
		line, markers, ok := scanReadOnlyHeredocLine(rawLine, state)
		if !ok {
			return s, false
		}
		if len(markers) == 0 {
			out = append(out, rawLine)
			continue
		}
		if len(markers) != 1 || len(state.outer) != 0 || state.quote != 0 {
			return s, false
		}
		m := markers[0]
		if !m.quoted {
			return s, false
		}
		family, segOK := readOnlyHeredocOwnerFamily(line, m.outPos)
		if !segOK {
			return s, false
		}
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
		if readOnlyScriptCategory(family, strings.Join(body, "\n")) != "" {
			return s, false
		}
		out = append(out, line)
		strippedAny = true
	}
	if !strippedAny {
		return s, false
	}
	return strings.Join(out, "\n"), true
}

func scanReadOnlyHeredocLine(rawLine string, st *heredocScan) (string, []roHeredocMarker, bool) {
	var out strings.Builder
	var markers []roHeredocMarker
	for i := 0; i < len(rawLine); i++ {
		c := rawLine[i]
		switch {
		case st.quote == '\'':
			if c == '\'' {
				st.quote = 0
			}
		case c == '$' && i+1 < len(rawLine) && rawLine[i+1] == '(':
			st.outer = append(st.outer, st.quote)
			st.quote = 0
			out.WriteByte(c)
			i++
			c = rawLine[i]
		case st.quote == '"':
			if c == '\\' && i+1 < len(rawLine) {
				out.WriteByte(c)
				i++
				c = rawLine[i]
			} else if c == '"' {
				st.quote = 0
			}
		case c == '\\' && i+1 < len(rawLine):
			out.WriteByte(c)
			i++
			c = rawLine[i]
		case c == '(' && len(st.outer) > 0:
			st.outer = append(st.outer, 0)
		case c == ')' && len(st.outer) > 0:
			st.quote = st.outer[len(st.outer)-1]
			st.outer = st.outer[:len(st.outer)-1]
		case c == '\'' || c == '"':
			st.quote = c
		case c == '#' && len(st.outer) == 0 && (i == 0 || isWordBoundary(rawLine[i-1])):
			out.WriteString(rawLine[i:])
			return out.String(), markers, true
		case c == '<' && i+1 < len(rawLine) && rawLine[i+1] == '<':
			if i+2 < len(rawLine) && rawLine[i+2] == '<' {
				return rawLine, nil, false
			}
			j := i + 2
			m := roHeredocMarker{outPos: out.Len()}
			if j < len(rawLine) && rawLine[j] == '-' {
				m.dash = true
				j++
			}
			for j < len(rawLine) && (rawLine[j] == ' ' || rawLine[j] == '\t') {
				j++
			}
			if j < len(rawLine) && (rawLine[j] == '\'' || rawLine[j] == '"') {
				q := rawLine[j]
				end := strings.IndexByte(rawLine[j+1:], q)
				if end < 0 {
					return rawLine, nil, false
				}
				m.word = rawLine[j+1 : j+1+end]
				m.quoted = true
				j += end + 2
			} else {
				return rawLine, nil, false
			}
			if !shellVarNameRe.MatchString(strings.ReplaceAll(m.word, "-", "_")) {
				return rawLine, nil, false
			}
			markers = append(markers, m)
			out.WriteByte(' ')
			i = j - 1
			continue
		}
		out.WriteByte(c)
	}
	return out.String(), markers, true
}

// readOnlyHeredocOwnerFamily finds the top-level shell segment containing outPos
// in a marker-stripped line and validates that it is a non-piped, read-only
// Python or Node stdin invocation.
func readOnlyHeredocOwnerFamily(line string, outPos int) (string, bool) {
	var quote byte
	parenDepth := 0
	segStart := 0
	pipedIn := false
	for i := 0; i <= len(line); i++ {
		if i == len(line) {
			if quote != 0 || parenDepth != 0 {
				return "", false
			}
			if outPos >= segStart && outPos <= len(line) {
				if pipedIn {
					return "", false
				}
				return validateReadOnlyHeredocSegment(line[segStart:])
			}
			return "", false
		}
		c := line[i]
		next := byte(0)
		if i+1 < len(line) {
			next = line[i+1]
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(line) {
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
			if i == outPos {
				return "", false
			}
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
		} else if c == '&' && (i == 0 || line[i-1] != '>') && next != '>' {
			sepLen = 1
		}
		if sepLen > 0 {
			if outPos >= segStart && outPos <= i {
				if pipedIn {
					return "", false
				}
				return validateReadOnlyHeredocSegment(line[segStart:i])
			}
			pipedIn = nextPipe
			i += sepLen - 1
			segStart = i + 1
		}
	}
	return "", false
}

func isSafeReadOnlyHeredocEnv(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name, value := word[:eq], word[eq+1:]
	return readOnlyHeredocSafeEnvVars[name] && safeShellCValRe.MatchString(value) && !strings.Contains(value, "..")
}

func validateReadOnlyHeredocSegment(rawSeg string) (string, bool) {
	seg := strings.TrimSpace(rawSeg)
	if seg == "" || strings.ContainsAny(seg, "$`()") {
		return "", false
	}
	spans := scanShellWordSpans(seg)
	if len(spans) == 0 {
		return "", false
	}
	idx := 0
	for idx < len(spans) && envVarAssignmentWordRe.MatchString(spans[idx].word) {
		if !isSafeReadOnlyHeredocEnv(spans[idx].word) {
			return "", false
		}
		idx++
	}
	for idx < len(spans) {
		head := filepath.Base(normalizeAbsoluteTool(spans[idx].word))
		switch head {
		case "time", "nohup", "command":
			if head == "command" && idx+1 < len(spans) && (spans[idx+1].word == "-v" || spans[idx+1].word == "-V") {
				return "", false
			}
			idx++
			continue
		case "timeout", "gtimeout":
			rem := make([]string, len(spans)-idx)
			for k := idx; k < len(spans); k++ {
				rem[k-idx] = spans[k].word
			}
			end, ok := timeoutPrefixLength(rem)
			if !ok {
				return "", false
			}
			idx += end
			continue
		}
		break
	}
	if idx >= len(spans) {
		return "", false
	}
	rawCmd := seg[spans[idx].start:spans[idx].end]
	if strings.ContainsAny(rawCmd, "'\"\\<>") {
		return "", false
	}
	family, ok := inlineScriptTool(filepath.Base(normalizeAbsoluteTool(spans[idx].word)))
	if !ok || (family != "python" && family != "python3" && family != "node") {
		return "", false
	}
	idx++
	seenDash := false
	for idx < len(spans) {
		w := spans[idx].word
		rawTok := seg[spans[idx].start:spans[idx].end]
		if devNullWordRe.MatchString(w) || dupStreamWordRe.MatchString(w) {
			idx++
			continue
		}
		if redirectWordRe.MatchString(w) && idx+1 < len(spans) && spans[idx+1].word == "/dev/null" {
			idx += 2
			continue
		}
		if strings.ContainsAny(rawTok, "<>") {
			return "", false
		}
		if w == "-" {
			if seenDash {
				return "", false
			}
			seenDash = true
			idx++
			continue
		}
		if (family == "node" && inlineSafeNodeFlags[w]) || (family != "node" && inlineSafePyFlags[w]) {
			idx++
			continue
		}
		return "", false
	}
	return family, true
}
