package runner

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Go port of .opencode/lib/permission-policy.js, used by the Claude Code and
// Antigravity approval gates. Keep both in sync: permission_policy_test.go and
// web/src/__tests__/runner-permission.test.ts share
// .opencode/lib/permission-policy.cases.json.
//
// Rules (identical in both ports):
//   - A command is destructive when the raw text, or the text with every
//     backslash and quote removed, matches the destructive patterns.
//   - A command is only ever "allow"ed when it has no shell obfuscation
//     (backslash, backtick, `$(`, `$'`, `${`, `$VAR`, quotes glued to a word,
//     unterminated quotes) and every segment is on the read-only safe list.
//   - Segments are parsed once and judged individually; nothing is
//     re-serialized and re-parsed.
//   - A leading standalone `cd <plain-token>` segment is dropped when the
//     token resolves inside the session worktree.

var (
	destructiveRe = regexp.MustCompile(`(?i)\b(rm|rmdir|unlink|shred|truncate|srm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*(?:/etc/|(?:\~|\$HOME|\$\{HOME\})/\.(?:zshrc|zshenv|zprofile|bashrc|bash_profile|profile|ssh\b|gnupg\b|netrc\b|npmrc\b))|\bfind\b[^\n;&|]*\s-(delete|exec\w*|ok\w*|fprint\w*|fls)\b|\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b|\b(?:npm|pnpm|yarn|cargo)\s+publish\b|\bterraform\s+(?:destroy|apply)\b|\bkubectl\s+delete\b|\bdocker\s+(?:system\s+prune|rm|rmi)\b`)

	// Git option abbreviations (`--forc`, `--har`), combined short flags
	// (`-uf`), `+refspec` force pushes and `:refspec` deletions count too.
	destructiveGitRe = regexp.MustCompile(`\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--d(?:e(?:l\S*)?)?(?:[=\s]|$)|\s--mi\S*|\s--pru\S*|\s-[a-zA-Z]*[fd][a-zA-Z]*(?:\s|$)|\s\+\S|\s:\S)|reset\b[^\n;&|]*\s--h(?:a\S*)?(?:[=\s]|$)|clean\b[^\n;&|]*\s-[a-zA-Z]*f|checkout\b[^\n;&|]*(?:\s--(?:\s|$)|\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$))|restore\b)`)

	deobfuscator = strings.NewReplacer(`\`, "", `'`, "", `"`, "")

	trailingParenRe  = regexp.MustCompile(`\)\s*$`)
	dollarExpandRe   = regexp.MustCompile(`[A-Za-z0-9_{('"@*#?!$-]`)
	redirectWordRe   = regexp.MustCompile(`^(?:\d*|&)>>?$`)
	devNullWordRe    = regexp.MustCompile(`^(?:\d*|&)>>?/dev/null$`)
	dupStreamWordRe  = regexp.MustCompile(`^\d*>&[12]$`)
	cdSegmentRe      = regexp.MustCompile(`^cd[ \t]+([A-Za-z0-9_./@%+,:][A-Za-z0-9_./@%+,:-]*)$`)
	findActionRe     = regexp.MustCompile(`^-(exec\w*|ok\w*|delete|fprint\w*|fls)$`)
	fdExecRe         = regexp.MustCompile(`^(-[^-]*[xX]|--exec)`)
	rgExecRe         = regexp.MustCompile(`^--(pre|hostname-bin)`)
	pytestWriteRe    = regexp.MustCompile(`^--(ba|ju|result-?log|pas|deb)`)
	tscWriteRe       = regexp.MustCompile(`(?i)^(-b$|--(out|declarationdir|tsbuildinfofile|generatetrace|generatecpuprofile|build))`)
	gitOutputRe      = regexp.MustCompile(`^--(o(u[^=]*)?|ex(t[^=]*)?)(=|$)`)
	npmScriptRe      = regexp.MustCompile(`^(build|lint|check)(:[\w.:-]+)?$`)
	pnpmScriptRe     = regexp.MustCompile(`^(test|build|lint|check)(:[\w.:-]+)?$`)
	cargoUnsafeRe    = regexp.MustCompile(`^(--config|-Z|--target-dir|--manifest-path)`)
	gitBranchShortRe = regexp.MustCompile(`^-(?:[arv]+)$`)
	gitBranchLongRe  = regexp.MustCompile(`^(?:--all|--remotes|--verbose|--show-current|--no-color)$`)
)

var (
	simpleSafeTools = setOf("ls", "pwd", "cat", "head", "tail", "wc", "find", "grep", "rg", "fd",
		"lsof", "ps", "which", "whereis", "uname", "stat", "file", "du", "df", "pytest", "tsc")
	gitReadSubcommands = setOf("status", "diff", "log", "show", "rev-parse")
	goReadSubcommands  = setOf("test", "vet", "build", "list", "version", "env")
	// Flags that run another binary or write to an arbitrary path.
	goUnsafeFlags = setOf("exec", "toolexec", "vettool", "o", "ldflags", "gccgoflags",
		"coverprofile", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "trace", "outputdir")
	goEnvWriteFlags = setOf("w", "u")
)

func setOf(items ...string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

// isDestructiveCommand mirrors isDestructivePermissionRequest for a bash request.
func isDestructiveCommand(command string) bool {
	blob := "bash\n" + strings.TrimSpace(command)
	return matchesDestructive(blob) || matchesDestructive(deobfuscator.Replace(blob))
}

func matchesDestructive(blob string) bool {
	return destructiveRe.MatchString(blob) || destructiveGitRe.MatchString(blob)
}

// decideCommandPermission mirrors decidePermission({permission:"bash", patterns:[command]}).
func decideCommandPermission(command string) string {
	return decideCommandPermissionIn(command, "")
}

// decideCommandPermissionIn mirrors decidePermission(input, {worktree}). The
// worktree scope lets a leading `cd <dir> &&` inside the session be ignored.
func decideCommandPermissionIn(command, worktree string) string {
	if isDestructiveCommand(command) || !isSafeCommand(command, worktree) {
		return "ask"
	}
	return "allow"
}

func isSafeCommand(command, worktree string) bool {
	if hasUnsafeShellSyntax(command) {
		return false
	}
	segments := splitCommandChain(command)
	if len(segments) > 0 && isWorktreeCD(segments[0], worktree) {
		segments = segments[1:]
	}
	if len(segments) == 0 {
		return false
	}
	for _, seg := range segments {
		if !isSafeSegment(seg) {
			return false
		}
	}
	return true
}

// hasUnsafeShellSyntax reports shell syntax the policy refuses to reason
// about: escapes, command substitution, parameter expansion, quotes glued to
// a word and unterminated quotes. Such commands are never auto-allowed.
func hasUnsafeShellSyntax(s string) bool {
	var quote byte
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
				quote = 0
				if next != 0 && !isWordBoundary(next) {
					return true
				}
			}
			continue
		}
		if c == '\\' || c == '`' {
			return true
		}
		if c == '$' && next != 0 && !(quote == '"' && next == '"') && dollarExpandRe.MatchString(string(next)) {
			return true
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
				if next != 0 && !isWordBoundary(next) {
					return true
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			if prev != 0 && !isWordBoundary(prev) && prev != '=' {
				return true
			}
			quote = c
		}
	}
	return quote != 0
}

