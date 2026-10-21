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
		rec, ok := s.store.Get(info.ID)
		if !ok || normalizeComparePath(rec.ProjectDir) != normalizeComparePath(oldRoot) {
			continue
		}
		if err := s.relinkSession(rec, newRoot); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rec.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) relinkSession(rec SessionRecord, newRoot string) error {
	manager := s.worktrees.ForDirectory(newRoot)
	newWorktree := newRoot
	branch, baseCommit := "", ""
	if rec.sandboxMode() == "worktree" {
		newWorktree = relocatedWorktreePath(rec, newRoot)
		if _, err := os.Stat(newWorktree); err != nil {
			meta, createErr := manager.CreateOnBranch(rec.ID, rec.RetainedBranch)
			if createErr != nil {
				return fmt.Errorf("relocated worktree missing and could not be recreated: %w", createErr)
			}
			newWorktree = meta.Directory
			branch, baseCommit = meta.Branch, meta.BaseCommit
		} else {
			branch, baseCommit = rec.Branch, rec.BaseCommit
		}
	}

	s.worktrees.ForgetSession(rec.ID)
	if rec.sandboxMode() == "worktree" {
		if err := manager.RestoreSession(rec.ID, newWorktree, branch, baseCommit); err != nil {
			return err
		}
		manager.RepairWorktrees(newRoot, []string{newWorktree})
	} else if err := manager.UseLocalDirectory(rec.ID); err != nil {
		return err
	}

	if rec.sandboxMode() != "worktree" {
		branch, baseCommit = "", ""
	}
	_, _, err := s.store.RelinkSession(rec.ID, newRoot, newWorktree, branch, baseCommit)
	return err
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
