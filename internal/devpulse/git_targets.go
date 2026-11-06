package devpulse

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
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

func parseGitNumstatLine(rawLine string) (int, int, string, bool) {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return 0, 0, "", false
	}
	var addStr, delStr, file string
	if parts := strings.SplitN(line, "\t", 3); len(parts) == 3 {
		addStr = strings.TrimSpace(parts[0])
		delStr = strings.TrimSpace(parts[1])
		file = strings.TrimSpace(parts[2])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return 0, 0, "", false
		}
		addStr = fields[0]
		delStr = fields[1]
		file = fields[len(fields)-1]
	}
	file = strings.Trim(file, "\"")
	if file == "" || isGeneratedOrLockFile(file) {
		return 0, 0, "", false
	}
	add, _ := strconv.Atoi(addStr)
	del, _ := strconv.Atoi(delStr)
	return add, del, file, true
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
	addCandidate := func(raw string) string {
		wtTop := findGitWorkingTreeRoot(raw)
		if wtTop == "" {
			return ""
		}
		repoRoot := wtTop
		if parent := hoxipaths.ProjectRootForWorktree(wtTop); parent != "" {
			if pTop := findGitWorkingTreeRoot(parent); pTop != "" {
				repoRoot = pTop
			}
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
		for _, dir := range queryOpenCodeSessionDirs() {
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

func (s *Service) CollectGitDailyStats(targets []GitTarget, startOfToday time.Time, res *DailyPulseResponse) {
	if s.source == nil {
		return
	}
	committedFileSet := make(map[string]struct{})
	seenCommits := make(map[string]struct{})
	sinceArg := "--since=" + startOfToday.Format(time.RFC3339)

	for _, t := range targets {
		args := []string{"log", sinceArg, "--no-merges", "--no-renames", "--numstat", "--pretty=format:COMMIT:%H\t%s"}
		if !t.IsWorktree {
			args = append(args, "--branches", "HEAD")
		}
		logOut, err := s.source.RunGitRaw(t.Dir, args...)
		if err != nil {
			continue
		}
		skipCurrent := false
		for _, rawLine := range strings.Split(logOut, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "COMMIT:") {
				rest := strings.TrimPrefix(line, "COMMIT:")
				parts := strings.SplitN(rest, "\t", 2)
				sha := strings.TrimSpace(parts[0])
				var subject string
				if len(parts) > 1 {
					subject = strings.TrimSpace(parts[1])
				}
				if sha != "" {
					if _, exists := seenCommits[sha]; exists {
						skipCurrent = true
						continue
					}
					seenCommits[sha] = struct{}{}
				}
				if isInternalSnapshotCommitSubject(subject) {
					skipCurrent = true
					continue
				}
				skipCurrent = false
				res.TodayCommits++
				continue
			}
			if skipCurrent {
				continue
			}
			add, del, file, ok := parseGitNumstatLine(rawLine)
			if !ok {
				continue
			}
			res.CommittedAdditions += add
			res.CommittedDeletions += del
			committedFileSet[t.RepoRoot+"\x00"+file] = struct{}{}
		}
	}
	res.CommittedFiles = len(committedFileSet)

	uncommittedFileSet := make(map[string]struct{})
	var budget untrackedBudget
	for _, t := range targets {
		add, del, branch := s.collectGitUncommittedInto(t.Dir, t.RepoRoot, uncommittedFileSet, &budget)
		res.UncommittedAdditions += add
		res.UncommittedDeletions += del
		if res.Branch == "" && branch != "" {
			res.Branch = branch
		}
	}
	res.UncommittedFiles = len(uncommittedFileSet)
	res.UncommittedTruncated = budget.truncated

	changed := make(map[string]struct{}, len(committedFileSet)+len(uncommittedFileSet))
	for file := range committedFileSet {
		changed[file] = struct{}{}
	}
	for file := range uncommittedFileSet {
		changed[file] = struct{}{}
	}
	res.ChangedFiles = len(changed)
}

func (s *Service) CollectGitHistory(targets []GitTarget, start, end, todayStart time.Time, day func(string) *HistoryDay) {
	if s.source == nil {
		return
	}
	sinceArg := "--since=" + start.Format(time.RFC3339)
	untilArg := "--until=" + end.Format(time.RFC3339)
	startKey := start.Format("2006-01-02")
	endInclusiveKey := end.AddDate(0, 0, -1).Format("2006-01-02")

	loc := start.Location()
	fileSets := make(map[string]map[string]struct{})
	seenCommits := make(map[string]struct{})

	for _, t := range targets {
		args := []string{
			"log", sinceArg, untilArg, "--no-merges", "--no-renames", "--numstat",
			"--pretty=format:COMMIT:%H\t%ct\t%s",
		}
		if !t.IsWorktree {
			args = append(args, "--branches", "HEAD")
		}
		logOut, err := s.source.RunGitRaw(t.Dir, args...)
		if err != nil {
			continue
		}
		var current string
		for _, rawLine := range strings.Split(logOut, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "COMMIT:") {
				rest := strings.TrimPrefix(line, "COMMIT:")
				parts := strings.SplitN(rest, "\t", 3)
				var sha, tsStr, subject string
				if len(parts) == 1 {
					tsStr = strings.TrimSpace(parts[0])
				} else {
					sha = strings.TrimSpace(parts[0])
					tsStr = strings.TrimSpace(parts[1])
					if len(parts) > 2 {
						subject = strings.TrimSpace(parts[2])
					}
				}
				if sha != "" {
					if _, exists := seenCommits[sha]; exists {
						current = ""
						continue
					}
					seenCommits[sha] = struct{}{}
				}
				if isInternalSnapshotCommitSubject(subject) {
					current = ""
					continue
				}
				sec, convErr := strconv.ParseInt(tsStr, 10, 64)
				if convErr != nil {
					current = ""
					continue
				}
				key := historyDayKey(sec*1000, loc)
				if key < startKey || key > endInclusiveKey {
					current = ""
					continue
				}
				current = key
				target := day(current)
				target.Commits++
				if fileSets[current] == nil {
					fileSets[current] = make(map[string]struct{})
				}
				continue
			}
			if current == "" {
				continue
			}
			add, del, file, ok := parseGitNumstatLine(rawLine)
			if !ok {
				continue
			}
			target := day(current)
			target.Additions += add
			target.Deletions += del
			fileSets[current][t.RepoRoot+"\x00"+file] = struct{}{}
		}
	}

	if !todayStart.Before(start) && todayStart.Before(end) {
		todayKey := todayStart.Format("2006-01-02")
		uncommittedFileSet := make(map[string]struct{})
		var budget untrackedBudget
		var uncommAdd, uncommDel int
		for _, t := range targets {
			a, d, _ := s.collectGitUncommittedInto(t.Dir, t.RepoRoot, uncommittedFileSet, &budget)
			uncommAdd += a
			uncommDel += d
		}
		if uncommAdd > 0 || uncommDel > 0 || len(uncommittedFileSet) > 0 {
			target := day(todayKey)
			target.Additions += uncommAdd
			target.Deletions += uncommDel
			if fileSets[todayKey] == nil {
				fileSets[todayKey] = make(map[string]struct{}, len(uncommittedFileSet))
			}
			for f := range uncommittedFileSet {
				fileSets[todayKey][f] = struct{}{}
			}
		}
	}

	for key, set := range fileSets {
		day(key).Files = len(set)
	}
}

type untrackedBudget struct {
	readCount int
	readBytes int64
	truncated bool
}

func (s *Service) collectGitUncommittedInto(workDir, repoRoot string, uncommittedFileSet map[string]struct{}, budget *untrackedBudget) (additions, deletions int, branch string) {
	if repoRoot == "" {
		repoRoot = workDir
	}
	seenInWorktree := make(map[string]struct{})
	if numstat, err := s.source.RunGit(workDir, "diff", "--no-renames", "--numstat", "HEAD"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
			add, del, file, ok := parseGitNumstatLine(line)
			if !ok {
				continue
			}
			if _, seen := seenInWorktree[file]; seen {
				continue
			}
			seenInWorktree[file] = struct{}{}
			additions += add
			deletions += del
			uncommittedFileSet[repoRoot+"\x00"+file] = struct{}{}
		}
	}

	countUntrackedFile := func(rel string) {
		rel = strings.Trim(strings.TrimSpace(rel), "\"")
		if rel == "" || strings.HasSuffix(rel, "/") || isGeneratedOrLockFile(rel) {
			return
		}
		if _, seen := seenInWorktree[rel]; seen {
			return
		}
		seenInWorktree[rel] = struct{}{}
		uncommittedFileSet[repoRoot+"\x00"+rel] = struct{}{}
		if budget.truncated || budget.readCount >= maxUntrackedFilesToCount {
			budget.truncated = true
			return
		}
		full := filepath.Join(workDir, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() > maxUntrackedFileSize {
			return
		}
		if budget.readBytes+info.Size() > maxUntrackedTotalBytes {
			budget.truncated = true
			return
		}
		data, err := os.ReadFile(full)
		if err != nil || len(data) == 0 || bytes.IndexByte(data, 0) != -1 {
			return
		}
		budget.readCount++
		budget.readBytes += int64(len(data))
		additions += bytes.Count(data, []byte{'\n'})
		if data[len(data)-1] != '\n' {
			additions++
		}
	}

	if porcelain, err := s.source.RunGitRaw(workDir, "status", "--porcelain", "-b"); err == nil {
		var expandDirs []string
		for _, rawLine := range strings.Split(porcelain, "\n") {
			if strings.HasPrefix(rawLine, "## ") {
				if br := s.source.ParseBranchFromPorcelainHeader(rawLine); br != "" {
					branch = br
				}
				continue
			}
			if !strings.HasPrefix(rawLine, "?? ") {
				continue
			}
			rel := strings.Trim(strings.TrimSpace(rawLine[3:]), "\"")
			if isGeneratedOrLockFile(rel) {
				continue
			}
			if strings.HasSuffix(rel, "/") {
				expandDirs = append(expandDirs, rel)
				continue
			}
			countUntrackedFile(rel)
		}
		if len(expandDirs) > 0 && !budget.truncated {
			args := append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, expandDirs...)
			if untrackedOut, err := s.source.RunGitRaw(workDir, args...); err == nil && untrackedOut != "" {
				for _, rel := range strings.Split(untrackedOut, "\x00") {
					countUntrackedFile(rel)
				}
			}
		}
	}
	return additions, deletions, branch
}

func queryOpenCodeSessionDirs() []string {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	const q = `SELECT COALESCE(json_group_array(directory), '[]') FROM (SELECT DISTINCT directory FROM session WHERE directory IS NOT NULL AND directory != '' LIMIT 200);`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 2000", dbPath, q).Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var dirs []string
	_ = json.Unmarshal(bytes.TrimSpace(out), &dirs)
	return dirs
}
