package runner

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Narrowing of isOpaqueShellCommand for routine read-only loops, such as
//
//	rg -l "watch\(" src | while read f; do rg -q watch <(rg "^import" "$f") || echo "MISSING: $f"; done
//
// A variable expansion stops being opaque only when its value is bounded by
// the same command and it only feeds read-only tools:
//   - the variable is a loop variable: `for x in <literal, non-secret items>`
//     or `while read x` fed through a pipe by a file-listing command that
//     cannot emit secret names (rg -l / rg --files with a type filter, which
//     skip hidden files such as .env; find or git ls-files restricted to
//     `*.ext` names);
//   - it appears only as an argument of a read-only tool (rg, grep, cat, head,
//     echo, ...), never as the command name and never in an argument of a
//     parameter-sensitive command (git, find, rm, kill, curl, ...);
//   - nothing else in the command rebinds it.
//
// Inherited environment variables (`cat "$KUBECONFIG"`), plain assignments
// (`P=.env; cat $P`), `$(…)`, backticks, eval, `sh -c` and parameter forms
// such as `${f:-.env}` stay opaque. `<(cmd)` is unpacked so cmd is judged as
// its own segment. Mirrors .opencode/lib/opaque-command.js; the shared
// .opencode/lib/opaque-command.cases.json keeps both ports aligned.

const (
	processSubstitutionPlaceholder = "/dev/fd/63"
	// cmdSubstVar stands in for a `$(…)` result: its value is whatever the
	// inner command prints, so only value-only tools may take it.
	cmdSubstVar = "__hoxi_cmdsub__"
)

var (
	shellKeywords = setOf("while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!")

	readOnlyDataTools = setOf("rg", "grep", "egrep", "fgrep", "cat", "head", "tail", "wc", "ls", "stat",
		"file", "echo", "printf", "diff", "cmp", "sort", "uniq", "jq", "test", "[", "basename",
		"dirname", "realpath", "true", "false", ":")

	// Builtins that can rebind a variable behind the policy's back.
	loopVarRebinders = setOf("declare", "local", "typeset", "readonly", "mapfile", "readarray", "let", "unset", "export")

	shellVarNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	extGlobRe        = regexp.MustCompile(`^\*\.[A-Za-z0-9]+$`)
	gitExtPathspecRe = regexp.MustCompile(`^[A-Za-z0-9_./-]*\*\.[A-Za-z0-9]+$`)
	rgListFlagRe     = regexp.MustCompile(`^-[a-zA-Z]*l[a-zA-Z]*$`)
	rgUnsafeShortRe  = regexp.MustCompile(`^-[a-zA-Z.]*[u.L][a-zA-Z.]*$`)
	dotGlobRe        = regexp.MustCompile(`(^|/)\.[^/]*[*?]`)

	// harmlessEnvVars hold system locations and settings, never secrets, so
	// they may feed read-only tools and PATH assignments. Anything else that
	// is inherited ($OPENAI_API_KEY, $KUBECONFIG, ...) stays opaque.
	harmlessEnvVars = setOf("HOME", "PATH", "PWD", "OLDPWD", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG",
		"TERM", "GOPATH", "GOROOT", "GOBIN", "GOCACHE", "GOMODCACHE", "CARGO_HOME", "PNPM_HOME",
		"JAVA_HOME", "ANDROID_HOME",
		"?", "pipestatus") // exit statuses

	// valueOnlyTools print or test their arguments without opening them as
	// files, so they may take a `$(…)` result.
	valueOnlyTools = setOf("echo", "printf", "test", "[", "basename", "dirname", "true", "false", ":")
)

// extractProcessSubstitutions rewrites each `<(cmd)` into a placeholder
// argument and each `$(cmd)` into the $__hoxi_cmdsub__ variable, and appends
// cmd as its own line, so it is judged like any other command. Nested parentheses or an unterminated form leave the command
// unchanged (and therefore opaque).
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

// opacityCandidates returns the commands judged for opacity. OpenCode sends
// the whole command in metadata.command and its own split of it in patterns
// (`read f`, `echo "MISSING: $f"`, ...); a piece judged alone loses the loop
// that binds its variable, so the whole command is the one judged.
func opacityCandidates(item map[string]interface{}) []string {
	meta := itemMetadata(item)
	if cmd := strings.TrimSpace(firstString(meta["command"])); cmd != "" {
		return []string{unwrapSandboxCommand(cmd)}
	}
	return commandCandidates(item)
}

