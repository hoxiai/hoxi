package runner

import (
	"path/filepath"
	"strings"
)

// Literal constant propagation. The rewritten text is only a judgment form:
// the shell still runs the original command. Substitution is all-or-nothing.
// A value that is a secret, a reassignment, a command-position use, or any
// shape this pass cannot see stays opaque. Loop expansion and value-class
// substitutions live in opaque_expand.go. Mirrors .opencode/lib/shell-constants.js.

const (
	maxLiteralLoopItems = 64
	maxSubshellDepth    = 2
)

const (
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
	// Value-sub assignments are replaced while segment offsets still match
	// the original text. Loop expansion and $NAME substitution run after.
	rewritten := replaceValueSubAssignments(command, segs, bindings)
	rewritten, expanded := expandLiteralForLoops(rewritten, bindings)
	substituted, ok := substituteVars(rewritten, bindings)
	if !ok {
		return command, constReject
	}
	rewritten = substituted
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
	subshells, _ := findSubshellSpans(command)
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
		insideLoop := indexInside(seg.start, loops) || indexInside(seg.start, subshells)
		names := expansionNamesOf(text)
		for _, name := range names {
			if _, ok := bound[name]; !ok {
				usedBefore[name] = true
			}
		}
		// A for-loop variable is expanded later when its list is literal.
		// Poison it only when it shadows a constant or was already rebound,
		// so the constant cannot leak into the loop.
		if name, ok := forLoopVar(text); ok {
			if _, exists := bound[name]; exists || poisoned[name] {
				poison(name)
			}
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
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return "", "", false
	}
	name = s[:eq]
	if !shellVarNameRe.MatchString(name) || assignmentValueHasBareSpace(s[eq+1:]) {
		return "", "", false
	}
	return name, s[eq+1:], true
}

// assignmentValueHasBareSpace reports whitespace that is not inside quotes
// or a $(...) value. `TREE=$(git write-tree)` is one assignment; `X=1 rm`
// is an assignment plus a command and stays opaque.
func assignmentValueHasBareSpace(s string) bool {
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
		if c == '$' && i+1 < len(s) && s[i+1] == '(' {
			end := processSubstitutionEnd(s, i+2)
			if end < 0 {
				return true
			}
			i = end
			continue
		}
		if c == ' ' || c == '\t' {
			return true
		}
	}
	return quote != 0
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
	if strings.ContainsAny(strings.ReplaceAll(v, "$?", ""), "\n*?[") || strings.Contains(v, "`") || strings.Contains(v, "$(") || strings.Contains(v, "${") {
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
		if quote == 0 && (c == ';' || c == '\n' || c == '|' || c == '(') {
			commandWord = true
			atWordStart = true
			b.WriteByte(c)
			continue
		}
		// `&` starts a new command. `&&` does too. `2>&1` and `>&` do not.
		if quote == 0 && c == '&' && (i == 0 || s[i-1] != '>') && (i+1 >= len(s) || s[i+1] != '>') {
			commandWord = true
			atWordStart = true
			b.WriteByte(c)
			continue
		}
		if quote == 0 && (c == ' ' || c == '\t' || c == '\r') {
			if !atWordStart {
				commandWord = false
			}
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
		if !pure || !ok {
			continue
		}
		repl := ""
		if v.valueSub {
			repl = v.inner
		} else if strings.Contains(v.text, "$") {
			repl = ":"
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
