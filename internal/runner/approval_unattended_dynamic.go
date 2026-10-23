package runner

import (
	"path/filepath"
	"regexp"
	"strings"
)

// unattendedCategoryDynamic refuses code built while the command runs: eval,
// text piped into a shell or interpreter, a command name taken from a
// variable or substitution, an inline interpreter script that runs processes,
// opens the network or reads the environment, and a git alias. The static
// push/publish/secret/egress rules cannot see through any of these, so in
// unattended mode they are refused instead of allowed. Mirrors
// .opencode/lib/unattended-policy.js; the shared fixture
// .opencode/lib/unattended.cases.json keeps both aligned.
const unattendedCategoryDynamic = "dynamic"

// unattendedMaxDepth bounds `bash -c '…'` style recursion.
const unattendedMaxDepth = 4

var (
	unattendedShells = setOf("sh", "bash", "zsh", "dash", "ksh", "mksh", "yash", "fish", "csh", "tcsh")
	// unattendedExtraInterpreters run inline code too, beyond inlineScriptTool.
	unattendedExtraInterpreters = setOf("osascript", "deno", "bun", "lua", "rscript", "pwsh", "powershell", "tclsh", "irb")
	// unattendedSensitiveTools change remote state or reveal credentials by
	// subcommand, so a subcommand built at run time is refused.
	unattendedSensitiveTools = setOf("git", "gh", "npm", "pnpm", "yarn", "bun", "cargo", "twine", "gem", "docker",
		"terraform", "kubectl", "helm", "security", "aws", "gcloud", "az")
	// unattendedNetworkTools can carry a credential off the machine.
	unattendedNetworkTools = setOf("curl", "wget", "http", "https", "xh", "httpie", "nc", "ncat", "netcat", "socat",
		"telnet", "ssh", "scp", "sftp", "rsync", "ftp", "tftp", "openssl", "gh", "git", "aws", "gcloud", "az",
		"dig", "nslookup", "host", "ping", "mail", "sendmail")
	// unattendedEnvDumpers print the whole environment.
	unattendedEnvDumpers = setOf("env", "printenv", "set", "export", "declare", "typeset", "compgen")
	// unattendedKeywords open or close a compound command; the command after
	// them is judged.
	unattendedKeywords = setOf("{", "}", "!", "if", "then", "else", "elif", "fi", "do", "done", "while", "until", "esac", "coproc", "time", "noglob", "nocorrect")
	unattendedAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?\+?=`)

	// unattendedScriptCapabilityRe matches inline interpreter code that can
	// run a process, load code dynamically, read the environment or open the
	// network. Code that matches none of these can only compute and print.
	// Kept byte-identical with SCRIPT_CAPABILITY_RE in unattended-policy.js.
	unattendedScriptCapabilityRe = regexp.MustCompile(`\b(?:subprocess|system|popen\w*|spawn\w*|exec\w*|fork\w*|pty|commands|shell_exec|passthru|proc_open|open3|ctypes|cffi|dlopen|eval|compile|importlib|getattr|vars|globals|pickle|marshal|environ|getenv|putenv|ENV|socket|urllib\d?|requests|httpx|aiohttp|http|https|smtplib|ftplib|telnetlib|paramiko|net|tls|dgram|undici|fetch|XMLHttpRequest|WebSocket|LWP|URI|ObjC|NSTask|Function)\b|__\w+__|child_process|worker_threads|process\s*\.\s*(?:env|binding|dlopen|mainModule)|sys\s*\.\s*modules|\brequire\s*\(\s*[^'"\s)]|\bimport\s*\(|Deno\s*\.\s*(?:run|Command|env)|Bun\s*\.\s*(?:spawn|env|\$)|Net::|HTTP::|IO::Socket|%x|\bqx\b|[Dd]o\s+[Ss]hell\s+[Ss]cript|` + "`")
)

// unattendedCtx accumulates what the whole command does across nested
// scripts, for the egress check.
type unattendedCtx struct {
	credRef bool // references a credential-named variable
	envDump bool // prints the whole environment
	network bool // runs a tool that can send data off the machine
}

// unattendedDynamicCategory classifies a command the static rules passed:
// it parses quoting and expansions, so `git p\ush` and `"git" push` are seen
// as `git push`, and it refuses dynamic execution. "" means allow.
func unattendedDynamicCategory(command string) string {
	ctx := &unattendedCtx{}
	if cat := unattendedScriptCategory(command, 0, ctx); cat != "" {
		return cat
	}
	if (ctx.credRef || ctx.envDump) && ctx.network {
		return unattendedCategoryEgress
	}
	return ""
}

func unattendedScriptCategory(text string, depth int, ctx *unattendedCtx) string {
	if depth > unattendedMaxDepth {
		return unattendedCategoryDynamic
	}
	if depth > 0 {
		if cat := scanUnattendedText(text); cat != "" {
			return cat
		}
	}
	sc, ok := parseUnattendedScript(text)
	if !ok {
		return unattendedCategoryDynamic
	}
	return sc.category(depth, ctx)
}

func (sc *uScript) category(depth int, ctx *unattendedCtx) string {
	for name := range sc.vars {
		if name == indirectExpansion || isCredentialEnvName(name) {
			ctx.credRef = true
		}
	}
	if !sc.dataOnly {
		for _, seg := range sc.segs {
			if cat := unattendedSegCategory(seg, depth, ctx); cat != "" {
				return cat
			}
		}
	}
	for _, child := range sc.children {
		if cat := child.category(depth+1, ctx); cat != "" {
			return cat
		}
	}
	return ""
}

func unattendedSegCategory(seg *uSeg, depth int, ctx *unattendedCtx) string {
	words := seg.words
	for len(words) > 0 && !words[0].dynamic {
		t := words[0].text
		if t == "function" && len(words) > 1 {
			words = words[2:]
			continue
		}
		if !unattendedKeywords[t] {
			break
		}
		words = words[1:]
	}
	if len(words) > 0 && !words[0].dynamic {
		switch words[0].text {
		case "case", "for", "select", "in":
			return "" // a loop or case header: its words are values
		}
	}
	for len(words) > 0 && unattendedAssignRe.MatchString(words[0].text) {
		words = words[1:]
	}
	words, cat := unwrapUnattendedWrappers(words)
	if cat != "" || len(words) == 0 {
		return cat
	}
	return unattendedArgvWordsCategory(seg, words, depth, ctx)
}

// unattendedArgvWordsCategory judges one command whose words start at the
// command name.
func unattendedArgvWordsCategory(seg *uSeg, words []uWord, depth int, ctx *unattendedCtx) string {
	head := words[0]
	if head.dynamic || head.procSub {
		return unattendedCategoryDynamic
	}
	base := strings.ToLower(filepath.Base(head.text))
	argv := make([]string, len(words))
	for i, w := range words {
		argv[i] = w.text
	}
	if cat := unattendedArgvCategory(argv); cat != "" {
		return cat
	}
	if unattendedNetworkTools[base] {
		ctx.network = true
	}
	if unattendedEnvDumpers[base] && unattendedDumpsEnv(base, argv[1:], ctx) {
		ctx.envDump = true
	}
	args := words[1:]
	switch {
	case base == "eval":
		return unattendedCategoryDynamic
	case base == "alias" && len(args) > 0:
		return unattendedCategoryDynamic
	case base == "source" || base == ".":
		if len(args) > 0 && (args[0].dynamic || args[0].procSub || isStdinPath(args[0].text)) {
			return unattendedCategoryDynamic
		}
		return ""
	case base == "parallel":
		return unattendedCategoryDynamic
	case unattendedShells[base]:
		return unattendedShellCategory(seg, args, depth, ctx)
	case base == "xargs":
		return unattendedXargsCategory(args, depth, ctx)
	case base == "find":
		return unattendedFindCategory(args, depth, ctx)
	case base == "watch":
		return unattendedWatchCategory(args, depth, ctx)
	}
	if family, ok := unattendedInterpreter(base); ok {
		return unattendedInterpreterCategory(seg, family, args, ctx)
	}
	if unattendedSensitiveTools[base] {
		return unattendedToolCategory(base, args, depth, ctx)
	}
	return ""
}

// unwrapUnattendedWrappers strips commands that only run the next command
// (`nohup`, `env A=b`, `timeout 5`, `xcrun`, ...). `env -S` splits a string
// into a new command and is refused.
func unwrapUnattendedWrappers(words []uWord) ([]uWord, string) {
	for len(words) > 0 {
		if words[0].dynamic {
			return words, ""
		}
		args := words[1:]
		skip := func(valueFlags map[string]bool, positional int) []uWord {
			i := 0
			for i < len(args) && strings.HasPrefix(args[i].text, "-") && args[i].text != "-" {
				if args[i].text == "--" {
					i++
					break
				}
				if valueFlags[args[i].text] {
					i++
				}
				i++
			}
			for positional > 0 && i < len(args) {
				i++
				positional--
			}
			if i > len(args) {
				i = len(args)
			}
			return args[i:]
		}
		switch strings.ToLower(filepath.Base(words[0].text)) {
		case "nohup", "builtin", "noglob", "nocorrect", "unbuffer", "chronic", "setsid":
			words = args
		case "time":
			words = skip(nil, 0)
		case "exec":
			words = skip(setOf("-a"), 0)
		case "command":
			for _, a := range args {
				if a.text == "-v" || a.text == "-V" {
					return nil, "" // a lookup, not a run
				}
			}
			words = skip(nil, 0)
		case "env":
			i := 0
			for i < len(args) {
				t := args[i].text
				switch {
				case args[i].dynamic:
					return nil, unattendedCategoryDynamic
				case t == "-S" || strings.HasPrefix(t, "--split-string") || (strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "S")):
					return nil, unattendedCategoryDynamic
				case t == "-u" || t == "-P" || t == "-C" || t == "--unset" || t == "--chdir":
					i += 2
					continue
				case t == "--":
					i++
				case strings.HasPrefix(t, "-") || unattendedAssignRe.MatchString(t):
					i++
					continue
				}
				break
			}
			if i >= len(args) {
				return []uWord{words[0]}, "" // bare env prints the environment
			}
			words = args[i:]
		case "nice":
			words = skip(setOf("-n", "--adjustment"), 0)
		case "timeout", "gtimeout":
			words = skip(setOf("-s", "--signal", "-k", "--kill-after"), 1)
		case "stdbuf":
			words = skip(setOf("-i", "-o", "-e"), 0)
		case "caffeinate":
			words = skip(setOf("-t", "-w"), 0)
		case "arch":
			words = skip(setOf("-arch", "-e"), 0)
		case "xcrun":
			for _, a := range args {
				if a.text == "-f" || a.text == "--find" {
					return nil, ""
				}
			}
			words = skip(setOf("-sdk", "--sdk", "--toolchain", "-toolchain"), 0)
		case "flock":
			words = skip(setOf("-w", "--timeout", "-E", "--conflict-exit-code"), 1)
		default:
			return words, ""
		}
	}
	return words, ""
}

func unattendedDumpsEnv(base string, args []string, ctx *unattendedCtx) bool {
	switch base {
	case "printenv":
		for _, a := range args {
			if isCredentialEnvName(a) {
				ctx.credRef = true
			}
		}
		return len(args) == 0
	case "env", "set":
		return len(args) == 0
	case "export", "declare", "typeset":
		return len(args) == 0 || args[0] == "-p" || args[0] == "-x"
	case "compgen":
		return len(args) > 0 && (args[0] == "-v" || args[0] == "-e")
	}
	return false
}

// unattendedShellCategory judges a shell: `-c` text is parsed and judged
// like the outer command; a script read from a pipe, an expanding heredoc,
// a process substitution or a dynamic word is refused; a script file runs as
// the file says (see AGENTS.md: file contents are not judged).
func unattendedShellCategory(seg *uSeg, args []uWord, depth int, ctx *unattendedCtx) string {
	cmdMode, stdinMode := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a.dynamic || a.procSub {
			return unattendedCategoryDynamic
		}
		t := a.text
		if t == "-" {
			stdinMode = true
			break
		}
		if t == "--" {
			if i+1 < len(args) && !stdinMode {
				return unattendedShellScriptArg(args[i+1], cmdMode, depth, ctx)
			}
			break
		}
		if len(t) > 1 && (t[0] == '-' || t[0] == '+') {
			if t == "-o" || t == "+o" || t == "-O" || t == "+O" || t == "--rcfile" || t == "--init-file" {
				i++
				continue
			}
			if !strings.HasPrefix(t, "--") {
				cmdMode = cmdMode || strings.Contains(t[1:], "c")
				stdinMode = stdinMode || strings.Contains(t[1:], "s")
			}
			continue
		}
		if stdinMode {
			break // positional parameters for a script read from stdin
		}
		return unattendedShellScriptArg(a, cmdMode, depth, ctx)
	}
	if cmdMode && !stdinMode {
		return ""
	}
	return unattendedStdinScriptCategory(seg, func(text string) string {
		return unattendedScriptCategory(text, depth+1, ctx)
	})
}

