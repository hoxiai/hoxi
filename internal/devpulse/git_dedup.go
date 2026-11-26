package devpulse

import (
	"context"
	"strconv"
	"strings"
	"time"
)

const gitEmptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type parsedCommit struct {
	sha        string
	dayKey     string
	subject    string
	repoRoot   string
	workDir    string
	isSnapshot bool
	hadFiles   bool
	files      []parsedFileStat
}

type sideBranchRef struct {
	name           string
	tipSHA         string
	lastCountedSHA string
}

func parseCommitHeader(line string, hasTimestamp bool, startKey, endInclusiveKey string, loc *time.Location) (parsedCommit, bool) {
	rest := strings.TrimPrefix(strings.TrimSpace(line), "COMMIT:")
	if !hasTimestamp {
		parts := strings.SplitN(rest, "\t", 2)
		sha := strings.TrimSpace(parts[0])
		var subject string
		if len(parts) > 1 {
			subject = strings.TrimSpace(parts[1])
		}
		return parsedCommit{
			sha:        sha,
			subject:    subject,
			isSnapshot: isInternalSnapshotCommitSubject(subject),
		}, true
	}

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
	sec, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return parsedCommit{}, false
	}
	key := historyDayKey(sec*1000, loc)
	if key < startKey || key > endInclusiveKey {
		return parsedCommit{}, false
	}
	return parsedCommit{
		sha:        sha,
		dayKey:     key,
		subject:    subject,
		isSnapshot: isInternalSnapshotCommitSubject(subject),
	}, true
}

// parseGitLogOutput parses both `-z` NUL-delimited `git log --numstat` output
// (including `-M` rename entries) and plain newline-delimited output used by
// unit test stubs.
func parseGitLogOutput(raw, repoRoot, workDir string, hasTimestamp bool, startKey, endInclusiveKey string, loc *time.Location) []parsedCommit {
	if raw == "" {
		return nil
	}
	var commits []parsedCommit
	var cur *parsedCommit
	flush := func() {
		if cur != nil {
			commits = append(commits, *cur)
			cur = nil
		}
	}
	start := func(header string) {
		flush()
		c, ok := parseCommitHeader(header, hasTimestamp, startKey, endInclusiveKey, loc)
		if !ok {
			return
		}
		c.repoRoot = repoRoot
		c.workDir = workDir
		cur = &c
	}

	if strings.Contains(raw, "\x00") {
		tokens := strings.Split(raw, "\x00")
		for i := 0; i < len(tokens); i++ {
			tok := tokens[i]
			if tok == "" {
				continue
			}
			trimmedLeft := strings.TrimLeft(tok, "\r\n")
			if trimmedLeft == "" {
				continue
			}
			if strings.HasPrefix(trimmedLeft, "COMMIT:") {
				headerLine := trimmedLeft
				rest := ""
				if nl := strings.IndexByte(trimmedLeft, '\n'); nl >= 0 {
					headerLine = strings.TrimRight(trimmedLeft[:nl], "\r")
					rest = strings.TrimLeft(trimmedLeft[nl+1:], "\r\n")
				}
				start(headerLine)
				if rest == "" {
					continue
				}
				tok = rest
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
			if cur == nil {
				continue
			}
			cur.hadFiles = true
			if st, ok := makeParsedFileStat(addStr, delStr, oldPath, newPath, false); ok {
				cur.files = append(cur.files, st)
			}
		}
		flush()
		return commits
	}

	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "COMMIT:") {
			start(line)
			continue
		}
		if cur == nil {
			continue
		}
		cur.hadFiles = true
		if st, ok := parseGitNumstatLineStat(rawLine); ok {
			cur.files = append(cur.files, st)
		}
	}
	flush()
	return commits
}

// isMergedSnapshotCommit reports whether an internal Hoxi pre-apply snapshot
// commit was merged into a non-hoxi local branch (such as main via Apply Merge).
// Unmerged snapshots sitting only on hoxi/* worktree branches stay skipped so
// patch-apply workflows never double-count them.
func (s *Service) isMergedSnapshotCommit(ctx context.Context, workDir, sha string) bool {
	if sha == "" || ctx.Err() != nil {
		return false
	}
	out, err := s.runGitRaw(ctx, workDir, "for-each-ref", "--contains", sha, "--format=%(refname:short)", "refs/heads/")
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		br := strings.TrimSpace(line)
		if br != "" && !strings.HasPrefix(br, "hoxi/") {
			return true
		}
	}
	return false
}

