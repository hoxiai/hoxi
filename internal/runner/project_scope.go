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

// projectCanonicalizer is optionally implemented by a ProjectResolver that
// merges projects. It maps a stored ID (possibly an alias of a merged hidden
// project) to the live project ID, reporting false for removed projects.
type projectCanonicalizer interface {
	CanonicalProjectID(projectID string) (string, bool)
}

// projectEnumerator is optionally implemented by a ProjectResolver that can
// enumerate every live project folder, so the runner can prewarm the engine per
// directory without importing workspace storage.
type projectEnumerator interface {
	AllProjectRoots() []string
}

type surfaceProjectResolver interface {
	EnsureProjectForDirectoryWithSurface(directory, surface string) (string, error)
}

// SetProjectResolver wires project attribution and backfills legacy sessions
// that predate stable project IDs. It is safe to call more than once.
func (s *Service) SetProjectResolver(resolver ProjectResolver) {
	s.projectResolver = resolver
	var canonical func(string) (string, bool)
	if c, ok := resolver.(projectCanonicalizer); ok {
		canonical = c.CanonicalProjectID
	}
	s.store.mu.Lock()
	s.store.canonicalProject = canonical
	s.store.mu.Unlock()
	s.backfillProjectIDs()
}

func (s *Service) projectIDForDirectory(directory string) (string, bool) {
	if s.projectResolver == nil || strings.TrimSpace(directory) == "" {
		return "", false
	}
	return s.projectResolver.ProjectIDForDirectory(directory)
}

func (s *Service) ensureProjectID(directory string) (string, error) {
	return s.ensureProjectIDWithSurface(directory, "")
}

func (s *Service) ensureProjectIDWithSurface(directory, surface string) (string, error) {
	if s.projectResolver == nil || strings.TrimSpace(directory) == "" {
		return "", nil
	}
	if sr, ok := s.projectResolver.(surfaceProjectResolver); ok {
		return sr.EnsureProjectForDirectoryWithSurface(directory, surface)
	}
	return s.projectResolver.EnsureProjectForDirectory(directory)
}

// backfillProjectIDs assigns every legacy session to its project exactly once
// and re-points sessions that still carry the ID of a merged hidden project.
// Sessions whose directory disappeared keep their record and are resolved
// again after the folder is relinked.
func (s *Service) backfillProjectIDs() {
	if s.projectResolver == nil {
		return
	}
	canonicalizer, _ := s.projectResolver.(projectCanonicalizer)
	for _, info := range s.store.List("") {
		rec, ok := s.store.Meta(info.ID)
		if !ok {
			continue
		}
		if current := strings.TrimSpace(rec.ProjectID); current != "" {
			if rec.Surface == "work" {
				_, _ = s.ensureProjectIDWithSurface(rec.ProjectDir, rec.Surface)
			}
			if canonicalizer == nil {
				continue
			}
			if live, ok := canonicalizer.CanonicalProjectID(current); ok && live != current {
				_ = s.store.SetProjectID(rec.ID, live)
			}
			continue
		}
		projectID, err := s.ensureProjectIDWithSurface(rec.ProjectDir, rec.Surface)
		if err != nil || projectID == "" {
			continue
		}
		_ = s.store.SetProjectID(rec.ID, projectID)
	}
}
