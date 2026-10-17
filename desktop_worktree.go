package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const gitCommandTimeout = 20 * time.Second

type worktreeMeta struct {
	SessionID  string `json:"sessionID"`
	Isolated   bool   `json:"isolated"`
	Directory  string `json:"directory"`
	Branch     string `json:"branch,omitempty"`
	BaseCommit string `json:"baseCommit,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

type worktreeStatus struct {
	SessionID        string `json:"sessionID"`
	Isolated         bool   `json:"isolated"`
	Directory        string `json:"directory"`
	Branch           string `json:"branch,omitempty"`
	BaseCommit       string `json:"baseCommit,omitempty"`
	HeadCommit       string `json:"headCommit,omitempty"`
	HeadSubject      string `json:"headSubject,omitempty"`
	Dirty            bool   `json:"dirty"`
	ChangedFiles     int    `json:"changedFiles"`
	UncommittedLines int    `json:"uncommittedLines"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

type worktreeManager struct {
	mu       sync.RWMutex
	rootDir  string
	sessions map[string]worktreeMeta
}

func newWorktreeManager(rootDir string) *worktreeManager {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}
	return &worktreeManager{
		rootDir:  absRoot,
		sessions: make(map[string]worktreeMeta),
	}
}

func (m *worktreeManager) SetRootDir(dir string) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return
	}
	if info, err := os.Stat(trimmed); err != nil || !info.IsDir() {
		return
	}
	absDir, err := filepath.Abs(trimmed)
	if err != nil {
		absDir = trimmed
	}
	m.mu.Lock()
	m.rootDir = absDir
	m.mu.Unlock()
}

