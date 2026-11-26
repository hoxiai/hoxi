package runner

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	approvalSourceCheckpoint       = "checkpoint"
	maxNativeCheckpointFilesPerCmd = 16
	maxNativeCheckpointFileBytes   = 4 * 1024 * 1024
	maxSessionCreatedFiles         = 512
	maxNativeCheckpointTickets     = 64
	maxSafeArtifactWalkEntries     = 4096
)

var (
	nativeRmFlagRe      = regexp.MustCompile(`^(?:-[rRfdv]+|--(?:recursive|force|dir|verbose))$`)
	nativeRestoreFlagRe = regexp.MustCompile(`^(?:-[Wq]+|--(?:worktree|quiet|source=HEAD))$`)

	disposableBuildArtifactNames = map[string]bool{
		"dist":                 true,
		"build":                true,
		"out":                  true,
		"coverage":             true,
		".next":                true,
		".nuxt":                true,
		".svelte-kit":          true,
		".turbo":               true,
		".parcel-cache":        true,
		".vite":                true,
		"__pycache__":          true,
		".pytest_cache":        true,
		".mypy_cache":          true,
		".ruff_cache":          true,
		"tsconfig.tsbuildinfo": true,
	}
)

func (s *Service) nativeOnDemandSandboxProfile(sessionID, rootDir, command string) string {
	mode := s.ApprovalPolicy().Mode
	if mode != ApprovalModeAuto || !sandbox.Available() ||
		strings.TrimSpace(command) == "" || strings.TrimSpace(rootDir) == "" ||
		optsOutOfOnDemandSandbox(command) {
		return ""
	}
	plain := openCodeBashItem(sessionID, command)
	if s.gate.hasAlways(sessionID, command) ||
		s.policyApproves(plain, rootDir) ||
		s.classGrantApproves(plain, sessionID, rootDir) ||
		!isOpaquePermissionRequestWith(plain, rootDir, scriptTrustFor(mode)) {
		return ""
	}
	if isDestructivePermissionRequest(plain) || !sandboxContainable(plain, rootDir) {
		return ""
	}
	var scope sandbox.PathScope
	if rec, ok := s.effectiveSessionRecord(sessionID); ok {
		scope = sessionPathScope(rec)
	}
	profile, err := sandbox.NewManager("").WriteOnDemandWithScope(rootDir, s.sandboxControlPorts(), scope)
	if err != nil {
		return ""
	}
	wrapped := openCodeBashItem(sessionID, sandboxWrappedCommand(profile, command))
	sanitizeOpenCodePermissionItem(wrapped)
	if !s.policyApproves(wrapped, rootDir) {
		return ""
	}
	return profile
}

func (s *Service) ensureCurrentTurnSnapshot(sessionID string) string {
	rec, ok := s.store.Get(sessionID)
	if !ok || strings.TrimSpace(rec.WorktreePath) == "" {
		return ""
	}
	var lastUserID string
	for i := len(rec.Turns) - 1; i >= 0; i-- {
		if rec.Turns[i].Info.Role == "user" {
			lastUserID = strings.TrimSpace(rec.Turns[i].Info.ID)
			break
		}
	}
	if lastUserID != "" && rec.Snapshots != nil {
		if snap := strings.TrimSpace(rec.Snapshots[lastUserID]); snap != "" {
			return snap
		}
	}
	if lastUserID == "" {
		return ""
	}
	s.recordTurnSnapshot(sessionID, lastUserID)
	if updated, ok := s.store.Get(sessionID); ok && updated.Snapshots != nil {
		return strings.TrimSpace(updated.Snapshots[lastUserID])
	}
	return ""
}

func (s *Service) isPathOwnedByCurrentTurn(sessionID, absTarget string) bool {
	if s == nil || s.fileCoord == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(absTarget) == "" {
		return false
	}
	ownerID := s.rootLeaseOwner(sessionID)
	key := canonicalCoordFileKey(absTarget)
	if key == "" {
		return false
	}
	s.fileCoord.mu.Lock()
	defer s.fileCoord.mu.Unlock()
	grp := s.fileCoord.ownerTurns[ownerID]
	if grp == nil {
		return false
	}
	_, owned := grp.paths[key]
	return owned
}

