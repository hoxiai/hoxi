package runner

import (
	"path"
	"path/filepath"
	"strings"
)

// Literal constant propagation, literal for-loop expansion and value-class
// command substitutions. The rewritten text is only a judgment form: the
// shell still runs the original command. Substitution is all-or-nothing.
// A value that is a secret, a reassignment, a command-position use, or any
// shape this pass cannot see stays opaque. Mirrors .opencode/lib/shell-constants.js.

const (
	maxLiteralLoopItems = 64
	maxSubshellDepth    = 2

	constNoop = iota
	constApplied
	constSecret
	constReject
)

const (
	sepStart = byte(0)
	sepSeq   = byte(';')
	sepAnd   = byte('&')
	sepOr    = byte('|')
	sepPipe  = byte('p')
	sepAsync = byte('b')
)

// paramSensitiveTools take a command substitution as a path, flag or ref,
// so a value-class $(...) result in their arguments stays opaque.
var paramSensitiveTools = setOf("git", "find", "rm", "rmdir", "unlink", "curl", "wget", "cp", "mv")

// acceptsValueClassArg reports whether a value-class $(...) result may be
// an argument of base. Read-only tools may take it; mkdir may too (it only
// creates directories). Parameter-sensitive commands and shells may not.
func acceptsValueClassArg(base string) bool {
	if paramSensitiveTools[base] || dataArgInterpreters[base] {
		return false
	}
	return readOnlyDataTools[base] || base == "mkdir"
}

type constSeg struct {
	start, end int
	before     byte
	next       byte
}

func (s constSeg) text(command string) string { return command[s.start:s.end] }

type constVal struct {
	text     string
	valueSub bool
	inner    string
	secret   bool
}

// findSubshellSpans returns the outer (...) groups that are not $(...) or
// <(...). ok is false when a group is unterminated.
func findSubshellSpans(s string) (spans [][2]int, ok bool) {
	var quote byte
	depth := 0
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && quote != '\'' && i+1 < len(s) {
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else if c == '$' && i+1 < len(s) && s[i+1] == '(' {
				end := processSubstitutionEnd(s, i+2)
				if end < 0 {
					return nil, false
				}
				i = end
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '$':
			if i+1 < len(s) && s[i+1] == '(' {
				end := processSubstitutionEnd(s, i+2)
				if end < 0 {
					return nil, false
				}
				i = end
			}
		case '<':
			if i+1 < len(s) && s[i+1] == '(' && (i == 0 || isWordBoundary(s[i-1])) {
				end := processSubstitutionEnd(s, i+2)
				if end < 0 {
					return nil, false
				}
				i = end
			}
		case '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ')':
			if depth == 0 {
				return nil, false
			}
			depth--
			if depth == 0 && start >= 0 {
				spans = append(spans, [2]int{start, i})
				start = -1
			}
		}
	}
	if quote != 0 || depth != 0 {
		return nil, false
	}
	return spans, true
}

// eraseSubshells replaces each outer (...) group with `:`. The caller has
// already judged the body on its own, so a cd inside it cannot leak.
func eraseSubshells(s string) string {
	spans, ok := findSubshellSpans(s)
	if !ok || len(spans) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		b.WriteString(s[prev:sp[0]])
		b.WriteByte(':')
		prev = sp[1] + 1
	}
	b.WriteString(s[prev:])
	return b.String()
}

// concretizeLiterals rewrites literal assignments and literal for-loops into
// text the existing judges can see. secret means a substituted path is a
// credential path: the caller stays opaque, and the scanner still sees the
// rewritten command.
func concretizeLiterals(command string) (string, int) {
	command = strings.TrimSpace(command)
	if command == "" || (!strings.Contains(command, "=") && !containsFor(command)) {
		return command, constNoop
	}
	segs := splitConstSegments(command)
	bindings, poisoned := constBindings(command, segs)
	if len(poisoned) > 0 && expansionsHit(command, poisoned) {
		return command, constReject
	}
	rewritten, expanded := expandLiteralForLoops(command, bindings)
	if substituted, ok := substituteVars(rewritten, bindings); ok {
		rewritten = substituted
	} else {
		return command, constReject
	}
	rewritten = replaceValueSubAssignments(rewritten, segs, bindings)
	if rewritten == command && !expanded {
		return command, constNoop
	}
	if constResultIsSecret(rewritten, bindings) {
		return rewritten, constSecret
	}
	return rewritten, constApplied
}