// destructiveScanForms returns the texts the destructive patterns scan: the
// command with data arguments masked, plus its unpacked form when it uses
// `<(…)`, so `cat <(cat .env)` is judged by its inner `cat .env`.
func destructiveScanForms(cmd string) []string {
	forms := []string{maskCommandDataArguments(cmd)}
	if unpacked := extractProcessSubstitutions(cmd); unpacked != cmd {
		forms = append(forms, maskCommandDataArguments(unpacked))
	}
	return forms
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

// stripShellKeywords drops leading control keywords (`do if ! rg …` → `rg …`).
func stripShellKeywords(words []string) []string {
	for len(words) > 0 && shellKeywords[words[0]] {
		words = words[1:]
	}
	return words
}

// loopBindings returns, for every variable the command binds, whether all of
// its bindings are trustworthy loop bindings.
func loopBindings(segments []string, pipedIn []bool) map[string]bool {
	vars := map[string]bool{}
	bind := func(name string, ok bool) {
		if prev, seen := vars[name]; seen {
			ok = ok && prev
		}
		vars[name] = ok
	}
	for i, seg := range segments {
		words := stripShellKeywords(shellWords(seg))
		j := 0
		for j < len(words) && envVarAssignmentWordRe.MatchString(words[j]) {
			if !isBenignPathAssignment(words[j]) {
				bind(words[j][:strings.IndexByte(words[j], '=')], false)
			}
			j++
		}
		if j >= len(words) {
			continue
		}
		cmd := words[j:]
		switch base := filepath.Base(cmd[0]); {
		case base == "for":
			if name, ok := forLoopBinding(seg, cmd); name != "" {
				bind(name, ok)
			}
		case base == "read":
			name, ok := readBinding(cmd)
			if name != "" {
				// `IFS= read -r f` only changes how read splits its input.
				ok = ok && onlyIFSAssignments(words[:j]) && i > 0 && pipedIn[i] && isSafeListingProducer(segments[i-1])
				bind(name, ok)
			}
		case base == "printf":
			for k := 1; k+1 < len(cmd); k++ {
				if cmd[k] == "-v" {
					bind(cmd[k+1], false)
				}
			}
		case loopVarRebinders[base]:
			for _, a := range cmd[1:] {
				if isBenignPathAssignment(a) {
					continue
				}
				name := a
				if eq := strings.IndexByte(a, '='); eq > 0 {
					name = a[:eq]
				}
				if shellVarNameRe.MatchString(name) {
					bind(name, false)
				}
			}
		}
	}
	return vars
}

func onlyIFSAssignments(words []string) bool {
	for _, w := range words {
		if !strings.HasPrefix(w, "IFS=") {
			return false
		}
	}
	return true
}

// forLoopBinding validates `for name in item...`: items must be literal and
// none may name a secret, sensitive or execution-controlling path.
func forLoopBinding(seg string, cmd []string) (string, bool) {
	if len(cmd) < 2 || !shellVarNameRe.MatchString(cmd[1]) {
		return "", false
	}
	name := cmd[1]
	if len(cmd) < 4 || cmd[2] != "in" || hasDollarExpansion(seg) || strings.Contains(seg, "`") {
		return name, false
	}
	for _, item := range cmd[3:] {
		if strings.Contains(item, "[") || dotGlobRe.MatchString(item) ||
			isSecretFilePath(item) || isSensitiveExternalPath(item) || isProtectedRelPath(item) {
			return name, false
		}
	}
	return name, true
}

// readBinding parses `read [-r] name`.
func readBinding(cmd []string) (string, bool) {
	var names []string
	for _, a := range cmd[1:] {
		switch {
		case a == "-r":
		case strings.HasPrefix(a, "-"):
			return lastVarName(cmd[1:]), false
		case shellVarNameRe.MatchString(a):
			names = append(names, a)
		default:
			return lastVarName(cmd[1:]), false
		}
	}
	if len(names) != 1 {
		return lastVarName(cmd[1:]), false
	}
	return names[0], true
}

func lastVarName(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		if shellVarNameRe.MatchString(args[i]) {
			return args[i]
		}
	}
	return ""
}

