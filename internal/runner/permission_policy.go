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

// envFilePattern and envrcFilePattern find a .env-style secret file or an
// .envrc in a command, under any directory (`apay/.env`). The unattended
// secret scan uses the same two, so no mode reads one the other refuses.
const (
	envFilePattern   = `(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*/)?\.en(?:v(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?|stag(?:e|ing)|test(?:ing)?|ci|preview|secret\w*|key\w*|private\w*|bak|backup)(?:\.local)?)?|\?|\*)(?:$|[\s;&|)'"*?])`
	envrcFilePattern = `(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*/)?\.envrc(?:$|[\s;&|)'"*?])`
)

var (
	destructiveRe = regexp.MustCompile(`(?i)` + strings.Join([]string{
		// Core destructive / privilege / disk / power commands (anchored to command/word boundaries, not mid-path dirs, hyphens, or source extensions)
		`(?:^|[\s;&|()<>\x60'"=:/{},$])(?:mkfs(?:\.[a-z0-9]+)?|(?:rm|rmdir|unlink|shred|truncate|srm|sudo|chmod|chown|dd|diskutil|killall|reboot|shutdown)(?:\.(?:exe|cmd|bat))?)(?:$|[\s;&|()<>\x60'"\\{},$])`,
		// kill (excluding pure `kill -0 <pid>` liveness probes) and pkill
		`\bkill\s+(?:(?:(?:-0\s+)*(?:-(?:[1-9a-mo-rt-z][a-z0-9]*|[sn][a-z0-9]+|0[a-z0-9]+|-)\s+|-[sn]\s+(?:[1-9a-z][a-z0-9]*|0[a-z0-9]+)\s+))+[-?\d%]|(?:-[1-9]\d*|[1-9?%]|0(?:\s|$)))`,
		`\bpkill\b`,
		// Redirections or pipes into /etc/ or user shell/credential dotfiles
		`[>|]\s*(?:/etc/|(?:\~|\$HOME|\$\{HOME\})/\.(?:zshrc|zshenv|zprofile|bashrc|bash_profile|profile|ssh\b|gnupg\b|netrc\b|npmrc\b))`,
		// Writes/copies/in-place edits targeting execution-controlling repo directories
		`(?:>>?|\b(?:cp|mv|install|ln|rsync|tee)\b[^\n;&|]*|\bsed\b[^\n;&|]*[ \t]-[a-z0-9]*i[^\n;&|]*)[ \t]*(?:[^\s;&|]*/)?\.(?:git/(?:hooks/|config\b)|opencode/|claude/|agents/|hoxi/skills/|vscode/settings\.json\b|gemini/(?:settings|config/hooks)\.json\b)`,
		// Credential directories and standalone credential files
		`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*/)?\.(?:ssh|aws|gnupg|kube)/`,
		`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*/)?\.(?:netrc|npmrc|git-credentials|credentials\.json)(?:$|[\s;&|)'"*?])`,
		`\.claude/\.credentials\.json`,
		`\.hoxi/(?:models|auth|tunnel(?:-dev)?|cloudflared|pairing|relay-keys|work-apps|approval-policy|hoxi)\.(?:json|db)`,
		`\.docker/config\.json`,
		// .env / .env.* / .envrc files
		envFilePattern,
		envrcFilePattern,
		// Destructive find flags, curl/wget pipe-to-shell, publish, cloud/container teardown, and local Hoxi control APIs
		`\bfind\b[^\n;&|]*\s-(delete|exec\w*|ok\w*|fprint\w*|fls)\b`,
		`\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b`,
		`\b(?:npm|pnpm|yarn|cargo)\s+publish\b`,
		`\bterraform\s+(?:destroy|apply)\b`,
		`\bkubectl\s+delete\b`,
		`\bdocker\s+(?:system\s+prune|rm|rmi)\b`,
		`/api/hoxi/(?:tools/run|computer/(?:action|grant|pause|stop))\b`,
	}, "|"))

	// Git option abbreviations (`--forc`, `--har`), combined short flags
	// (`-uf`), `+refspec` force pushes and `:refspec` deletions count too.
	// `checkout .` / `checkout -f` and `stash drop|clear` discard work, and
	// `switch --force` / `--discard-changes` (including abbreviations) discard local changes.
	destructiveGitRe = regexp.MustCompile(`\bgit\b[^\n;&|]*?\b(?:` + strings.Join([]string{
		`branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))`,
		`worktree\s+(?:remove|prune)\b`,
		`push\b[^\n;&|]*(?:\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--d(?:e(?:l\S*)?)?(?:[=\s]|$)|\s--mi\S*|\s--pru\S*|\s-[a-zA-Z]*[fd][a-zA-Z]*(?:\s|$)|\s\+\S|\s:\S)`,
		`reset\b[^\n;&|]*\s--h(?:a\S*)?(?:[=\s]|$)`,
		`checkout\b[^\n;&|]*(?:\s--(?:\s|$)|\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|\s\.(?:[;&|]|\s|/|$))`,
		`stash\s+(?:drop|clear)\b`,
		`switch\b[^\n;&|]*(?:\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--di(?:s(?:c\S*)?)?(?:[=\s]|$))`,
	}, "|") + `)`)

	destructiveGitCleanRestoreRe = regexp.MustCompile(`\bgit\b[^\n;&|()]*?\b(?:clean\b[^\n;&|()]*\s-[a-zA-Z]*f[^\n;&|()]*|restore\b[^\n;&|()]*)`)
	safeGitRestoreStagedRe       = regexp.MustCompile(`^git(?:\s+(?:-C\s+[^\s<>$` + "`" + `]+|--no-pager|--bare|--git-dir=[^\s<>$` + "`" + `]+|--work-tree=[^\s<>$` + "`" + `]+))*\s+restore((?:\s+(?:-[Sq]+|--staged|--quiet))+)(?:\s+--)?(?:\s+[^\s<>$` + "`" + `-][^\s<>$` + "`" + `]*)+(?:\s+(?:2?>\s*/dev/null|2>&1))*\s*$`)
	safeGitRestoreHasStagedRe    = regexp.MustCompile(`(?:\s-[qS]*S[qS]*|\s--staged)(?:\s|$)`)
	safeGitCleanDryRunRe         = regexp.MustCompile(`^git(?:\s+(?:-C\s+[^\s<>$` + "`" + `]+|--no-pager))*\s+clean((?:\s+(?:-[fdxXnq]+|--force|--dry-run|--quiet))+)(?:\s+--)?(?:\s+[^\s<>$` + "`" + `-][^\s<>$` + "`" + `]*)*(?:\s+(?:2?>\s*/dev/null|2>&1))*\s*$`)
	safeGitCleanHasDryRunRe      = regexp.MustCompile(`(?:\s-[fdxXnq]*n[fdxXnq]*|\s--dry-run)(?:\s|$)`)

	deobfuscator = strings.NewReplacer(`\`, "", `'`, "", `"`, "", `[`, "", `]`, "")

	trailingParenRe        = regexp.MustCompile(`\)\s*$`)
	dollarExpandRe         = regexp.MustCompile(`[A-Za-z0-9_{('"@*#?!$-]`)
	redirectWordRe         = regexp.MustCompile(`^(?:\d*|&)>>?$`)
	devNullWordRe          = regexp.MustCompile(`^(?:\d*|&)>>?/dev/null$`)
	dupStreamWordRe        = regexp.MustCompile(`^\d*>&[12]$`)
	cdSegmentRe            = regexp.MustCompile(`^cd[ \t]+([A-Za-z0-9_./@%+,:][A-Za-z0-9_./@%+,:~-]*)$`)
	cdNullRedirectSuffixRe = regexp.MustCompile(`(?:[ \t]+(?:(?:\d*|&)>>?[ \t]*/dev/null|\d*>&[12]))+$`)
	findActionRe           = regexp.MustCompile(`^-(exec\w*|ok\w*|delete|fprint\w*|fls)$`)
	fdExecRe               = regexp.MustCompile(`^(-[^-]*[xX]|--exec)`)
	rgExecRe               = regexp.MustCompile(`^--(pre|hostname-bin)`)
	pytestWriteRe          = regexp.MustCompile(`^--(ba|ju|result-?log|pas|deb)`)
	tscWriteRe             = regexp.MustCompile(`(?i)^(-b$|--(out|declarationdir|tsbuildinfofile|generatetrace|generatecpuprofile|build))`)
	gitOutputRe            = regexp.MustCompile(`^--(o(u[^=]*)?|ex(t[^=]*)?)(=|$)`)
	npmScriptRe            = regexp.MustCompile(`^(build|lint|check)(:[\w.:-]+)?$`)
	pnpmScriptRe           = regexp.MustCompile(`^(test|build|lint|check)(:[\w.:-]+)?$`)
	cargoUnsafeRe          = regexp.MustCompile(`^(--config|-Z|--target-dir|--manifest-path)`)
	gitBranchShortRe       = regexp.MustCompile(`^-(?:[arv]+)$`)
	gitBranchLongRe        = regexp.MustCompile(`^(?:--all|--remotes|--verbose|--show-current|--no-color)$`)

	benignPathExportRe     = regexp.MustCompile(`^export[ \t]+PATH=[A-Za-z0-9_./:$~{}-]+$`)
	envVarAssignmentWordRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	inlineScriptFlags      = setOf("-c", "-e", "--eval", "-p", "--print", "-")
	interpValueFlags       = setOf("-m", "-W", "-X", "-r", "--require", "--import", "-I", "-C")
	awkTools               = setOf("awk", "gawk", "mawk", "nawk")
	interpreterVersionRe   = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)
	awkExecRe              = regexp.MustCompile(`(?i)\bsystem\s*\(`)
)

