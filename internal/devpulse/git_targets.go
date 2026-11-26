package devpulse

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

type GitTarget struct {
	Dir        string
	RepoRoot   string
	IsWorktree bool
}

func TargetsCacheKey(targets []GitTarget) string {
	parts := make([]string, len(targets))
	for i, t := range targets {
		parts[i] = t.Dir
	}
	return strings.Join(parts, "\x00")
}

func IsGeneratedOrLockFile(relPath string) bool {
	return isGeneratedOrLockFile(relPath)
}

func isGeneratedOrLockFile(relPath string) bool {
	p := filepath.ToSlash(strings.TrimSpace(relPath))
	if p == "" {
		return true
	}
	ignoredPrefixes := []string{
		".hoxi/",
		"web/dist/",
		"dist/",
		"node_modules/",
		"vendor/",
		".next/",
		"build/bin/",
	}
	for _, pref := range ignoredPrefixes {
		if strings.HasPrefix(p, pref) || strings.Contains(p, "/"+pref) {
			return true
		}
	}
	base := filepath.Base(p)
	ignoredNames := []string{
		"package-lock.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"bun.lockb",
		"go.sum",
		"Cargo.lock",
	}
	for _, name := range ignoredNames {
		if base == name {
			return true
		}
	}
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") || strings.HasSuffix(base, ".map") {
		return true
	}
	return false
}

func unquoteGitPath(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return strings.Trim(s, "\"")
	}
	return s
}

type parsedFileStat struct {
	oldPath string
	path    string
	add     int
	del     int
}

func makeParsedFileStat(addStr, delStr, oldPath, newPath string, unquote bool) (parsedFileStat, bool) {
	if unquote {
		oldPath = unquoteGitPath(oldPath)
		newPath = unquoteGitPath(newPath)
	}
	if newPath == "" {
		return parsedFileStat{}, false
	}
	if isGeneratedOrLockFile(newPath) && (oldPath == "" || isGeneratedOrLockFile(oldPath)) {
		return parsedFileStat{}, false
	}
	add, _ := strconv.Atoi(strings.TrimSpace(addStr))
	del, _ := strconv.Atoi(strings.TrimSpace(delStr))
	return parsedFileStat{
		oldPath: oldPath,
		path:    newPath,
		add:     add,
		del:     del,
	}, true
}

func parseGitRenameDisplayPath(raw string) (oldPath, newPath string) {
	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, " => ") {
		return "", unquoteGitPath(trimmed)
	}
	if open := strings.IndexByte(trimmed, '{'); open >= 0 {
		if closeIdx := strings.IndexByte(trimmed[open:], '}'); closeIdx > 0 {
			closeIdx += open
			inner := trimmed[open+1 : closeIdx]
			if left, right, ok := strings.Cut(inner, " => "); ok {
				prefix := trimmed[:open]
				suffix := trimmed[closeIdx+1:]
				join := func(mid string) string {
					joined := prefix + mid + suffix
					joined = strings.ReplaceAll(joined, "//", "/")
					return strings.TrimPrefix(joined, "/")
				}
				return unquoteGitPath(join(left)), unquoteGitPath(join(right))
			}
		}
	}
	left, right, ok := strings.Cut(trimmed, " => ")
	if !ok {
		return "", unquoteGitPath(trimmed)
	}
	return unquoteGitPath(left), unquoteGitPath(right)
}

func parseGitNumstatLine(rawLine string) (int, int, string, bool) {
	st, ok := parseGitNumstatLineStat(rawLine)
	if !ok {
		return 0, 0, "", false
	}
	return st.add, st.del, st.path, true
}

func parseGitNumstatLineStat(rawLine string) (parsedFileStat, bool) {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return parsedFileStat{}, false
	}
	var addStr, delStr, file string
	if parts := strings.SplitN(line, "\t", 3); len(parts) == 3 {
		addStr = strings.TrimSpace(parts[0])
		delStr = strings.TrimSpace(parts[1])
		file = strings.TrimSpace(parts[2])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return parsedFileStat{}, false
		}
		addStr = fields[0]
		delStr = fields[1]
		file = fields[len(fields)-1]
	}
	oldPath, newPath := parseGitRenameDisplayPath(file)
	return makeParsedFileStat(addStr, delStr, oldPath, newPath, false)
}

