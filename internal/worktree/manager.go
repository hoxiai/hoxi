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
	BaseBranch       string `json:"baseBranch,omitempty"`
	BaseCommit       string `json:"baseCommit,omitempty"`
	HeadCommit       string `json:"headCommit,omitempty"`
	HeadSubject      string `json:"headSubject,omitempty"`
	Dirty            bool   `json:"dirty"`
	ChangedFiles     int    `json:"changedFiles"`
	UncommittedLines int    `json:"uncommittedLines"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	AheadCount       int    `json:"aheadCount"`
	BehindCount      int    `json:"behindCount"`
	HasUpstream      bool   `json:"hasUpstream"`
	CompareURL       string `json:"compareURL,omitempty"`
	Health           string `json:"health,omitempty"`
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
		roots: &managerRoots{
			managers:   make(map[string]*worktreeManager),
			quarantine: newQuarantineTracker(),
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
	ensureGitExclude(repoRoot, ".hoxi/")

	worktreeDir := filepath.Join(worktreesRoot, sessionID)
	branch := "hoxi/" + sessionID
	if retainedBranch != "" {
		branch = retainedBranch
	}

	isNewWorktree := false
	if _, err := os.Stat(worktreeDir); os.IsNotExist(err) {
		isNewWorktree = true
		_, _ = runGit(repoRoot, "worktree", "prune")
		if retainedBranch != "" {
			_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", worktreeDir, branch)
			if err != nil {
				return worktreeMeta{}, fmt.Errorf("reattach retained branch: %w", err)
			}
		} else {
			_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", "-b", branch, worktreeDir, strings.TrimSpace(baseCommit))
			if err != nil {
				// Branch may already exist from previous run; try without -b
				_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", worktreeDir, branch)
				if err != nil {
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

	// Symlink .opencode into the worktree so plugins and memory are shared
	srcOpenCode := filepath.Join(repoRoot, ".opencode")
	dstOpenCode := filepath.Join(worktreeDir, ".opencode")
	if _, err := os.Stat(srcOpenCode); err == nil {
		if _, err := os.Lstat(dstOpenCode); os.IsNotExist(err) {
			_ = os.Symlink(srcOpenCode, dstOpenCode)
		}
	}

	// Sync root-level .env / .env.* files into the worktree without overwriting
	_, _ = syncWorktreeEnvFiles(repoRoot, worktreeDir)
	if isNewWorktree {
		_ = runWorktreeSetupScript(repoRoot, worktreeDir, sessionID, branch)
	}
	if m.roots != nil && m.roots.quarantine != nil {
		m.roots.quarantine.Clear(worktreeDir)
	}
	globalRepoDiffCache.invalidate(worktreeDir)

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

type repoStatusSummary struct {
	branch       string
	headCommit   string
	headSubject  string
	remoteURL    string
	changedFiles int
	additions    int
	deletions    int
	aheadCount   int
	hasUpstream  bool
	valid        bool
}

func statusSingleRepo(repo gitRepoTarget, diffTarget string, nestedPrefixes map[string]struct{}) repoStatusSummary {
	fingerprint := computeRepoStateFingerprint(repo.Dir, diffTarget)
	if cached, hit := globalRepoStatusCache.get(repo.Dir, diffTarget, fingerprint); hit {
		return cached
	}

	porcelain, porcelainErr := runGitRaw(repo.Dir, "status", "--porcelain", "-b")
	changedFiles := 0
	aheadCount := 0
	hasUpstream := false
	branch := ""
	var untrackedFiles []string

	for _, rawLine := range strings.Split(porcelain, "\n") {
		if strings.HasPrefix(rawLine, "## ") {
			if br := parseBranchFromPorcelainHeader(rawLine); br != "" {
				branch = br
			}
			aheadCount, hasUpstream = parseAheadAndUpstreamFromPorcelainHeader(rawLine)
			continue
		}
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || len(rawLine) < 4 {
			continue
		}
		rel := strings.TrimSpace(rawLine[3:])
		if repo.RelPrefix == "" && isWithinNestedRepo(rel, nestedPrefixes) {
			continue
		}
		changedFiles++
		if strings.HasPrefix(rawLine, "?? ") && len(untrackedFiles) < maxUntrackedFilesToCount {
			if rel != "" {
				untrackedFiles = append(untrackedFiles, rel)
			}
		}
	}

	if branch == "" {
		if br, err := runGit(repo.Dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && br != "" {
			branch = br
		}
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
		if repo.RelPrefix == "" && isWithinNestedRepo(f, nestedPrefixes) {
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
		if info, statErr := os.Stat(full); statErr == nil && !info.IsDir() && info.Size() <= maxUntrackedFileSize {
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

	var headCommit, headSubject string
	if logLine, logErr := runGit(repo.Dir, "log", "-1", "--format=%h\t%s"); logErr == nil && logLine != "" {
		parts := strings.SplitN(logLine, "\t", 2)
		headCommit = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			headSubject = strings.TrimSpace(parts[1])
		}
	}

	summary := repoStatusSummary{
		branch:       branch,
		headCommit:   headCommit,
		headSubject:  headSubject,
		remoteURL:    resolveGitRemoteURLFast(repo.Dir, "origin"),
		changedFiles: changedFiles,
		additions:    additions,
		deletions:    deletions,
		aheadCount:   aheadCount,
		hasUpstream:  hasUpstream,
		valid:        porcelainErr == nil || branch != "" || headCommit != "",
	}
	if summary.valid {
		globalRepoStatusCache.put(repo.Dir, diffTarget, fingerprint, summary)
	}
	return summary
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

	repos := discoverGitRepos(meta.Directory)
	nestedPrefixes := nestedRepoPrefixSet(repos)
	rootDiffTarget := "HEAD"
	if meta.Isolated && strings.TrimSpace(meta.BaseCommit) != "" {
		rootDiffTarget = strings.TrimSpace(meta.BaseCommit)
	}

	summaries := make([]repoStatusSummary, len(repos))
	if len(repos) == 1 {
		target := "HEAD"
		if repos[0].RelPrefix == "" {
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
				if r.RelPrefix == "" {
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
	hasUpstream := false
	var headCommit, headSubject, primaryRemoteURL string
	primarySet := false
	anyValid := false

	for i, s := range summaries {
		if !s.valid {
			continue
		}
		anyValid = true
		changedFiles += s.changedFiles
		additions += s.additions
		deletions += s.deletions
		aheadCount += s.aheadCount
		hasUpstream = hasUpstream || s.hasUpstream

		repoDirty := s.changedFiles > 0 || s.additions > 0 || s.deletions > 0
		if repos[i].RelPrefix == "" {
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
	behindCount := 0
	if meta.Isolated && anyValid {
		var baseAhead int
		baseBranch, behindCount, baseAhead = m.resolveBaseBranchDrift(meta)
		if aheadCount == 0 && baseAhead > 0 {
			aheadCount = baseAhead
		}
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
	}, nil
}

func (m *worktreeManager) Apply(sessionID, mode string) error {
	sessionID = sanitizeSessionID(sessionID)
	m.roots.Lock()
	guard := m.roots.applyGuard
	m.roots.Unlock()
	apply := func() error { return m.apply(sessionID, mode) }
	if guard != nil {
		return guard(sessionID, apply)
	}
	return apply()
}

func (m *worktreeManager) apply(sessionID, mode string) error {
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
	if branch, err := runGit(meta.Directory, "symbolic-ref", "--short", "HEAD"); err == nil {
		meta.Branch = branch
	}

	if err := snapshotWorktree(meta.Directory, sessionID); err != nil {
		return err
	}
	defer func() {
		globalRepoDiffCache.invalidate(meta.Directory)
		globalRepoDiffCache.invalidate(m.rootDir)
	}()

	if mode == "merge" {
		_, err := runGit(m.rootDir, "merge", "--no-ff", "-m", appendHoxiCoAuthor("Merge hoxi session "+sessionID), meta.Branch)
		return err
	}

	// Default mode: "patch" — applies changes cleanly onto main workspace without polluting commit history
	base, err := runGit(m.rootDir, "merge-base", "HEAD", meta.Branch)
	if err != nil || strings.TrimSpace(base) == "" {
		return fmt.Errorf("resolve apply base: %v", err)
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
	cmd.Env = nonInteractiveGitEnv()
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
		cleanupSyncedEnvFiles(m.rootDir, meta.Directory)
		if lstat, lErr := os.Lstat(filepath.Join(meta.Directory, ".opencode")); lErr == nil && lstat.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(filepath.Join(meta.Directory, ".opencode"))
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
	cmd.Env = nonInteractiveGitEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}
