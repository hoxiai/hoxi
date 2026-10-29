package runner

import (
	"path"
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
	return isOpaqueShellCommandDepth(command, worktree, allowInlineScripts, 0)
}

// isOpaqueShellCommandDepth judges command, treating each (...) subshell as
// its own command up to maxSubshellDepth. Deeper nesting stays opaque.
// When the command is still opaque, literal assignments and literal for-loops
// are substituted and the expanded text is judged again. A secret substitution
// stays opaque; the destructive scanner still sees that expanded text.
func isOpaqueShellCommandDepth(command, worktree string, allowInlineScripts bool, depth int) bool {
	if depth > maxSubshellDepth {
		return true
	}
	command = strings.TrimSpace(unwrapSandboxCommand(command))
	if command == "" || strings.Contains(command, cmdSubstVar) || strings.Contains(command, seqLoopPlaceholder) {
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
	command = maskRemoteSSHSegments(command)
	command = normalizeNumericForHeaders(command)
	if _, ok := findSubshellSpans(command); !ok {
		return true
	}
	if subshellsClear(command, worktree, allowInlineScripts, depth) {
		erased := eraseSubshells(command)
		if !opaqueShellCore(erased, worktree, allowInlineScripts, commandSubsAreValueClass(erased)) {
			return false
		}
	}
	expanded, status := concretizeLiterals(command)
	switch status {
	case constSecret, constReject:
		return true
	case constApplied:
		if !subshellsClear(expanded, worktree, allowInlineScripts, depth) {
			return true
		}
		erased := eraseSubshells(expanded)
		return opaqueShellCore(erased, worktree, allowInlineScripts, commandSubsAreValueClass(erased))
	default:
		return true
	}
}

// subshellsClear reports whether every outer (...) body is itself clear.
// An unterminated group fails closed.
func subshellsClear(command, worktree string, allowInlineScripts bool, depth int) bool {
	spans, ok := findSubshellSpans(command)
	if !ok {
		return false
	}
	for _, sp := range spans {
		if isOpaqueShellCommandDepth(command[sp[0]+1:sp[1]], worktree, allowInlineScripts, depth+1) {
			return false
		}
	}
	return true
}

// opaqueShellCore is the segment judge. valueClass lets a value-class $(...)
// result sit in a read-only tool argument (and in mkdir). It does not expand
// assignments or subshells; the caller does that once.
func opaqueShellCore(command, worktree string, allowInlineScripts, valueClass bool) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return true
	}
	command = extractProcessSubstitutions(command)
	if hasShellGrouping(command) || hasOpaqueShellSyntax(command) {
		return true
	}
	segments, pipedIn := splitCommandChainWithPipes(command)
	cwd := strings.TrimSpace(worktree)
	if cwd == "" {
		cwd = "."
	}
	if len(segments) > 1 && !pipedIn[0] && isWorktreeCD(segments[0], worktree) {
		if nextCWD, ok := advanceScopedCD(segments[0], cwd, worktree); ok {
			cwd = nextCWD
		}
		segments = segments[1:]
		pipedIn = pipedIn[1:]
	}
	if len(segments) == 0 {
		return true
	}
	outsideCWD := false
	hasExec := false
	vars := loopBindings(segments, pipedIn, valueClass)
	for i, rawSeg := range segments {
		seg := strings.TrimSpace(rawSeg)
		if seg == "" || strings.HasPrefix(seg, "#") {
			continue
		}
		words := stripShellKeywords(shellWords(seg))
		if len(words) == 0 {
			continue
		}
		if hasDollarExpansion(seg) && !isBenignPathExport(seg) && !expansionsAreBounded(seg, words, vars, valueClass) {
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
		if base == "set" {
			if !isHarmlessSet(cmdWords) {
				return true
			}
			continue
		}
		if base == "cd" || base == "pushd" || base == "popd" {
			if base != "cd" || outsideCWD {
				outsideCWD = true
				cwd = ""
			} else if nextCWD, ok := advanceScopedCD(seg, cwd, worktree); ok {
				cwd = nextCWD
			} else {
				outsideCWD = true
				cwd = ""
			}
			continue
		}
		if outsideCWD && !isRecoverableSegment(cmdWords, "", map[string]bool{}) {
			return true
		}
		prevSeg := ""
		if i > 0 {
			prevSeg = segments[i-1]
		}
		if dataArgInterpreters[base] &&
			!(base == "xargs" && isSafeXargsSegment(args, i > 0 && pipedIn[i], prevSeg)) &&
			!(base == "ssh" && len(args) == 1 && args[0] == maskedDataArgument) &&
			!isSafeShellScriptSegment(base, args, i > 0 && pipedIn[i]) {
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

func isSafeExternalCDTarget(resolved string) bool {
	cleaned := filepath.Clean(resolved)
	slash := filepath.ToSlash(cleaned)
	if slash == "" || slash == "/" || filepath.Dir(cleaned) == cleaned || isSensitiveExternalPath(resolved) || isSecretFilePath(resolved) || isProtectedShellReadPath(resolved) {
		return false
	}
	if real, ok := resolvePathWithSymlinks(resolved, 0); ok && real != "" {
		realClean := filepath.Clean(real)
		realSlash := filepath.ToSlash(realClean)
		if realSlash == "/" || filepath.Dir(realClean) == realClean || isSensitiveExternalPath(real) || isSecretFilePath(real) || isProtectedShellReadPath(real) {
			return false
		}
	}
	return true
}

func advanceScopedCD(seg, cwd, worktree string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	trimmed := strings.TrimSpace(seg)
	if strings.HasPrefix(trimmed, "cd ") || strings.HasPrefix(trimmed, "cd\t") {
		arg := strings.TrimSpace(trimmed[2:])
		if len(arg) >= 2 && ((arg[0] == '"' && arg[len(arg)-1] == '"') || (arg[0] == '\'' && arg[len(arg)-1] == '\'')) {
			arg = arg[1 : len(arg)-1]
		}
		if synth, ok := normalizeHomePrefixPath(arg); ok {
			arg = synth
		}
		trimmed = "cd " + arg
	}
	m := cdSegmentRe.FindStringSubmatch(trimmed)
	if m == nil {
		return "", false
	}
	target := m[1]
	if strings.HasPrefix(target, "~") || strings.HasPrefix(target, "-") {
		return "", false
	}
	if isAbsOrRootedPath(target) {
		for _, part := range strings.Split(filepath.ToSlash(target), "/") {
			if part == ".." {
				return "", false
			}
		}
		cleaned := filepath.Clean(target)
		if !isSafeExternalCDTarget(cleaned) {
			return "", false
		}
		return cleaned, true
	}
	wt := strings.TrimSpace(worktree)
	if isAbsOrRootedPath(cwd) {
		resolved := filepath.Clean(filepath.Join(cwd, target))
		if wt != "" && isAbsOrRootedPath(wt) {
			base := filepath.Clean(wt)
			slashCWD := filepath.ToSlash(cwd)
			slashBase := strings.TrimSuffix(filepath.ToSlash(base), "/")
			slashResolved := filepath.ToSlash(resolved)
			if slashCWD == slashBase || strings.HasPrefix(slashCWD, slashBase+"/") {
				if (slashResolved != slashBase && !strings.HasPrefix(slashResolved, slashBase+"/")) || !pathWithin(base, resolved) {
					return "", false
				}
				return resolved, true
			}
		}
		if !isSafeExternalCDTarget(resolved) {
			return "", false
		}
		return resolved, true
	}
	cleaned := path.Clean(cwd + "/" + filepath.ToSlash(target))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || isSensitiveExternalPath(cleaned) || isSecretFilePath(cleaned) || isProtectedShellReadPath(cleaned) {
		return "", false
	}
	return cleaned, true
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
			for k < len(words) {
				w := words[k]
				switch {
				case envVarAssignmentWordRe.MatchString(w), w == "-i", w == "-", w == "--ignore-environment",
					strings.HasPrefix(w, "--unset=") && shellVarNameRe.MatchString(strings.TrimPrefix(w, "--unset=")):
					k++
				case w == "-u" && k+1 < len(words) && shellVarNameRe.MatchString(words[k+1]):
					k += 2
				default:
					goto doneEnv
				}
			}
		doneEnv:
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

// inlineScriptTool resolves an interpreter binary, including version-suffixed
// names such as python3.12, node20 or ruby3.2, to its canonical family.
func inlineScriptTool(base string) (string, bool) {
	for _, name := range []string{"python", "python3", "node", "perl", "ruby", "php"} {
		if base == name {
			return name, true
		}
		if rest := strings.TrimPrefix(base, name); rest != base && interpreterVersionRe.MatchString(rest) {
			return name, true
		}
	}
	if base == "nodejs" {
		return "node", true
	}
	return "", false
}

// awkProgramExecutes reports whether an awk-family invocation carries an inline
// program whose reach cannot be verified: system() calls, command pipes, or a
// program read from a file all execute outside the shell's static view.
func awkProgramExecutes(base string, args []string) bool {
	if !awkTools[base] {
		return false
	}
	var program []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			continue
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "--file="):
			return true
		case a == "-F" || a == "--field-separator" || a == "-v" || a == "--assign":
			i++
			continue
		case strings.HasPrefix(a, "-F") || strings.HasPrefix(a, "-v") || strings.HasPrefix(a, "--"):
			continue
		case strings.HasPrefix(a, "-") && a != "-":
			continue
		default:
			program = append(program, a)
		}
	}
	if len(program) == 0 {
		return true
	}
	text := strings.Join(program, " ")
	return awkExecRe.MatchString(text) || awkTextHasPipe(text)
}

func awkTextHasPipe(text string) bool {
	inString, inRegex, canStartRegex := false, false, true
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			if c == '\\' {
				i++
			} else if c == '"' {
				inString, canStartRegex = false, false
			}
			continue
		}
		if inRegex {
			if c == '\\' {
				i++
			} else if c == '/' {
				inRegex, canStartRegex = false, false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '/':
			if canStartRegex && strings.IndexByte(text[i+1:], '/') >= 0 {
				inRegex = true
			}
		case '|':
			if i+1 < len(text) && text[i+1] == '|' {
				i++
				canStartRegex = true
				continue
			}
			return true
		case ' ', '\t', '\n', '\r':
		case '~', '!', ',', '(', ';', '{', '}', ':', '?', '&', '=':
			canStartRegex = true
		default:
			canStartRegex = false
		}
	}
	return false
}
