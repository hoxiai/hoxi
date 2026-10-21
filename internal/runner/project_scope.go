package runner

import "strings"

// ProjectResolver maps execution directories to the stable project that owns
// them. The gateway injects it so runner never imports workspace storage and
// stays testable with a fake resolver.
type ProjectResolver interface {
	ProjectIDForDirectory(directory string) (string, bool)
	EnsureProjectForDirectory(directory string) (string, error)
	ProjectRoots(projectID string) []string
}

// SetProjectResolver wires project attribution and backfills legacy sessions
// that predate stable project IDs. It is safe to call more than once.
func (s *Service) SetProjectResolver(resolver ProjectResolver) {
	s.projectResolver = resolver
	s.backfillProjectIDs()
}

func (s *Service) projectIDForDirectory(directory string) (string, bool) {
	if s.projectResolver == nil || strings.TrimSpace(directory) == "" {
		return "", false
	}
	return s.projectResolver.ProjectIDForDirectory(directory)
}

func (s *Service) ensureProjectID(directory string) (string, error) {
	if s.projectResolver == nil || strings.TrimSpace(directory) == "" {
		return "", nil
	}
	return s.projectResolver.EnsureProjectForDirectory(directory)
}

// backfillProjectIDs assigns every legacy session to its project exactly once.
// Sessions whose directory disappeared keep their record and are resolved
// again after the folder is relinked.
func (s *Service) backfillProjectIDs() {
	if s.projectResolver == nil {
		return
	}
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok || strings.TrimSpace(rec.ProjectID) != "" {
			continue
		}
		projectID, err := s.projectResolver.EnsureProjectForDirectory(rec.ProjectDir)
		if err != nil || projectID == "" {
			continue
		}
		_ = s.store.SetProjectID(rec.ID, projectID)
	}
}
