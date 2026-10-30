package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	gitCommandTimeout        = 20 * time.Second
	maxUntrackedFilesToCount = 24
	maxUntrackedFileSize     = 64 * 1024
)

type worktreeMeta struct {
	SessionID     string `json:"sessionID"`
	Isolated      bool   `json:"isolated"`
	Directory     string `json:"directory"`
	Branch        string `json:"branch,omitempty"`
	BaseCommit    string `json:"baseCommit,omitempty"`
	CreatedAt     string `json:"createdAt"`
	SetupScript   string `json:"setupScript,omitempty"`
	SetupStatus   string `json:"setupStatus,omitempty"`
	SetupError    string `json:"setupError,omitempty"`
	AllocatedPort int    `json:"allocatedPort,omitempty"`
}

type worktreeStatus struct {
	SessionID        string               `json:"sessionID"`
	Isolated         bool                 `json:"isolated"`
	Directory        string               `json:"directory"`
	Branch           string               `json:"branch,omitempty"`
	BaseBranch       string               `json:"baseBranch,omitempty"`
	BaseCommit       string               `json:"baseCommit,omitempty"`
	HeadCommit       string               `json:"headCommit,omitempty"`
	HeadSubject      string               `json:"headSubject,omitempty"`
	Dirty            bool                 `json:"dirty"`
	ChangedFiles     int                  `json:"changedFiles"`
	UncommittedLines int                  `json:"uncommittedLines"`
	Additions        int                  `json:"additions"`
	Deletions        int                  `json:"deletions"`
	AheadCount       int                  `json:"aheadCount"`
	BehindCount      int                  `json:"behindCount"`
	HasUpstream      bool                 `json:"hasUpstream"`
	CompareURL       string               `json:"compareURL,omitempty"`
	Health           string               `json:"health,omitempty"`
	SetupScript      string               `json:"setupScript,omitempty"`
	SetupStatus      string               `json:"setupStatus,omitempty"`
	SetupError       string               `json:"setupError,omitempty"`
	AllocatedPort    int                  `json:"allocatedPort,omitempty"`
	RunStatus        string               `json:"runStatus,omitempty"`
	PreviewURL       string               `json:"previewURL,omitempty"`
	Repos            []worktreeRepoStatus `json:"repos,omitempty"`
}

type worktreeManager struct {
	mu       sync.RWMutex
	ops      sync.Mutex
	rootDir  string
	sessions map[string]worktreeMeta
	roots    *managerRoots
	// llm and recordAudit back the AI review helpers (audit, commit message).
	llm         llm.Client
	recordAudit AuditRecorder
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
		roots: &managerRoots{
			managers:   make(map[string]*worktreeManager),
			quarantine: newQuarantineTracker(),
			verify:     newVerifyTracker(),
		},
	}
	m.roots.managers[absRoot] = m
	return m
}

func (m *worktreeManager) RootDir() string {
	return m.rootDir
}

func (m *worktreeManager) Create(sessionID string) (worktreeMeta, error) {
	return m.CreateOnBranch(sessionID, "")
}

