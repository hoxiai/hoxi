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
// Inherited environment variables (`cat "$KUBECONFIG"`), secret assignments
// (`P=.env; cat $P`), backticks, eval, `sh -c` and parameter forms such as
// `${f:-.env}` stay opaque. A literal assignment in the same command is
// substituted before this judge runs (opaque_constants.go). `<(cmd)` is
// unpacked so cmd is judged as its own segment. Mirrors
// .opencode/lib/opaque-command.js; the shared
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
		"dirname", "realpath", "true", "false", ":", "exit", "return", "break", "continue",
		"lsof", "ps", "du", "df", "cut", "tr", "nl", "column", "shasum", "sha256sum", "md5", "xxd", "od", "sleep")

	// Builtins that can rebind a variable behind the policy's back.
	loopVarRebinders = setOf("declare", "local", "typeset", "readonly", "mapfile", "readarray", "let", "unset", "export")

	shellVarNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	extGlobRe        = regexp.MustCompile(`^\*\.[A-Za-z0-9]+$`)
	gitExtPathspecRe = regexp.MustCompile(`^[A-Za-z0-9_./-]*\*\.[A-Za-z0-9]+$`)
	rgListFlagRe     = regexp.MustCompile(`^-[a-zA-Z]*l[a-zA-Z]*$`)
	rgUnsafeShortRe     = regexp.MustCompile(`^-[a-zA-Z.]*[u.L][a-zA-Z.]*$`)
	dotGlobRe           = regexp.MustCompile(`(^|/)\.[^/]*[*?]`)
	sedReadOnlyScriptRe = regexp.MustCompile(`^\s*\d+(?:\s*,\s*(?:\d+|\+\d+))?\s*[pq](?:\s*[;\n]\s*\d+(?:\s*,\s*(?:\d+|\+\d+))?\s*[pq])*\s*$`)
	headTailCountRe     = regexp.MustCompile(`^[+-]?\d+$`)
	headTailFlagRe      = regexp.MustCompile(`^-(?:[nc]?[+-]?\d+|q|v|-quiet|-silent|-lines=[+-]?\d+|-bytes=[+-]?\d+)$`)

	// harmlessEnvVars hold system locations and settings, never secrets, so
	// they may feed read-only tools and PATH assignments. Anything else that
	// is inherited ($OPENAI_API_KEY, $KUBECONFIG, ...) stays opaque.
	harmlessEnvVars = setOf("HOME", "PATH", "PWD", "OLDPWD", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG",
		"TERM", "GOPATH", "GOROOT", "GOBIN", "GOCACHE", "GOMODCACHE", "CARGO_HOME", "PNPM_HOME",
		"JAVA_HOME", "ANDROID_HOME",
		"?", "!", "pipestatus") // exit status, and $! the last background pid

	// valueOnlyTools print or test their arguments without opening them as
	// files, so they may take a `$(…)` result.
	valueOnlyTools = setOf("echo", "printf", "test", "[", "basename", "dirname", "true", "false", ":", "exit", "return", "break", "continue")
)

// stripShellKeywords drops leading control keywords (`do if ! rg …` → `rg …`).
func stripShellKeywords(words []string) []string {
	for len(words) > 0 && shellKeywords[words[0]] {
		words = words[1:]
	}
	return words
}