func (m *worktreeManager) Create(sessionID string) (worktreeMeta, error) {
	sessionID = sanitizeSessionID(sessionID)
	if sessionID == "" {
		return worktreeMeta{}, errors.New("sessionID is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.sessions[sessionID]; ok {
		if _, err := os.Stat(existing.Directory); err == nil {
			return existing, nil
		}
	}

	topLevel, err := runGit(m.rootDir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		meta := worktreeMeta{
			SessionID: sessionID,
			Isolated:  false,
			Directory: m.rootDir,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		m.sessions[sessionID] = meta
		return meta, nil
	}

	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))
	baseCommit, err := runGit(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		meta := worktreeMeta{
			SessionID: sessionID,
			Isolated:  false,
			Directory: repoRoot,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		m.sessions[sessionID] = meta
		return meta, nil
	}

	worktreesRoot := filepath.Join(repoRoot, ".hoxi", "worktrees")
	if err := os.MkdirAll(worktreesRoot, 0o755); err != nil {
		return worktreeMeta{}, fmt.Errorf("create worktrees dir: %w", err)
	}
	ensureGitExclude(repoRoot, ".hoxi/")

	worktreeDir := filepath.Join(worktreesRoot, sessionID)
	branch := "hoxi/" + sessionID

	if _, err := os.Stat(worktreeDir); os.IsNotExist(err) {
		_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", "-b", branch, worktreeDir, strings.TrimSpace(baseCommit))
		if err != nil {
			// Branch may already exist from previous run; try without -b
			_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", worktreeDir, branch)
			if err != nil {
				return worktreeMeta{}, fmt.Errorf("git worktree add: %w", err)
			}
		}
	}

	// Symlink .opencode into the worktree so plugins and memory are shared
	srcOpenCode := filepath.Join(repoRoot, ".opencode")
	dstOpenCode := filepath.Join(worktreeDir, ".opencode")
	if _, err := os.Stat(srcOpenCode); err == nil {
		if _, err := os.Lstat(dstOpenCode); os.IsNotExist(err) {
			_ = os.Symlink(srcOpenCode, dstOpenCode)
		}
	}

	meta := worktreeMeta{
		SessionID:  sessionID,
		Isolated:   true,
		Directory:  worktreeDir,
		Branch:     branch,
		BaseCommit: strings.TrimSpace(baseCommit),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	m.sessions[sessionID] = meta
	return meta, nil
}

func (m *worktreeManager) Status(sessionID string) (worktreeStatus, error) {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if sessionID != "" && sessionID != "__root__" {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				br, _ := runGit(candidate, "rev-parse", "--abbrev-ref", "HEAD")
				if br == "" {
					br = "hoxi/" + sessionID
				}
				meta = worktreeMeta{
					SessionID: sessionID,
					Isolated:  true,
					Directory: candidate,
					Branch:    br,
				}
				m.mu.Lock()
				m.sessions[sessionID] = meta
				m.mu.Unlock()
				ok = true
			}
		}
		if !ok {
			meta = worktreeMeta{
				SessionID: sessionID,
				Isolated:  false,
				Directory: m.rootDir,
			}
		}
	}

	if br, err := runGit(meta.Directory, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && br != "" {
		meta.Branch = br
	}

	porcelain, _ := runGitRaw(meta.Directory, "status", "--porcelain")
	changedFiles := 0
	var untrackedFiles []string
	for _, rawLine := range strings.Split(porcelain, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		changedFiles++
		if strings.HasPrefix(rawLine, "?? ") {
			rel := strings.TrimSpace(rawLine[3:])
			if rel != "" {
				untrackedFiles = append(untrackedFiles, rel)
			}
		}
	}

	diffTarget := "HEAD"
	if meta.Isolated && strings.TrimSpace(meta.BaseCommit) != "" {
		diffTarget = strings.TrimSpace(meta.BaseCommit)
	}
	numstat, err := runGit(meta.Directory, "diff", "--numstat", diffTarget)
	if err != nil && diffTarget != "HEAD" {
		numstat, _ = runGit(meta.Directory, "diff", "--numstat", "HEAD")
	}

	additions := 0
	deletions := 0
	diffFileSet := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if a, err := strconv.Atoi(fields[0]); err == nil {
			additions += a
		}
		if d, err := strconv.Atoi(fields[1]); err == nil {
			deletions += d
		}
		diffFileSet[fields[len(fields)-1]] = struct{}{}
	}

	for _, rel := range untrackedFiles {
		full := filepath.Join(meta.Directory, rel)
		if info, err := os.Stat(full); err == nil && !info.IsDir() && info.Size() <= 512*1024 {
			if data, err := os.ReadFile(full); err == nil && len(data) > 0 {
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

	var headCommit, headSubject string
	if logLine, err := runGit(meta.Directory, "log", "-1", "--format=%h\t%s"); err == nil && logLine != "" {
		parts := strings.SplitN(logLine, "\t", 2)
		headCommit = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			headSubject = strings.TrimSpace(parts[1])
		}
	}

	return worktreeStatus{
		SessionID:        meta.SessionID,
		Isolated:         meta.Isolated,
		Directory:        meta.Directory,
		Branch:           meta.Branch,
		BaseCommit:       meta.BaseCommit,
		HeadCommit:       headCommit,
		HeadSubject:      headSubject,
		Dirty:            changedFiles > 0 || additions > 0 || deletions > 0,
		ChangedFiles:     changedFiles,
		UncommittedLines: additions + deletions,
		Additions:        additions,
		Deletions:        deletions,
	}, nil
}

func (m *worktreeManager) Apply(sessionID, mode string) error {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			br, _ := runGit(candidate, "rev-parse", "--abbrev-ref", "HEAD")
			if br == "" {
				br = "hoxi/" + sessionID
			}
			meta = worktreeMeta{
				SessionID: sessionID,
				Isolated:  true,
				Directory: candidate,
				Branch:    br,
			}
			ok = true
		}
	}
	if !ok || !meta.Isolated {
		return errors.New("session does not have an isolated worktree")
	}

	// Commit any uncommitted changes in the worktree before generating patch or merging
	statusOut, err := runGit(meta.Directory, "status", "--porcelain")
	if err == nil && strings.TrimSpace(statusOut) != "" {
		_, _ = runGit(meta.Directory, "add", "-A")
		_, _ = runGit(meta.Directory, "commit", "-m", "chore(hoxi): snapshot before apply ("+sessionID+")")
	}

	if mode == "merge" {
		_, err := runGit(m.rootDir, "merge", "--no-ff", "-m", "Merge hoxi session "+sessionID, meta.Branch)
		return err
	}

	// Default mode: "patch" — applies changes cleanly onto main workspace without polluting commit history
	base := meta.BaseCommit
	if base == "" {
		base, _ = runGit(m.rootDir, "merge-base", "HEAD", meta.Branch)
	}
	diffPatch, err := runGitRaw(meta.Directory, "diff", "--binary", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return fmt.Errorf("generate worktree patch: %w", err)
	}
	if strings.TrimSpace(diffPatch) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", m.rootDir, "apply", "--3way", "-")
	cmd.Stdin = strings.NewReader(diffPatch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply --3way failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *worktreeManager) Remove(sessionID string, force bool) error {
	return m.RemoveWithOptions(sessionID, force, false)
}

func (m *worktreeManager) RemoveWithOptions(sessionID string, force bool, keepBranch bool) error {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.Lock()
	meta, ok := m.sessions[sessionID]
	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			br, _ := runGit(candidate, "rev-parse", "--abbrev-ref", "HEAD")
			if br == "" {
				br = "hoxi/" + sessionID
			}
			meta = worktreeMeta{
				SessionID: sessionID,
				Isolated:  true,
				Directory: candidate,
				Branch:    br,
			}
		} else {
			m.mu.Unlock()
			return nil
		}
	}
	defer m.mu.Unlock()

	if !meta.Isolated {
		delete(m.sessions, sessionID)
		return nil
	}

	if !force {
		porcelain, err := runGit(meta.Directory, "status", "--porcelain")
		if err == nil && strings.TrimSpace(porcelain) != "" {
			return errors.New("worktree has uncommitted changes; pass force=true to discard")
		}
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, meta.Directory)
	if _, err := runGit(m.rootDir, args...); err != nil {
		_, _ = runGit(m.rootDir, "worktree", "prune")
	}
	if !keepBranch && meta.Branch != "" {
		_, _ = runGit(m.rootDir, "branch", "-D", meta.Branch)
	}
	delete(m.sessions, sessionID)
	return nil
}

