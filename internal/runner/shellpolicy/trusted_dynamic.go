package shellpolicy

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Full trust skips the static opacity checks, so the literal denylist in
// trusted_denylist.go cannot see code assembled while the command runs. This
// file reuses the unattended parser (unattended_dynamic.go) to ask, never
// refuse, for the shapes that hide a hard-floor command from those literal
// rules (AGENTS.md rule 6, "管道执行"):
//
//   - executing text nobody can read before it runs: a decoder, network or
//     multi-stage pipe into a shell or interpreter (`… | base64 -d | sh`,
//     `… | sh /dev/stdin`), a command substitution or process substitution
//     that is executed (`sh -c "$(curl …)"`, `bash <(curl …)`,
//     `sh < <(curl …)`, `source <(…)`), and eval (including after shell
//     keywords such as `if …; then eval …` or `! eval …`);
//   - sudo/secret/egress/publish hidden behind quoting or variables, which
//     the parser sees as the literal command (`x=su; y=do; $x$y`);
//   - whole-environment and credential-named variable dumps (`env`,
//     `printenv OPENAI_API_KEY`) and osascript `do shell script`.
//
// What full trust is for keeps running: an opaque variable (`$CMD build`,
// `bash -c "$CMD"`), a script piped from a plain file (`cat run.sh | bash`,
// the same as `bash run.sh`, unless the same command downloads files) or from
// plain literal text without format specifiers or character escapes judged
// like a heredoc (`printf 'echo hi' | sh`), inline interpreter code and
// heredoc scripts, and a plain `git push`.

// trustedBrowserSecretRe matches browser cookie, password and token stores.
var trustedBrowserSecretRe = regexp.MustCompile(`(?i)(?:google/chrome|chromium|bravesoftware|microsoft(?:\\?\s|_)?edge|vivaldi|opera|arc/user(?:\\?\s|_)?data|firefox|mozilla|library/cookies|library/safari)[^;&|\n]*?[/\\](?:cookies(?:\.sqlite|\.binarycookies)?|login(?:\\?\s|_)data|web(?:\\?\s|_)data|logins\.json|key4\.db)(?:$|[\s'";&|)*?-])`)

// trustedPipedScriptMaxDepth bounds re-judging literal text piped into a shell.
const trustedPipedScriptMaxDepth = 3

// trustedDynamicHighRisk reports whether command builds or hides what it runs
// in a way full trust must still confirm with the user.
func trustedDynamicHighRisk(command string) bool {
	return trustedDynamicHighRiskDepth(command, 0)
}

func trustedDynamicHighRiskDepth(command string, depth int) bool {
	if depth > trustedPipedScriptMaxDepth {
		return true
	}
	text := command
	ctx := &unattendedCtx{codeJudge: trustedInlineCodeJudge}
	if names, ok := shellExpansionNames(text); ok {
		for _, name := range names {
			ctx.credRef = ctx.credRef || isCredentialEnvName(name)
		}
	}
	expanded, status := concretizeLiterals(text)
	switch status {
	case constSecret:
		return true
	case constApplied:
		text = expanded
	}
	sc, ok := parseUnattendedScript(text)
	if !ok {
		// Unparseable shell cannot be shown to run nothing hidden.
		return true
	}
	j := trustedJudge{ctx: ctx, depth: depth, fullSrc: text}
	if j.script(sc, text, 0) {
		return true
	}
	return ctx.envDump || ctx.credRef
}

// trustedInlineCodeJudge keeps full trust's inline-script contract except for
// osascript, whose `do shell script` is a shell (and the usual way to ask for
// administrator privileges).
func trustedInlineCodeJudge(family, code string) string {
	if family == "osascript" {
		return unattendedInlineCodeCategory(family, code)
	}
	return ""
}

type trustedJudge struct {
	ctx     *unattendedCtx
	depth   int    // literal piped-script recursion
	fullSrc string // whole command at this level (for cross-segment download checks)
}

