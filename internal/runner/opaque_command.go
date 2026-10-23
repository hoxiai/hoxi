package runner

import (
	"path/filepath"
	"strings"
)

// isOpaqueShellCommand reports whether a shell command uses indirection,
// obfuscation, variable expansion, nested shells, or inline script evaluation
// whose reach cannot be statically verified before execution. Such commands
// must ask the user in every approval mode (including auto and trusted).
// Mirrors isOpaqueShellCommand in .opencode/lib/opaque-command.js.
func isOpaqueShellCommand(command, worktree string) bool {
	return isOpaqueShellCommandWith(command, worktree, false)
}

// isOpaqueShellCommandWith is isOpaqueShellCommand with full trust's one
// relaxation: allowInlineScripts lets interpreter scripts through (`python3 -
// <<'EOF'`, `python3 -c`, `node -e`), matching `python3 script.py`, which was
// never opaque. Shells (`sh`, `bash`, `eval`, `xargs`, ...), awk programs and
// heredocs whose body still expands stay opaque.
func isOpaqueShellCommandWith(command, worktree string, allowInlineScripts bool) bool {
	command = strings.TrimSpace(unwrapSandboxCommand(command))
	if command == "" {
		return true
	}
	command = joinLineContinuations(command)
	// A literal heredoc is data; whatever reads it is judged below, so
	// `python3 - <<'EOF'` or `bash <<'EOF'` still ask.
	if strings.Contains(command, "<<") {
		stripped, ok := stripLiteralHeredocs(command)
		if !ok {
			return true
		}
		command = stripped
	}
	command = extractProcessSubstitutions(command)
	if hasShellGrouping(command) || hasOpaqueShellSyntax(command) {
		return true
	}
	segments, pipedIn := splitCommandChainWithPipes(command)
	if len(segments) > 1 && !pipedIn[0] && isWorktreeCD(segments[0], worktree) {
		segments = segments[1:]
		pipedIn = pipedIn[1:]
	}
	if len(segments) == 0 {
		return true
	}
	outsideCWD := false
	hasExec := false
	vars := loopBindings(segments, pipedIn)
	for i, rawSeg := range segments {
		seg := strings.TrimSpace(rawSeg)
		if seg == "" || strings.HasPrefix(seg, "#") {
			continue
		}
		words := stripShellKeywords(shellWords(seg))
		if len(words) == 0 {
			continue
		}
		if hasDollarExpansion(seg) && !isBenignPathExport(seg) && !expansionsAreBounded(seg, words, vars) {
			return true
		}
		if words[0] == "for" {
			continue // a loop binder, validated by loopBindings
		}
		hasExec = true
		cmdIdx := 0
		for cmdIdx < len(words) && envVarAssignmentWordRe.MatchString(words[cmdIdx]) {
			cmdIdx++
		}
		if cmdIdx >= len(words) {
			continue
		}
		cmdWords, lookupOnly, ok := unwrapCommandPrefixes(words[cmdIdx:])
		if !ok {
			return true
		}
		if lookupOnly {
			continue
		}
		base := filepath.Base(normalizeAbsoluteTool(cmdWords[0]))
		args := cmdWords[1:]
		if base == "cd" || base == "pushd" || base == "popd" {
			if base != "cd" || !isScopedCDSegment(seg, worktree) {
				outsideCWD = true
			}
			continue
		}
		if outsideCWD && !isRecoverableSegment(cmdWords, "", map[string]bool{}) {
			return true
		}
		if dataArgInterpreters[base] {
			return true
		}
		if awkProgramExecutes(base, args) {
			return true
		}
		if family, ok := inlineScriptTool(base); ok && !allowInlineScripts && (pipedIn[i] || hasInlineScriptFlag(family, args)) {
			return true
		}
	}
	return !hasExec
}