// nativeCheckpointRecoverable reports whether a native bash command in auto or
// trusted mode only performs recoverable or disposable workspace cleanup:
//  1. deleting a regular file created earlier in this same session;
//  2. deleting a gitignored disposable build/cache artifact directory/file
//     that contains zero tracked files and no symlinks/secrets; or
//  3. deleting or git-restoring <= 16 concrete regular files (<= 4 MiB each)
//     whose exact bytes are already preserved in this turn's Git snapshot.
func (s *Service) nativeCheckpointRecoverable(rec SessionRecord, ask approvalAsk, rootDir string) ([]string, bool) {
	if rec.HasPathScope() {
		return nil, false
	}
	pol := s.ApprovalPolicy()
	if (pol.Mode != ApprovalModeAuto && pol.Mode != ApprovalModeTrusted) || pol.EffectiveRule(permissionCategoryBash) != "allow" {
		return nil, false
	}
	rootDir = filepath.Clean(strings.TrimSpace(rootDir))
	if rootDir == "" || !filepath.IsAbs(rootDir) {
		return nil, false
	}
	cmd := strings.TrimSpace(ask.command)
	if cmd == "" || strings.Contains(cmd, "<<") || strings.ContainsAny(cmd, "$\\`*?[](){}") {
		return nil, false
	}
	// Reject pipes into or out of destructive segments.
	if strings.Contains(cmd, "|") {
		for _, part := range strings.Split(cmd, "|") {
			if strings.Contains(part, "||") {
				continue
			}
			if isDestructiveCommand(part) {
				return nil, false
			}
		}
	}
	segments := splitCommandChain(cmd)
	if len(segments) == 0 {
		return nil, false
	}
	cwd := rootDir
	if strings.TrimSpace(ask.cwd) != "" {
		cleanCwd := filepath.Clean(strings.TrimSpace(ask.cwd))
		if !pathWithin(rootDir, cleanCwd) || isProtectedWorktreePath(rootDir, cleanCwd) {
			return nil, false
		}
		cwd = cleanCwd
	}

	type targetSpec struct {
		absPath   string
		relToRoot string
		isGitOp   bool
	}
	var targets []targetSpec
	sawDestructive := false

	for _, rawSeg := range segments {
		seg := strings.TrimSpace(rawSeg)
		if seg == "" || strings.HasPrefix(seg, "#") {
			continue
		}
		if nextCWD, ok := advanceLiteralScopedCD(seg, cwd, rootDir); ok {
			cwd = nextCWD
			continue
		}
		if !isDestructiveCommand(seg) {
			if !s.policyApproves(openCodeBashItem(rec.ID, seg), cwd) {
				return nil, false
			}
			continue
		}
		sawDestructive = true
		segTargets, isGitOp, ok := parseRecoverableNativeSegment(seg, cwd, rootDir)
		if !ok || len(segTargets) == 0 {
			return nil, false
		}
		for _, absPath := range segTargets {
			rel, err := filepath.Rel(rootDir, absPath)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
				return nil, false
			}
			targets = append(targets, targetSpec{
				absPath:   absPath,
				relToRoot: filepath.ToSlash(rel),
				isGitOp:   isGitOp,
			})
		}
		if len(targets) > maxNativeCheckpointFilesPerCmd {
			return nil, false
		}
	}
	if !sawDestructive || len(targets) == 0 {
		return nil, false
	}

	var checkpointRelPaths []string
	seenCheckpoint := make(map[string]bool)
	var snapSHA string

	for _, t := range targets {
		if !validateSafeWorkspaceTargetAncestors(rootDir, t.absPath) {
			return nil, false
		}
		baseName := filepath.Base(t.absPath)
		if baseName == "node_modules" || isProtectedWorktreePath(rootDir, t.absPath) ||
			isSecretFilePath(t.absPath) || isSecretFilePath(t.relToRoot) || isSensitiveExternalPath(t.absPath) {
			return nil, false
		}
		info, statErr := os.Lstat(t.absPath)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, false
		}

		// 1. Regular file created by this session itself during this conversation.
		if !t.isGitOp && statErr == nil && info.Mode().IsRegular() && s.gate != nil && s.gate.isSessionCreatedFile(rec.ID, t.absPath) {
			continue
		}

		// 2. Gitignored disposable build/cache output containing zero tracked files.
		if !t.isGitOp && disposableBuildArtifactNames[baseName] && worktree.IsGitIgnoredUntrackedTarget(rootDir, t.relToRoot) {
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr == nil && inspectSafeArtifactTree(rootDir, t.absPath) {
				continue
			}
		}

		// 3. Concrete regular file backed by the current turn's Git snapshot in rec.WorktreePath.
		if normalizeComparePath(rootDir) != normalizeComparePath(rec.WorktreePath) {
			return nil, false
		}
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxNativeCheckpointFileBytes {
			return nil, false
		}
		if snapSHA == "" {
			snapSHA = s.ensureCurrentTurnSnapshot(rec.ID)
			if snapSHA == "" {
				return nil, false
			}
		}
		snapOID, ok := worktree.SnapshotBlobOID(rec.WorktreePath, snapSHA, t.absPath)
		if !ok {
			return nil, false
		}
		if !s.isPathOwnedByCurrentTurn(rec.ID, t.absPath) {
			curOID, hashOK := worktree.HashFileOID(rec.WorktreePath, t.absPath)
			if !hashOK || curOID != snapOID {
				return nil, false
			}
		}
		if !seenCheckpoint[t.relToRoot] {
			seenCheckpoint[t.relToRoot] = true
			checkpointRelPaths = append(checkpointRelPaths, t.relToRoot)
		}
	}
	return checkpointRelPaths, true
}

