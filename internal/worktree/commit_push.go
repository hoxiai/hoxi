package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	HoxiCoAuthorTrailer = "Co-authored-by: Hoxi AI <code@hoxi.ai>"
	gitPushTimeout      = 45 * time.Second
)

type CommitAndPushResult struct {
	OK      bool   `json:"ok"`
	Commit  string `json:"commit,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Remote  string `json:"remote,omitempty"`
	Pushed  bool   `json:"pushed"`
	Message string `json:"message,omitempty"`
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

func (m *worktreeManager) PushWorktree(sessionID string) (CommitAndPushResult, error) {
	m.ops.Lock()
	defer m.ops.Unlock()

	workDir := m.resolveWorkDir(sessionID)
	branch, _ := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")

	remote, err := runGitPush(workDir, branch)
	if err != nil {
		return CommitAndPushResult{}, err
	}
	return CommitAndPushResult{
		OK:     true,
		Commit: hash,
		Branch: branch,
		Remote: remote,
		Pushed: true,
	}, nil
}

func (m *worktreeManager) CommitAndPushWorktree(sessionID, message string, push bool) (CommitAndPushResult, error) {
	m.ops.Lock()
	defer m.ops.Unlock()

	workDir := m.resolveWorkDir(sessionID)
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
		OK:      true,
		Commit:  hash,
		Branch:  branch,
		Remote:  remote,
		Pushed:  push,
		Message: msg,
	}, nil
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
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return strings.TrimSpace(string(output)), nil
}
