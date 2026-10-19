package worktree

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	HoxiCoAuthorTrailer = "Co-authored-by: Hoxi <code@hoxi.ai>"
	gitPushTimeout      = 45 * time.Second
)

type CommitAndPushResult struct {
	OK         bool   `json:"ok"`
	Commit     string `json:"commit,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Remote     string `json:"remote,omitempty"`
	Pushed     bool   `json:"pushed"`
	Message    string `json:"message,omitempty"`
	CompareURL string `json:"compareURL,omitempty"`
}

func appendHoxiCoAuthor(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return msg
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "co-authored-by:") && strings.Contains(lower, "code@hoxi.ai") {
		return msg
	}
	return msg + "\n\n" + HoxiCoAuthorTrailer
}

func parseBranchFromPorcelainHeader(headerLine string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(headerLine, "##"))
	if rest == "" {
		return ""
	}
	for _, prefix := range []string{"No commits yet on ", "Initial commit on "} {
		if strings.HasPrefix(rest, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(rest, prefix))
		}
	}
	if strings.HasPrefix(rest, "HEAD") {
		return "HEAD"
	}
	if idx := strings.Index(rest, "..."); idx > 0 {
		rest = rest[:idx]
	}
	if idx := strings.IndexByte(rest, ' '); idx > 0 {
		rest = rest[:idx]
	}
	return strings.TrimSpace(rest)
}

func parseAheadAndUpstreamFromPorcelainHeader(headerLine string) (int, bool) {
	rest := strings.TrimSpace(strings.TrimPrefix(headerLine, "##"))
	if rest == "" || strings.HasPrefix(rest, "HEAD") {
		return 0, false
	}
	hasUpstream := strings.Contains(rest, "...")
	openIdx := strings.IndexByte(rest, '[')
	closeIdx := strings.LastIndexByte(rest, ']')
	if openIdx < 0 || closeIdx <= openIdx {
		return 0, hasUpstream
	}
	bracket := rest[openIdx+1 : closeIdx]
	for _, part := range strings.Split(bracket, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "ahead ") {
			numStr := strings.TrimSpace(strings.TrimPrefix(part, "ahead "))
			if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
				return n, hasUpstream
			}
		}
	}
	return 0, hasUpstream
}

func (m *worktreeManager) resolveWorkDir(sessionID string) string {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if ok && meta.Isolated && meta.Directory != "" {
		return meta.Directory
	}
	if sessionID != "" && sessionID != "__root__" {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return m.rootDir
}

func (m *worktreeManager) CommitWorktree(sessionID, message string) (string, error) {
	res, err := m.CommitAndPushWorktree(sessionID, message, false)
	if err != nil {
		return "", err
	}
	return res.Commit, nil
}

func repoHasOwnChanges(repo gitRepoTarget, nestedPrefixes map[string]struct{}) (bool, error) {
	porcelain, err := runGitRaw(repo.Dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	for _, rawLine := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(rawLine) == "" || len(rawLine) < 4 {
			continue
		}
		rel := strings.TrimSpace(rawLine[3:])
		if repo.RelPrefix == "" && isWithinNestedRepo(rel, nestedPrefixes) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func pushDiscoveredRepos(repos []gitRepoTarget) (CommitAndPushResult, error) {
	var target gitRepoTarget
	for _, r := range repos {
		porcelain, err := runGitRaw(r.Dir, "status", "--porcelain", "-b")
		if err != nil {
			continue
		}
		if target.Dir == "" {
			target = r
		}
		firstLine := strings.SplitN(porcelain, "\n", 2)[0]
		if ahead, _ := parseAheadAndUpstreamFromPorcelainHeader(firstLine); ahead > 0 {
			target = r
			break
		}
	}
	if target.Dir == "" && len(repos) > 0 {
		target = repos[0]
	}
	branch, _ := runGit(target.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	hash, _ := runGit(target.Dir, "rev-parse", "--short", "HEAD")
	remote, err := runGitPush(target.Dir, branch)
	if err != nil {
		return CommitAndPushResult{}, err
	}
	return CommitAndPushResult{
		OK:         true,
		Commit:     hash,
		Branch:     branch,
		Remote:     remote,
		Pushed:     true,
		CompareURL: buildRemoteCompareURL(resolveGitRemoteURLFast(target.Dir, remote), "", branch),
	}, nil
}

func (m *worktreeManager) PushWorktree(sessionID string) (CommitAndPushResult, error) {
	m.ops.Lock()
	defer m.ops.Unlock()

	workDir := m.resolveWorkDir(sessionID)
	repos := discoverGitRepos(workDir)
	if len(repos) > 1 || (len(repos) == 1 && repos[0].RelPrefix != "") {
		return pushDiscoveredRepos(repos)
	}

	branch, _ := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")

	remote, err := runGitPush(workDir, branch)
	if err != nil {
		return CommitAndPushResult{}, err
	}
	return CommitAndPushResult{
		OK:         true,
		Commit:     hash,
		Branch:     branch,
		Remote:     remote,
		Pushed:     true,
		CompareURL: buildRemoteCompareURL(resolveGitRemoteURLFast(workDir, remote), "", branch),
	}, nil
}

func (m *worktreeManager) CommitAndPushWorktree(sessionID, message string, push bool) (CommitAndPushResult, error) {
	m.ops.Lock()
	defer m.ops.Unlock()

	workDir := m.resolveWorkDir(sessionID)
	repos := discoverGitRepos(workDir)
	if len(repos) > 1 || (len(repos) == 1 && repos[0].RelPrefix != "") {
		nestedPrefixes := nestedRepoPrefixSet(repos)
		var dirtyRepos []gitRepoTarget
		for _, r := range repos {
			if dirty, err := repoHasOwnChanges(r, nestedPrefixes); err == nil && dirty {
				dirtyRepos = append(dirtyRepos, r)
			}
		}

		msg := strings.TrimSpace(message)
		if len(dirtyRepos) == 0 {
			if !push {
				return CommitAndPushResult{}, errors.New("nothing to commit, working tree clean")
			}
			res, err := pushDiscoveredRepos(repos)
			res.Message = msg
			return res, err
		}

		if msg == "" {
			return CommitAndPushResult{}, errors.New("commit message is required")
		}
		commitMsg := appendHoxiCoAuthor(msg)
		var hashes []string
		var firstBranch, firstRemote, firstRepoDir string

		for _, r := range dirtyRepos {
			if _, err := runGit(r.Dir, "add", "-A"); err != nil {
				return CommitAndPushResult{}, err
			}
			if r.RelPrefix == "" && len(nestedPrefixes) > 0 {
				for prefix := range nestedPrefixes {
					_, _ = runGit(r.Dir, "reset", "-q", "--", prefix)
				}
			}
			if _, err := runGit(r.Dir, "commit", "-m", commitMsg); err != nil {
				return CommitAndPushResult{}, err
			}
			globalRepoDiffCache.invalidate(r.Dir)
			h, _ := runGit(r.Dir, "rev-parse", "--short", "HEAD")
			br, _ := runGit(r.Dir, "rev-parse", "--abbrev-ref", "HEAD")
			if h != "" {
				hashes = append(hashes, h)
			}
			if firstBranch == "" && br != "" {
				firstBranch = br
				firstRepoDir = r.Dir
			}
			if push {
				rem, pushErr := runGitPush(r.Dir, br)
				if pushErr != nil {
					return CommitAndPushResult{
						OK:     false,
						Commit: strings.Join(hashes, ","),
						Branch: firstBranch,
						Pushed: false,
					}, pushErr
				}
				if firstRemote == "" {
					firstRemote = rem
				}
			}
		}

		return CommitAndPushResult{
			OK:         true,
			Commit:     strings.Join(hashes, ","),
			Branch:     firstBranch,
			Remote:     firstRemote,
			Pushed:     push,
			Message:    msg,
			CompareURL: buildRemoteCompareURL(resolveGitRemoteURLFast(firstRepoDir, firstRemote), "", firstBranch),
		}, nil
	}

	porcelain, err := runGit(workDir, "status", "--porcelain")
	if err != nil {
		return CommitAndPushResult{}, fmt.Errorf("git status: %w", err)
	}

	hasChanges := strings.TrimSpace(porcelain) != ""
	msg := strings.TrimSpace(message)

	if hasChanges {
		if msg == "" {
			return CommitAndPushResult{}, errors.New("commit message is required")
		}
		commitMsg := appendHoxiCoAuthor(msg)
		if _, err := runGit(workDir, "add", "-A"); err != nil {
			return CommitAndPushResult{}, err
		}
		if _, err := runGit(workDir, "commit", "-m", commitMsg); err != nil {
			return CommitAndPushResult{}, err
		}
		globalRepoDiffCache.invalidate(workDir)
	} else if !push {
		return CommitAndPushResult{}, errors.New("nothing to commit, working tree clean")
	}

	hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")
	branch, _ := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")

	var remote string
	if push {
		r, pushErr := runGitPush(workDir, branch)
		if pushErr != nil {
			return CommitAndPushResult{
				OK:     false,
				Commit: hash,
				Branch: branch,
				Pushed: false,
			}, pushErr
		}
		remote = r
	}

	return CommitAndPushResult{
		OK:         true,
		Commit:     hash,
		Branch:     branch,
		Remote:     remote,
		Pushed:     push,
		Message:    msg,
		CompareURL: buildRemoteCompareURL(resolveGitRemoteURLFast(workDir, remote), "", branch),
	}, nil
}

func resolveGitRemoteURLFast(repoDir, preferredRemote string) string {
	if strings.TrimSpace(repoDir) == "" {
		return ""
	}
	if strings.TrimSpace(preferredRemote) == "" {
		preferredRemote = "origin"
	}
	gitDir := resolveGitDirFast(repoDir)
	commonDir := gitDir
	if gitDir != "" {
		if rawCommon, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			rel := strings.TrimSpace(string(rawCommon))
			if rel != "" {
				if filepath.IsAbs(rel) {
					commonDir = filepath.Clean(rel)
				} else {
					commonDir = filepath.Clean(filepath.Join(gitDir, rel))
				}
			}
		}
	}
	if commonDir != "" {
		if cfgBytes, err := os.ReadFile(filepath.Join(commonDir, "config")); err == nil {
			var currentRemote, fallbackURL string
			for _, rawLine := range strings.Split(string(cfgBytes), "\n") {
				line := strings.TrimSpace(rawLine)
				if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
					currentRemote = ""
					if strings.HasPrefix(line, `[remote "`) && strings.HasSuffix(line, `"]`) {
						currentRemote = line[len(`[remote "`): len(line)-2]
					}
					continue
				}
				if currentRemote != "" {
					if idx := strings.IndexByte(line, '='); idx > 0 {
						k := strings.TrimSpace(line[:idx])
						v := strings.TrimSpace(line[idx+1:])
						if strings.EqualFold(k, "url") && v != "" {
							if currentRemote == preferredRemote {
								return v
							}
							if fallbackURL == "" {
								fallbackURL = v
							}
						}
					}
				}
			}
			if fallbackURL != "" {
				return fallbackURL
			}
		}
	}
	u, _ := runGit(repoDir, "remote", "get-url", preferredRemote)
	return u
}