func advanceLiteralScopedCD(seg, cwd, rootDir string) (string, bool) {
	words := plainShellWords(seg)
	if len(words) != 2 || words[0] != "cd" {
		return "", false
	}
	target := words[1]
	if target == "" || target == "-" || strings.HasPrefix(target, "~") {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	target = filepath.Clean(target)
	if !pathWithin(rootDir, target) || isProtectedWorktreePath(rootDir, target) || isSensitiveExternalPath(target) {
		return "", false
	}
	return target, true
}

func parseRecoverableNativeSegment(seg, cwd, rootDir string) ([]string, bool, bool) {
	words := stripPlainNullRedirects(plainShellWords(seg))
	if len(words) < 2 {
		return nil, false, false
	}
	cmd := words[0]
	args := words[1:]
	resolveTargets := func(rawTargets []string) ([]string, bool) {
		if len(rawTargets) == 0 || len(rawTargets) > maxNativeCheckpointFilesPerCmd {
			return nil, false
		}
		var out []string
		for _, raw := range rawTargets {
			if raw == "" || raw == "." || raw == ".." || strings.HasPrefix(raw, "~") {
				return nil, false
			}
			abs := raw
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cwd, abs)
			}
			abs = filepath.Clean(abs)
			if normalizeComparePath(abs) == normalizeComparePath(rootDir) || !pathWithin(rootDir, abs) {
				return nil, false
			}
			out = append(out, abs)
		}
		return out, true
	}

	switch cmd {
	case "rm", "rmdir", "unlink":
		var rawTargets []string
		endFlags := false
		for _, a := range args {
			if !endFlags && a == "--" {
				endFlags = true
				continue
			}
			if !endFlags && strings.HasPrefix(a, "-") {
				if cmd == "unlink" || !nativeRmFlagRe.MatchString(a) {
					return nil, false, false
				}
				continue
			}
			rawTargets = append(rawTargets, a)
		}
		resolved, ok := resolveTargets(rawTargets)
		return resolved, false, ok

	case "git":
		if len(args) < 2 {
			return nil, false, false
		}
		sub := args[0]
		rest := args[1:]
		switch sub {
		case "restore":
			var rawTargets []string
			endFlags := false
			for _, a := range rest {
				if !endFlags && a == "--" {
					endFlags = true
					continue
				}
				if !endFlags && strings.HasPrefix(a, "-") {
					if !nativeRestoreFlagRe.MatchString(a) {
						return nil, false, false
					}
					continue
				}
				rawTargets = append(rawTargets, a)
			}
			resolved, ok := resolveTargets(rawTargets)
			return resolved, true, ok
		case "checkout":
			// Require explicit `--` so branch switches can never be parsed as file restores.
			dashIdx := -1
			for i, a := range rest {
				if a == "--" {
					dashIdx = i
					break
				}
			}
			if dashIdx < 0 || dashIdx > 1 || (dashIdx == 1 && rest[0] != "HEAD") {
				return nil, false, false
			}
			resolved, ok := resolveTargets(rest[dashIdx+1:])
			return resolved, true, ok
		}
	}
	return nil, false, false
}