func containsFor(s string) bool {
	var quote byte
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
		if c == '\'' {
			quote = '\''
			continue
		}
		if c == 'f' && wordAt(s, i, "for") {
			return true
		}
	}
	return false
}

func wordAt(s string, i int, word string) bool {
	if i+len(word) > len(s) || s[i:i+len(word)] != word {
		return false
	}
	if i > 0 && !isWordBoundary(s[i-1]) {
		return false
	}
	return i+len(word) == len(s) || isWordBoundary(s[i+len(word)])
}

func splitConstSegments(command string) []constSeg {
	var segs []constSeg
	var quote byte
	segStart := 0
	before := sepStart
	flush := func(at int, next byte) {
		if strings.TrimSpace(command[segStart:at]) != "" {
			segs = append(segs, constSeg{start: segStart, end: at, before: before, next: next})
		}
		segStart = at
		before = next
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		next := byte(0)
		if i+1 < len(command) {
			next = command[i+1]
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(command) {
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			}
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
		case c == '&' && next == '&':
			flush(i, sepAnd)
			i++
			segStart = i + 1
		case c == '|' && next == '|':
			flush(i, sepOr)
			i++
			segStart = i + 1
		case c == '|':
			flush(i, sepPipe)
			segStart = i + 1
		case c == ';' || c == '\n':
			flush(i, sepSeq)
			segStart = i + 1
		case c == '&' && (i == 0 || command[i-1] != '>') && next != '>':
			flush(i, sepAsync)
			segStart = i + 1
		}
	}
	flush(len(command), sepSeq)
	return segs
}

func constBindings(command string, segs []constSeg) (map[string]constVal, map[string]bool) {
	bound := map[string]constVal{}
	poisoned := map[string]bool{}
	loops := forLoopRanges(command)
	poison := func(name string) {
		if name == "" {
			return
		}
		poisoned[name] = true
		delete(bound, name)
	}
	usedBefore := map[string]bool{}
	for _, seg := range segs {
		text := strings.TrimSpace(seg.text(command))
		insideLoop := indexInside(seg.start, loops)
		names := expansionNamesOf(text)
		for _, name := range names {
			if _, ok := bound[name]; !ok {
				usedBefore[name] = true
			}
		}
		if name, ok := forLoopVar(text); ok {
			poison(name)
		}
		for _, name := range rebindNames(text) {
			poison(name)
		}
		name, raw, pure := pureAssignment(text)
		if name == "" {
			continue
		}
		uncertain := seg.before == sepAnd || seg.before == sepOr || seg.before == sepPipe || seg.before == sepAsync || seg.next == sepPipe
		if !pure || insideLoop || uncertain || usedBefore[name] || harmlessEnvVars[name] || poisoned[name] {
			poison(name)
			continue
		}
		if _, exists := bound[name]; exists {
			poison(name)
			continue
		}
		val, ok := parseConstValue(raw)
		if !ok {
			poison(name)
			continue
		}
		bound[name] = val
	}
	return bound, poisoned
}

func indexInside(i int, ranges [][2]int) bool {
	for _, r := range ranges {
		if i >= r[0] && i < r[1] {
			return true
		}
	}
	return false
}

func expansionNamesOf(s string) []string {
	names, ok := shellExpansionNames(s)
	if !ok {
		return nil
	}
	return names
}

func expansionsHit(s string, names map[string]bool) bool {
	found, ok := shellExpansionNames(s)
	if !ok {
		// An operator form such as ${x:-y} is already opaque on its own.
		return false
	}
	for _, name := range found {
		if names[name] {
			return true
		}
	}
	return false
}

func pureAssignment(seg string) (name, raw string, pure bool) {
	seg = strings.TrimSpace(seg)
	if strings.HasPrefix(seg, "export ") || strings.HasPrefix(seg, "export\t") {
		rest := strings.TrimSpace(seg[len("export"):])
		name, raw, ok := splitOneAssignment(rest)
		return name, raw, ok
	}
	name, raw, ok := splitOneAssignment(seg)
	return name, raw, ok
}

func splitOneAssignment(s string) (name, raw string, ok bool) {
	if strings.ContainsAny(s, " \t") {
		return "", "", false
	}
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return "", "", false
	}
	name = s[:eq]
	if !shellVarNameRe.MatchString(name) {
		return "", "", false
	}
	return name, s[eq+1:], true
}