func parseRemoteWebBaseURL(rawRemoteURL string) (string, string) {
	raw := strings.TrimSpace(rawRemoteURL)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") ||
		strings.HasPrefix(raw, "ssh://") || strings.HasPrefix(raw, "git://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ""
		}
		host := strings.ToLower(u.Hostname())
		repoPath := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
		if host == "" || repoPath == "" {
			return "", ""
		}
		scheme := "https"
		hostWithPort := host
		if u.Scheme == "http" {
			scheme = "http"
			hostWithPort = u.Host
		} else if u.Scheme == "https" {
			hostWithPort = u.Host
		}
		return host, scheme + "://" + hostWithPort + "/" + repoPath
	}

	// SCP-style SSH URL: [user@]host:owner/repo.git
	trimmed := raw
	if atIdx := strings.IndexByte(trimmed, '@'); atIdx >= 0 {
		colonIdx := strings.IndexByte(trimmed, ':')
		if colonIdx > atIdx {
			trimmed = trimmed[atIdx+1:]
		}
	}
	colonIdx := strings.IndexByte(trimmed, ':')
	if colonIdx <= 0 {
		return "", ""
	}
	host := strings.ToLower(strings.TrimSpace(trimmed[:colonIdx]))
	repoPath := strings.Trim(strings.TrimSuffix(strings.TrimSpace(trimmed[colonIdx+1:]), ".git"), "/")
	if host == "" || repoPath == "" || strings.Contains(host, "/") {
		return "", ""
	}
	return host, "https://" + host + "/" + repoPath
}