func isWordBoundary(c byte) bool {
	return strings.IndexByte(" \t\r\n;&|()<>", c) >= 0
}

// shellWords splits a segment that passed hasUnsafeShellSyntax into words
// and drops the (whole-word) quotes.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			inWord = true
		case ' ', '\t', '\r', '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// isWorktreeCD reports whether a segment is exactly `cd <plain-token>` whose
// target stays inside the worktree. Tokens with quotes, escapes, globs, `~`,
// `$` or a leading `-` (`cd -`, `cd -P`) never match cdSegmentRe.
func isWorktreeCD(segment, worktree string) bool {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" || !filepath.IsAbs(worktree) {
		return false
	}
	m := cdSegmentRe.FindStringSubmatch(strings.TrimSpace(segment))
	if m == nil {
		return false
	}
	target := m[1]
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	base := filepath.Clean(worktree)
	if target != base && !strings.HasPrefix(target, strings.TrimSuffix(base, "/")+"/") {
		return false
	}
	// The Go gate additionally resolves symlinks; the JS port is lexical only.
	return pathWithin(base, target)
}

// toolchainBases are absolute binaries whose basename the shared policy
// already recognizes. Only binaries inside trustedToolDirs are rewritten.
var toolchainBases = setOf("go", "npm", "pnpm", "yarn", "cargo", "pytest", "tsc", "npx", "node", "git")

