package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ApprovalModeTrusted behaves like auto mode and additionally lets through
// destructive commands whose effect stays inside the session's own Hoxi git
// worktree, where the per-prompt checkpoints (message rollback) and git can
// undo them, or that only delete throwaway files under the system temp
// directories. Everything else that is destructive still reaches the user:
// secret reads, credential/sensitive paths, execution-controlling files
// (.git, .opencode, .claude, .hoxi, ...), sudo, force pushes, publishing,
// shared git state (branches, stash, worktrees), process control and the
// doom-loop guard. See AGENTS.md rule 6.
const ApprovalModeTrusted = "trusted"

var (
	trustedGlobReplacer = strings.NewReplacer("*", "x", "?", "x")
	rmFlagRe            = regexp.MustCompile(`^(?:-[rRfdvi]+|--(?:recursive|force|dir|verbose))$`)
	chmodFlagRe         = regexp.MustCompile(`^(?:-[Rvf]+|--(?:recursive|verbose|silent|quiet))$`)
	chmodModeRe         = regexp.MustCompile(`^(?:0?[4-7][0-7]{2}|(?:[ugoa]*[+=][rwxX]+|[ugoa]*-[wxX]+)(?:,(?:[ugoa]*[+=][rwxX]+|[ugoa]*-[wxX]+))*)$`)
	gitRefRe            = regexp.MustCompile(`^[A-Za-z0-9_./~^-]+$`)
	gitCleanFlagRe      = regexp.MustCompile(`^(?:-[fdxXnqi]+|--(?:force|dry-run|quiet))$`)
	gitRestoreFlagRe    = regexp.MustCompile(`^(?:-[WSq]+|--(?:staged|worktree|quiet|source=[A-Za-z0-9_./~^-]+))$`)
)

// isIsolatedWorktree reports whether dir is a Hoxi session worktree, as
// opposed to the user's own checkout ("local" sandbox mode).
func isIsolatedWorktree(dir string) bool {
	slash := filepath.ToSlash(filepath.Clean(strings.TrimSpace(dir)))
	return strings.Contains(slash, "/.hoxi/worktrees/")
}

// modeAllowsControllableRequest reports whether mode lets item run without a
// destructive confirmation prompt. Trusted and unattended modes accept all
// recoverable worktree classes; auto mode accepts only throwaway temp-entry
// cleanup and non-recursive, non-special-bit chmod on non-protected workspace
// or temp files.
func modeAllowsControllableRequest(mode string, item map[string]interface{}, worktree string) bool {
	switch mode {
	case ApprovalModeTrusted, ApprovalModeUnattended:
		return trustedRecoverableRequest(item, worktree)
	case ApprovalModeAuto:
		return autoControllableRequest(item, worktree)
	default:
		return false
	}
}

func isControllableShellItem(item map[string]interface{}) bool {
	meta := itemMetadata(item)
	perm := permissionName(item)
	if perm != "bash" && !(perm == "tool" && bashToolNames[strings.ToLower(firstString(meta["tool"]))]) {
		return false
	}
	if destructive, _ := meta["destructive"].(bool); destructive {
		return false
	}
	if external, _ := meta["externalDirectory"].(bool); external {
		return false
	}
	return true
}

// trustedRecoverableRequest reports whether a destructive shell request only
// affects the isolated worktree in a recoverable way.
func trustedRecoverableRequest(item map[string]interface{}, worktree string) bool {
	if !isControllableShellItem(item) {
		return false
	}
	worktree = recoverableWorktree(worktree)
	candidates, ok := recoverableCandidates(item, worktree)
	if !ok {
		return false
	}
	for _, cmd := range candidates {
		if !isRecoverableWorktreeCommand(cmd, worktree) {
			return false
		}
	}
	return true
}

// autoControllableRequest reports whether a destructive-flagged shell request
// only cleans up concrete throwaway temp entries or sets ordinary permissions
// on concrete non-protected files.
func autoControllableRequest(item map[string]interface{}, dir string) bool {
	if !isControllableShellItem(item) {
		return false
	}
	candidates, ok := controllableCandidates(item, func(piece string) bool {
		return isAutoControllableCommand(piece, dir)
	})
	if !ok {
		return false
	}
	for _, cmd := range candidates {
		if !isAutoControllableCommand(cmd, dir) {
			return false
		}
	}
	return true
}

