package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RelinkProject moves every conversation rooted at oldRoot to newRoot after a
// workspace folder is renamed or moved on disk. Project identity is unchanged;
// only execution paths and engine resume handles are rewritten.
func (s *Service) RelinkProject(projectID, oldRoot, newRoot string) error {
	oldRoot = filepath.Clean(strings.TrimSpace(oldRoot))
	newRoot = filepath.Clean(strings.TrimSpace(newRoot))
	if oldRoot == "" || newRoot == "" || normalizeComparePath(oldRoot) == normalizeComparePath(newRoot) {
		return nil
	}
	info, err := os.Stat(newRoot)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("relink target does not exist: %s", newRoot)
	}

	var errs []error
	for _, info := range s.store.ListProject(projectID, oldRoot) {
		if err := s.relinkSession(info.ID, oldRoot, newRoot); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", info.ID, err))
		}
	}
	return errors.Join(errs...)
}

// relinkSession moves one conversation under its turn lock. A running session
// is skipped with an error rather than having its directory pulled out from
// under the process. The previous worktree binding is restored if anything
// after ForgetSession fails, so the session never ends up unbound.
func (s *Service) relinkSession(sessionID, oldRoot, newRoot string) error {
	unlock := s.turnMu.lock(sessionID)
	defer unlock()
	s.mu.Lock()
	busy := s.procs[sessionID] != nil
	s.mu.Unlock()
	if busy {
		return errors.New("session is running; it was not relinked")
	}
	rec, ok := s.store.Meta(sessionID)
	if !ok || normalizeComparePath(rec.ProjectDir) != normalizeComparePath(oldRoot) {
		return nil
	}

	manager := s.worktrees.ForDirectory(newRoot)
	mode := rec.sandboxMode()
	newWorktree := newRoot
	branch, baseCommit := "", ""
	if mode == "worktree" {
		newWorktree = relocatedWorktreePath(rec, newRoot)
		if _, err := os.Stat(newWorktree); err == nil {
			branch, baseCommit = rec.Branch, rec.BaseCommit
		} else {
			meta, createErr := manager.CreateOnBranch(rec.ID, rec.RetainedBranch)
			if createErr != nil {
				return fmt.Errorf("relocated worktree missing and could not be recreated: %w", createErr)
			}
			if meta.Isolated {
				newWorktree, branch, baseCommit = meta.Directory, meta.Branch, meta.BaseCommit
			} else {
				// The new root is not a Git repository: an isolated
				// worktree is impossible, so the session runs locally
				// instead of pretending apply/discard are isolated.
				mode, newWorktree = "local", newRoot
			}
		}
	}

	oldBinding, oldKnown, _ := s.worktrees.BindingForSession(rec.ID)
	restoreOld := func() {
		s.worktrees.ForgetSession(rec.ID)
		if !oldKnown {
			return
		}
		if info, err := os.Stat(rec.ProjectDir); err != nil || !info.IsDir() {
			return
		}
		oldManager := s.worktrees.ForDirectory(rec.ProjectDir)
		if oldBinding.Isolated {
			_ = oldManager.RestoreSession(rec.ID, oldBinding.Directory, rec.Branch, rec.BaseCommit)
		} else {
			_ = oldManager.UseLocalDirectory(rec.ID)
		}
	}

	s.worktrees.ForgetSession(rec.ID)
	var bindErr error
	if mode == "worktree" {
		bindErr = manager.RestoreSession(rec.ID, newWorktree, branch, baseCommit)
	} else {
		bindErr = manager.UseLocalDirectory(rec.ID)
	}
	if bindErr != nil {
		restoreOld()
		return bindErr
	}
	if mode == "worktree" {
		manager.RepairWorktrees(newRoot, []string{newWorktree})
	}

	sandboxMode := ""
	if mode != rec.sandboxMode() {
		sandboxMode = mode
	}
	updated, updatedOK, err := s.store.RelinkSession(rec.ID, newRoot, newWorktree, branch, baseCommit, sandboxMode)
	if err != nil {
		restoreOld()
		return err
	}
	if updatedOK {
		s.broadcast(rec.ID, SSEEvent{
			Type:       "session.updated",
			Properties: map[string]interface{}{"info": updated.ToSessionInfo()},
		})
	}
	return nil
}

