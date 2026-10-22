package workspace

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Project identity is separate from the directory a session executes in.
// Sessions store the stable workspace profile ID, so listing and attribution
// do not depend on the folder path. Relinking a moved folder only needs to
// update the profile path, and only happens on an explicit folder replace.

func canonicalProjectDirectory(directory string) (string, error) {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return "", errors.New("project directory is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(resolved)
	if err != nil || !stat.IsDir() {
		return "", errors.New("project directory does not exist")
	}
	resolved = filepath.Clean(resolved)
	if isForbiddenProjectRoot(resolved) {
		return "", errors.New("filesystem roots and the home directory cannot be projects")
	}
	return resolved, nil
}

func projectComparePath(path string) string {
	return filepath.ToSlash(comparableFolderPath(path))
}

// hoxiWorktreeRoot returns the project root for a path inside Hoxi's own
// worktree directory. It leaves other nested paths untouched.
func hoxiWorktreeRoot(path string) string {
	marker := "/.hoxi/worktrees/"
	slash := filepath.ToSlash(path)
	index := strings.Index(slash, marker)
	if index <= 0 {
		return path
	}
	remainder := slash[index+len(marker):]
	if remainder == "" || strings.Contains(remainder, "/") {
		return path
	}
	return filepath.FromSlash(slash[:index])
}

// findProjectLocked matches a directory to the workspace that owns it. A
// profile whose primary folder is the directory always wins over a parent
// workspace that merely lists it as a secondary folder. Hoxi worktrees under
// <root>/.hoxi/worktrees/<session> belong to root's project; nested project
// folders stay separate.
func (s *workspaceStore) findProjectLocked(directory string) (workspaceProfile, bool) {
	target := projectComparePath(directory)
	if target == "" {
		return workspaceProfile{}, false
	}
	find := func(primaryOnly bool) (workspaceProfile, bool) {
		var hidden *workspaceProfile
		for index := range s.workspaces {
			ws := &s.workspaces[index]
			for _, folder := range ws.Folders {
				if primaryOnly && !folder.IsPrimary {
					continue
				}
				root := projectComparePath(folder.Path)
				if root == "" {
					continue
				}
				matches := target == root
				if !matches && !primaryOnly {
					matches = strings.HasPrefix(target, root+"/.hoxi/worktrees/")
				}
				if !matches {
					continue
				}
				if !ws.Hidden {
					return *ws, true
				}
				if hidden == nil {
					hidden = ws
				}
			}
		}
		if hidden != nil {
			return *hidden, true
		}
		return workspaceProfile{}, false
	}
	if ws, ok := find(true); ok {
		return ws, true
	}
	return find(false)
}

// ProjectIDForDirectory returns the stable project ID that owns directory.
func (s *workspaceStore) ProjectIDForDirectory(directory string) (string, bool) {
	canonical, err := canonicalProjectDirectory(directory)
	if err != nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws, ok := s.findProjectLocked(canonical)
	if !ok {
		return "", false
	}
	return ws.ID, true
}

// EnsureProjectForDirectory resolves directory to a project, registering a
// hidden project on first use. The ID stays stable for the lifetime of the
// project even if the folder is later renamed.
func (s *workspaceStore) EnsureProjectForDirectory(directory string) (string, error) {
	canonical, err := canonicalProjectDirectory(directory)
	if err != nil {
		return "", err
	}
	canonical = hoxiWorktreeRoot(canonical)
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws, ok := s.findProjectLocked(canonical); ok {
		return ws.ID, nil
	}
	prev := s.snapshotLocked()
	id, aliases := generateWorkspaceUUID(), []string(nil)
	if tomb, ok := s.tombstoneForPathLocked(canonical); ok {
		// The folder's project was removed earlier; reuse its ID so the
		// sessions created before the removal are attributed again.
		id, aliases = tomb.ID, tomb.Aliases
	}
	now := time.Now().UTC().Format(time.RFC3339)
	created := workspaceProfile{
		ID:      id,
		Aliases: append([]string(nil), aliases...),
		Name:    filepath.Base(canonical),
		Color:   defaultWorkspaceIdentityColor,
		Folders: []workspaceFolder{
			{ID: generateWorkspaceUUID(), Path: canonical, IsPrimary: true, Position: 0},
		},
		Hidden:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.workspaces = append(s.workspaces, created)
	s.clearTombstonesLocked(created)
	if err := s.commitLocked(prev); err != nil {
		return "", err
	}
	return created.ID, nil
}

// CanonicalProjectID maps a live project ID, or the ID of a hidden project
// merged into a live one, to the live profile ID. It reports false for IDs
// that no longer belong to any project.
func (s *workspaceStore) CanonicalProjectID(projectID string) (string, bool) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ws := range s.workspaces {
		if ws.ID == projectID {
			return ws.ID, true
		}
	}
	for _, ws := range s.workspaces {
		for _, alias := range ws.Aliases {
			if alias == projectID {
				return ws.ID, true
			}
		}
	}
	return "", false
}

// ProjectRoots returns the folders that belong to projectID. Sessions in any
// root include the project even when the workspace groups several folders.
func (s *workspaceStore) ProjectRoots(projectID string) []string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ws := range s.workspaces {
		if ws.ID != projectID {
			continue
		}
		roots := make([]string, 0, len(ws.Folders))
		for _, folder := range ws.Folders {
			roots = append(roots, folder.Path)
		}
		return roots
	}
	return nil
}

// handleResolveProject resolves a directory without registering it. Hidden
// projects are created when a session is created, so a read-only GET (which
// may arrive through the remote tunnel) never mutates the store.
func (s *workspaceStore) handleResolveProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	directory := r.URL.Query().Get("directory")
	canonical, err := canonicalProjectDirectory(directory)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID, _ := s.ProjectIDForDirectory(canonical)
	var roots []string
	if projectID != "" {
		roots = s.ProjectRoots(projectID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projectID": projectID,
		"directory": canonical,
		"roots":     roots,
	})
}

// SetFolderRelinkHandler lets the gateway move sessions when a workspace
// folder path changes. It is called after the profile is persisted, so the
// handler can resolve project roots without re-entering the write lock.
func (s *workspaceStore) SetFolderRelinkHandler(handler func(projectID, oldPath, newPath string) error) {
	s.mu.Lock()
	s.relinkHandler = handler
	s.mu.Unlock()
}

type folderRelink struct {
	projectID string
	oldPath   string
	newPath   string
}

func (s *workspaceStore) dispatchFolderRelinks(relinks []folderRelink) {
	if len(relinks) == 0 {
		return
	}
	s.mu.RLock()
	handler := s.relinkHandler
	s.mu.RUnlock()
	if handler == nil {
		return
	}
	for _, relink := range relinks {
		if err := handler(relink.projectID, relink.oldPath, relink.newPath); err != nil {
			log.Printf("workspace relink %s: %v", relink.projectID, err)
		}
	}
}