// systemToolDirs must match SYSTEM_TOOL_DIRS in permission-policy.js.
var systemToolDirs = []string{"/usr/local/go/bin", "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}

var (
	trustedToolDirsOnce sync.Once
	trustedToolDirsSet  map[string]bool
)

func trustedToolDirs() map[string]bool {
	trustedToolDirsOnce.Do(func() {
		trustedToolDirsSet = make(map[string]bool, len(systemToolDirs))
		for _, dir := range systemToolDirs {
			if dir = strings.TrimSpace(dir); dir != "" {
				trustedToolDirsSet[filepath.Clean(dir)] = true
			}
		}
	})
	return trustedToolDirsSet
}

// normalizeAbsoluteTool rewrites `/usr/local/go/bin/go` to `go` when the
// binary lives in a trusted toolchain directory.
func normalizeAbsoluteTool(word string) string {
	if !strings.HasPrefix(word, "/") {
		return word
	}
	base := filepath.Base(word)
	if !toolchainBases[base] || !trustedToolDirs()[filepath.Clean(filepath.Dir(word))] {
		return word
	}
	return base
}

// splitCommandChain mirrors the JS SHELL_SPLIT_RE split. RE2 has no
// lookaround, so the `|` / `&` rules are checked by hand.
func splitCommandChain(command string) []string {
	var segments []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		seg := strings.TrimSpace(trailingParenRe.ReplaceAllString(cur.String(), ""))
		if seg != "" {
			segments = append(segments, seg)
		}
		cur.Reset()
	}
	s := command
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
				flush()
				i++
			case c == '`':
				flush()
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
			flush()
			i++
		case c == ';', c == '\n', c == '`', c == '|':
			flush()
		case c == '&' && (i == 0 || s[i-1] != '>') && next != '>':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return segments
}

// stripNullRedirects drops `>/dev/null`, `2> /dev/null`, `&>/dev/null` and
// `2>&1`; any other word containing `<` or `>` makes the segment unsafe.
func stripNullRedirects(words []string) ([]string, bool) {
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case devNullWordRe.MatchString(w), dupStreamWordRe.MatchString(w):
			continue
		case redirectWordRe.MatchString(w) && i+1 < len(words) && words[i+1] == "/dev/null":
			i++
			continue
		case strings.ContainsAny(w, "<>"):
			return nil, false
		}
		out = append(out, w)
	}
	return out, true
}

func isSafeSegment(segment string) bool {
	words, ok := stripNullRedirects(shellWords(segment))
	if !ok || len(words) == 0 {
		return false
	}
	for _, w := range words {
		if findActionRe.MatchString(w) {
			return false
		}
	}
	words[0] = normalizeAbsoluteTool(words[0])
	return isSafeWords(words)
}

func isSafeWords(words []string) bool {
	cmd, args := words[0], words[1:]
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch {
	case simpleSafeTools[cmd]:
		return toolArgsSafe(cmd, args)
	case cmd == "git":
		if isReadOnlyGitBranch(args) || (arg(0) == "worktree" && arg(1) == "list") {
			return true
		}
		return gitReadSubcommands[arg(0)] && noneMatch(args[1:], gitOutputRe)
	case cmd == "go":
		return goReadSubcommands[arg(0)] && goArgsSafe(arg(0), args[1:])
	case cmd == "npm":
		if arg(0) == "test" {
			return npmArgsSafe(args[1:])
		}
		return arg(0) == "run" && npmScriptRe.MatchString(arg(1)) && npmArgsSafe(args[2:])
	case cmd == "pnpm":
		return pnpmScriptRe.MatchString(arg(0)) && npmArgsSafe(args[1:])
	case cmd == "cargo":
		return (arg(0) == "check" || arg(0) == "test") && noneMatch(args[1:], cargoUnsafeRe)
	case cmd == "npx":
		return arg(0) == "tsc" && toolArgsSafe("tsc", args[1:])
	case cmd == "node":
		return len(args) == 1 && args[0] == "-v"
	}
	return false
}

func toolArgsSafe(cmd string, args []string) bool {
	switch cmd {
	case "fd":
		return noneMatch(args, fdExecRe)
	case "rg":
		return noneMatch(args, rgExecRe)
	case "pytest":
		return noneMatch(args, pytestWriteRe)
	case "tsc":
		return noneMatch(args, tscWriteRe)
	}
	return true
}

func noneMatch(args []string, re *regexp.Regexp) bool {
	for _, a := range args {
		if re.MatchString(a) {
			return false
		}
	}
	return true
}

// goArgsSafe rejects Go flags (single or double dash, with or without
// `=value`) that run another binary or write to an arbitrary path.
func goArgsSafe(sub string, args []string) bool {
	blocked := goUnsafeFlags
	if sub == "env" {
		blocked = goEnvWriteFlags
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		name, _, _ = strings.Cut(name, "=")
		if blocked[name] {
			return false
		}
	}
	return true
}

// npmArgsSafe rejects npm/pnpm options before `--`; any npm option can set
// config such as `--script-shell`.
func npmArgsSafe(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return true
		}
		if strings.HasPrefix(a, "-") {
			return false
		}
	}
	return true
}

func isReadOnlyGitBranch(args []string) bool {
	if len(args) == 0 || args[0] != "branch" {
		return false
	}
	listing := false
	for _, arg := range args[1:] {
		switch {
		case arg == "--list":
			listing = true
		case gitBranchShortRe.MatchString(arg) || gitBranchLongRe.MatchString(arg):
			continue
		case !listing || strings.HasPrefix(arg, "-"):
			return false
		}
	}
	return true
}