var (
	simpleSafeTools = setOf("ls", "pwd", "cat", "head", "tail", "wc", "find", "grep", "rg", "fd",
		"lsof", "ps", "which", "whereis", "uname", "stat", "file", "du", "df", "pytest", "tsc")
	gitReadSubcommands = setOf("status", "diff", "log", "show", "rev-parse")
	goReadSubcommands  = setOf("test", "vet", "build", "list", "version", "env")
	// Flags that run another binary or write to an arbitrary path.
	goUnsafeFlags = setOf("exec", "toolexec", "vettool", "o", "ldflags", "gccgoflags",
		"coverprofile", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "trace", "outputdir")
	goEnvWriteFlags          = setOf("w", "u")
	hoxiCodeIndexSubcommands = setOf("search", "inspect", "callers", "callees", "impact", "context", "explore", "refresh", "status")
	hoxiDocReadSubcommands   = setOf("inspect", "read")
	hoxiEcomReadSubcommands  = setOf("profit", "listing-check", "ads-audit", "help")
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
	command = unwrapSandboxCommand(command)
	blob := "bash\n" + strings.Join(destructiveScanForms(strings.TrimSpace(command)), "\n")
	return matchesDestructive(blob) || matchesDestructive(deobfuscator.Replace(blob))
}

func isSafeGitCleanOrRestoreMatch(m string) bool {
	if sub := safeGitRestoreStagedRe.FindStringSubmatch(m); sub != nil {
		return safeGitRestoreHasStagedRe.MatchString(sub[1])
	}
	if sub := safeGitCleanDryRunRe.FindStringSubmatch(m); sub != nil {
		return safeGitCleanHasDryRunRe.MatchString(sub[1])
	}
	return false
}