// isSafeListingProducer reports a literal file-listing command whose output
// cannot name a hidden or secret file.
func isSafeListingProducer(seg string) bool {
	if hasDollarExpansion(seg) || strings.Contains(seg, "`") {
		return false
	}
	words, ok := stripNullRedirects(shellWords(seg))
	if !ok || len(words) == 0 || envVarAssignmentWordRe.MatchString(words[0]) {
		return false
	}
	args := words[1:]
	switch filepath.Base(normalizeAbsoluteTool(words[0])) {
	case "rg":
		listing, files, filtered := false, false, false
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "--hidden" || a == "-L" || a == "--follow" || strings.HasPrefix(a, "--no-ignore") ||
				strings.HasPrefix(a, "--pre") || strings.HasPrefix(a, "--hostname-bin") ||
				(strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && rgUnsafeShortRe.MatchString(a)):
				return false
			case a == "--files-with-matches" || a == "--files-without-match" || rgListFlagRe.MatchString(a):
				listing = true
			case a == "--files":
				files = true
			case (a == "-g" || a == "--glob") && i+1 < len(args):
				filtered = filtered || extGlobRe.MatchString(args[i+1])
				i++
			case strings.HasPrefix(a, "--glob="):
				filtered = filtered || extGlobRe.MatchString(strings.TrimPrefix(a, "--glob="))
			case (a == "-t" || a == "--type") && i+1 < len(args):
				filtered = true
				i++
			case strings.HasPrefix(a, "--type="):
				filtered = true
			}
		}
		return listing || (files && filtered)
	case "find":
		named := false
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case !strings.HasPrefix(a, "-") && a != "!" && a != "(" && a != ")":
				if named {
					return false // a stray word after the predicates
				}
			case a == "-name" || a == "-iname":
				if i+1 >= len(args) || !extGlobRe.MatchString(args[i+1]) {
					return false
				}
				named = true
				i++
			case a == "-type" || a == "-maxdepth" || a == "-mindepth":
				i++
			case a == "-not" || a == "!" || a == "-o" || a == "-or" || a == "-a" || a == "-and" || a == "-print":
			default:
				return false
			}
		}
		return named
	case "git":
		if len(args) < 2 || args[0] != "ls-files" {
			return false
		}
		for _, a := range args[1:] {
			if a == "--" {
				continue
			}
			if !gitExtPathspecRe.MatchString(a) {
				return false
			}
		}
		return true
	}
	return false
}

// expansionsAreBounded reports whether every expansion in seg is safe:
//   - a benign PATH assignment (`PATH=$HOME/bin:$PATH`, `export PATH=…`);
//   - otherwise only in arguments of a read-only tool, never in the command
//     name, and each name is a trustworthy loop variable, a harmless
//     environment variable not rebound by the command, or a `$(…)` result
//     taken by a value-only tool.
func expansionsAreBounded(seg string, words []string, vars map[string]bool) bool {
	if _, ok := shellExpansionNames(seg); !ok {
		return false
	}
	j := 0
	for j < len(words) && envVarAssignmentWordRe.MatchString(words[j]) {
		if strings.Contains(words[j], "$") && !isBenignPathAssignment(words[j]) {
			return false
		}
		j++
	}
	if j >= len(words) {
		return true
	}
	if strings.Contains(words[j], "$") {
		return false
	}
	base := filepath.Base(normalizeAbsoluteTool(words[j]))
	args := words[j+1:]
	if base == "export" {
		for _, a := range args {
			if strings.Contains(a, "$") && !isBenignPathAssignment(a) {
				return false
			}
		}
		return true
	}
	expanding := make([]string, 0, len(args))
	for _, a := range args {
		if strings.Contains(a, "$") {
			expanding = append(expanding, a)
		}
	}
	if len(expanding) == 0 {
		return true
	}
	if base == "git" {
		return gitMessageExpansionsOnly(args, vars)
	}
	if !readOnlyDataTools[base] {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--pre") || strings.HasPrefix(a, "--hostname-bin") ||
			strings.HasPrefix(a, "--compress-program") || (base == "printf" && a == "-v") {
			return false
		}
	}
	for _, a := range expanding {
		names, ok := wordExpansionNames(a)
		if !ok {
			return false
		}
		for _, name := range names {
			trusted, bound := vars[name]
			switch {
			case name == cmdSubstVar:
				if bound || !valueOnlyTools[base] {
					return false
				}
			case bound:
				if !trusted {
					return false
				}
			case !harmlessEnvVars[name]:
				return false
			}
		}
	}
	return true
}