func unattendedShellScriptArg(a uWord, cmdMode bool, depth int, ctx *unattendedCtx) string {
	if a.dynamic || a.procSub {
		return unattendedCategoryDynamic
	}
	if cmdMode {
		return unattendedScriptCategory(a.text, depth+1, ctx)
	}
	if isStdinPath(a.text) {
		return unattendedCategoryDynamic
	}
	return "" // a script file
}

// isStdinPath names a file that is the command's standard input, so a
// script "file" there is whatever was piped in.
func isStdinPath(p string) bool {
	switch p {
	case "/dev/stdin", "/dev/fd/0", "/proc/self/fd/0":
		return true
	}
	return false
}

// unattendedStdinScriptCategory judges a program a shell or interpreter
// reads from standard input.
func unattendedStdinScriptCategory(seg *uSeg, judge func(string) string) string {
	switch {
	case seg.stdin == "procsub":
		return unattendedCategoryDynamic
	case seg.stdin == "heredoc" || seg.stdin == "herestring":
		if seg.stdinDynamic {
			return unattendedCategoryDynamic
		}
		return judge(seg.stdinText)
	case seg.stdin == "file":
		return ""
	case seg.pipedIn:
		return unattendedCategoryDynamic
	}
	return ""
}

func unattendedInterpreter(base string) (string, bool) {
	if family, ok := inlineScriptTool(base); ok {
		return family, true
	}
	if base == "nodejs" {
		return "node", true
	}
	if unattendedExtraInterpreters[base] {
		return base, true
	}
	return "", false
}

