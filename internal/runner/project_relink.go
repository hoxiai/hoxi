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
	rec, ok := s.store.Get(sessionID)
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
	if _, _, err := s.store.RelinkSession(rec.ID, newRoot, newWorktree, branch, baseCommit, sandboxMode); err != nil {
		restoreOld()
		return err
	}
	return nil
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