// gitMessageExpansionsOnly accepts expansions only in the message of `git
// commit|tag|merge|stash -m …` (`git commit -m "$(cat <<'EOF' … EOF)"`):
// a message is data, never a path, flag or ref.
func gitMessageExpansionsOnly(args []string, vars map[string]bool) bool {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if gitGlobalValueFlags[args[i]] {
			i++
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	sub := args[i]
	if sub != "commit" && sub != "tag" && sub != "merge" && sub != "stash" {
		return false
	}
	for k := i + 1; k < len(args); k++ {
		a := args[k]
		if !strings.Contains(a, "$") {
			continue
		}
		prev := args[k-1]
		isMessage := prev == "-m" || prev == "--message" ||
			(sub == "commit" && len(prev) > 2 && prev[0] == '-' && prev[1] != '-' && strings.HasSuffix(prev, "m"))
		if !isMessage {
			return false
		}
		names, ok := wordExpansionNames(a)
		if !ok {
			return false
		}
		for _, name := range names {
			trusted, bound := vars[name]
			if bound && !trusted || !bound && name != cmdSubstVar && !harmlessEnvVars[name] {
				return false
			}
		}
	}
	return true
}

// isBenignPathAssignment accepts `PATH=…` whose expansions are all harmless
// environment variables (`PATH=$HOME/fvm/bin:$PATH`).
func isBenignPathAssignment(word string) bool {
	if !strings.HasPrefix(word, "PATH=") {
		return false
	}
	value := word[len("PATH="):]
	if strings.ContainsAny(value, "`;&|<>()") {
		return false
	}
	names, ok := wordExpansionNames(value)
	if !ok {
		return false
	}
	for _, name := range names {
		if !harmlessEnvVars[name] {
			return false
		}
	}
	return true
}

// wordExpansionNames lists `$name` / `${name}` in one unquoted word; any
// other `$` form fails.
func wordExpansionNames(word string) ([]string, bool) {
	var names []string
	for i := 0; i < len(word); i++ {
		if word[i] != '$' || i+1 >= len(word) {
			continue
		}
		next := word[i+1]
		switch {
		case next == '{':
			end := strings.IndexByte(word[i+2:], '}')
			if end < 0 || !shellVarNameRe.MatchString(word[i+2:i+2+end]) {
				return nil, false
			}
			names = append(names, word[i+2:i+2+end])
			i += end + 2
		case next == '_' || (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z'):
			k := i + 1
			for k < len(word) && (word[k] == '_' || (word[k] >= 'a' && word[k] <= 'z') || (word[k] >= 'A' && word[k] <= 'Z') || (word[k] >= '0' && word[k] <= '9')) {
				k++
			}
			names = append(names, word[i+1:k])
			i = k - 1
		case next == '?':
			names = append(names, "?")
			i++
		default:
			return nil, false
		}
	}
	return names, true
}

// shellExpansionNames lists `$name` / `${name}` expansions outside single
// quotes; any other expansion form (positional, special, operators) fails.
func shellExpansionNames(s string) ([]string, bool) {
	var names []string
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
			continue
		case c == '\\':
			i++
			continue
		case c == '"':
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			continue
		case c == '\'' && quote == 0:
			quote = '\''
			continue
		case c != '$' || i+1 >= len(s):
			continue
		}
		next := s[i+1]
		switch {
		case next == '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 || !shellVarNameRe.MatchString(s[i+2:i+2+end]) {
				return nil, false
			}
			names = append(names, s[i+2:i+2+end])
			i += end + 2
		case next == '_' || (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z'):
			k := i + 1
			for k < len(s) && (s[k] == '_' || (s[k] >= 'a' && s[k] <= 'z') || (s[k] >= 'A' && s[k] <= 'Z') || (s[k] >= '0' && s[k] <= '9')) {
				k++
			}
			names = append(names, s[i+1:k])
			i = k - 1
		case next == '?':
			names = append(names, "?")
			i++
		case dollarExpandRe.MatchString(string(next)) && !(quote == '"' && next == '"'):
			return nil, false
		}
	}
	return names, true
}
