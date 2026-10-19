package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func (r SessionRecord) sandboxMode() string {
	if r.SandboxMode != "" {
		return r.SandboxMode
	}
	// Records created before mode selection was supported always used worktrees.
	if normalizeComparePath(r.WorktreePath) == normalizeComparePath(r.ProjectDir) {
		return "local"
	}
	return "worktree"
}

func normalizeSandboxMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "local"
	}
	if mode != "local" && mode != "worktree" {
		return "", errors.New("sandboxMode must be local or worktree")
	}
	return mode, nil
}

func existingDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", errors.New("session directory is required")
	}
	abs, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil {
		return "", err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("session directory does not exist: %s", abs)
	}
	return abs, nil
}

func validateSessionDirectory(rec SessionRecord) error {
	if rec.sandboxMode() == "worktree" {
		return ValidateWorktreeDir(rec.WorktreePath, rec.ProjectDir)
	}
	if rec.sandboxMode() != "local" {
		return errors.New("invalid session sandbox mode")
	}
	project, err := existingDirectory(rec.ProjectDir)
	if err != nil {
		return err
	}
	directory, err := existingDirectory(rec.WorktreePath)
	if err != nil {
		return err
	}
	if directory != project {
		return errors.New("local session directory does not match its project")
	}
	return nil
}

func (s *Service) provisionSessionDirectory(rec *SessionRecord) error {
	if rec.SandboxMode == "local" {
		rec.WorktreePath = rec.ProjectDir
		rec.Branch, rec.BaseCommit = "", ""
		if err := validateSessionDirectory(*rec); err != nil {
			return err
		}
		return s.worktrees.ForDirectory(rec.ProjectDir).UseLocalDirectory(rec.ID)
	}
	meta, err := s.worktrees.ForDirectory(rec.ProjectDir).CreateOnBranch(rec.ID, rec.RetainedBranch)
	if err != nil {
		return fmt.Errorf("failed to create isolated git worktree: %w", err)
	}
	if !meta.Isolated {
		return errors.New("cannot create an isolated Git worktree; ensure the project has at least one commit")
	}
	if err := ValidateWorktreeDir(meta.Directory, rec.ProjectDir); err != nil {
		return err
	}
	rec.WorktreePath, rec.Branch, rec.BaseCommit = meta.Directory, meta.Branch, meta.BaseCommit
	rec.RetainedBranch = ""
	return nil
}

func (s *Service) changeSessionSandbox(sessionID, mode string) (SessionInfo, error) {
	return s.changeSessionSandboxWithOptions(sessionID, mode, false, true)
}

func (s *Service) changeSessionSandboxWithOptions(sessionID, mode string, force, keepBranch bool) (SessionInfo, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	mode, err := normalizeSandboxMode(mode)
	if err != nil {
		return SessionInfo{}, err
	}
	s.mu.Lock()
	busy := s.procs[sessionID] != nil
	s.mu.Unlock()
	if busy {
		return SessionInfo{}, errors.New("cannot change sandbox mode while the session is running")
	}
	rec, ok := s.store.Get(sessionID)
	if ok && rec.Subagent {
		return SessionInfo{}, errSubagentSessionReadOnly
	}
	if !ok {
		return SessionInfo{}, errors.New("runner session not found")
	}
	if rec.sandboxMode() == mode {
		return rec.ToSessionInfo(), nil
	}
	if mode == "local" {
		// A normal mode switch is safe; the explicit discard action supplies force.
		if _, err := existingDirectory(rec.ProjectDir); err != nil {
			return SessionInfo{}, err
		}
		if keepBranch {
			branch, err := s.worktrees.ForDirectory(rec.ProjectDir).Status(sessionID)
			if err != nil {
				return SessionInfo{}, err
			}
			rec.RetainedBranch = branch.Branch
		} else {
			rec.RetainedBranch = ""
		}
	}
	original, _ := s.store.Get(sessionID)
	rec.SandboxMode = mode
	if mode == "local" {
		rec.WorktreePath, rec.Branch, rec.BaseCommit = rec.ProjectDir, "", ""
	} else {
		if err := s.provisionSessionDirectory(&rec); err != nil {
			return SessionInfo{}, err
		}
		if original.sandboxMode() == "local" && original.RetainedBranch == "" {
			_ = worktree.TransferWorkingState(rec.ProjectDir, rec.WorktreePath)
		}
	}
	updated, _, err := s.store.Update(sessionID, func(r *SessionRecord) {
		r.SandboxMode, r.WorktreePath = rec.SandboxMode, rec.WorktreePath
		r.Branch, r.BaseCommit = rec.Branch, rec.BaseCommit
		r.RetainedBranch = rec.RetainedBranch
		// Engine resume handles belong to their original working directory.
		r.ExternalID, r.ContextPending = "", len(r.Turns) > 0
		r.ContextTurnCount = len(r.Turns)
	})
	if err != nil {
		return SessionInfo{}, err
	}
	if mode == "local" {
		manager := s.worktrees.ForDirectory(rec.ProjectDir)
		if err := manager.RemoveWithOptions(sessionID, force, keepBranch); err != nil {
			// A branch deletion can fail after removal; never point back at a missing directory.
			if _, statErr := os.Stat(original.WorktreePath); statErr == nil {
				_, _, rollbackErr := s.store.Update(sessionID, func(r *SessionRecord) {
					r.SandboxMode, r.WorktreePath = original.SandboxMode, original.WorktreePath
					r.Branch, r.BaseCommit, r.RetainedBranch = original.Branch, original.BaseCommit, original.RetainedBranch
					r.ExternalID, r.ContextPending, r.ContextTurnCount = original.ExternalID, original.ContextPending, original.ContextTurnCount
				})
				return SessionInfo{}, errors.Join(err, rollbackErr)
			}
			_ = manager.UseLocalDirectory(sessionID)
			return SessionInfo{}, err
		}
		if err := manager.UseLocalDirectory(sessionID); err != nil {
			return SessionInfo{}, err
		}
	}
	info := updated.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{Type: "session.updated", Properties: map[string]interface{}{"info": info}})
	return info, nil
}

func (s *Service) guardWorktreeApply(sessionID string, apply func() error) error {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if _, known := s.store.Get(sessionID); known {
		s.mu.Lock()
		busy := s.procs[sessionID] != nil
		s.mu.Unlock()
		if busy {
			return errors.New("cannot apply worktree while the session is running")
		}
	}
	return apply()
}