// CreateOnBranch reattaches a retained branch when returning from local mode.
func (m *worktreeManager) CreateOnBranch(sessionID, retainedBranch string) (worktreeMeta, error) {
	sessionID = sanitizeSessionID(sessionID)
	if sessionID == "" {
		return worktreeMeta{}, errors.New("sessionID is required")
	}

	m.ops.Lock()
	defer m.ops.Unlock()

	m.mu.RLock()
	existing, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if ok && existing.Isolated {
		if _, err := os.Stat(existing.Directory); err == nil {
			return existing, nil
		}
		if retainedBranch == "" && strings.TrimSpace(existing.Branch) != "" {
			retainedBranch = strings.TrimSpace(existing.Branch)
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
	// A folder nested inside a repository is its own project here. Checking out
	// the parent repository would put the session in another workspace and let
	// it edit files outside the folder the user opened, so it stays shared.
	if !sameRootPath(topLevel, m.rootDir) {
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
	markSpotlightNoIndex(worktreesRoot)
	ensureGitExclude(repoRoot, ".hoxi/", ".claude/")

	worktreeDir := filepath.Join(worktreesRoot, sessionID)
	branch := "hoxi/" + sessionID
	if retainedBranch != "" {
		branch = retainedBranch
	}

	isNewWorktree := false
	if _, err := os.Stat(worktreeDir); os.IsNotExist(err) {
		isNewWorktree = true
		_, _ = runGit(repoRoot, "worktree", "prune")
		attached := false
		if retainedBranch != "" {
			if _, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", worktreeDir, branch); err == nil {
				attached = true
			}
		}
		if !attached {
			branch = "hoxi/" + sessionID
			if cur, _ := runGit(repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(cur) == branch {
				branch = "hoxi/" + sessionID + "-wt"
			}
			if _, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", "-b", branch, worktreeDir, strings.TrimSpace(baseCommit)); err != nil {
				if _, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", worktreeDir, branch); err != nil {
					return worktreeMeta{}, fmt.Errorf("git worktree add: %w", err)
				}
			}
		}
	}
	if actual, err := runGit(worktreeDir, "symbolic-ref", "--short", "HEAD"); err == nil {
		branch = actual
	}

	// Reused branches may predate commits added to the project while detached.
	// Their diff baseline must be their common ancestor, never the new project HEAD.
	worktreeHead, err := runGit(worktreeDir, "rev-parse", "HEAD")
	if err != nil {
		return worktreeMeta{}, err
	}
	baseCommit, err = runGit(repoRoot, "merge-base", "HEAD", worktreeHead)
	if err != nil {
		return worktreeMeta{}, fmt.Errorf("resolve worktree base: %w", err)
	}

	// Share an existing project .opencode (plugins/skills) with the worktree.
	// Hoxi never creates .opencode itself: it only links one the user or this
	// repository already tracks. Memory lives in the project root .hoxi/memory.
	srcOpenCode := filepath.Join(repoRoot, ".opencode")
	dstOpenCode := filepath.Join(worktreeDir, ".opencode")
	if _, err := os.Stat(srcOpenCode); err == nil {
		if _, err := os.Lstat(dstOpenCode); os.IsNotExist(err) {
			_ = os.Symlink(srcOpenCode, dstOpenCode)
		}
	}
	if lstat, err := os.Lstat(dstOpenCode); err == nil && lstat.Mode()&os.ModeSymlink != 0 {
		excludeSharedOpenCodeLink(repoRoot, worktreeDir)
	}

	// Sync root-level .env / .env.* files into the worktree without overwriting
	_, _ = syncWorktreeEnvFiles(repoRoot, worktreeDir)
	allocatedPort := allocateWorktreePort(sessionID)
	var setupRes worktreeSetupResult
	if m.roots != nil && m.roots.verify != nil {
		ent := m.roots.verify.entryFor(sessionID, worktreeDir)
		ent.mu.Lock()
		if ent.allocatedPort > 0 {
			allocatedPort = ent.allocatedPort
		} else {
			ent.allocatedPort = allocatedPort
		}
		ent.mu.Unlock()
	}
	if isNewWorktree {
		setupRes = executeWorktreeSetupScript(repoRoot, worktreeDir, sessionID, branch, allocatedPort)
		if m.roots != nil && m.roots.verify != nil {
			ent := m.roots.verify.entryFor(sessionID, worktreeDir)
			ent.mu.Lock()
			ent.setupScript = setupRes.Script
			ent.setupStatus = setupRes.Status
			ent.setupOutput = setupRes.Output
			ent.setupError = setupRes.Error
			ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
			ent.mu.Unlock()
		}
	}
	if m.roots != nil && m.roots.quarantine != nil {
		m.roots.quarantine.Clear(worktreeDir)
	}
	globalRepoDiffCache.invalidate(worktreeDir)

	meta := worktreeMeta{
		SessionID:     sessionID,
		Isolated:      true,
		Directory:     worktreeDir,
		Branch:        branch,
		BaseCommit:    strings.TrimSpace(baseCommit),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		SetupScript:   setupRes.Script,
		SetupStatus:   setupRes.Status,
		SetupError:    setupRes.Error,
		AllocatedPort: allocatedPort,
	}
	m.storeMeta(meta)
	return meta, nil
}

func (m *worktreeManager) Status(sessionID string) (worktreeStatus, error) {
	return m.StatusWithExtraDirs(sessionID, nil)
}

func (m *worktreeManager) StatusWithExtraDirs(sessionID string, extraDirs []string) (worktreeStatus, error) {
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

	health := classifyWorktreeHealth(m.rootDir, meta)
	if meta.Isolated && health == HealthAbsentRestorable {
		return worktreeStatus{
			SessionID:  meta.SessionID,
			Isolated:   meta.Isolated,
			Directory:  meta.Directory,
			Branch:     meta.Branch,
			BaseCommit: meta.BaseCommit,
			Health:     HealthAbsentRestorable,
		}, nil
	}
	if meta.Isolated && m.roots != nil && m.roots.quarantine != nil && m.roots.quarantine.Blocked(meta.Directory) {
		return worktreeStatus{
			SessionID:  meta.SessionID,
			Isolated:   meta.Isolated,
			Directory:  meta.Directory,
			Branch:     meta.Branch,
			BaseCommit: meta.BaseCommit,
			Health:     HealthQuarantined,
		}, nil
	}

	var repos []gitRepoTarget
	if !meta.Isolated && len(extraDirs) > 0 {
		repos = discoverWorkspaceGitRepos(meta.Directory, extraDirs)
	} else {
		repos = discoverGitRepos(meta.Directory)
	}
	nestedPrefixes := nestedRepoPrefixSet(repos)
	rootDiffTarget := "HEAD"
	if meta.Isolated && strings.TrimSpace(meta.BaseCommit) != "" {
		rootDiffTarget = strings.TrimSpace(meta.BaseCommit)
	}

	summaries := make([]repoStatusSummary, len(repos))
	if len(repos) == 1 {
		target := "HEAD"
		if !repos[0].External && repos[0].RelPrefix == "" {
			target = rootDiffTarget
		}
		summaries[0] = statusSingleRepo(repos[0], target, nestedPrefixes)
	} else if len(repos) > 1 {
		sem := make(chan struct{}, maxConcurrentGitRepos)
		var wg sync.WaitGroup
		for i, repo := range repos {
			wg.Add(1)
			go func(idx int, r gitRepoTarget) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				target := "HEAD"
				if !r.External && r.RelPrefix == "" {
					target = rootDiffTarget
				}
				summaries[idx] = statusSingleRepo(r, target, nestedPrefixes)
			}(i, repo)
		}
		wg.Wait()
	}

	changedFiles := 0
	additions := 0
	deletions := 0
	aheadCount := 0
	behindCount := 0
	hasUpstream := false
	var headCommit, headSubject, primaryRemoteURL string
	primarySet := false
	anyValid := false
	repoStatuses := make([]worktreeRepoStatus, 0, len(repos))

	for i, s := range summaries {
		if !s.valid {
			continue
		}
		anyValid = true
		changedFiles += s.changedFiles
		additions += s.additions
		deletions += s.deletions
		aheadCount += s.aheadCount
		behindCount += s.behindCount
		hasUpstream = hasUpstream || s.hasUpstream

		repoDirty := s.changedFiles > 0 || s.additions > 0 || s.deletions > 0
		repoStatuses = append(repoStatuses, worktreeRepoStatus{
			Repo:         repos[i].RelPrefix,
			Name:         repoDisplayName(repos[i]),
			Directory:    repos[i].Dir,
			Branch:       s.branch,
			HeadCommit:   s.headCommit,
			HeadSubject:  s.headSubject,
			Dirty:        repoDirty,
			ChangedFiles: s.changedFiles,
			Additions:    s.additions,
			Deletions:    s.deletions,
			AheadCount:   s.aheadCount,
			BehindCount:  s.behindCount,
			HasUpstream:  s.hasUpstream,
			CompareURL:   buildRemoteCompareURL(s.remoteURL, "", s.branch),
		})

		if !repos[i].External && repos[i].RelPrefix == "" {
			if s.branch != "" {
				meta.Branch = s.branch
			}
			headCommit = s.headCommit
			headSubject = s.headSubject
			primaryRemoteURL = s.remoteURL
			primarySet = true
		} else if !primarySet && (repoDirty || meta.Branch == "") {
			if s.branch != "" {
				meta.Branch = s.branch
			}
			headCommit = s.headCommit
			headSubject = s.headSubject
			primaryRemoteURL = s.remoteURL
			if repoDirty {
				primarySet = true
			}
		}
	}

	if meta.Isolated && m.roots != nil && m.roots.quarantine != nil {
		if anyValid {
			m.roots.quarantine.Clear(meta.Directory)
		} else if m.roots.quarantine.RecordFailure(meta.Directory) {
			health = HealthQuarantined
		}
	}

	var baseBranch string
	if meta.Isolated && anyValid {
		var baseAhead, baseBehind int
		baseBranch, baseBehind, baseAhead = m.resolveBaseBranchDrift(meta)
		if baseBehind > behindCount {
			behindCount = baseBehind
		}
		if aheadCount == 0 && baseAhead > 0 {
			aheadCount = baseAhead
		}
		if len(repoStatuses) > 0 && repoStatuses[0].Repo == "" {
			if repoStatuses[0].AheadCount == 0 && baseAhead > 0 {
				repoStatuses[0].AheadCount = baseAhead
			}
			if baseBehind > repoStatuses[0].BehindCount {
				repoStatuses[0].BehindCount = baseBehind
			}
			if repoStatuses[0].CompareURL == "" {
				repoStatuses[0].CompareURL = buildRemoteCompareURL(primaryRemoteURL, baseBranch, meta.Branch)
			}
		}
	}

	setupScript := meta.SetupScript
	setupStatus := meta.SetupStatus
	setupError := meta.SetupError
	allocatedPort := meta.AllocatedPort
	var runStatus, previewURL string
	if m.roots != nil && m.roots.verify != nil {
		vState := m.roots.verify.entryFor(meta.SessionID, meta.Directory).snapshot(WorktreeVerifyConfig{}, resolveWorktreeSetupScript(m.rootDir))
		if vState.SetupScript != "" {
			setupScript = vState.SetupScript
		}
		if vState.SetupStatus != "" && vState.SetupStatus != "none" {
			setupStatus = vState.SetupStatus
			setupError = vState.SetupError
		}
		if vState.AllocatedPort > 0 {
			allocatedPort = vState.AllocatedPort
		}
		if vState.RunStatus != "idle" {
			runStatus = vState.RunStatus
		}
		previewURL = vState.PreviewURL
	}

	return worktreeStatus{
		SessionID:        meta.SessionID,
		Isolated:         meta.Isolated,
		Directory:        meta.Directory,
		Branch:           meta.Branch,
		BaseBranch:       baseBranch,
		BaseCommit:       meta.BaseCommit,
		HeadCommit:       headCommit,
		HeadSubject:      headSubject,
		Dirty:            changedFiles > 0 || additions > 0 || deletions > 0,
		ChangedFiles:     changedFiles,
		UncommittedLines: additions + deletions,
		Additions:        additions,
		Deletions:        deletions,
		AheadCount:       aheadCount,
		BehindCount:      behindCount,
		HasUpstream:      hasUpstream,
		CompareURL:       buildRemoteCompareURL(primaryRemoteURL, baseBranch, meta.Branch),
		Health:           health,
		SetupScript:      setupScript,
		SetupStatus:      setupStatus,
		SetupError:       setupError,
		AllocatedPort:    allocatedPort,
		RunStatus:        runStatus,
		PreviewURL:       previewURL,
		Repos:            repoStatuses,
	}, nil
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

	if m.roots != nil && m.roots.verify != nil {
		m.roots.verify.StopDirectory(meta.Directory)
	}
	if !force {
		cleanupSyncedEnvFiles(m.rootDir, meta.Directory)
		if lstat, lErr := os.Lstat(filepath.Join(meta.Directory, ".opencode")); lErr == nil && lstat.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(filepath.Join(meta.Directory, ".opencode"))
		}
		porcelain, err := runGit(meta.Directory, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("read worktree status: %w", err)
		}
		if strings.TrimSpace(porcelain) != "" {
			syncWorktreeEnvFiles(m.rootDir, meta.Directory)
			return errors.New("worktree has uncommitted changes; pass force=true to discard")
		}
	}

	args := []string{"worktree", "remove"}
	if branch, err := runGit(meta.Directory, "symbolic-ref", "--short", "HEAD"); err == nil {
		meta.Branch = branch
	}
	if force {
		args = append(args, "--force")
	}
	args = append(args, meta.Directory)
	if _, err := runGit(m.rootDir, args...); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	detachAndRemoveDir(meta.Directory)
	if m.roots != nil && m.roots.quarantine != nil {
		m.roots.quarantine.Clear(meta.Directory)
	}
	globalRepoDiffCache.invalidate(meta.Directory)

	if !keepBranch && meta.Branch != "" {
		if _, refErr := runGit(m.rootDir, "show-ref", "--verify", "--quiet", "refs/heads/"+meta.Branch); refErr == nil {
			_, _ = runGit(m.rootDir, "branch", "-D", meta.Branch)
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

// EnsureGitExclude appends patterns to <repo>/.git/info/exclude without
// touching tracked .gitignore files. It is a no-op outside a Git repository so
// Hoxi never creates a .git directory in a plain folder.
func EnsureGitExclude(repoRoot string, patterns ...string) {
	ensureGitExclude(repoRoot, patterns...)
}

// excludeSharedOpenCodeLink hides the worktree's .opencode symlink from Git.
// Left visible, `git add -A` commits it as a symlink, and merging that branch
// fails whenever the project keeps an untracked .opencode/ directory ("would
// lose untracked files"). info/exclude is shared by every worktree, so the
// pattern pair ignores the root .opencode entry while re-including it when it
// is a real directory: Git sees symlinks as files, so only the link is hidden
// and the project's own .opencode/ stays visible in its status.
func excludeSharedOpenCodeLink(repoRoot, worktreeDir string) {
	if gitIgnoresPath(worktreeDir, ".opencode") {
		return
	}
	patterns := []string{"/.opencode"}
	// Never re-include a project .opencode/ the user already ignores.
	if !gitIgnoresPath(repoRoot, ".opencode") {
		patterns = append(patterns, "!/.opencode/")
	}
	ensureGitExclude(repoRoot, patterns...)
}

// gitIgnoresPath reports whether rel (relative to dir) matches an ignore rule.
func gitIgnoresPath(dir, rel string) bool {
	_, err := runGit(dir, "check-ignore", "-q", "--", rel)
	return err == nil
}

func ensureGitExclude(repoRoot string, patterns ...string) {
	if len(patterns) == 0 {
		return
	}
	gitPath := filepath.Join(repoRoot, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		return
	}
	excludeFile := filepath.Join(repoRoot, ".git", "info", "exclude")
	if info, err := os.Stat(filepath.Join(repoRoot, ".git")); err == nil && !info.IsDir() {
		if relOrAbs, gErr := runGit(repoRoot, "rev-parse", "--git-path", "info/exclude"); gErr == nil && strings.TrimSpace(relOrAbs) != "" {
			p := strings.TrimSpace(relOrAbs)
			if !filepath.IsAbs(p) {
				p = filepath.Join(repoRoot, p)
			}
			excludeFile = filepath.Clean(p)
		}
	}
	_ = os.MkdirAll(filepath.Dir(excludeFile), 0o755)
	data, _ := os.ReadFile(excludeFile)
	content := string(data)
	existing := make(map[string]bool)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		existing[line] = true
		existing[strings.TrimSuffix(line, "/")] = true
	}
	var toAppend strings.Builder
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		norm := strings.TrimSuffix(pattern, "/")
		if existing[pattern] || existing[norm] {
			continue
		}
		if strings.TrimPrefix(norm, "/") == ".claude" && len(trackedPathsUnder(repoRoot, ".claude")) > 0 {
			continue
		}
		if len(content) > 0 && !strings.HasSuffix(content, "\n") && toAppend.Len() == 0 {
			toAppend.WriteByte('\n')
		}
		toAppend.WriteString(pattern)
		toAppend.WriteByte('\n')
		existing[pattern] = true
		existing[norm] = true
	}
	if toAppend.Len() > 0 {
		_ = os.WriteFile(excludeFile, append(data, []byte(toAppend.String())...), 0o644)
	}
}

func runGit(workDir string, args ...string) (string, error) {
	out, err := runGitRaw(workDir, args...)
	return strings.TrimSpace(out), err
}

func runGitRaw(workDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmdArgs := append([]string{"-c", "core.autocrlf=false", "-C", workDir}, args...)
	cmd := subproc.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = nonInteractiveGitEnv()
	stdout := &cappedGitOutputBuffer{limit: maxGitCommandOutputBytes}
	stderr := &cappedGitOutputBuffer{limit: maxGitCommandOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%s: %w", errText, err)
	}
	return stdout.String(), nil
}