func parseConstValue(raw string) (constVal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return constVal{}, false
	}
	if strings.HasPrefix(raw, "$(") && strings.HasSuffix(raw, ")") && processSubstitutionEnd(raw, 2) == len(raw)-1 {
		inner := raw[2 : len(raw)-1]
		if !isValueClassSegment(inner) || opaqueShellCore(inner, "", false, false) {
			return constVal{}, false
		}
		return constVal{valueSub: true, inner: inner}, true
	}
	val, ok := unquoteConst(raw)
	if !ok || constLiteralBad(val) {
		return constVal{}, false
	}
	return constVal{text: val, secret: constPathSecret(val)}, true
}

func unquoteConst(raw string) (string, bool) {
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' && strings.Count(raw, "'") == 2 {
		return raw[1 : len(raw)-1], true
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		body := raw[1 : len(raw)-1]
		if strings.ContainsAny(body, "\"`") || strings.Contains(body, "$(") {
			return "", false
		}
		return body, true
	}
	if strings.ContainsAny(raw, " \t\"'`") {
		return "", false
	}
	return raw, true
}

func constLiteralBad(v string) bool {
	if strings.ContainsAny(v, "\n*?[") || strings.Contains(v, "`") || strings.Contains(v, "$(") || strings.Contains(v, "${") {
		return true
	}
	names, ok := wordExpansionNames(v)
	if !ok {
		return true
	}
	for _, name := range names {
		if !harmlessEnvVars[name] {
			return true
		}
	}
	return false
}

func constPathSecret(p string) bool {
	return isSecretFilePath(p) || isSensitiveExternalPath(p)
}

func constResultIsSecret(command string, bindings map[string]constVal) bool {
	for _, v := range bindings {
		if v.secret || (!v.valueSub && constPathSecret(v.text)) {
			return true
		}
	}
	for _, seg := range splitConstSegments(command) {
		for _, w := range shellWords(seg.text(command)) {
			if constPathSecret(w) {
				return true
			}
		}
	}
	return false
}

func rebindNames(seg string) []string {
	words := stripShellKeywords(shellWords(seg))
	if len(words) == 0 {
		return nil
	}
	base := filepath.Base(words[0])
	var names []string
	switch {
	case base == "read":
		if name, _ := readBinding(words); name != "" {
			names = append(names, name)
		}
	case base == "printf":
		for k := 1; k+1 < len(words); k++ {
			if words[k] == "-v" && shellVarNameRe.MatchString(words[k+1]) {
				names = append(names, words[k+1])
			}
		}
	case loopVarRebinders[base]:
		for _, a := range words[1:] {
			name := a
			if eq := strings.IndexByte(a, '='); eq > 0 {
				name = a[:eq]
			}
			if shellVarNameRe.MatchString(name) && !isBenignPathAssignment(a) {
				names = append(names, name)
			}
		}
	}
	return names
}

func forLoopVar(seg string) (string, bool) {
	words := shellWords(strings.TrimSpace(seg))
	if len(words) < 2 || words[0] != "for" || !shellVarNameRe.MatchString(words[1]) {
		return "", false
	}
	return words[1], true
}

// substituteVars replaces bound $NAME / ${NAME} uses. It fails closed when a
// bound name is the command word or uses a parameter operator.
func substituteVars(s string, vars map[string]constVal) (string, bool) {
	if len(vars) == 0 {
		return s, true
	}
	var b strings.Builder
	var quote byte
	atWordStart := true
	commandWord := true
	fail := false
	writeRaw := func(v string) {
		if atWordStart && commandWord {
			fail = true
		}
		b.WriteString(v)
		atWordStart = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '\'' {
			b.WriteByte(c)
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(c)
			i++
			b.WriteByte(s[i])
			atWordStart = false
			continue
		}
		if c == '$' && i+1 < len(s) && s[i+1] == '(' {
			end := processSubstitutionEnd(s, i+2)
			if end < 0 {
				return s, false
			}
			inner, ok := substituteVars(s[i+2:end], vars)
			if !ok {
				return s, false
			}
			b.WriteString("$(")
			b.WriteString(inner)
			b.WriteByte(')')
			i = end
			atWordStart = false
			continue
		}
		if quote == '"' && c == '"' {
			quote = 0
			b.WriteByte(c)
			continue
		}
		if quote == 0 && (c == '\'' || c == '"') {
			quote = c
			b.WriteByte(c)
			continue
		}
		if quote == 0 && (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
			if !atWordStart {
				commandWord = false
			}
			atWordStart = true
			b.WriteByte(c)
			continue
		}
		if quote == 0 && (c == ';' || c == '\n' || c == '|' || c == '&') {
			commandWord = true
			atWordStart = true
			b.WriteByte(c)
			continue
		}
		if c == '$' && i+1 < len(s) {
			name, next, ok := readDollarName(s, i)
			if !ok {
				return s, false
			}
			if v, bound := vars[name]; bound {
				repl := "$" + cmdSubstVar
				if !v.valueSub {
					repl = v.text
				}
				writeRaw(repl)
				i = next - 1
				continue
			}
			b.WriteString(s[i:next])
			i = next - 1
			atWordStart = false
			continue
		}
		b.WriteByte(c)
		atWordStart = false
		if quote == 0 && c == '=' {
			// NAME=value prefix: the next word is still the command.
			commandWord = true
			atWordStart = true
		}
	}
	if fail {
		return s, false
	}
	return b.String(), true
}