// recoverableCandidates returns the shell commands to judge for recoverable
// destructive checks within dir. OpenCode also lists its own split of the
// command, plus the sandbox stop-marker traps, in patterns; a fragment judged
// alone loses the assignment that makes a temp-path delete recoverable, so the
// whole command is judged while still checking any extra destructive piece not
// contained in whole.
func recoverableCandidates(item map[string]interface{}, dir string) ([]string, bool) {
	return controllableCandidates(item, func(piece string) bool {
		return isRecoverableWorktreeCommand(piece, dir)
	})
}

func controllableCandidates(item map[string]interface{}, checkPiece func(string) bool) ([]string, bool) {
	candidates := commandCandidates(item)
	meta := itemMetadata(item)
	if cmd := strings.TrimSpace(firstString(meta["command"])); cmd != "" {
		whole := unwrapSandboxCommand(cmd)
		for _, piece := range candidates {
			piece = strings.TrimSpace(piece)
			if piece == "" || strings.Contains(whole, piece) {
				continue
			}
			if isDestructivePermissionRequest(map[string]interface{}{"permission": "bash", "patterns": []interface{}{piece}}) &&
				!checkPiece(piece) {
				return nil, false
			}
		}
		candidates = []string{whole}
	}
	if len(candidates) == 0 {
		return nil, false
	}
	return candidates, true
}

// recoverableWorktree returns worktree when it is an isolated session
// worktree, else "": only temp-directory deletions are relaxed outside one.
func recoverableWorktree(worktree string) string {
	if !isIsolatedWorktree(worktree) {
		return ""
	}
	return worktree
}

func safeChmodDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	cleaned := filepath.Clean(dir)
	slash := filepath.ToSlash(cleaned)
	if slash == "/" || filepath.Dir(cleaned) == cleaned || isSensitiveExternalPath(cleaned) || isSecretFilePath(cleaned) {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && samePath(cleaned, home) {
		return ""
	}
	return cleaned
}

func isAutoControllableCommand(command, dir string) bool {
	classes := map[string]bool{}
	if !recoverableCommandClassesWithTrust(command, safeChmodDir(dir), classes, scriptTrustReadOnly) {
		return false
	}
	for class := range classes {
		if class != recoverClassTempDelete && class != recoverClassSafeChmod {
			return false
		}
	}
	return true
}

// Recoverable work comes in classes. Full trust mode accepts every class;
// a session grant ("此类都允许") only ever covers the grantable ones.
const (
	recoverClassTempDelete = "temp-delete" // named entries under /tmp or $TMPDIR
	recoverClassDirDelete  = "dir-delete"  // non-protected paths in the session directory
	recoverClassDirWipe    = "dir-wipe"    // a glob at the session directory root (`rm -rf *`)
	recoverClassGitDiscard = "git-discard" // git checkout -- / checkout . / restore / clean
	recoverClassGitReset   = "git-reset"   // git reset --hard moves the branch
	recoverClassGitClean   = "git-clean"   // git clean deletes untracked (and with -x ignored) files
	recoverClassChmod      = "chmod"       // recursive or globbed chmod in isolated worktree
	recoverClassSafeChmod  = "chmod-safe"  // non-recursive safe chmod on concrete workspace/temp files
)

var sessionGrantableClasses = setOf(recoverClassTempDelete, recoverClassDirDelete, recoverClassGitDiscard)

// isRecoverableWorktreeCommand accepts a command only when every segment is
// either not destructive at all or one of the recoverable forms below. An
// empty worktree means the session runs in the user's own checkout: then
// only temp-directory deletions qualify. Destructive segments must be plain
// words (no expansion, escapes or glued quotes); after an out-of-worktree `cd`
// the working directory is unknown, so only absolute temp paths still qualify.
func isRecoverableWorktreeCommand(command, worktree string) bool {
	return recoverableCommandClassesWithTrust(command, worktree, map[string]bool{}, scriptTrustReadOnly)
}

// recoverableCommandClasses is isRecoverableWorktreeCommand for session class
// grants (scriptTrustNone) that also records the class of every destructive segment.
func recoverableCommandClasses(command, worktree string, classes map[string]bool) bool {
	return recoverableCommandClassesWithTrust(command, worktree, classes, scriptTrustNone)
}

