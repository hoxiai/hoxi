package worktree

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

const (
	gitCommandTimeout        = 20 * time.Second
	maxUntrackedFilesToCount = 24
	maxUntrackedFileSize     = 64 * 1024
)

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
	AheadCount       int    `json:"aheadCount"`
	HasUpstream      bool   `json:"hasUpstream"`
}

type worktreeManager struct {
	mu       sync.RWMutex
	ops      sync.Mutex
	rootDir  string
	sessions map[string]worktreeMeta
	roots    *managerRoots
}

type Manager = worktreeManager

func NewManager(rootDir string) *Manager {
	return newWorktreeManager(rootDir)
}

func newWorktreeManager(rootDir string) *worktreeManager {
	var absRoot string
	if trimmed := strings.TrimSpace(rootDir); trimmed != "" {
		if resolved, err := filepath.Abs(trimmed); err == nil {
			absRoot = resolved
		} else {
			absRoot = trimmed
		}
	}
	m := &worktreeManager{
		rootDir:  absRoot,
		sessions: make(map[string]worktreeMeta),
		roots:    &managerRoots{managers: make(map[string]*worktreeManager)},
	}
	m.roots.managers[absRoot] = m
	return m
}

func (m *worktreeManager) RootDir() string {
	return m.rootDir
}

func (m *worktreeManager) Create(sessionID string) (worktreeMeta, error) {
	sessionID = sanitizeSessionID(sessionID)
	if sessionID == "" {
		return worktreeMeta{}, errors.New("sessionID is required")
	}

	m.ops.Lock()
	defer m.ops.Unlock()

	m.mu.RLock()
	existing, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if ok {
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
		m.storeMeta(meta)
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
		m.storeMeta(meta)
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
	m.storeMeta(meta)
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
				meta = worktreeMeta{
					SessionID: sessionID,
					Isolated:  true,
					Directory: candidate,
					Branch:    "hoxi/" + sessionID,
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

	// Single git status --porcelain -b call yields both branch header and file status lines
	porcelain, _ := runGitRaw(meta.Directory, "status", "--porcelain", "-b")
	changedFiles := 0
	aheadCount := 0
	hasUpstream := false
	var untrackedFiles []string
	for _, rawLine := range strings.Split(porcelain, "\n") {
		if strings.HasPrefix(rawLine, "## ") {
			if br := parseBranchFromPorcelainHeader(rawLine); br != "" {
				meta.Branch = br
			}
			aheadCount, hasUpstream = parseAheadAndUpstreamFromPorcelainHeader(rawLine)
			continue
		}
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		changedFiles++
		if strings.HasPrefix(rawLine, "?? ") && len(untrackedFiles) < maxUntrackedFilesToCount {
			rel := strings.TrimSpace(rawLine[3:])
			if rel != "" {
				untrackedFiles = append(untrackedFiles, rel)
			}
		}
	}

	if meta.Branch == "" {
		if br, err := runGit(meta.Directory, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && br != "" {
			meta.Branch = br
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
		if info, err := os.Stat(full); err == nil && !info.IsDir() && info.Size() <= maxUntrackedFileSize {
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
		AheadCount:       aheadCount,
		HasUpstream:      hasUpstream,
	}, nil
}

func (m *worktreeManager) Apply(sessionID, mode string) error {
	m.ops.Lock()
	defer m.ops.Unlock()
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

	if err := snapshotWorktree(meta.Directory, sessionID); err != nil {
		return err
	}

	if mode == "merge" {
		_, err := runGit(m.rootDir, "merge", "--no-ff", "-m", appendHoxiCoAuthor("Merge hoxi session "+sessionID), meta.Branch)
		return err
	}

	// Default mode: "patch" — applies changes cleanly onto main workspace without polluting commit history
	base := meta.BaseCommit
	if base == "" {
		resolved, err := runGit(m.rootDir, "merge-base", "HEAD", meta.Branch)
		if err != nil || strings.TrimSpace(resolved) == "" {
			if err == nil {
				err = errors.New("empty merge-base")
			}
			return fmt.Errorf("resolve apply base: %w", err)
		}
		base = resolved
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
	m.ops.Lock()
	defer m.ops.Unlock()
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
		} else {
			return nil
		}
	}
	if !meta.Isolated {
		m.mu.Lock()
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		return nil
	}

	if !force {
		porcelain, err := runGit(meta.Directory, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("read worktree status: %w", err)
		}
		if strings.TrimSpace(porcelain) != "" {
			return errors.New("worktree has uncommitted changes; pass force=true to discard")
		}
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, meta.Directory)
	if _, err := runGit(m.rootDir, args...); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	if !keepBranch && meta.Branch != "" {
		if _, err := runGit(m.rootDir, "branch", "-D", meta.Branch); err != nil {
			m.mu.Lock()
			delete(m.sessions, sessionID)
			m.mu.Unlock()
			return fmt.Errorf("delete worktree branch: %w", err)
		}
	}
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	return nil
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
