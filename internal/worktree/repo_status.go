package worktree

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type worktreeRepoStatus struct {
	Repo         string `json:"repo"`
	Name         string `json:"name"`
	Directory    string `json:"directory"`
	Branch       string `json:"branch,omitempty"`
	HeadCommit   string `json:"headCommit,omitempty"`
	HeadSubject  string `json:"headSubject,omitempty"`
	Dirty        bool   `json:"dirty"`
	ChangedFiles int    `json:"changedFiles"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	AheadCount   int    `json:"aheadCount"`
	BehindCount  int    `json:"behindCount"`
	HasUpstream  bool   `json:"hasUpstream"`
	CompareURL   string `json:"compareURL,omitempty"`
}

type repoStatusSummary struct {
	branch       string
	headCommit   string
	headSubject  string
	remoteURL    string
	changedFiles int
	additions    int
	deletions    int
	aheadCount   int
	behindCount  int
	hasUpstream  bool
	valid        bool
}

type cachedHeadCommitInfo struct {
	headSHA     string
	headCommit  string
	headSubject string
}

var (
	headCommitCacheMu sync.RWMutex
	headCommitCache   = make(map[string]cachedHeadCommitInfo)
)

// resolveGitBranchFast reads the current branch name directly from .git/HEAD
// in memory, falling back to git rev-parse only when needed.
func resolveGitBranchFast(repoDir string) string {
	gitDir := resolveGitDirFast(repoDir)
	if gitDir != "" {
		if rawHead, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
			headLine := strings.TrimSpace(string(rawHead))
			if strings.HasPrefix(headLine, "ref:") {
				ref := strings.TrimSpace(strings.TrimPrefix(headLine, "ref:"))
				if strings.HasPrefix(ref, "refs/heads/") {
					return strings.TrimPrefix(ref, "refs/heads/")
				}
				return filepath.Base(ref)
			}
			if len(headLine) >= 40 {
				return "HEAD"
			}
		}
	}
	br, _ := runGit(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	return br
}

// resolveHeadCommitInfoFast returns the short commit hash and subject for headSHA,
// caching the result in memory by (repoDir, headSHA) so working-tree edits never
// spawn redundant `git log -1` subprocesses when HEAD has not moved.
func resolveHeadCommitInfoFast(repoDir, headSHA string) (string, string) {
	cleanDir := filepath.Clean(repoDir)
	if headSHA != "" {
		headCommitCacheMu.RLock()
		cached, ok := headCommitCache[cleanDir]
		headCommitCacheMu.RUnlock()
		if ok && cached.headSHA == headSHA && cached.headCommit != "" {
			return cached.headCommit, cached.headSubject
		}
	}

	logLine, logErr := runGit(repoDir, "log", "-1", "--format=%h\t%s")
	if logErr != nil || logLine == "" {
		return "", ""
	}
	parts := strings.SplitN(logLine, "\t", 2)
	headCommit := strings.TrimSpace(parts[0])
	var headSubject string
	if len(parts) > 1 {
		headSubject = strings.TrimSpace(parts[1])
	}
	if headSHA != "" && headCommit != "" {
		headCommitCacheMu.Lock()
		if len(headCommitCache) >= 128 {
			for k := range headCommitCache {
				delete(headCommitCache, k)
				break
			}
		}
		headCommitCache[cleanDir] = cachedHeadCommitInfo{
			headSHA:     headSHA,
			headCommit:  headCommit,
			headSubject: headSubject,
		}
		headCommitCacheMu.Unlock()
	}
	return headCommit, headSubject
}

func canonicalDirKey(dir string) string {
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

// discoverWorkspaceGitRepos discovers all Git repositories in primaryDir (including
// nested sub-repositories) as well as any additional workspace directories in extraDirs,
// deduplicating by canonical directory path.
func discoverWorkspaceGitRepos(primaryDir string, extraDirs []string) []gitRepoTarget {
	primaryRepos := discoverGitRepos(primaryDir)
	if len(extraDirs) == 0 {
		return primaryRepos
	}

	seenDirs := make(map[string]struct{}, len(primaryRepos)+len(extraDirs))
	usedPrefixes := make(map[string]struct{}, len(primaryRepos)+len(extraDirs))
	out := make([]gitRepoTarget, 0, len(primaryRepos)+len(extraDirs))

	if key := canonicalDirKey(primaryDir); key != "" {
		seenDirs[key] = struct{}{}
	}
	for _, r := range primaryRepos {
		if key := canonicalDirKey(r.Dir); key != "" {
			seenDirs[key] = struct{}{}
		}
		if r.RelPrefix != "" {
			usedPrefixes[r.RelPrefix] = struct{}{}
		}
		out = append(out, r)
	}

	for _, rawExtra := range extraDirs {
		trimmed := strings.TrimSpace(rawExtra)
		if trimmed == "" {
			continue
		}
		extraClean := filepath.Clean(trimmed)
		extraKey := canonicalDirKey(extraClean)
		if extraKey == "" {
			continue
		}
		if _, exists := seenDirs[extraKey]; exists {
			continue
		}

		folderBase := filepath.ToSlash(filepath.Base(extraClean))
		if folderBase == "" || folderBase == "." || folderBase == "/" {
			folderBase = "workspace"
		}
		if _, exists := usedPrefixes[folderBase]; exists {
			parentBase := filepath.ToSlash(filepath.Base(filepath.Dir(extraClean)))
			if parentBase != "" && parentBase != "." && parentBase != "/" {
				folderBase = parentBase + "/" + folderBase
			}
		}
		usedPrefixes[folderBase] = struct{}{}

		extraRepos := discoverGitRepos(extraClean)
		for _, er := range extraRepos {
			repoKey := canonicalDirKey(er.Dir)
			if repoKey == "" {
				continue
			}
			if _, exists := seenDirs[repoKey]; exists {
				continue
			}
			seenDirs[repoKey] = struct{}{}
			prefix := folderBase
			if er.RelPrefix != "" {
				prefix = folderBase + "/" + strings.TrimPrefix(er.RelPrefix, "/")
			}
			out = append(out, gitRepoTarget{
				Dir:       er.Dir,
				RelPrefix: prefix,
				External:  true,
			})
		}
	}
	return out
}

func repoDisplayName(repo gitRepoTarget) string {
	if repo.RelPrefix != "" {
		return repo.RelPrefix
	}
	base := filepath.Base(filepath.Clean(repo.Dir))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return repo.Dir
	}
	return base
}

func statusSingleRepo(repo gitRepoTarget, diffTarget string, nestedPrefixes map[string]struct{}) repoStatusSummary {
	fingerprint, headSHA, porcelainZ, porcelainErr := computeRepoStateFingerprintWithPorcelain(repo.Dir, diffTarget)
	if cached, hit := globalRepoStatusCache.get(repo.Dir, diffTarget, fingerprint); hit {
		return cached
	}
	flightKey := filepath.Clean(repo.Dir) + "\x00" + repo.RelPrefix + "\x00" + diffTarget + "\x00" + fingerprint
	return statusFlightGroup.do(flightKey, func() repoStatusSummary {
		if cached, hit := globalRepoStatusCache.get(repo.Dir, diffTarget, fingerprint); hit {
			return cached
		}
		return computeStatusSingleRepo(repo, diffTarget, fingerprint, headSHA, porcelainZ, porcelainErr, nestedPrefixes)
	})
}

func computeStatusSingleRepo(
	repo gitRepoTarget,
	diffTarget, fingerprint, headSHA, porcelainZ string,
	porcelainErr error,
	nestedPrefixes map[string]struct{},
) repoStatusSummary {

	changedFiles := 0
	aheadCount := 0
	behindCount := 0
	hasUpstream := false
	branch := ""
	var untrackedFiles []string

	if porcelainErr == nil {
		entries := strings.Split(porcelainZ, "\x00")
		for i := 0; i < len(entries); i++ {
			rawLine := entries[i]
			if rawLine == "" {
				continue
			}
			if strings.HasPrefix(rawLine, "## ") {
				if br := parseBranchFromPorcelainHeader(rawLine); br != "" {
					branch = br
				}
				aheadCount, behindCount, hasUpstream = parseAheadBehindAndUpstreamFromPorcelainHeader(rawLine)
				continue
			}
			if len(rawLine) < 4 {
				continue
			}
			x, y := rawLine[0], rawLine[1]
			rel := strings.TrimSpace(rawLine[3:])
			if (x == 'R' || x == 'C' || y == 'R' || y == 'C') && i+1 < len(entries) {
				i++
			}
			if rel == "" || strings.HasPrefix(rel, ".hoxi/") {
				continue
			}
			if !repo.External && repo.RelPrefix == "" && isWithinNestedRepo(rel, nestedPrefixes) {
				continue
			}
			changedFiles++
			if strings.HasPrefix(rawLine, "?? ") && len(untrackedFiles) < maxUntrackedFilesToCount {
				untrackedFiles = append(untrackedFiles, rel)
			}
		}
	}

	if branch == "" {
		branch = resolveGitBranchFast(repo.Dir)
	}

	var numstat string
	if diffTarget != "HEAD" || changedFiles > 0 || porcelainErr != nil {
		var err error
		numstat, err = runGit(repo.Dir, "diff", "--numstat", diffTarget)
		if err != nil && diffTarget != "HEAD" {
			numstat, err = runGit(repo.Dir, "diff", "--numstat", "HEAD")
		}
		if err != nil {
			numstat, _ = runGit(repo.Dir, "diff", "--cached", "--numstat")
		}
	}

	additions := 0
	deletions := 0
	diffFileSet := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			fields = strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
		}
		f := strings.TrimSpace(fields[len(fields)-1])
		if f == "" || strings.HasPrefix(f, ".hoxi/") {
			continue
		}
		if !repo.External && repo.RelPrefix == "" && isWithinNestedRepo(f, nestedPrefixes) {
			continue
		}
		if a, convErr := strconv.Atoi(strings.TrimSpace(fields[0])); convErr == nil {
			additions += a
		}
		if d, convErr := strconv.Atoi(strings.TrimSpace(fields[1])); convErr == nil {
			deletions += d
		}
		diffFileSet[f] = struct{}{}
	}

	for _, rel := range untrackedFiles {
		full := filepath.Join(repo.Dir, rel)
		info, statErr := os.Lstat(full)
		if statErr != nil || info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			additions++
			continue
		}
		if info.Mode().IsRegular() && regularFileStaysWithinRepo(repo.Dir, full) && info.Size() <= maxUntrackedFileSize {
			if data, readErr := os.ReadFile(full); readErr == nil && len(data) > 0 {
				cnt := bytes.Count(data, []byte{'\n'})
				if data[len(data)-1] != '\n' {
					cnt++
				}
				additions += cnt
			}
		}
	}

	if len(diffFileSet) > changedFiles {
		changedFiles = len(diffFileSet)
	}

	headCommit, headSubject := resolveHeadCommitInfoFast(repo.Dir, headSHA)

	summary := repoStatusSummary{
		branch:       branch,
		headCommit:   headCommit,
		headSubject:  headSubject,
		remoteURL:    resolveGitRemoteURLFast(repo.Dir, "origin"),
		changedFiles: changedFiles,
		additions:    additions,
		deletions:    deletions,
		aheadCount:   aheadCount,
		behindCount:  behindCount,
		hasUpstream:  hasUpstream,
		valid:        porcelainErr == nil || branch != "" || headCommit != "",
	}
	if summary.valid {
		globalRepoStatusCache.put(repo.Dir, diffTarget, fingerprint, summary)
		if changedFiles == 0 && additions == 0 && deletions == 0 && diffTarget == "HEAD" {
			globalRepoDiffCache.put(repo.Dir, diffTarget, fingerprint, []worktreeFileDiff{})
		}
	}
	return summary
}