// script judges one parsed level. src is the text sc was parsed from, so a
// dynamic word can be told apart: `$CMD` (allowed) versus `$(…)` (asked).
// Nested bodies have no source and fall back to asking on dynamic words.
func (j trustedJudge) script(sc *uScript, src string, depth int) bool {
	for name := range sc.vars {
		if name == indirectExpansion || isCredentialEnvName(name) {
			j.ctx.credRef = true
		}
	}
	if !sc.dataOnly {
		for i, seg := range sc.segs {
			var prev *uSeg
			if i > 0 {
				prev = sc.segs[i-1]
			}
			if j.segment(seg, prev, src, depth) {
				return true
			}
		}
	}
	for _, child := range sc.children {
		if j.script(child, "", depth+1) {
			return true
		}
	}
	return false
}

func (j trustedJudge) segment(seg, prev *uSeg, src string, depth int) bool {
	switch cat := unattendedSegCategory(seg, depth, j.ctx); cat {
	case "", unattendedCategoryTool, unattendedCategoryEdit:
		// Tool-level checks (hoxi schedule, edits) run elsewhere.
		return false
	case unattendedCategoryPublish:
		return !isPlainGitPushSeg(seg)
	case unattendedCategoryDynamic:
		if seg.pipedIn {
			unpiped := *seg
			unpiped.pipedIn = false
			if unattendedSegCategory(&unpiped, depth, j.ctx) == "" {
				// Only the pipe made it dynamic: judge what is piped in.
				return !j.knownPipedScript(prev)
			}
		}
		lenient := &unattendedCtx{codeJudge: func(string, string) string { return "" }}
		if unattendedSegCategory(seg, depth, lenient) == "" {
			// Only the inline-code judge (osascript `do shell script`) objected.
			return true
		}
		return executesUnknownText(seg, src)
	default:
		return true
	}
}

// executesUnknownText reports a dynamic segment whose executed text comes
// from eval, a command or process substitution, stdin device paths
// (`/dev/stdin`), or an expanding heredoc, rather than from a plain variable.
func executesUnknownText(seg *uSeg, src string) bool {
	words, _ := unwrapUnattendedWrappers(stripTrustedSegPrefix(seg.words))
	if len(words) > 0 && !words[0].dynamic && strings.EqualFold(filepath.Base(words[0].text), "eval") {
		return true
	}
	if seg.stdin == "procsub" {
		return true
	}
	if (seg.stdin == "heredoc" || seg.stdin == "herestring") && seg.stdinDynamic && hasCommandSubstitution(seg.stdinText) {
		return true
	}
	for _, w := range words {
		if isStdinPath(w.text) {
			return true
		}
	}
	if len(words) > 0 && words[0].dynamic {
		head := words[0]
		if src == "" || head.start < 0 || head.end > len(src) || head.start >= head.end {
			return true
		}
		raw := src[head.start:head.end]
		if hasCommandSubstitution(raw) || assignsReferencedVar(src, raw) {
			// A command name assembled from variables set in this same
			// command (`x=su; y=do; $x$y`) only hides a literal command.
			return true
		}
	}
	for _, w := range seg.words {
		if w.procSub {
			return true
		}
		if !w.dynamic {
			continue
		}
		if src == "" || w.start < 0 || w.end > len(src) || w.start >= w.end {
			return true
		}
		if hasCommandSubstitution(src[w.start:w.end]) {
			return true
		}
	}
	return false
}

var trustedVarRefRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)

// assignsReferencedVar reports whether src assigns any variable raw expands.
func assignsReferencedVar(src, raw string) bool {
	for _, m := range trustedVarRefRe.FindAllStringSubmatch(raw, -1) {
		if hasVarAssignmentInSource(src, m[1]) {
			return true
		}
	}
	return false
}

func hasVarAssignmentInSource(src, name string) bool {
	if name == "" {
		return false
	}
	if strings.Contains(src, "read "+name) || strings.Contains(src, "for "+name+" ") {
		return true
	}
	n := len(name)
	for i := 0; i < len(src); {
		idx := strings.Index(src[i:], name)
		if idx < 0 {
			return false
		}
		pos := i + idx
		end := pos + n
		i = end
		if pos > 0 && isEnvIdentChar(src[pos-1], false) {
			continue
		}
		if end < len(src) && src[end] == '+' {
			end++
		}
		if end < len(src) && src[end] == '=' {
			if pos == 0 || isTrustedAssignBoundary(src[pos-1]) {
				return true
			}
		}
	}
	return false
}

func isTrustedAssignBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ';', '&', '|', '(':
		return true
	}
	return false
}

func hasCommandSubstitution(s string) bool {
	return strings.Contains(s, "$(") || strings.Contains(s, "`")
}

// hasOpaquePrintEscape reports backslash escapes that printf or dash/zsh echo
// can use to synthesize arbitrary characters (`\x73`, `\0163`, `\u0073`, `\b`,
// `\c`), while still allowing harmless newline/tab escapes (`\n`, `\r`, `\t`).
func hasOpaquePrintEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			continue
		}
		if i+1 >= len(s) {
			return true
		}
		switch s[i+1] {
		case 'n', 'r', 't':
			i++
		default:
			return true
		}
	}
	return false
}

// knownPipedScript reports whether the text piped into a shell or interpreter
// is a plain file (run like `bash file`) or plain literal text without format
// specifiers or character escapes that itself passes.
func (j trustedJudge) knownPipedScript(prev *uSeg) bool {
	if prev == nil || prev.pipedIn || prev.stdin != "" {
		return false
	}
	words, cat := unwrapUnattendedWrappers(stripTrustedSegPrefix(prev.words))
	if cat != "" || len(words) == 0 {
		return false
	}
	args := make([]string, 0, len(words)-1)
	for _, w := range words {
		if w.dynamic || w.procSub {
			return false
		}
	}
	for _, w := range words[1:] {
		args = append(args, w.text)
	}
	switch strings.ToLower(filepath.Base(words[0].text)) {
	case "cat":
		if trustedDownloaderRe.MatchString(j.fullSrc) {
			return false
		}
		files := 0
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if isStdinPath(a) || isProtectedOrAttachmentScriptPath(a) {
				return false
			}
			files++
		}
		return files > 0
	case "printf":
		// Reject format strings (`%s`, `%b`) and multi-arg substitution, plus
		// escapes that synthesize characters (`\x73`, `\0163`).
		if len(args) != 1 || strings.HasPrefix(args[0], "-") ||
			strings.Contains(args[0], "%") || hasOpaquePrintEscape(args[0]) {
			return false
		}
		return j.literalPipedTextSafe(args[0])
	case "echo":
		i := 0
		for i < len(args) && strings.HasPrefix(args[i], "-") && len(args[i]) > 1 {
			flag := args[i][1:]
			// Allow only -n / -E; reject -e (or combined -ne) which enables
			// escape interpretation in bash.
			if strings.Trim(flag, "nE") != "" {
				return false
			}
			i++
		}
		text := strings.Join(args[i:], " ")
		// dash/zsh builtin echo interprets octal/hex escapes even without -e.
		if hasOpaquePrintEscape(text) {
			return false
		}
		return j.literalPipedTextSafe(text)
	}
	return false
}

func (j trustedJudge) literalPipedTextSafe(text string) bool {
	if strings.TrimSpace(text) == "" {
		return true
	}
	return !isDestructiveCommand(text) &&
		!trustedDynamicHighRiskDepth(text, j.depth+1) &&
		!isTrustedHighRiskCommand(text, "")
}

// stripTrustedSegPrefix mirrors unattendedSegCategory's prefix stripping:
// shell keywords (`then`, `do`, `else`, `!`, `function name`) followed by
// leading environment assignments.
func stripTrustedSegPrefix(words []uWord) []uWord {
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
	for len(words) > 0 && !words[0].dynamic && unattendedAssignRe.MatchString(words[0].text) {
		words = words[1:]
	}
	return words
}

// isPlainGitPushSeg reports a literal `git … push …` segment. Force pushes and
// remote branch deletes are still caught by the destructive checks; anything
// else the unattended rules call publishing (npm publish, `bash -c 'git push;
// …'`) keeps asking.
func isPlainGitPushSeg(seg *uSeg) bool {
	words, cat := unwrapUnattendedWrappers(stripTrustedSegPrefix(seg.words))
	if cat != "" || len(words) == 0 {
		return false
	}
	argv := make([]string, len(words))
	for i, w := range words {
		if w.dynamic || w.procSub {
			return false
		}
		argv[i] = w.text
	}
	w := normalizeAbsoluteTool(argv[0])
	return (w == "git" || w == "git.exe") && gitHasSubcommand(argv[1:], "push")
}
