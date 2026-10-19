package runner

import (
	"regexp"
	"strings"
)

// Go port of .opencode/lib/permission-policy.js, used by the Antigravity
// approval gate. Keep both in sync: permission_policy_test.go and
// web/src/__tests__/permissions.test.ts share
// .opencode/lib/permission-policy.cases.json.

var (
	destructiveRe = regexp.MustCompile(`(?i)\b(rm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*/etc/|\bfind\b[^\n;&|]*\s-(delete|exec|execdir|ok|okdir)\b|\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b`)

	destructiveGitRe = regexp.MustCompile(`\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:--force\b|-f\b|--force-with-lease\b)|reset\b[^\n;&|]*--hard\b|clean\b[^\n;&|]*\s-[a-zA-Z]*f)`)

	safeCommandRes = []*regexp.Regexp{
		regexp.MustCompile(`^(ls|pwd|cat|head|tail|wc|find|grep|rg|fd)\b`),
		regexp.MustCompile(`^git\s+(status|diff|log|show|rev-parse)\b`),
		regexp.MustCompile(`^git\s+worktree\s+list(?:\s|$)`),
		regexp.MustCompile(`^go\s+(test|vet|build|list|env|version)\b`),
		regexp.MustCompile(`^npm\s+(test|run\s+build|run\s+lint|run\s+check)\b`),
		regexp.MustCompile(`^pnpm\s+(test|build|lint|check)\b`),
		regexp.MustCompile(`^cargo\s+(check|test)\b`),
		regexp.MustCompile(`^(pytest|tsc|npx\s+tsc)\b`),
		regexp.MustCompile(`^node\s+-v\b`),
	}

	trailingParenRe   = regexp.MustCompile(`\)\s*$`)
	unsafeFindFlagRe  = regexp.MustCompile(`\s-(exec|execdir|delete|ok|okdir|fprint)\b`)
	substitutionRe    = regexp.MustCompile("`|\\$\\(")
	devNullRedirectRe = regexp.MustCompile(`\s+\d*>>?\s*/dev/null\b`)
	stderrToStdoutRe  = regexp.MustCompile(`\s+2>&1\b`)
	redirectRe        = regexp.MustCompile(`[<>]`)
	gitBranchRe       = regexp.MustCompile(`^git\s+branch(?:\s|$)`)
	gitBranchShortRe  = regexp.MustCompile(`^-(?:[arv]+)$`)
	gitBranchLongRe   = regexp.MustCompile(`^(?:--all|--remotes|--verbose|--show-current|--no-color)$`)
)

// isDestructiveCommand mirrors isDestructivePermissionRequest for a bash request.
func isDestructiveCommand(command string) bool {
	blob := "bash\n" + strings.TrimSpace(command)
	return destructiveRe.MatchString(blob) || destructiveGitRe.MatchString(blob)
}

// decideCommandPermission mirrors decidePermission({permission:"bash", patterns:[command]}).
func decideCommandPermission(command string) string {
	if isDestructiveCommand(command) {
		return "ask"
	}
	segments := splitCommandChain(command)
	if len(segments) == 0 {
		return "ask"
	}
	for _, seg := range segments {
		if !isSafeSegment(seg) {
			return "ask"
		}
	}
	return "allow"
}

// splitCommandChain mirrors the JS SHELL_SPLIT_RE split. RE2 has no
// lookaround, so the `|` / `&` rules are checked by hand.
func splitCommandChain(command string) []string {
	var segments []string
	var cur strings.Builder
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
		switch {
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

func isReadOnlyGitBranch(command string) bool {
	if !gitBranchRe.MatchString(command) {
		return false
	}
	args := strings.Fields(command)
	if len(args) > 2 {
		args = args[2:]
	} else {
		args = nil
	}
	listing := false
	for _, arg := range args {
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

func isSafeSegment(segment string) bool {
	if unsafeFindFlagRe.MatchString(segment) || substitutionRe.MatchString(segment) {
		return false
	}
	command := devNullRedirectRe.ReplaceAllString(segment, "")
	command = strings.TrimSpace(stderrToStdoutRe.ReplaceAllString(command, ""))
	if redirectRe.MatchString(command) {
		return false
	}
	if isReadOnlyGitBranch(command) {
		return true
	}
	for _, re := range safeCommandRes {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}