func recoverableCommandClassesWithTrust(command, worktree string, classes map[string]bool, trust scriptTrust) bool {
	return recoverableCommandClassesAt(command, worktree, classes, trust, 0)
}

// recoverableCommandClassesAt judges (...) bodies on their own, up to
// maxSubshellDepth, then substitutes literal assignments so a temp-path
// delete written as `D=/tmp/x; rm -rf "$D"` is visible. A secret substitution
// or an opaque body fails closed.
func recoverableCommandClassesAt(command, worktree string, classes map[string]bool, trust scriptTrust, depth int) bool {
	if depth > maxSubshellDepth {
		return false
	}
	command = joinLineContinuations(normalizeWindowsDrivePaths(command))
	if strings.Contains(command, "<<") {
		stripped, ok := stripBenignHeredocs(command)
		if !ok {
			return false
		}
		command = stripped
	}
	spans, ok := findSubshellSpans(command)
	if !ok {
		return false
	}
	for _, sp := range spans {
		if !recoverableCommandClassesAt(command[sp[0]+1:sp[1]], worktree, classes, trust, depth+1) {
			return false
		}
	}
	command = eraseSubshells(command)
	// Only a fully applied substitution is judged in place of the original.
	// A rejection (a PATH export, a reassignment) keeps the original text,
	// which still fails closed on `$` inside a destructive segment. A secret
	// value never becomes recoverable.
	if expanded, status := concretizeLiterals(command); status == constApplied {
		command = expanded
	} else if status == constSecret {
		return false
	}
	if hasProtectedGitOutput(command) {
		return false
	}
	// A `# hoxi-stop:` marker line is a comment segment and passes as non-destructive.
	if hasShellGrouping(command) || isOpaqueShellCommandWith(command, worktree, trust) {
		return false
	}
	segments := splitCommandChain(command)
	if len(segments) == 0 {
		return false
	}
	cwd := worktree
	hasSegment := false
	for _, rawSeg := range segments {
		seg := strings.TrimSpace(rawSeg)
		if seg == "" || strings.HasPrefix(seg, "#") {
			continue
		}
		words := shellWords(seg)
		if len(words) == 0 {
			continue
		}
		hasSegment = true
		switch base := filepath.Base(words[0]); {
		case base == "cd" || base == "pushd" || base == "popd":
			if base == "cd" && cwd != "" && worktree != "" {
				if nextCWD, ok := advanceScopedCD(seg, cwd, worktree); ok && pathWithin(worktree, nextCWD) && !isProtectedWorktreePath(worktree, nextCWD) {
					cwd = nextCWD
					continue
				}
			}
			cwd = ""
			continue
		case dataArgInterpreters[base]:
			// `curl ... | sh`, `xargs rm`, `sudo ...`: each half can look
			// harmless while the pipeline is not.
			return false
		}
		if !isDestructiveCommand(seg) {
			continue
		}
		if strings.ContainsAny(seg, "$\\") || hasUnsafeShellSyntax(trustedGlobReplacer.Replace(seg)) {
			return false
		}
		if !isRecoverableSegmentIn(words, cwd, worktree, classes) {
			return false
		}
	}
	return hasSegment
}

// hasShellGrouping reports subshells, command substitution, functions and
// brace groups (outside single quotes), plus unbalanced quotes.
func hasShellGrouping(s string) bool {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			i++
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '`' || (c == '$' && i+1 < len(s) && s[i+1] == '(') {
				return true
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '{':
			if nextIdx, ok := skipSafeBraceSpan(s, i); ok {
				i = nextIdx
				continue
			}
			return true
		case c == '`' || c == '(' || c == ')' || c == '}':
			return true
		}
	}
	return quote != 0
}

func isRecoverableSegment(words []string, worktree string, classes map[string]bool) bool {
	return isRecoverableSegmentIn(words, worktree, worktree, classes)
}