type gitBranchInfo struct {
	Name       string `json:"name"`
	Current    bool   `json:"current"`
	IsSandbox  bool   `json:"isSandbox"`
	Worktree   string `json:"worktree,omitempty"`
	SessionID  string `json:"sessionID,omitempty"`
	HeadCommit string `json:"headCommit,omitempty"`
	Subject    string `json:"subject,omitempty"`
}

type branchListResponse struct {
	CurrentBranch string          `json:"currentBranch"`
	Branches      []gitBranchInfo `json:"branches"`
	SandboxCount  int             `json:"sandboxCount"`
}

func (m *worktreeManager) ListBranches() (branchListResponse, error) {
	currentBranch, _ := runGit(m.rootDir, "rev-parse", "--abbrev-ref", "HEAD")

	// Map checked-out branches to their worktree directory
	wtByBranch := make(map[string]string)
	if wtRaw, err := runGitRaw(m.rootDir, "worktree", "list", "--porcelain"); err == nil {
		var curWT string
		for _, line := range strings.Split(wtRaw, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "worktree ") {
				curWT = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			} else if strings.HasPrefix(line, "branch refs/heads/") {
				br := strings.TrimSpace(strings.TrimPrefix(line, "branch refs/heads/"))
				if curWT != "" && br != "" {
					wtByBranch[br] = curWT
				}
			} else if line == "" {
				curWT = ""
			}
		}
	}

	refOut, err := runGitRaw(
		m.rootDir,
		"for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:short)\t%(objectname:short)\t%(contents:subject)",
		"refs/heads",
	)
	if err != nil {
		return branchListResponse{CurrentBranch: currentBranch, Branches: []gitBranchInfo{}}, nil
	}

	var branches []gitBranchInfo
	sandboxCount := 0
	for _, line := range strings.Split(strings.TrimSpace(refOut), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		var commit, subject string
		if len(parts) > 1 {
			commit = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			subject = strings.TrimSpace(parts[2])
		}
		wtDir := wtByBranch[name]
		isSandbox := strings.HasPrefix(name, "hoxi/") || strings.Contains(wtDir, filepath.Join(".hoxi", "worktrees"))
		var sid string
		if strings.HasPrefix(name, "hoxi/") {
			sid = strings.TrimPrefix(name, "hoxi/")
		} else if idx := strings.Index(wtDir, filepath.Join(".hoxi", "worktrees")+string(filepath.Separator)); idx >= 0 {
			sid = wtDir[idx+len(filepath.Join(".hoxi", "worktrees"))+1:]
		}
		if isSandbox {
			sandboxCount++
		}
		branches = append(branches, gitBranchInfo{
			Name:       name,
			Current:    name == currentBranch,
			IsSandbox:  isSandbox,
			Worktree:   wtDir,
			SessionID:  sid,
			HeadCommit: commit,
			Subject:    subject,
		})
	}

	return branchListResponse{
		CurrentBranch: currentBranch,
		Branches:      branches,
		SandboxCount:  sandboxCount,
	}, nil
}

func sanitizeBranchName(name string) (string, error) {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "", errors.New("branch name is required")
	}
	if strings.HasPrefix(cleaned, "-") || strings.Contains(cleaned, "..") || strings.ContainsAny(cleaned, " \t\n~^:?*[\\") {
		return "", errors.New("invalid git branch name")
	}
	return cleaned, nil
}

func (m *worktreeManager) CheckoutBranch(branch string, create bool) (worktreeStatus, error) {
	cleaned, err := sanitizeBranchName(branch)
	if err != nil {
		return worktreeStatus{}, err
	}
	if create {
		if _, err := runGit(m.rootDir, "checkout", "-b", cleaned); err != nil {
			return worktreeStatus{}, err
		}
	} else {
		if _, err := runGit(m.rootDir, "checkout", cleaned); err != nil {
			return worktreeStatus{}, err
		}
	}
	return m.Status("__root__")
}

