package runner

import (
	"errors"
	"strings"
)

// ResolveBrowserSession makes the inspector (Hoxi ID) and OpenCode tool (engine
// handle) share one browser without relaxing the session's worktree boundary.
// Unknown IDs remain independent, including unadopted OpenCode sessions.
func (s *Service) ResolveBrowserSession(sessionID, directory string) (string, error) {
	if rec, ok := s.store.Meta(sessionID); ok {
		return browserSessionScope(rec, directory)
	}
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
		}
		for _, engineID := range rec.OpenCodeIDs {
			if engineID == sessionID {
				// Handles from a previous worktree must not attach to the
				// browser in the session's new worktree.
				if bound := rec.OpenCodeDirectories[engineID]; bound != "" && normalizeComparePath(bound) != normalizeComparePath(directory) {
					return "", errors.New("browser engine directory does not match its binding")
				}
				return browserSessionScope(rec, directory)
			}
		}
	}
	return sessionID, nil
}

func browserSessionScope(rec SessionRecord, directory string) (string, error) {
	bound := rec.WorktreePath
	if bound == "" {
		bound = rec.ProjectDir
	}
	if bound == "" || normalizeComparePath(bound) != normalizeComparePath(directory) {
		return "", errors.New("browser directory does not match its session sandbox")
	}
	return rec.ID, nil
}

// ResolveSessionTarget maps a session ID (including subagent or OpenCode engine
// IDs) to its root Hoxi session ID, title and project directory so the macOS
// status bar menu can display readable labels and focus the right conversation.
func (s *Service) ResolveSessionTarget(sessionID string) (resolvedID, title, directory string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s == nil || s.store == nil {
		return sessionID, "", ""
	}
	rec, ok := s.lookupSessionMeta(sessionID)
	if !ok {
		return sessionID, "", ""
	}
	visited := map[string]bool{rec.ID: true}
	for depth := 0; depth < 8 && strings.TrimSpace(rec.ParentID) != ""; depth++ {
		parentID := strings.TrimSpace(rec.ParentID)
		if visited[parentID] {
			break
		}
		visited[parentID] = true
		parent, found := s.store.Meta(parentID)
		if !found {
			break
		}
		rec = parent
	}
	return rec.ID, strings.TrimSpace(rec.Title), firstNonEmptyStr(rec.ProjectDir, rec.WorktreePath)
}

func (s *Service) lookupSessionMeta(sessionID string) (SessionRecord, bool) {
	if rec, ok := s.store.Meta(sessionID); ok {
		return rec, true
	}
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
		}
		if rec.ExternalID == sessionID {
			return rec, true
		}
		for _, engineID := range rec.OpenCodeIDs {
			if engineID == sessionID {
				return rec, true
			}
		}
	}
	return SessionRecord{}, false
}