func isRecoverableSegmentIn(words []string, cwd, worktree string, classes map[string]bool) bool {
	words, ok := stripNullRedirects(words)
	if !ok || len(words) == 0 {
		return false
	}
	name := words[0]
	if strings.Contains(name, "/") {
		if !trustedToolDirs()[filepath.Dir(filepath.Clean(name))] {
			return false
		}
		name = filepath.Base(name)
	}
	args := words[1:]
	switch name {
	case "rm", "rmdir", "unlink":
		return flaggedWorktreePathsIn(args, rmFlagRe, cwd, worktree, classes)
	case "chmod":
		recursive := false
		i := 0
		for i < len(args) && chmodFlagRe.MatchString(args[i]) {
			if strings.Contains(args[i], "R") || args[i] == "--recursive" {
				recursive = true
			}
			i++
		}
		if i >= len(args) || !chmodModeRe.MatchString(args[i]) {
			return false
		}
		targets := args[i+1:]
		pathClasses := map[string]bool{}
		if !allWorktreePathsIn(targets, cwd, worktree, pathClasses) {
			return false
		}
		hasDirGlob := false
		if pathClasses[recoverClassDirDelete] {
			for _, t := range targets {
				if strings.ContainsAny(t, "*?") {
					hasDirGlob = true
					break
				}
			}
		}
		if recursive || pathClasses[recoverClassDirWipe] || hasDirGlob {
			classes[recoverClassChmod] = true
		} else {
			classes[recoverClassSafeChmod] = true
		}
		return true
	case "git":
		return cwd != "" && worktree != "" && isRecoverableGitSegmentIn(args, cwd, worktree, classes)
	}
	return false
}

func flaggedWorktreePathsIn(args []string, flagRe *regexp.Regexp, cwd, worktree string, classes map[string]bool) bool {
	i := 0
	for i < len(args) && args[i] != "--" && strings.HasPrefix(args[i], "-") {
		if !flagRe.MatchString(args[i]) {
			return false
		}
		i++
	}
	if i < len(args) && args[i] == "--" {
		i++
	}
	return allWorktreePathsIn(args[i:], cwd, worktree, classes)
}

func allWorktreePaths(paths []string, worktree string, classes map[string]bool) bool {
	return allWorktreePathsIn(paths, worktree, worktree, classes)
}

// allWorktreePathsIn accepts paths inside the session directory (resolved from
// cwd and checked against root worktree) or under a temp root and records the
// deletion class of each one.
func allWorktreePathsIn(paths []string, cwd, worktree string, classes map[string]bool) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		resolvedP := p
		if cwd != "" && worktree != "" && cwd != worktree && p != "" &&
			!strings.HasPrefix(p, "-") && !strings.HasPrefix(p, "~") && !isAbsOrRootedPath(p) {
			bad := false
			for _, part := range strings.Split(filepath.ToSlash(p), "/") {
				if part == ".." || (strings.HasPrefix(part, ".") && strings.ContainsAny(part, "*?")) {
					bad = true
					break
				}
			}
			if !bad {
				resolvedP = filepath.Join(cwd, p)
			}
		}
		switch {
		case cwd != "" && worktree != "" && isWorktreePathArg(resolvedP, worktree):
			if isWorktreeRootGlob(resolvedP, worktree) {
				classes[recoverClassDirWipe] = true
			} else {
				classes[recoverClassDirDelete] = true
			}
		case isTempPathArg(p):
			classes[recoverClassTempDelete] = true
		default:
			return false
		}
	}
	return true
}

// isWorktreeRootGlob reports a glob that lists the session directory root
// itself (`*`, `./*`), which empties the whole checkout.
func isWorktreeRootGlob(arg, worktree string) bool {
	if !strings.ContainsAny(arg, "*?") {
		return false
	}
	target := arg
	if !isAbsOrRootedPath(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	dir := filepath.Dir(target[:strings.IndexAny(target, "*?")] + "x")
	return samePath(dir, worktree) || filepath.Clean(dir) == filepath.Clean(worktree)
}

// trustedTempRoots are the system temp directories whose contents are
// throwaway by definition.
func trustedTempRoots() []string {
	roots := []string{"/tmp", "/private/tmp"}
	if dir := strings.TrimSpace(os.TempDir()); dir != "" {
		roots = append(roots, filepath.Clean(dir))
	}
	return roots
}

// isTempPathArg accepts an absolute path strictly below a temp root. The root
// itself, and globs that start a segment there (`/tmp/*`), keep asking.
func isTempPathArg(arg string) bool {
	if !isAbsOrRootedPath(arg) || strings.HasPrefix(arg, "~") || strings.ContainsAny(arg, "$[") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(arg), "/") {
		if part == ".." || (part != "" && strings.ContainsAny(part[:1], "*?.") && strings.ContainsAny(part, "*?")) {
			return false
		}
	}
	target := filepath.Clean(arg)
	if strings.ContainsAny(target, "*?") {
		// The glob must sit inside a named entry (`/tmp/probe*`), so the
		// directory it lists is the temp root's child level at most.
		target = target[:strings.IndexAny(target, "*?")] + "x"
	}
	for _, root := range trustedTempRoots() {
		if pathWithin(root, target) && !samePath(root, target) && !isSecretFilePath(target) {
			return true
		}
	}
	// macOS also uses /var/folders/<id>/T/<entry>. The per-user T directory
	// itself stays manual; only a concrete entry under it is throwaway.
	return isMacOSTempPath(filepath.ToSlash(target)) && !isSecretFilePath(target)
}