// collectDeduplicatedCommits gathers parsed commits across targets, filters
// unmerged internal snapshot commits, and removes cross-branch duplicate
// commits (e.g. Apply Patch followed by commit on main, cherry-picks, or
// squashed branch changes already landed on HEAD).
func (s *Service) collectDeduplicatedCommits(
	ctx context.Context,
	targets []GitTarget,
	results []gitTargetRunResult,
	since time.Time,
	hasTimestamp bool,
	startKey, endInclusiveKey string,
	loc *time.Location,
) ([]parsedCommit, map[string][]sideBranchRef, map[string]map[string]struct{}) {
	seenSHAs := make(map[string]struct{})
	snapshotMerged := make(map[string]bool)
	repoOrder := make([]string, 0, len(targets))
	repoDirByRoot := make(map[string]string)
	byRepo := make(map[string][]parsedCommit)

	for i, t := range targets {
		if !results[i].logOK || results[i].logOut == "" {
			continue
		}
		repoRoot := t.RepoRoot
		if repoRoot == "" {
			repoRoot = t.Dir
		}
		if _, ok := repoDirByRoot[repoRoot]; !ok {
			repoOrder = append(repoOrder, repoRoot)
			repoDirByRoot[repoRoot] = t.Dir
		} else if !t.IsWorktree {
			repoDirByRoot[repoRoot] = t.Dir
		}
		parsed := parseGitLogOutput(results[i].logOut, repoRoot, t.Dir, hasTimestamp, startKey, endInclusiveKey, loc)
		for _, c := range parsed {
			if c.sha != "" {
				if _, dup := seenSHAs[c.sha]; dup {
					continue
				}
				seenSHAs[c.sha] = struct{}{}
			}
			if c.isSnapshot {
				snapKey := repoRoot + "\x00" + c.sha
				merged, checked := snapshotMerged[snapKey]
				if !checked {
					merged = s.isMergedSnapshotCommit(ctx, repoDirByRoot[repoRoot], c.sha)
					snapshotMerged[snapKey] = merged
				}
				if !merged {
					continue
				}
			}
			byRepo[repoRoot] = append(byRepo[repoRoot], c)
		}
	}

	var allCommits []parsedCommit
	sideBranchesByRepo := make(map[string][]sideBranchRef)
	committedFilesByRepo := make(map[string]map[string]struct{})
	for _, repoRoot := range repoOrder {
		repoCommits := byRepo[repoRoot]
		if len(repoCommits) == 0 {
			continue
		}
		deduped, sideBranches := s.deduplicateRepoCommits(ctx, repoDirByRoot[repoRoot], since, repoCommits)
		if len(sideBranches) > 0 {
			sideBranchesByRepo[repoRoot] = sideBranches
		}
		for _, c := range deduped {
			allCommits = append(allCommits, c)
			if len(c.files) > 0 {
				if committedFilesByRepo[repoRoot] == nil {
					committedFilesByRepo[repoRoot] = make(map[string]struct{})
				}
				for _, f := range c.files {
					committedFilesByRepo[repoRoot][f.path] = struct{}{}
				}
			}
		}
	}
	return allCommits, sideBranchesByRepo, committedFilesByRepo
}