// unattendedInlineFlag reports whether arg introduces inline code for the
// interpreter family.
func unattendedInlineFlag(family, arg string) bool {
	switch family {
	case "python", "python3":
		return arg == "-c"
	case "node", "bun":
		return arg == "-e" || arg == "--eval" || arg == "-p" || arg == "--print"
	case "deno":
		return arg == "eval"
	case "perl", "ruby":
		return strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsAny(arg[1:], "eE")
	case "php":
		return arg == "-r"
	case "pwsh", "powershell":
		lower := strings.ToLower(arg)
		return lower == "-c" || lower == "-command" || strings.HasPrefix(lower, "-e")
	default:
		return arg == "-e"
	}
}

// unattendedInterpreterCategory judges an interpreter. Inline code passes
// only when it is literal and shows none of unattendedScriptCapabilityRe; a
// program piped in or built from an expansion is refused.
func unattendedInterpreterCategory(seg *uSeg, family string, args []uWord, ctx *unattendedCtx) string {
	judge := func(code string) string {
		if unattendedScriptCapabilityRe.MatchString(code) {
			return unattendedCategoryDynamic
		}
		return scanUnattendedText(code)
	}
	var options []string
	hasCode := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a.dynamic || a.procSub {
			return unattendedCategoryDynamic
		}
		t := a.text
		if unattendedInlineFlag(family, t) {
			if i+1 >= len(args) {
				return ""
			}
			code := args[i+1]
			if code.dynamic || code.procSub {
				return unattendedCategoryDynamic
			}
			if family == "pwsh" || family == "powershell" {
				if strings.HasPrefix(strings.ToLower(t), "-e") {
					return unattendedCategoryDynamic // -EncodedCommand
				}
			}
			if cat := judge(code.text); cat != "" {
				return cat
			}
			hasCode = true
			i++
			continue
		}
		if t == "-" || isStdinPath(t) {
			break
		}
		if strings.HasPrefix(t, "-") {
			options = append(options, t)
			if interpValueFlags[t] {
				if i+1 < len(args) {
					options = append(options, args[i+1].text)
				}
				i++
			}
			continue
		}
		if hasCode {
			break // positional arguments to the inline code
		}
		return "" // a script file (or a deno subcommand) runs as written
	}
	if hasCode {
		// Modules loaded by option (`perl -MLWP::Simple -e …`) count too.
		return judge(strings.Join(options, " "))
	}
	return unattendedStdinScriptCategory(seg, judge)
}