func escapeGitRefPath(ref string) string {
	parts := strings.Split(strings.TrimSpace(ref), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func buildRemoteCompareURL(rawRemoteURL, baseBranch, branch string) string {
	branch = strings.TrimSpace(branch)
	baseBranch = strings.TrimSpace(baseBranch)
	if branch == "" || branch == "HEAD" || branch == baseBranch {
		return ""
	}
	if baseBranch == "" && (branch == "main" || branch == "master" || branch == "trunk") {
		return ""
	}
	host, webBase := parseRemoteWebBaseURL(rawRemoteURL)
	if webBase == "" {
		return ""
	}
	if strings.Contains(host, "gitlab") {
		q := "merge_request[source_branch]=" + url.QueryEscape(branch)
		if baseBranch != "" {
			q += "&merge_request[target_branch]=" + url.QueryEscape(baseBranch)
		}
		return webBase + "/-/merge_requests/new?" + q
	}
	if strings.Contains(host, "bitbucket") {
		return webBase + "/pull-requests/new?source=" + url.QueryEscape(branch)
	}
	if baseBranch != "" {
		return webBase + "/compare/" + escapeGitRefPath(baseBranch) + "..." + escapeGitRefPath(branch) + "?expand=1"
	}
	return webBase + "/compare/" + escapeGitRefPath(branch) + "?expand=1"
}

func runGitPush(workDir, branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return "", errors.New("cannot push detached HEAD; please checkout a branch first")
	}

	// Check if upstream tracking branch is already set
	if upstream, err := runGit(workDir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil && upstream != "" {
		remote := "origin"
		if idx := strings.IndexByte(upstream, '/'); idx > 0 {
			remote = upstream[:idx]
		}
		if _, err := runGitWithTimeout(workDir, gitPushTimeout, "push"); err != nil {
			return remote, err
		}
		return remote, nil
	}

	// Otherwise resolve remote (prefer "origin") and push with -u
	remotesRaw, err := runGit(workDir, "remote")
	if err != nil || strings.TrimSpace(remotesRaw) == "" {
		return "", errors.New("no git remote configured (e.g. origin)")
	}
	remote := ""
	for _, r := range strings.Split(remotesRaw, "\n") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if r == "origin" {
			remote = "origin"
			break
		}
		if remote == "" {
			remote = r
		}
	}
	if remote == "" {
		return "", errors.New("no git remote configured")
	}

	if _, err := runGitWithTimeout(workDir, gitPushTimeout, "push", "-u", remote, branch); err != nil {
		return remote, err
	}
	return remote, nil
}

func runGitWithTimeout(workDir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmdArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = nonInteractiveGitEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return strings.TrimSpace(string(output)), nil
}