func hasDestructiveGitCleanOrRestore(blob string) bool {
	for _, m := range destructiveGitCleanRestoreRe.FindAllString(blob, -1) {
		if !isSafeGitCleanOrRestoreMatch(m) {
			return true
		}
	}
	return false
}

func matchesDestructive(blob string) bool {
	return destructiveRe.MatchString(blob) || destructiveGitRe.MatchString(blob) || hasDestructiveGitCleanOrRestore(blob) || hasProtectedGitOutput(blob)
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
	command = unwrapSandboxCommand(command)
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
				if i+3 < len(s) && s[i+1:i+4] == `\''` {
					i += 3
					continue
				}
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
		if c == '*' || c == '?' || c == '[' {
			return true
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
			if quote == '\'' && c == '\'' && i+3 < len(s) && s[i+1:i+4] == `\''` {
				cur.WriteByte('\'')
				i += 3
				continue
			}
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

var windowsDrivePathRe = regexp.MustCompile(`(?i)\b[a-z]:\\[^\s;&|'"` + "`" + `()<>]*`)

func normalizeWindowsDrivePaths(command string) string {
	if filepath.Separator != '\\' || !strings.Contains(command, `:\`) {
		return command
	}
	return windowsDrivePathRe.ReplaceAllStringFunc(command, func(m string) string {
		return strings.ReplaceAll(m, `\`, `/`)
	})
}

func isAbsOrRootedPath(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}

func stripCDNullRedirects(s string) string {
	if strings.HasPrefix(s, "cd ") || strings.HasPrefix(s, "cd\t") {
		return cdNullRedirectSuffixRe.ReplaceAllString(s, "")
	}
	return s
}

// isWorktreeCD accepts one literal (optionally quoted) path within the worktree.
// The resolved target must also stay outside sensitive/protected directories.
func isWorktreeCD(segment, worktree string) bool {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" || !isAbsOrRootedPath(worktree) {
		return false
	}
	target, ok := literalCDTarget(segment)
	if !ok {
		return false
	}
	if !isAbsOrRootedPath(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	base := filepath.Clean(worktree)
	slashTarget := filepath.ToSlash(target)
	slashBase := strings.TrimSuffix(filepath.ToSlash(base), "/")
	if slashTarget != slashBase && !strings.HasPrefix(slashTarget, slashBase+"/") {
		return false
	}
	// The Go gate additionally resolves symlinks; the JS port is lexical only.
	return isSafeExternalCDTarget(target) && pathWithin(base, target)
}

// toolchainBases are absolute binaries whose basename the shared policy
// already recognizes. Only binaries inside trustedToolDirs are rewritten.
var toolchainBases = setOf("go", "npm", "pnpm", "yarn", "cargo", "pytest", "tsc", "npx", "node", "git", "hoxi")

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
	segments, _ := splitCommandChainWithPipes(command)
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
		return len(args) == 1 && (args[0] == "-v" || args[0] == "--version" || args[0] == "-h" || args[0] == "--help")
	case cmd == "kill":
		return isSafeKillProbeArgs(args)
	case cmd == "hoxi":
		if arg(0) == "doc" {
			return hoxiDocReadSubcommands[arg(1)] &&
				len(args) >= 3 &&
				hoxiCodeIndexArgsSafe(args[2:])
		}
		if arg(0) == "ecom" {
			return len(args) >= 2 &&
				hoxiEcomReadSubcommands[arg(1)] &&
				hoxiEcomArgsSafe(args[2:])
		}
		return (arg(0) == "code-index" || arg(0) == "codeindex") &&
			hoxiCodeIndexSubcommands[arg(1)] &&
			hoxiCodeIndexArgsSafe(args[2:])
	}
	return false
}

func isSafeKillProbeArgs(args []string) bool {
	if len(args) >= 2 && args[0] == "-0" {
		args = args[1:]
	} else if len(args) >= 3 && (args[0] == "-s" || args[0] == "-n") && args[1] == "0" {
		args = args[2:]
	} else {
		return false
	}
	for _, a := range args {
		if a == "" || a[0] < '1' || a[0] > '9' {
			return false
		}
		for i := 1; i < len(a); i++ {
			if a[i] < '0' || a[i] > '9' {
				return false
			}
		}
	}
	return true
}

func hoxiEcomArgsSafe(args []string) bool {
	for _, raw := range args {
		a := strings.TrimSpace(raw)
		if a == "-o" || a == "--out" || a == "-out" ||
			strings.HasPrefix(a, "-o=") || strings.HasPrefix(a, "--out=") || strings.HasPrefix(a, "-out=") {
			return false
		}
	}
	return hoxiCodeIndexArgsSafe(args)
}

func hoxiCodeIndexArgsSafe(args []string) bool {
	for _, raw := range args {
		a := strings.TrimSpace(raw)
		if a == "" {
			continue
		}
		if a == "-dir" || a == "--dir" || strings.HasPrefix(a, "-dir=") || strings.HasPrefix(a, "--dir=") {
			return false
		}
		val := a
		if strings.HasPrefix(a, "-") {
			eq := strings.IndexByte(a, '=')
			if eq < 0 {
				continue
			}
			val = a[eq+1:]
		}
		for _, seg := range strings.FieldsFunc(val, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == ".." {
				return false
			}
		}
		if isProtectedRelPath(val) || isSecretFilePath(val) || isSensitiveExternalPath(val) {
			return false
		}
	}
	return true
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