// unattendedXargsCategory judges the command xargs runs. Arguments read
// from standard input are appended (or substituted for the -I string), so
// they are modelled as dynamic words.
func unattendedXargsCategory(args []uWord, depth int, ctx *unattendedCtx) string {
	valueFlags := setOf("-I", "-J", "-L", "-n", "-P", "-R", "-S", "-s", "-E", "-d", "-a", "--max-args",
		"--max-procs", "--delimiter", "--arg-file", "--replace", "--eof", "--max-lines", "--max-chars")
	replace := ""
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].text, "-") {
		t := args[i].text
		if args[i].dynamic {
			return unattendedCategoryDynamic
		}
		if t == "--" {
			i++
			break
		}
		switch {
		case t == "-I" || t == "-J" || t == "--replace":
			if i+1 < len(args) {
				replace = args[i+1].text
			}
		case strings.HasPrefix(t, "-I") || strings.HasPrefix(t, "-J"):
			replace = t[2:]
		case strings.HasPrefix(t, "--replace="):
			replace = strings.TrimPrefix(t, "--replace=")
		}
		if valueFlags[t] {
			i++
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	return unattendedRunnerCategory(args[i:], replace, true, depth, ctx)
}

// unattendedFindCategory judges each `-exec`/`-execdir`/`-ok` command; the
// `{}` placeholder is a dynamic word.
func unattendedFindCategory(args []uWord, depth int, ctx *unattendedCtx) string {
	for i := 0; i < len(args); i++ {
		switch args[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
		default:
			continue
		}
		j := i + 1
		for j < len(args) && args[j].text != ";" && args[j].text != "+" {
			j++
		}
		if j > i+1 {
			if cat := unattendedRunnerCategory(args[i+1:j], "{}", false, depth, ctx); cat != "" {
				return cat
			}
		}
		i = j
	}
	return ""
}

// unattendedWatchCategory judges `watch [opts] cmd...`, which runs its
// arguments through `sh -c`.
func unattendedWatchCategory(args []uWord, depth int, ctx *unattendedCtx) string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].text, "-") {
		if args[i].text == "-n" || args[i].text == "--interval" {
			i++
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	parts := make([]string, 0, len(args)-i)
	for _, a := range args[i:] {
		if a.dynamic || a.procSub {
			return unattendedCategoryDynamic
		}
		parts = append(parts, a.text)
	}
	return unattendedScriptCategory(strings.Join(parts, " "), depth+1, ctx)
}

// unattendedRunnerCategory judges argv run by xargs or find. Words holding
// the placeholder become dynamic; appendInput adds the arguments xargs reads.
func unattendedRunnerCategory(argv []uWord, placeholder string, appendInput bool, depth int, ctx *unattendedCtx) string {
	words := make([]uWord, 0, len(argv)+1)
	substituted := false
	for _, w := range argv {
		if placeholder != "" && strings.Contains(w.text, placeholder) {
			w.dynamic = true
			substituted = true
		}
		words = append(words, w)
	}
	if appendInput && !substituted {
		words = append(words, uWord{dynamic: true})
	}
	words, cat := unwrapUnattendedWrappers(words)
	if cat != "" || len(words) == 0 {
		return cat
	}
	return unattendedArgvWordsCategory(&uSeg{}, words, depth+1, ctx)
}
