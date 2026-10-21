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
// update the profile path.

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
	return filepath.Clean(resolved), nil
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

// findProjectLocked matches a directory to the workspace that owns it. Hoxi
// worktrees under <root>/.hoxi/worktrees/<session> belong to root's project;
// nested project folders stay separate.
func (s *workspaceStore) findProjectLocked(directory string) (workspaceProfile, bool) {
	target := projectComparePath(directory)
	if target == "" {
		return workspaceProfile{}, false
	}
	for _, ws := range s.workspaces {
		for _, folder := range ws.Folders {
			root := projectComparePath(folder.Path)
			if root == "" {
				continue
			}
			if target == root || strings.HasPrefix(target, root+"/.hoxi/worktrees/") {
				return ws, true
			}
		}
	}
	return workspaceProfile{}, false
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
	now := time.Now().UTC().Format(time.RFC3339)
	created := workspaceProfile{
		ID:    generateWorkspaceUUID(),
		Name:  filepath.Base(canonical),
		Color: defaultWorkspaceIdentityColor,
		Folders: []workspaceFolder{
			{ID: generateWorkspaceUUID(), Path: canonical, IsPrimary: true, Position: 0},
		},
		Hidden:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.workspaces = append(s.workspaces, created)
	s.persistLocked()
	return created.ID, nil
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
	projectID, err := s.EnsureProjectForDirectory(canonical)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projectID": projectID,
		"directory": canonical,
		"roots":     s.ProjectRoots(projectID),
	})
}

// promoteHiddenLocked turns an auto-registered project into a user-visible
// workspace without changing its ID, so sessions created before the workspace
// was saved keep their attribution.
func (s *workspaceStore) promoteHiddenLocked(hidden workspaceProfile, folders []workspaceFolderInput, input saveWorkspaceRequest) (workspaceProfile, error) {
	name := s.resolveNameLocked(input.Name, folders[0].Path, hidden.ID)
	color := normalizeWorkspaceIdentityColor(input.Color)
	if color == "" {
		color = hidden.Color
	}
	if color == "" {
		color = defaultWorkspaceIdentityColor
	}
	existingIDs := make(map[string]string, len(hidden.Folders))
	for _, folder := range hidden.Folders {
		existingIDs[projectComparePath(folder.Path)] = folder.ID
	}
	wsFolders := make([]workspaceFolder, len(folders))
	for index, folder := range folders {
		id := existingIDs[projectComparePath(folder.Path)]
		if id == "" {
			id = generateWorkspaceUUID()
		}
		wsFolders[index] = workspaceFolder{
			ID:        id,
			Path:      folder.Path,
			IsPrimary: folder.IsPrimary,
			Position:  index,
		}
	}
	updated := workspaceProfile{
		ID:        hidden.ID,
		Name:      name,
		Color:     color,
		Folders:   wsFolders,
		Hidden:    false,
		CreatedAt: hidden.CreatedAt,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for index, ws := range s.workspaces {
		if ws.ID == hidden.ID {
			s.workspaces[index] = updated
			break
		}
	}
	for _, folder := range updated.Folders {
		s.removeDismissedLocked(folder.Path)
	}
	s.removeDismissedLocked(updated.ID)
	s.persistLocked()
	return updated, nil
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

func changedFolderPaths(before, updated workspaceProfile) []folderRelink {
	var relinks []folderRelink
	for index := 0; index < len(before.Folders) && index < len(updated.Folders); index++ {
		oldPath := before.Folders[index].Path
		newPath := updated.Folders[index].Path
		if projectComparePath(oldPath) == projectComparePath(newPath) {
			continue
		}
		// Only a real move/rename relinks sessions; reordering existing
		// folders must not reassign their conversations.
		if _, err := os.Stat(oldPath); err == nil {
			continue
		}
		relinks = append(relinks, folderRelink{
			projectID: before.ID,
			oldPath:   oldPath,
			newPath:   newPath,
		})
	}
	return relinks
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