func parseGitNumstatOutput(raw string) []parsedFileStat {
	if raw == "" {
		return nil
	}
	var out []parsedFileStat
	if strings.Contains(raw, "\x00") {
		tokens := strings.Split(raw, "\x00")
		for i := 0; i < len(tokens); i++ {
			tok := strings.TrimLeft(tokens[i], "\r\n")
			if tok == "" {
				continue
			}
			parts := strings.SplitN(tok, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			addStr, delStr, pathPart := parts[0], parts[1], parts[2]
			var oldPath, newPath string
			if pathPart == "" {
				if i+2 >= len(tokens) {
					break
				}
				oldPath = tokens[i+1]
				newPath = tokens[i+2]
				i += 2
			} else {
				newPath = pathPart
			}
			if st, ok := makeParsedFileStat(addStr, delStr, oldPath, newPath, false); ok {
				out = append(out, st)
			}
		}
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if st, ok := parseGitNumstatLineStat(line); ok {
			out = append(out, st)
		}
	}
	return out
}

func isInternalSnapshotCommitSubject(subject string) bool {
	return strings.HasPrefix(strings.TrimSpace(subject), "chore(hoxi): snapshot before apply (")
}

func samePulseDir(a, b string) bool {
	ca := canonicalPulseDir(a)
	return ca != "" && ca == canonicalPulseDir(b)
}

func canonicalPulseDir(dir string) string {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" || clean == "." {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(clean); err == nil {
		return filepath.Clean(abs)
	}
	return clean
}

func findGitWorkingTreeRoot(dir string) string {
	curr := canonicalPulseDir(dir)
	if info, err := os.Stat(curr); curr == "" || err != nil || !info.IsDir() {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			return curr
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == "" || parent == "." {
			return ""
		}
		curr = parent
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ResolveGitTargets discovers project roots, resolves session directories, and
// scans active .hoxi/worktrees for the given directory/session scope.
func (s *Service) ResolveGitTargets(rawDir, sessionID string) []GitTarget {
	var roots, worktrees []GitTarget
	seen := make(map[string]struct{})
	addTarget := func(dir, repoRoot string, isWT bool) {
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		t := GitTarget{Dir: dir, RepoRoot: repoRoot, IsWorktree: isWT}
		if isWT {
			worktrees = append(worktrees, t)
		} else {
			roots = append(roots, t)
		}
	}
	var dismissed dismissedDirFilter
	if s.source != nil {
		dismissed, _ = s.source.(dismissedDirFilter)
	}
	respectDismissed := strings.TrimSpace(rawDir) == "" && dismissed != nil
	seenRaw := make(map[string]struct{})
	addCandidate := func(raw string) string {
		trimmed := filepath.Clean(strings.TrimSpace(raw))
		if trimmed == "" || trimmed == "." {
			return ""
		}
		if _, dup := seenRaw[trimmed]; dup {
			return ""
		}
		seenRaw[trimmed] = struct{}{}
		if respectDismissed && dismissed.IsDismissedDirectory(trimmed) {
			return ""
		}
		wtTop := findGitWorkingTreeRoot(trimmed)
		if wtTop == "" {
			return ""
		}
		repoRoot := wtTop
		if parent := hoxipaths.ProjectRootForWorktree(wtTop); parent != "" {
			if pTop := findGitWorkingTreeRoot(parent); pTop != "" {
				repoRoot = pTop
			}
		} else if linkedRoot := resolveLinkedWorktreeRepoRoot(wtTop); linkedRoot != "" {
			repoRoot = linkedRoot
		}
		if respectDismissed && (dismissed.IsDismissedDirectory(repoRoot) || dismissed.IsDismissedDirectory(wtTop)) {
			return ""
		}
		addTarget(repoRoot, repoRoot, false)
		if !samePulseDir(wtTop, repoRoot) {
			addTarget(wtTop, repoRoot, true)
		}
		return repoRoot
	}

	var sessDir, sessProj string
	if s.source != nil {
		sessDir, sessProj = s.source.SessionDirectories(sessionID)
	}
	if trimmed := strings.TrimSpace(rawDir); trimmed != "" {
		if explicitRoot := addCandidate(trimmed); sessDir != "" && explicitRoot != "" {
			sRoot := firstNonEmptyString(hoxipaths.ProjectRootForWorktree(sessDir), sessProj, sessDir)
			if samePulseDir(explicitRoot, findGitWorkingTreeRoot(sRoot)) {
				addCandidate(sessDir)
			}
		}
	} else {
		if s.source != nil {
			for _, candidate := range s.source.CandidateDirectories() {
				addCandidate(candidate)
			}
		}
		for _, dir := range s.cachedOpenCodeSessionDirs() {
			addCandidate(dir)
		}
		addCandidate(sessProj)
		addCandidate(sessDir)
	}

	for _, r := range roots {
		entries, _ := os.ReadDir(filepath.Join(r.RepoRoot, ".hoxi", "worktrees"))
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			wtDir := filepath.Join(r.RepoRoot, ".hoxi", "worktrees", entry.Name())
			if wtTop := findGitWorkingTreeRoot(wtDir); wtTop != "" && !samePulseDir(wtTop, r.RepoRoot) {
				addTarget(wtTop, r.RepoRoot, true)
			}
		}
	}
	return append(roots, worktrees...)
}

type worktreeHeadKind int

const (
	worktreeNotShared worktreeHeadKind = iota
	worktreeSharedBranch
	worktreeSharedDetached
)

func readWorktreeCommonGitDir(workDir string) (gitDir, commonDir string, ok bool) {
	dotGit := filepath.Join(workDir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() || info.Size() > 16*1024 {
		return "", "", false
	}
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return "", "", false
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", "", false
	}
	gitDir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if gitDir == "" {
		return "", "", false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(workDir, gitDir)
	}
	commonRaw, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return "", "", false
	}
	commonRel := strings.TrimSpace(string(commonRaw))
	if commonRel == "" {
		return "", "", false
	}
	commonDir = commonRel
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	return gitDir, commonDir, true
}

