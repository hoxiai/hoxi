package devpulse

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxGitPulseWorkers = 6

type gitUncommittedScan struct {
	diffOut        string
	branch         string
	untrackedFiles []string
}

type gitTargetRunResult struct {
	logOut      string
	logOK       bool
	uncommitted gitUncommittedScan
}

type untrackedBudget struct {
	readCount int
	readBytes int64
	truncated bool
}

func (s *Service) scanGitUncommittedCmds(ctx context.Context, workDir string) gitUncommittedScan {
	var out gitUncommittedScan
	if ctx.Err() != nil {
		return out
	}
	porcelain, statusErr := s.runGitRaw(ctx, workDir, "status", "--porcelain", "-b", "-z")
	hasTrackedChanges := statusErr != nil || porcelain == ""
	if statusErr == nil && porcelain != "" {
		var expandDirs []string
		sep := "\n"
		if strings.Contains(porcelain, "\x00") {
			sep = "\x00"
		}
		entries := strings.Split(porcelain, sep)
		for i := 0; i < len(entries); i++ {
			rawLine := entries[i]
			if rawLine == "" {
				continue
			}
			if strings.HasPrefix(rawLine, "## ") {
				if br := s.source.ParseBranchFromPorcelainHeader(rawLine); br != "" {
					out.branch = br
				}
				continue
			}
			if len(rawLine) < 4 {
				hasTrackedChanges = true
				continue
			}
			if strings.HasPrefix(rawLine, "?? ") {
				rel := unquoteGitPath(rawLine[3:])
				if isGeneratedOrLockFile(rel) {
					continue
				}
				if strings.HasSuffix(rel, "/") {
					expandDirs = append(expandDirs, rel)
					continue
				}
				out.untrackedFiles = append(out.untrackedFiles, rel)
				continue
			}
			hasTrackedChanges = true
			if sep == "\x00" {
				x, y := rawLine[0], rawLine[1]
				if (x == 'R' || x == 'C' || y == 'R' || y == 'C') && i+1 < len(entries) {
					i++
				}
			}
		}
		if len(expandDirs) > 0 && ctx.Err() == nil {
			args := append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, expandDirs...)
			if untrackedOut, err := s.runGitRaw(ctx, workDir, args...); err == nil && untrackedOut != "" {
				for _, rel := range strings.Split(untrackedOut, "\x00") {
					if rel != "" {
						out.untrackedFiles = append(out.untrackedFiles, rel)
					}
				}
			}
		}
	}
	if hasTrackedChanges && ctx.Err() == nil {
		if numstat, err := s.runGit(ctx, workDir, "diff", "--no-renames", "--numstat", "HEAD"); err == nil {
			out.diffOut = numstat
		}
	}
	return out
}