// loopBindings returns, for every variable the command binds, whether all of
// its bindings are trustworthy loop bindings.
func loopBindings(segments []string, pipedIn []bool, valueClass bool) map[string]bool {
	vars := map[string]bool{}
	bind := func(name string, ok bool) {
		if prev, seen := vars[name]; seen {
			ok = ok && prev
		}
		vars[name] = ok
	}
	for i, seg := range segments {
		words := stripShellKeywords(shellWords(seg))
		if len(words) == 1 && !pipedIn[i] && valueClass && strings.HasSuffix(words[0], "=$"+cmdSubstVar) {
			name := words[0][:strings.IndexByte(words[0], '=')]
			if shellVarNameRe.MatchString(name) {
				bind(name, true)
				continue
			}
		}
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
		if item == seqLoopPlaceholder {
			continue
		}
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
		named, pruned := false, false
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case !strings.HasPrefix(a, "-") && a != "!" && a != "(" && a != ")" && a != "\\(" && a != "\\)":
				if named || pruned || strings.Contains(a, "..") || dotGlobRe.MatchString(a) ||
					isSecretFilePath(a) || isSensitiveExternalPath(a) || isProtectedRelPath(a) {
					return false // a stray word after the predicates or a sensitive/protected root
				}
			case a == "-name" || a == "-iname":
				if i+1 >= len(args) || !extGlobRe.MatchString(args[i+1]) {
					return false
				}
				named = true
				i++
			case a == "-path" || a == "-ipath":
				if i+1 >= len(args) || strings.Contains(args[i+1], "..") || dotGlobRe.MatchString(args[i+1]) ||
					isSecretFilePath(args[i+1]) || isSensitiveExternalPath(args[i+1]) || isProtectedRelPath(args[i+1]) {
					return false
				}
				i++
			case a == "-type" || a == "-maxdepth" || a == "-mindepth":
				if i+1 >= len(args) {
					return false
				}
				i++
			case a == "-prune":
				pruned = true
			case a == "-o" || a == "-or":
				if !named && !pruned {
					return false
				}
				named, pruned = false, false
			case a == "-not" || a == "!" || a == "-a" || a == "-and" || a == "-print" || a == "(" || a == ")" || a == "\\(" || a == "\\)":
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
func expansionsAreBounded(seg string, words []string, vars map[string]bool, valueClass bool) bool {
	if _, ok := shellExpansionNames(seg); !ok {
		return false
	}
	if len(words) == 1 && valueClass && strings.HasSuffix(words[0], "=$"+cmdSubstVar) {
		return true
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
	if strings.Contains(words[j], "$") && !isSafeHomeScriptWord(words[j], vars) {
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
	if base == "cd" {
		return len(args) == 1 && isSafeHomePathWord(args[0], vars)
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
		return gitMessageExpansionsOnly(args, vars) || gitReadOnlyLoopExpansions(args, vars)
	}
	// mkdir may take a value-class $(...) (it only creates directories) but
	// not an ordinary variable. Every other non-read-only command stays opaque.
	readOnlyTool := readOnlyDataTools[base] || (base == "sed" && isReadOnlySedArgs(args))
	if !readOnlyTool && !(valueClass && base == "mkdir") {
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
				if bound || (!valueOnlyTools[base] && !(valueClass && (acceptsValueClassArg(base) || (base == "sed" && isReadOnlySedArgs(args))))) {
					return false
				}
			case bound:
				if !trusted || !readOnlyTool {
					return false
				}
			case !harmlessEnvVars[name] || !readOnlyTool:
				return false
			}
		}
	}
	return true
}

func isReadOnlySedArgs(rawArgs []string) bool {
	args, ok := stripNullRedirects(rawArgs)
	if !ok || len(args) == 0 {
		return false
	}
	quiet, hasScript, i := false, false, 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if a == "-n" || a == "--quiet" || a == "--silent" || a == "-nE" || a == "-nr" || a == "-En" || a == "-rn" {
			quiet = true
			i++
			continue
		}
		if a == "-E" || a == "-r" || a == "--regexp-extended" {
			i++
			continue
		}
		if a == "-e" {
			if i+1 >= len(args) || strings.ContainsAny(args[i+1], "$`") || !sedReadOnlyScriptRe.MatchString(args[i+1]) {
				return false
			}
			hasScript = true
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-") {
			return false
		}
		break
	}
	if !quiet {
		return false
	}
	if !hasScript {
		if i >= len(args) || strings.ContainsAny(args[i], "$`") || !sedReadOnlyScriptRe.MatchString(args[i]) {
			return false
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	for ; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			return false
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

func gitReadOnlyLoopExpansions(args []string, vars map[string]bool) bool {
	onlyTrustedLoopVars := func(word string) bool {
		if isSafeHomePathWord(word, vars) {
			return true
		}
		names, ok := wordExpansionNames(word)
		if !ok || len(names) == 0 {
			return false
		}
		for _, name := range names {
			if trusted, bound := vars[name]; !bound || !trusted {
				return false
			}
		}
		return true
	}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		a := args[i]
		if a == "-c" || strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "--config-env") {
			return false
		}
		if a == "-C" || a == "--git-dir" || a == "--work-tree" {
			if i+1 >= len(args) || (strings.Contains(args[i+1], "$") && !onlyTrustedLoopVars(args[i+1])) {
				return false
			}
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-C") || strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") {
			if strings.Contains(a, "$") && !onlyTrustedLoopVars(a) {
				return false
			}
			i++
			continue
		}
		if strings.Contains(a, "$") || gitGlobalValueFlags[a] {
			return false
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	sub, rest := args[i], args[i+1:]
	if sub == "branch" {
		for _, a := range rest {
			if strings.Contains(a, "$") {
				return false
			}
		}
		return isReadOnlyGitBranch(append([]string{"branch"}, rest...))
	}
	if sub == "worktree" {
		return len(rest) == 1 && rest[0] == "list"
	}
	if !gitReadSubcommands[sub] && sub != "ls-files" && sub != "ls-remote" && sub != "describe" && sub != "shortlog" &&
		sub != "ls-tree" && sub != "merge-base" && sub != "check-ignore" {
		return false
	}
	for _, a := range rest {
		if gitOutputRe.MatchString(a) || (strings.Contains(a, "$") && (strings.HasPrefix(a, "-") || !onlyTrustedLoopVars(a))) {
			return false
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
		case next == '?' || next == '!':
			names = append(names, string(next))
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
		case next == '?' || next == '!':
			names = append(names, string(next))
			i++
		case dollarExpandRe.MatchString(string(next)) && !(quote == '"' && next == '"'):
			return nil, false
		}
	}
	return names, true
}