func resolveLinkedWorktreeRepoRoot(workDir string) string {
	_, commonDir, ok := readWorktreeCommonGitDir(workDir)
	if !ok {
		return ""
	}
	canonicalCommon := canonicalPulseDir(commonDir)
	if canonicalCommon == "" || filepath.Base(canonicalCommon) != ".git" {
		return ""
	}
	mainRoot := filepath.Dir(canonicalCommon)
	if samePulseDir(workDir, mainRoot) {
		return ""
	}
	if root := findGitWorkingTreeRoot(mainRoot); root != "" && samePulseDir(root, mainRoot) {
		return root
	}
	return ""
}

// classifyWorktreeHead checks whether workDir is a linked Git worktree sharing
// its common gitdir with repoRoot, and whether its HEAD points to a local
// branch (ref: refs/heads/*) or is detached.
func classifyWorktreeHead(workDir, repoRoot string) worktreeHeadKind {
	gitDir, commonDir, ok := readWorktreeCommonGitDir(workDir)
	if !ok {
		return worktreeNotShared
	}
	if !samePulseDir(commonDir, filepath.Join(repoRoot, ".git")) {
		return worktreeNotShared
	}
	headRaw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return worktreeNotShared
	}
	if strings.HasPrefix(strings.TrimSpace(string(headRaw)), "ref: refs/heads/") {
		return worktreeSharedBranch
	}
	return worktreeSharedDetached
}

func fileStampPair(path string) (size, mod int64) {
	if path == "" {
		return 0, 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return -1, 0
	}
	return info.Size(), info.ModTime().UnixNano()
}

func (s *Service) cachedOpenCodeSessionDirs() []string {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return nil
	}
	dbSize, dbMod := fileStampPair(dbPath)
	if dbSize < 0 {
		return nil
	}
	walSize, walMod := fileStampPair(dbPath + "-wal")
	key := ocDirsCacheKey{
		dbPath:  dbPath,
		dbSize:  dbSize,
		dbMod:   dbMod,
		walSize: walSize,
		walMod:  walMod,
	}
	s.mu.Lock()
	if s.ocDirsLoaded && s.ocDirsKey == key {
		dirs := append([]string(nil), s.ocDirs...)
		s.mu.Unlock()
		return dirs
	}
	s.mu.Unlock()

	dirs := queryOpenCodeSessionDirs(dbPath)
	s.mu.Lock()
	s.ocDirsKey = key
	s.ocDirsLoaded = true
	s.ocDirs = append([]string(nil), dirs...)
	s.mu.Unlock()
	return dirs
}

func queryOpenCodeSessionDirs(dbPath string) []string {
	if dbPath == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var dirs []string
	_ = sqlitecli.WithReadOnly(ctx, dbPath, 2*time.Second, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `SELECT DISTINCT directory FROM session WHERE directory IS NOT NULL AND directory != '' LIMIT 200;`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if rows.Scan(&d) == nil && d != "" {
				dirs = append(dirs, d)
			}
		}
		return rows.Err()
	})
	return dirs
}