func plainShellWords(seg string) []string {
	isSep := func(b byte) bool {
		return b == ' ' || b == '\t' || b == '\r' || b == '\n'
	}
	var words []string
	var cur strings.Builder
	inWord := false
	var quote byte
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				if i+1 < len(seg) && !isSep(seg[i+1]) {
					return nil
				}
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			if i > 0 && !isSep(seg[i-1]) && seg[i-1] != '=' {
				return nil
			}
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
	if quote != 0 {
		return nil
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

func stripPlainNullRedirects(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == ">/dev/null" || w == "2>/dev/null" || w == "&>/dev/null" || w == "2>&1" || w == "1>&2":
			continue
		case (w == ">" || w == "2>" || w == "&>") && i+1 < len(words) && words[i+1] == "/dev/null":
			i++
			continue
		case strings.ContainsAny(w, "<>"):
			return nil
		default:
			out = append(out, w)
		}
	}
	return out
}

func validateSafeWorkspaceTargetAncestors(rootDir, absTarget string) bool {
	cleanRoot := filepath.Clean(rootDir)
	cur := filepath.Dir(filepath.Clean(absTarget))
	for {
		info, err := os.Lstat(cur)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if normalizeComparePath(cur) == normalizeComparePath(cleanRoot) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

func (g *approvalGate) recordSessionCreatedFile(sessionID, absPath string) {
	sid := strings.TrimSpace(sessionID)
	key := canonicalCoordFileKey(absPath)
	if g == nil || sid == "" || key == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessionCreated == nil {
		g.sessionCreated = make(map[string]map[string]bool)
	}
	set := g.sessionCreated[sid]
	if set == nil {
		set = make(map[string]bool)
		g.sessionCreated[sid] = set
	}
	if !set[key] && len(set) >= maxSessionCreatedFiles {
		return
	}
	set[key] = true
}

func (g *approvalGate) isSessionCreatedFile(sessionID, absPath string) bool {
	sid := strings.TrimSpace(sessionID)
	key := canonicalCoordFileKey(absPath)
	if g == nil || sid == "" || key == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessionCreated[sid][key]
}

func (g *approvalGate) stashNativeCheckpointPaths(sessionID, callID string, relPaths []string) {
	sid := strings.TrimSpace(sessionID)
	cid := strings.TrimSpace(callID)
	if g == nil || sid == "" || cid == "" || len(relPaths) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.checkpointPaths == nil {
		g.checkpointPaths = make(map[string]map[string][]string)
	}
	byCall := g.checkpointPaths[sid]
	if byCall == nil {
		byCall = make(map[string][]string)
		g.checkpointPaths[sid] = byCall
	}
	if _, exists := byCall[cid]; !exists && len(byCall) >= maxNativeCheckpointTickets {
		for k := range byCall {
			delete(byCall, k)
			break
		}
	}
	byCall[cid] = append([]string(nil), relPaths...)
}

func (g *approvalGate) consumeNativeCheckpointPaths(sessionID, callID string) []string {
	sid := strings.TrimSpace(sessionID)
	cid := strings.TrimSpace(callID)
	if g == nil || sid == "" || cid == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	byCall := g.checkpointPaths[sid]
	if byCall == nil {
		return nil
	}
	paths := byCall[cid]
	delete(byCall, cid)
	if len(byCall) == 0 {
		delete(g.checkpointPaths, sid)
	}
	return paths
}

func inspectSafeArtifactTree(rootDir, absTarget string) bool {
	count := 0
	okTree := true
	_ = filepath.WalkDir(absTarget, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			okTree = false
			return fs.SkipAll
		}
		count++
		if count > maxSafeArtifactWalkEntries {
			okTree = false
			return fs.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			resolved, rErr := filepath.EvalSymlinks(p)
			if rErr != nil || !pathWithin(rootDir, resolved) || isSecretFilePath(resolved) || isProtectedWorktreePath(rootDir, resolved) {
				okTree = false
				return fs.SkipAll
			}
			return nil
		}
		if isSecretFilePath(p) || isProtectedWorktreePath(rootDir, p) {
			okTree = false
			return fs.SkipAll
		}
		return nil
	})
	return okTree
}