// HealSessionDirectory relinks sessionID to its project's primary root when
// the session belongs to a live project whose folder list no longer contains
// the session's ProjectDir (and the old ProjectDir is not owned by another
// project). This self-heals sessions whose workspace folder was changed while
// the session was running or before relinking ran.
func (s *Service) HealSessionDirectory(sessionID string) bool {
	if s == nil || s.store == nil || s.projectResolver == nil {
		return false
	}
	rec, ok := s.store.Meta(strings.TrimSpace(sessionID))
	if !ok {
		return false
	}
	targetRoot, shouldHeal := s.staleSessionTargetRoot(rec)
	if !shouldHeal {
		return false
	}
	return s.relinkSession(rec.ID, rec.ProjectDir, targetRoot) == nil
}

// HealProjectSessions relinks any stale sessions of projectID whose ProjectDir
// is no longer part of the project's folders.
func (s *Service) HealProjectSessions(projectID, directory string) {
	if s == nil || s.store == nil || s.projectResolver == nil {
		return
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" && strings.TrimSpace(directory) != "" {
		projectID, _ = s.projectIDForDirectory(directory)
	}
	if projectID == "" {
		return
	}
	canonicalID, live := s.canonicalProjectID(projectID)
	if !live || canonicalID == "" {
		return
	}
	roots := s.projectResolver.ProjectRoots(canonicalID)
	if len(roots) == 0 {
		return
	}
	rootSet := make(map[string]bool, len(roots))
	for _, root := range roots {
		rootSet[normalizeComparePath(root)] = true
	}
	for _, rec := range s.store.listProjectRecords(projectID, s.projectScopeDirectory(projectID, directory)) {
		if rec.Subagent || strings.TrimSpace(rec.ProjectID) == "" || strings.TrimSpace(rec.ProjectDir) == "" {
			continue
		}
		if rootSet[normalizeComparePath(rec.ProjectDir)] {
			continue
		}
		_ = s.HealSessionDirectory(rec.ID)
	}
}

func (s *Service) staleSessionTargetRoot(rec SessionRecord) (string, bool) {
	if rec.ID == "" || rec.Subagent || strings.TrimSpace(rec.ProjectID) == "" || strings.TrimSpace(rec.ProjectDir) == "" {
		return "", false
	}
	canonicalID, live := s.canonicalProjectID(rec.ProjectID)
	if !live || canonicalID == "" {
		return "", false
	}
	roots := s.projectResolver.ProjectRoots(canonicalID)
	if len(roots) == 0 {
		return "", false
	}
	normProj := normalizeComparePath(rec.ProjectDir)
	for _, root := range roots {
		if normalizeComparePath(root) == normProj {
			return "", false
		}
	}
	if otherID, ok := s.projectResolver.ProjectIDForDirectory(rec.ProjectDir); ok && otherID != "" && otherID != canonicalID {
		return "", false
	}
	primaryRoot := filepath.Clean(strings.TrimSpace(roots[0]))
	if info, err := os.Stat(primaryRoot); err != nil || !info.IsDir() {
		return "", false
	}
	if len(roots) == 1 {
		return primaryRoot, true
	}
	if _, err := os.Stat(rec.ProjectDir); errors.Is(err, os.ErrNotExist) {
		return primaryRoot, true
	}
	return "", false
}

// IsSessionInLiveProjectOutsideRoot reports whether sessionID belongs to a
// live workspace whose current folder roots do not include candidateDir.
func (s *Service) IsSessionInLiveProjectOutsideRoot(sessionID, candidateDir string) bool {
	if s == nil || s.store == nil || s.projectResolver == nil {
		return false
	}
	rec, ok := s.store.Meta(strings.TrimSpace(sessionID))
	if !ok || strings.TrimSpace(rec.ProjectID) == "" {
		return false
	}
	canonicalID, live := s.canonicalProjectID(rec.ProjectID)
	if !live || canonicalID == "" {
		return false
	}
	roots := s.projectResolver.ProjectRoots(canonicalID)
	if len(roots) == 0 {
		return false
	}
	normCandidate := normalizeComparePath(candidateDir)
	for _, root := range roots {
		if normalizeComparePath(root) == normCandidate {
			return false
		}
	}
	return true
}

func relocatedWorktreePath(rec SessionRecord, newRoot string) string {
	prefix := normalizeComparePath(rec.ProjectDir) + "/.hoxi/worktrees/"
	old := normalizeComparePath(rec.WorktreePath)
	if name := strings.TrimPrefix(old, prefix); name != old {
		if name != "" && !strings.Contains(name, "/") {
			return filepath.Join(newRoot, ".hoxi", "worktrees", filepath.FromSlash(name))
		}
	}
	return filepath.Join(newRoot, ".hoxi", "worktrees", rec.ID)
}