func applyGitUncommittedScan(workDir, repoRoot string, scan gitUncommittedScan, uncommittedFileSet map[string]struct{}, budget *untrackedBudget) (additions, deletions int, branch string) {
	if repoRoot == "" {
		repoRoot = workDir
	}
	branch = scan.branch
	seenInWorktree := make(map[string]struct{})
	if scan.diffOut != "" {
		for _, line := range strings.Split(strings.TrimSpace(scan.diffOut), "\n") {
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
	for _, rel := range scan.untrackedFiles {
		rel = unquoteGitPath(rel)
		if rel == "" || strings.HasSuffix(rel, "/") || isGeneratedOrLockFile(rel) {
			continue
		}
		if _, seen := seenInWorktree[rel]; seen {
			continue
		}
		seenInWorktree[rel] = struct{}{}
		uncommittedFileSet[repoRoot+"\x00"+rel] = struct{}{}
		if budget.truncated || budget.readCount >= maxUntrackedFilesToCount {
			budget.truncated = true
			continue
		}
		full := filepath.Join(workDir, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() > maxUntrackedFileSize {
			continue
		}
		if budget.readBytes+info.Size() > maxUntrackedTotalBytes {
			budget.truncated = true
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil || len(data) == 0 || bytes.IndexByte(data, 0) != -1 {
			continue
		}
		budget.readCount++
		budget.readBytes += int64(len(data))
		additions += bytes.Count(data, []byte{'\n'})
		if data[len(data)-1] != '\n' {
			additions++
		}
	}
	return additions, deletions, branch
}

func runParallelGit(ctx context.Context, n int, fn func(idx int)) {
	if n <= 1 {
		if n == 1 && ctx.Err() == nil {
			fn(0)
		}
		return
	}
	workers := maxGitPulseWorkers
	if n < workers {
		workers = n
	}
	jobs := make(chan int, n)
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				fn(idx)
			}
		}()
	}
	wg.Wait()
}

func (s *Service) runGitTargetsBatch(ctx context.Context, targets []GitTarget, baseLogArgs []string, includeUncommitted bool) []gitTargetRunResult {
	results := make([]gitTargetRunResult, len(targets))
	// Phase 1: run root git logs and all uncommitted status checks concurrently.
	runParallelGit(ctx, len(targets), func(idx int) {
		t := targets[idx]
		if !t.IsWorktree {
			args := append(append([]string(nil), baseLogArgs...), "--branches", "HEAD")
			if out, err := s.runGitRaw(ctx, t.Dir, args...); err == nil {
				results[idx].logOut = out
				results[idx].logOK = true
			}
		}
		if includeUncommitted && ctx.Err() == nil {
			results[idx].uncommitted = s.scanGitUncommittedCmds(ctx, t.Dir)
		}
	})
	if ctx.Err() != nil {
		return results
	}

	rootLogOK := make(map[string]bool)
	for i, t := range targets {
		if !t.IsWorktree && results[i].logOK {
			rootLogOK[t.RepoRoot] = true
		}
	}

	// Phase 2: only scan worktrees whose HEAD is not covered by root `--branches`.
	// Only pass `HEAD --not --branches` when the worktree genuinely shares
	// commondir with repoRoot; an independent nested repo needs a full log.
	type wtJob struct {
		idx            int
		detachedShared bool
	}
	var wtJobs []wtJob
	for i, t := range targets {
		if !t.IsWorktree {
			continue
		}
		if rootLogOK[t.RepoRoot] {
			switch classifyWorktreeHead(t.Dir, t.RepoRoot) {
			case worktreeSharedBranch:
				continue
			case worktreeSharedDetached:
				wtJobs = append(wtJobs, wtJob{idx: i, detachedShared: true})
				continue
			}
		}
		wtJobs = append(wtJobs, wtJob{idx: i, detachedShared: false})
	}
	if len(wtJobs) > 0 {
		runParallelGit(ctx, len(wtJobs), func(j int) {
			job := wtJobs[j]
			t := targets[job.idx]
			args := append([]string(nil), baseLogArgs...)
			if job.detachedShared {
				args = append(args, "HEAD", "--not", "--branches")
			}
			if out, err := s.runGitRaw(ctx, t.Dir, args...); err == nil {
				results[job.idx].logOut = out
				results[job.idx].logOK = true
			}
		})
	}
	return results
}

func (s *Service) CollectGitDailyStats(targets []GitTarget, startOfToday time.Time, res *DailyPulseResponse) {
	s.CollectGitDailyStatsCtx(context.Background(), targets, startOfToday, res)
}

func (s *Service) CollectGitDailyStatsCtx(ctx context.Context, targets []GitTarget, startOfToday time.Time, res *DailyPulseResponse) {
	if s.source == nil || len(targets) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	baseArgs := []string{"log", "--since=" + startOfToday.Format(time.RFC3339), "--no-merges", "--no-renames", "--numstat", "--pretty=format:COMMIT:%H\t%s"}
	results := s.runGitTargetsBatch(ctx, targets, baseArgs, true)

	committedFileSet := make(map[string]struct{})
	seenCommits := make(map[string]struct{})
	for i, t := range targets {
		if !results[i].logOK || results[i].logOut == "" {
			continue
		}
		skipCurrent := false
		for _, rawLine := range strings.Split(results[i].logOut, "\n") {
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
	for i, t := range targets {
		add, del, branch := applyGitUncommittedScan(t.Dir, t.RepoRoot, results[i].uncommitted, uncommittedFileSet, &budget)
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
	s.CollectGitHistoryCtx(context.Background(), targets, start, end, todayStart, day)
}

func (s *Service) CollectGitHistoryCtx(ctx context.Context, targets []GitTarget, start, end, todayStart time.Time, day func(string) *HistoryDay) {
	if s.source == nil || len(targets) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startKey := start.Format("2006-01-02")
	endInclusiveKey := end.AddDate(0, 0, -1).Format("2006-01-02")
	includeToday := !todayStart.Before(start) && todayStart.Before(end)

	baseArgs := []string{
		"log",
		"--since=" + start.Format(time.RFC3339),
		"--until=" + end.Format(time.RFC3339),
		"--no-merges", "--no-renames", "--numstat",
		"--pretty=format:COMMIT:%H\t%ct\t%s",
	}
	results := s.runGitTargetsBatch(ctx, targets, baseArgs, includeToday)
	if ctx.Err() != nil {
		return
	}

	loc := start.Location()
	fileSets := make(map[string]map[string]struct{})
	seenCommits := make(map[string]struct{})

	for i, t := range targets {
		if !results[i].logOK || results[i].logOut == "" {
			continue
		}
		var current string
		for _, rawLine := range strings.Split(results[i].logOut, "\n") {
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

	if includeToday {
		todayKey := todayStart.Format("2006-01-02")
		uncommittedFileSet := make(map[string]struct{})
		var budget untrackedBudget
		var uncommAdd, uncommDel int
		for i, t := range targets {
			a, d, _ := applyGitUncommittedScan(t.Dir, t.RepoRoot, results[i].uncommitted, uncommittedFileSet, &budget)
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
