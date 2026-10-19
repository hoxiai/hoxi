package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

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

func (m *worktreeManager) primaryGitRepoDir() string {
	if repos := discoverGitRepos(m.rootDir); len(repos) > 0 {
		return repos[0].Dir
	}
	return m.rootDir
}

func (m *worktreeManager) ListBranches() (branchListResponse, error) {
	targetDir := m.primaryGitRepoDir()
	currentBranch, _ := runGit(targetDir, "rev-parse", "--abbrev-ref", "HEAD")

	// Map checked-out branches to their worktree directory
	wtByBranch := make(map[string]string)
	if wtRaw, err := runGitRaw(targetDir, "worktree", "list", "--porcelain"); err == nil {
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
		targetDir,
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
	m.ops.Lock()
	defer m.ops.Unlock()
	cleaned, err := sanitizeBranchName(branch)
	if err != nil {
		return worktreeStatus{}, err
	}
	targetDir := m.primaryGitRepoDir()
	if create {
		if _, err := runGit(targetDir, "checkout", "-b", cleaned); err != nil {
			return worktreeStatus{}, err
		}
	} else {
		if _, err := runGit(targetDir, "checkout", cleaned); err != nil {
			return worktreeStatus{}, err
		}
	}
	globalRepoDiffCache.invalidate(targetDir)
	return m.Status("__root__")
}

func (m *worktreeManager) RenameBranch(sessionID, newBranch string) (string, error) {
	cleaned, err := sanitizeBranchName(newBranch)
	if err != nil {
		return "", err
	}
	sessionID = sanitizeSessionID(sessionID)

	m.ops.Lock()
	defer m.ops.Unlock()

	workDir := m.rootDir
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()
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
		m.storeMeta(meta)
	}
	return cleaned, nil
}

func (m *worktreeManager) PruneSandboxes(excludeSessionID string) (int, error) {
	m.ops.Lock()
	defer m.ops.Unlock()
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
			cleanupSyncedEnvFiles(m.rootDir, wtDir)
			_ = os.Remove(filepath.Join(wtDir, ".opencode"))
			if _, err := runGit(m.rootDir, "worktree", "remove", "--force", wtDir); err != nil {
				detachAndRemoveDir(wtDir)
			}
			globalRepoDiffCache.invalidate(wtDir)
			if m.roots != nil && m.roots.quarantine != nil {
				m.roots.quarantine.Clear(wtDir)
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
	m.ops.Lock()
	defer m.ops.Unlock()
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

	repoDir, repoRelPath := resolveRepoForRelPath(workDir, cleaned)
	if repoDir != filepath.Clean(workDir) {
		base = "HEAD"
	}

	// Try restoring from base commit or HEAD; if untracked in base, remove the newly added file
	if _, err := runGit(repoDir, "checkout", base, "--", repoRelPath); err != nil {
		if _, headErr := runGit(repoDir, "checkout", "HEAD", "--", repoRelPath); headErr != nil {
			_ = os.Remove(filepath.Join(repoDir, repoRelPath))
		}
	}
	globalRepoDiffCache.invalidate(repoDir)
	return nil
}