func (m *worktreeManager) RenameBranch(sessionID, newBranch string) (string, error) {
	cleaned, err := sanitizeBranchName(newBranch)
	if err != nil {
		return "", err
	}
	sessionID = sanitizeSessionID(sessionID)

	m.mu.Lock()
	defer m.mu.Unlock()

	workDir := m.rootDir
	meta, ok := m.sessions[sessionID]
	if ok && meta.Isolated && meta.Directory != "" {
		workDir = meta.Directory
	} else if sessionID != "" && sessionID != "__root__" {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			workDir = candidate
			meta = worktreeMeta{
				SessionID: sessionID,
				Isolated:  true,
				Directory: candidate,
			}
			ok = true
		}
	}

	if _, err := runGit(workDir, "branch", "-m", cleaned); err != nil {
		return "", err
	}
	if ok {
		meta.Branch = cleaned
		m.sessions[sessionID] = meta
	}
	return cleaned, nil
}

func (m *worktreeManager) PruneSandboxes(excludeSessionID string) (int, error) {
	excludeSessionID = sanitizeSessionID(excludeSessionID)
	removed := 0

	worktreesRoot := filepath.Join(m.rootDir, ".hoxi", "worktrees")
	if entries, err := os.ReadDir(worktreesRoot); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			sid := entry.Name()
			if excludeSessionID != "" && sid == excludeSessionID {
				continue
			}
			wtDir := filepath.Join(worktreesRoot, sid)
			if _, err := runGit(m.rootDir, "worktree", "remove", "--force", wtDir); err != nil {
				_ = os.RemoveAll(wtDir)
			}
			m.mu.Lock()
			delete(m.sessions, sid)
			m.mu.Unlock()
			removed++
		}
	}
	_, _ = runGit(m.rootDir, "worktree", "prune")

	if refOut, err := runGitRaw(m.rootDir, "for-each-ref", "--format=%(refname:short)", "refs/heads/hoxi"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(refOut), "\n") {
			br := strings.TrimSpace(line)
			if br == "" {
				continue
			}
			if excludeSessionID != "" && br == "hoxi/"+excludeSessionID {
				continue
			}
			if _, err := runGit(m.rootDir, "branch", "-D", br); err == nil {
				removed++
			}
		}
	}

	return removed, nil
}

func (m *worktreeManager) RevertFile(sessionID, relPath string) error {
	sessionID = sanitizeSessionID(sessionID)
	cleaned := filepath.Clean(strings.TrimPrefix(strings.TrimSpace(relPath), "./"))
	if cleaned == "" || cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return errors.New("invalid relative file path")
	}

	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	workDir := m.rootDir
	base := "HEAD"
	if ok && meta.Isolated && meta.Directory != "" {
		workDir = meta.Directory
		if meta.BaseCommit != "" {
			base = meta.BaseCommit
		}
	} else {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			workDir = candidate
		}
	}

	// Try restoring from base commit or HEAD; if untracked in base, remove the newly added file
	if _, err := runGit(workDir, "checkout", base, "--", cleaned); err != nil {
		if _, headErr := runGit(workDir, "checkout", "HEAD", "--", cleaned); headErr != nil {
			_ = os.Remove(filepath.Join(workDir, cleaned))
		}
	}
	return nil
}

func (m *worktreeManager) CommitWorktree(sessionID, message string) (string, error) {
	sessionID = sanitizeSessionID(sessionID)
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", errors.New("commit message is required")
	}

	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	workDir := m.rootDir
	if ok && meta.Isolated && meta.Directory != "" {
		workDir = meta.Directory
	} else if sessionID != "" {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			workDir = candidate
		}
	}

	if _, err := runGit(workDir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := runGit(workDir, "commit", "-m", msg); err != nil {
		return "", err
	}
	hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")
	return hash, nil
}

func sanitizeSessionID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ensureGitExclude(repoRoot, pattern string) {
	excludeFile := filepath.Join(repoRoot, ".git", "info", "exclude")
	data, err := os.ReadFile(excludeFile)
	if err == nil && !strings.Contains(string(data), pattern) {
		_ = os.WriteFile(excludeFile, append(data, []byte("\n"+pattern+"\n")...), 0o644)
	}
}

func runGit(workDir string, args ...string) (string, error) {
	out, err := runGitRaw(workDir, args...)
	return strings.TrimSpace(out), err
}

func runGitRaw(workDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmdArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}