func samePath(a, b string) bool {
	ra, okA := resolvePathWithSymlinks(a, 0)
	rb, okB := resolvePathWithSymlinks(b, 0)
	return okA && okB && filepath.Clean(ra) == filepath.Clean(rb)
}

// isWorktreePathArg accepts a path that stays inside the worktree after
// symlink resolution and is neither execution-controlling nor a secret.
// Globs are allowed except dot-globs (which can match `..`); the worktree
// root itself may only be reached through a glob (`rm -rf *`).
func isWorktreePathArg(arg, worktree string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "~") || strings.ContainsAny(arg, "$[") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(arg), "/") {
		if part == ".." || (strings.HasPrefix(part, ".") && strings.ContainsAny(part, "*?")) {
			return false
		}
	}
	target := arg
	if !isAbsOrRootedPath(target) {
		target = filepath.Join(worktree, target)
	} else if !filepath.IsAbs(target) && filepath.IsAbs(worktree) {
		return false
	}
	target = filepath.Clean(target)
	globbed := strings.ContainsAny(target, "*?")
	if globbed {
		target = filepath.Dir(target[:strings.IndexAny(target, "*?")] + "x")
	}
	if !pathWithin(worktree, target) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(worktree), target)
	if err != nil {
		return false
	}
	if rel == "." {
		return globbed
	}
	return !isProtectedRelPath(rel) && !isSecretFilePath(rel) && !isProtectedWorktreePath(worktree, target)
}

// isRecoverableGitSegment accepts only commands that discard work inside this
// worktree. Branches, the stash and worktree lists are shared by every
// session of the repository, so they keep asking.
func isRecoverableGitSegmentIn(args []string, cwd, worktree string, classes map[string]bool) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false // global options such as -C can point at another repository
	}
	rest := args[1:]
	switch args[0] {
	case "reset":
		hard, refs := false, 0
		for _, a := range rest {
			switch {
			case a == "--hard":
				hard = true
			case a == "-q" || a == "--quiet":
			case gitRefRe.MatchString(a) && !strings.HasPrefix(a, "-"):
				refs++
			default:
				return false
			}
		}
		classes[recoverClassGitReset] = true
		return hard && refs <= 1
	case "checkout":
		dash := -1
		for i, a := range rest {
			if a == "--" {
				dash = i
				break
			}
		}
		if dash < 0 {
			classes[recoverClassGitDiscard] = true
			return len(rest) == 1 && rest[0] == "."
		}
		before := rest[:dash]
		if len(before) > 1 || (len(before) == 1 && (!gitRefRe.MatchString(before[0]) || strings.HasPrefix(before[0], "-"))) {
			return false
		}
		classes[recoverClassGitDiscard] = true
		return allWorktreePathsIn(rest[dash+1:], cwd, worktree, map[string]bool{})
	case "restore":
		i := 0
		for i < len(rest) && rest[i] != "--" && strings.HasPrefix(rest[i], "-") {
			if !gitRestoreFlagRe.MatchString(rest[i]) {
				return false
			}
			i++
		}
		if i < len(rest) && rest[i] == "--" {
			i++
		}
		classes[recoverClassGitDiscard] = true
		return allWorktreePathsIn(rest[i:], cwd, worktree, map[string]bool{})
	case "clean":
		i := 0
		for i < len(rest) && rest[i] != "--" && strings.HasPrefix(rest[i], "-") {
			if !gitCleanFlagRe.MatchString(rest[i]) {
				return false
			}
			i++
		}
		if i < len(rest) && rest[i] == "--" {
			i++
		}
		classes[recoverClassGitClean] = true
		return i == len(rest) || allWorktreePathsIn(rest[i:], cwd, worktree, map[string]bool{})
	}
	return false
}