func (s *Service) deduplicateRepoCommits(ctx context.Context, repoDir string, since time.Time, commits []parsedCommit) ([]parsedCommit, []sideBranchRef) {
	if len(commits) == 0 || ctx.Err() != nil {
		return commits, nil
	}
	refsOut, err := s.runGitRaw(ctx, repoDir, "for-each-ref", "--format=%(committerdate:unix)\t%(objectname)\t%(refname:short)", "refs/heads/")
	if err != nil || strings.TrimSpace(refsOut) == "" {
		return commits, nil
	}
	headSHA, err := s.runGit(ctx, repoDir, "rev-parse", "--verify", "HEAD")
	if err != nil || headSHA == "" {
		return commits, nil
	}

	sinceUnix := since.Unix()
	var candidateSide []sideBranchRef
	for _, line := range strings.Split(refsOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		tipTs, parseErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		tipSHA := strings.TrimSpace(parts[1])
		brName := strings.TrimSpace(parts[2])
		if parseErr != nil || tipSHA == "" || brName == "" || tipSHA == headSHA {
			continue
		}
		if tipTs >= sinceUnix {
			candidateSide = append(candidateSide, sideBranchRef{name: brName, tipSHA: tipSHA})
		}
	}
	if len(candidateSide) == 0 {
		return commits, nil
	}

	headLogOut, err := s.runGitRaw(ctx, repoDir, "log", "--since="+since.Format(time.RFC3339), "--no-merges", "--format=%H", "HEAD")
	if err != nil {
		return commits, nil
	}
	onHead := make(map[string]struct{})
	for _, line := range strings.Split(headLogOut, "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			onHead[sha] = struct{}{}
		}
	}

	headTouchedFiles := make(map[string]struct{})
	sideCommitBySHA := make(map[string]parsedCommit)
	for _, c := range commits {
		if c.sha == "" {
			continue
		}
		if _, ok := onHead[c.sha]; ok {
			for _, f := range c.files {
				headTouchedFiles[f.path] = struct{}{}
			}
		} else {
			sideCommitBySHA[c.sha] = c
		}
	}
	if len(sideCommitBySHA) == 0 {
		return commits, nil
	}

	dupSHAs := make(map[string]struct{})
	landedFilesByCommit := make(map[string]map[string]struct{})
	branchCommitSHAs := make(map[string][]string)
	var populatedSide []sideBranchRef

	for _, br := range candidateSide {
		if ctx.Err() != nil {
			break
		}
		revOut, revErr := s.runGitRaw(ctx, repoDir, "rev-list", "--no-merges", "--since="+since.Format(time.RFC3339), br.name, "--not", "HEAD")
		if revErr != nil || strings.TrimSpace(revOut) == "" {
			continue
		}
		var brSHAs []string
		for _, line := range strings.Split(revOut, "\n") {
			sha := strings.TrimSpace(line)
			if _, ok := sideCommitBySHA[sha]; ok {
				brSHAs = append(brSHAs, sha)
			}
		}
		if len(brSHAs) == 0 {
			continue
		}
		populatedSide = append(populatedSide, br)
		branchCommitSHAs[br.name] = brSHAs

		if len(headTouchedFiles) > 0 {
			overlapSet := make(map[string]struct{})
			var overlapFiles []string
			for _, sha := range brSHAs {
				for _, f := range sideCommitBySHA[sha].files {
					if _, onH := headTouchedFiles[f.path]; !onH {
						continue
					}
					if _, seen := overlapSet[f.path]; seen {
						continue
					}
					overlapSet[f.path] = struct{}{}
					overlapFiles = append(overlapFiles, f.path)
				}
			}
			if len(overlapFiles) > 0 {
				if cherryOut, cErr := s.runGitRaw(ctx, repoDir, "cherry", "HEAD", br.name); cErr == nil && cherryOut != "" {
					for _, line := range strings.Split(cherryOut, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							sha := strings.TrimSpace(strings.TrimPrefix(line, "- "))
							if _, ok := sideCommitBySHA[sha]; ok {
								dupSHAs[sha] = struct{}{}
							}
						}
					}
				}

				// Detect squashed multi-commit Apply (patch) + commit on HEAD: if HEAD
				// touched the same file in this window, branch changed it since merge-base,
				// and HEAD now matches the branch blob byte-for-byte, the branch's lines
				// for that file are already counted on HEAD.
				remOverlapSet := make(map[string]struct{})
				var remOverlapFiles []string
				for _, sha := range brSHAs {
					if _, dup := dupSHAs[sha]; dup {
						continue
					}
					for _, f := range sideCommitBySHA[sha].files {
						if _, onH := headTouchedFiles[f.path]; !onH {
							continue
						}
						if _, seen := remOverlapSet[f.path]; seen {
							continue
						}
						remOverlapSet[f.path] = struct{}{}
						remOverlapFiles = append(remOverlapFiles, f.path)
					}
				}
				if len(remOverlapFiles) > 0 {
					diffHeadArgs := append([]string{"diff", "--name-only", "-z", "HEAD", br.name, "--"}, remOverlapFiles...)
					if diffHeadOut, dErr := s.runGitRaw(ctx, repoDir, diffHeadArgs...); dErr == nil {
						differsFromHead := make(map[string]struct{})
						for _, p := range strings.Split(diffHeadOut, "\x00") {
							if p != "" {
								differsFromHead[p] = struct{}{}
							}
						}
						var sameAsHead []string
						for _, f := range remOverlapFiles {
							if _, differs := differsFromHead[f]; !differs {
								sameAsHead = append(sameAsHead, f)
							}
						}
						if len(sameAsHead) > 0 {
							if mb, mbErr := s.runGit(ctx, repoDir, "merge-base", "HEAD", br.name); mbErr == nil && mb != "" {
								diffBaseArgs := append([]string{"diff", "--name-only", "-z", mb, br.name, "--"}, sameAsHead...)
								if diffBaseOut, bErr := s.runGitRaw(ctx, repoDir, diffBaseArgs...); bErr == nil && diffBaseOut != "" {
									for _, p := range strings.Split(diffBaseOut, "\x00") {
										if p == "" {
											continue
										}
										for _, sha := range brSHAs {
											if _, dup := dupSHAs[sha]; dup {
												continue
											}
											if landedFilesByCommit[sha] == nil {
												landedFilesByCommit[sha] = make(map[string]struct{})
											}
											landedFilesByCommit[sha][p] = struct{}{}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Deduplicate cherry-picked copies between distinct side branches when they
	// touch overlapping files.
	if len(populatedSide) >= 2 && len(populatedSide) <= 16 {
		branchFiles := make(map[string]map[string]struct{}, len(populatedSide))
		for _, br := range populatedSide {
			fset := make(map[string]struct{})
			for _, sha := range branchCommitSHAs[br.name] {
				if _, dup := dupSHAs[sha]; dup {
					continue
				}
				for _, f := range sideCommitBySHA[sha].files {
					fset[f.path] = struct{}{}
				}
			}
			branchFiles[br.name] = fset
		}
		for a := 0; a < len(populatedSide); a++ {
			filesA := branchFiles[populatedSide[a].name]
			if len(filesA) == 0 {
				continue
			}
			for b := a + 1; b < len(populatedSide); b++ {
				if ctx.Err() != nil {
					break
				}
				filesB := branchFiles[populatedSide[b].name]
				hasOverlap := false
				for f := range filesB {
					if _, ok := filesA[f]; ok {
						hasOverlap = true
						break
					}
				}
				if !hasOverlap {
					continue
				}
				if cherryOut, cErr := s.runGitRaw(ctx, repoDir, "cherry", populatedSide[a].name, populatedSide[b].name); cErr == nil && cherryOut != "" {
					for _, line := range strings.Split(cherryOut, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							sha := strings.TrimSpace(strings.TrimPrefix(line, "- "))
							if _, ok := sideCommitBySHA[sha]; ok {
								dupSHAs[sha] = struct{}{}
							}
						}
					}
				}
			}
		}
	}

	kept := make([]parsedCommit, 0, len(commits))
	keptSHAs := make(map[string]struct{}, len(commits))
	for _, c := range commits {
		if c.sha != "" {
			if _, dup := dupSHAs[c.sha]; dup {
				continue
			}
			if landed := landedFilesByCommit[c.sha]; len(landed) > 0 {
				filtered := make([]parsedFileStat, 0, len(c.files))
				for _, f := range c.files {
					if _, skip := landed[f.path]; !skip {
						filtered = append(filtered, f)
					}
				}
				if len(filtered) == 0 && c.hadFiles {
					continue
				}
				c.files = filtered
			}
			keptSHAs[c.sha] = struct{}{}
		}
		kept = append(kept, c)
	}

	var activeSide []sideBranchRef
	for _, br := range populatedSide {
		for _, sha := range branchCommitSHAs[br.name] {
			if _, ok := keptSHAs[sha]; ok {
				br.lastCountedSHA = sha
				activeSide = append(activeSide, br)
				break
			}
		}
	}
	return kept, activeSide
}