func readDollarName(s string, i int) (name string, next int, ok bool) {
	if i+1 >= len(s) || s[i] != '$' {
		return "", i, false
	}
	n := s[i+1]
	switch {
	case n == '{':
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return "", i, false
		}
		body := s[i+2 : i+2+end]
		if !shellVarNameRe.MatchString(body) {
			return "", i, false
		}
		return body, i + 2 + end + 1, true
	case n == '_' || (n >= 'a' && n <= 'z') || (n >= 'A' && n <= 'Z'):
		k := i + 1
		for k < len(s) && (s[k] == '_' || (s[k] >= 'a' && s[k] <= 'z') || (s[k] >= 'A' && s[k] <= 'Z') || (s[k] >= '0' && s[k] <= '9')) {
			k++
		}
		return s[i+1 : k], k, true
	case n == '?' || n == '!':
		return string(n), i + 2, true
	default:
		return "", i + 1, true
	}
}

func replaceValueSubAssignments(command string, segs []constSeg, bindings map[string]constVal) string {
	if len(bindings) == 0 {
		return command
	}
	var b strings.Builder
	prev := 0
	for _, seg := range segs {
		name, _, pure := pureAssignment(strings.TrimSpace(seg.text(command)))
		v, ok := bindings[name]
		if !pure || !ok || !v.valueSub {
			continue
		}
		b.WriteString(command[prev:seg.start])
		b.WriteString(v.inner)
		prev = seg.end
	}
	b.WriteString(command[prev:])
	return b.String()
}

func forLoopRanges(s string) [][2]int {
	var ranges [][2]int
	scanForLoops(s, func(start, end int, items []string, body string) bool {
		ranges = append(ranges, [2]int{start, end})
		return false // keep the original text; expansion is a separate pass
	})
	return ranges
}

func expandLiteralForLoops(s string, bindings map[string]constVal) (string, bool) {
	changed := false
	out := scanForLoops(s, func(start, end int, items []string, body string) bool {
		if len(items) == 0 || len(items) > maxLiteralLoopItems || !literalForItems(items) {
			return false
		}
		name := forNameAt(s[start:end])
		if name == "" {
			return false
		}
		if _, shadowed := bindings[name]; shadowed {
			return false
		}
		var copies []string
		for _, item := range items {
			one, ok := substituteVars(body, map[string]constVal{name: {text: item}})
			if !ok {
				return false
			}
			copies = append(copies, strings.TrimSpace(one))
		}
		replaced := strings.Join(copies, "\n")
		s = s[:start] + replaced + s[end:]
		changed = true
		return true
	})
	if changed {
		return out, true
	}
	return s, false
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
		if strings.ContainsAny(item, "$*?[`") || strings.Contains(item, "${") {
			return false
		}
		if isSecretFilePath(item) || isSensitiveExternalPath(item) || isProtectedRelPath(item) {
			return false
		}
	}
	return true
}

// scanForLoops walks outermost for/do/done loops. visit returns true when it
// rewrote s and the scan should restart.
func scanForLoops(s string, visit func(start, end int, items []string, body string) bool) string {
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
			body := s[bodyStart:bodyEnd]
			if visit(i, doneEnd, items, body) {
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
		lines := false
		for _, a := range args {
			switch a {
			case "-l", "--lines":
				lines = true
			case "-c", "-m", "-L", "-w", "--bytes", "--chars", "--max-line-length", "--words":
				return false
			}
		}
		return lines
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

// commandSubsAreValueClass reports whether every $(...) in s is a value-class
// command that is itself not opaque. Nested substitutions fail closed.
func commandSubsAreValueClass(s string) bool {
	var quote byte
	found := false
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
			inner := s[i+2:end]
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