// unwrapCommandPrefixes strips wrappers that only run the next command
// (`nohup`, `time`, `exec`, `command`, `env NAME=value`) so that command is
// judged itself. `command -v x` is a lookup (lookupOnly). A bare `env` prints
// every environment variable, secrets included, so it fails (ok=false).
func unwrapCommandPrefixes(words []string) (cmd []string, lookupOnly, ok bool) {
	for len(words) > 0 {
		switch filepath.Base(normalizeAbsoluteTool(words[0])) {
		case "nohup", "time", "exec":
			if len(words) == 1 {
				return words, false, true
			}
			words = words[1:]
		case "command":
			if len(words) >= 2 && (words[1] == "-v" || words[1] == "-V") {
				return words, true, true
			}
			if len(words) == 1 {
				return words, false, true
			}
			words = words[1:]
		case "env":
			k := 1
			for k < len(words) && envVarAssignmentWordRe.MatchString(words[k]) {
				k++
			}
			if k == 1 || k >= len(words) {
				return words, false, false
			}
			words = words[k:]
		default:
			return words, false, true
		}
	}
	return words, false, true
}

// joinLineContinuations folds `\` + newline into a space, as the shell does,
// so `git add a.go \` / `  b.go` is judged as one command.
func joinLineContinuations(s string) string {
	s = strings.ReplaceAll(s, "\\\r\n", " ")
	return strings.ReplaceAll(s, "\\\n", " ")
}

type heredocMarker struct {
	word   string
	dash   bool
	quoted bool
}

// stripLiteralHeredocs removes heredoc bodies so an interpreter script fed
// through `<<'EOF'` is judged by its command line. It fails (the command
// stays opaque) on an unterminated heredoc, a here-string (`<<<`), an
// escaped delimiter, or an unquoted delimiter whose body still expands
// `$…` or backticks.
func stripLiteralHeredocs(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	state := &heredocScan{}
	for i := 0; i < len(lines); i++ {
		line, markers, ok := takeHeredocMarkers(lines[i], state)
		if !ok {
			return s, false
		}
		out = append(out, line)
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
	return strings.Join(out, "\n"), true
}

// heredocScan carries quote state across lines; a `$(` (even inside double
// quotes) opens a fresh context, as in `git commit -m "$(cat <<'EOF'`.
type heredocScan struct {
	quote byte
	outer []byte // quote state to restore when the matching `)` closes
}

// takeHeredocMarkers removes `<<[-]DELIM` markers from one line.
func takeHeredocMarkers(line string, st *heredocScan) (string, []heredocMarker, bool) {
	var out strings.Builder
	var markers []heredocMarker
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case st.quote == '\'':
			if c == '\'' {
				st.quote = 0
			}
		case c == '$' && i+1 < len(line) && line[i+1] == '(':
			st.outer = append(st.outer, st.quote)
			st.quote = 0
			out.WriteByte(c)
			i++
			c = line[i]
		case st.quote == '"':
			if c == '\\' && i+1 < len(line) {
				out.WriteByte(c)
				i++
				c = line[i]
			} else if c == '"' {
				st.quote = 0
			}
		case c == '\\' && i+1 < len(line):
			out.WriteByte(c)
			i++
			c = line[i]
		case c == '(' && len(st.outer) > 0:
			st.outer = append(st.outer, 0)
		case c == ')' && len(st.outer) > 0:
			st.quote = st.outer[len(st.outer)-1]
			st.outer = st.outer[:len(st.outer)-1]
		case c == '\'' || c == '"':
			st.quote = c
		case c == '<' && i+1 < len(line) && line[i+1] == '<':
			if i+2 < len(line) && line[i+2] == '<' {
				return line, nil, false // here-string
			}
			j := i + 2
			m := heredocMarker{}
			if j < len(line) && line[j] == '-' {
				m.dash = true
				j++
			}
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j < len(line) && (line[j] == '\'' || line[j] == '"') {
				q := line[j]
				end := strings.IndexByte(line[j+1:], q)
				if end < 0 {
					return line, nil, false
				}
				m.word, m.quoted = line[j+1:j+1+end], true
				j += end + 2
			} else {
				k := j
				for k < len(line) && (line[k] == '_' || line[k] == '-' || (line[k] >= 'a' && line[k] <= 'z') ||
					(line[k] >= 'A' && line[k] <= 'Z') || (line[k] >= '0' && line[k] <= '9')) {
					k++
				}
				m.word = line[j:k]
				j = k
			}
			if !shellVarNameRe.MatchString(strings.ReplaceAll(m.word, "-", "_")) {
				return line, nil, false
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
