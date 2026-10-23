package runner

import "errors"

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
