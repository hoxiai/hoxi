package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// Go port of .opencode/lib/permission-policy.js, used by the Antigravity
// approval gate. Keep both in sync: permission_policy_test.go and
// web/src/__tests__/permissions.test.ts share
// .opencode/lib/permission-policy.cases.json.

var (
	destructiveRe = regexp.MustCompile(`(?i)\b(rm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*/etc/|\bfind\b[^\n;&|]*\s-(delete|exec|execdir|ok|okdir)\b|\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b`)

	destructiveGitRe = regexp.MustCompile(`\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:--force\b|-f\b|--force-with-lease\b)|reset\b[^\n;&|]*--hard\b|clean\b[^\n;&|]*\s-[a-zA-Z]*f)`)

	safeCommandRes = []*regexp.Regexp{
		regexp.MustCompile(`^(ls|pwd|cat|head|tail|wc|find|grep|rg|fd|lsof|ps|which|whereis|uname|stat|file|du|df)\b`),
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

// decideCommandPermissionIn evaluates a runner command after normalizing the
// forms agents commonly emit but the shared policy does not spell out: a
// leading `cd <worktree> &&` and absolute toolchain paths. The worktree scope
// keeps `cd` from moving `go test` into an arbitrary directory.
func decideCommandPermissionIn(command, worktree string) string {
	return decideCommandPermission(normalizeCommandForPolicy(command, worktree))
}

// toolchainBases are absolute binaries whose basename the shared policy
// already recognizes. Only binaries inside trustedToolDirs are rewritten.
var toolchainBases = map[string]bool{
	"go":     true,
	"npm":    true,
	"pnpm":   true,
	"yarn":   true,
	"cargo":  true,
	"pytest": true,
	"tsc":    true,
	"npx":    true,
	"node":   true,
	"git":    true,
}

var (
	trustedToolDirsOnce sync.Once
	trustedToolDirsSet  map[string]bool
)

func trustedToolDirs() map[string]bool {
	trustedToolDirsOnce.Do(func() {
		dirs := append(agentcli.CommonUserBinDirs(),
			"/usr/local/go/bin", "/usr/local/bin", "/opt/homebrew/bin",
			"/usr/bin", "/bin", "/usr/sbin", "/sbin")
		trustedToolDirsSet = make(map[string]bool, len(dirs))
		for _, dir := range dirs {
			if dir = strings.TrimSpace(dir); dir != "" {
				trustedToolDirsSet[filepath.Clean(dir)] = true
			}
		}
	})
	return trustedToolDirsSet
}

// normalizeCommandForPolicy rebuilds a command chain in the shape the shared
// policy understands. Segments are re-joined with `&&` because the policy only
// needs each segment to be safe, not to preserve `|` versus `&&`.
func normalizeCommandForPolicy(command, worktree string) string {
	segments := splitCommandChain(command)
	if len(segments) == 0 {
		return strings.TrimSpace(command)
	}
	normalized := make([]string, 0, len(segments))
	for i, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		if i == 0 {
			if rest, ok := stripWorktreeCD(segment, worktree); ok {
				if rest == "" {
					continue
				}
				segment = rest
			}
		}
		normalized = append(normalized, normalizeAbsoluteTool(segment))
	}
	return strings.Join(normalized, " && ")
}

// stripWorktreeCD validates and removes a leading `cd <dir>` segment. It only
// succeeds when the target resolves inside the session worktree.
func stripWorktreeCD(segment, worktree string) (string, bool) {
	lower := strings.ToLower(segment)
	if lower != "cd" && !strings.HasPrefix(lower, "cd ") && !strings.HasPrefix(lower, "cd\t") {
		return "", false
	}
	rest := strings.TrimSpace(segment[2:])
	if rest == "" {
		return "", false
	}
	token, tail := cutFirstField(rest)
	token = strings.Trim(token, `"'`)
	if token == "" || strings.ContainsAny(token, "$`") {
		return "", false
	}
	if token == "~" || strings.HasPrefix(token, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		token = filepath.Join(home, strings.TrimPrefix(token, "~"))
	}
	if !filepath.IsAbs(token) {
		if worktree == "" {
			return "", false
		}
		token = filepath.Join(worktree, token)
	}
	if worktree == "" || !pathWithin(worktree, token) {
		return "", false
	}
	return strings.TrimSpace(tail), true
}

// normalizeAbsoluteTool rewrites `/usr/local/go/bin/go test ...` to
// `go test ...` when the binary lives in a trusted toolchain directory.
func normalizeAbsoluteTool(segment string) string {
	body := strings.TrimLeft(segment, " \t")
	if !strings.HasPrefix(body, "/") {
		return segment
	}
	token, tail := cutFirstField(body)
	base := filepath.Base(token)
	if !toolchainBases[base] || !trustedToolDirs()[filepath.Clean(filepath.Dir(token))] {
		return segment
	}
	return segment[:len(segment)-len(body)] + base + tail
}

func cutFirstField(s string) (string, string) {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
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
