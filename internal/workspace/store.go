package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type workspaceStore struct {
	mu          sync.RWMutex
	storagePath string
	workspaces  []workspaceProfile
	dismissed   []string
}

type Store = workspaceStore

func NewStore(rootDir string) *Store {
	return newWorkspaceStore(rootDir)
}

func (s *workspaceStore) RegisterRoutes(mux *http.ServeMux) {
	s.registerRoutes(mux)
}

func newWorkspaceStore(rootDir string) *workspaceStore {
	storagePath := userHoxiWorkspacesPath()
	if storagePath == "" && strings.TrimSpace(rootDir) != "" {
		storagePath = filepath.Join(strings.TrimSpace(rootDir), ".hoxi", "workspaces.json")
	}
	s := &workspaceStore{
		storagePath: storagePath,
		workspaces:  []workspaceProfile{},
		dismissed:   []string{},
	}
	s.loadFromDisk(rootDir)
	return s
}

func (s *workspaceStore) loadFromDisk(seedRootDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := false
	if s.storagePath != "" {
		if data, err := os.ReadFile(s.storagePath); err == nil {
			var parsed workspaceStoreFile
			if err := json.Unmarshal(data, &parsed); err == nil {
				if parsed.Workspaces != nil {
					s.workspaces = parsed.Workspaces
				}
				if parsed.Dismissed != nil {
					s.dismissed = parsed.Dismissed
				}
				loaded = true
			}
		}
	}

	if !loaded && strings.TrimSpace(seedRootDir) != "" {
		if ws, err := s.buildSeededWorkspace(seedRootDir); err == nil {
			s.workspaces = []workspaceProfile{ws}
			s.persistLocked()
		}
	}
}

func (s *workspaceStore) buildSeededWorkspace(rootDir string) (workspaceProfile, error) {
	folders, err := validateWorkspaceFolders([]workspaceFolderInput{{Path: rootDir, IsPrimary: true}})
	if err != nil || len(folders) == 0 {
		return workspaceProfile{}, errors.New("invalid seed rootDir")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return workspaceProfile{
		ID:    generateWorkspaceUUID(),
		Name:  filepath.Base(folders[0].Path),
		Color: defaultWorkspaceIdentityColor,
		Folders: []workspaceFolder{
			{ID: generateWorkspaceUUID(), Path: folders[0].Path, IsPrimary: true, Position: 0},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *workspaceStore) persistLocked() {
	if s.storagePath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.storagePath), 0o755)
	payload := workspaceStoreFile{
		Workspaces: s.workspaces,
		Dismissed:  s.dismissed,
	}
	if raw, err := json.MarshalIndent(payload, "", "  "); err == nil {
		_ = os.WriteFile(s.storagePath, append(raw, '\n'), 0o644)
	}
}

func (s *workspaceStore) addDismissedLocked(target string) {
	clean := strings.TrimSpace(target)
	if clean == "" {
		return
	}
	comp := comparableFolderPath(clean)
	for _, d := range s.dismissed {
		if d == clean || comparableFolderPath(d) == comp {
			return
		}
	}
	s.dismissed = append(s.dismissed, clean)
}

func (s *workspaceStore) removeDismissedLocked(target string) {
	clean := strings.TrimSpace(target)
	if clean == "" {
		return
	}
	comp := comparableFolderPath(clean)
	next := make([]string, 0, len(s.dismissed))
	for _, d := range s.dismissed {
		if d != clean && comparableFolderPath(d) != comp {
			next = append(next, d)
		}
	}
	s.dismissed = next
}

func validateWorkspaceFolders(inputs []workspaceFolderInput) ([]workspaceFolderInput, error) {
	if len(inputs) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	home := comparableFolderPath(canonicalHomeDir())
	canonical := make([]workspaceFolderInput, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))

	for _, item := range inputs {
		trimmed := strings.TrimSpace(item.Path)
		if trimmed == "" {
			continue
		}
		resolved := filepath.Clean(trimmed)
		if realPath, err := filepath.EvalSymlinks(resolved); err == nil && realPath != "" {
			resolved = filepath.Clean(realPath)
		}
		stat, err := os.Stat(resolved)
		if err != nil || !stat.IsDir() {
			continue
		}
		comp := comparableFolderPath(resolved)
		if _, ok := seen[comp]; ok {
			continue
		}
		seen[comp] = struct{}{}
		if comp == home || comp == "/" || comp == "\\" || filepath.Dir(resolved) == resolved {
			return nil, errors.New("Filesystem roots and the home directory cannot be workspace folders.")
		}
		canonical = append(canonical, workspaceFolderInput{Path: resolved, IsPrimary: item.IsPrimary})
	}

	if len(canonical) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	var primary workspaceFolderInput
	others := make([]workspaceFolderInput, 0, len(canonical)-1)
	for _, f := range canonical {
		if f.IsPrimary && primary.Path == "" {
			primary = workspaceFolderInput{Path: f.Path, IsPrimary: true}
		} else {
			others = append(others, workspaceFolderInput{Path: f.Path, IsPrimary: false})
		}
	}
	if primary.Path == "" && len(others) > 0 {
		primary = workspaceFolderInput{Path: others[0].Path, IsPrimary: true}
		others = others[1:]
	}
	return append([]workspaceFolderInput{primary}, others...), nil
}

func (s *workspaceStore) resolveNameLocked(rawName, primaryPath, excludeID string) string {
	candidate := strings.TrimSpace(rawName)
	if candidate == "" {
		candidate = filepath.Base(primaryPath)
	}
	if candidate == "" || candidate == "." || candidate == "/" {
		candidate = "Workspace"
	}
	occupied := make(map[string]struct{}, len(s.workspaces))
	for _, ws := range s.workspaces {
		if ws.ID != excludeID {
			occupied[strings.ToLower(ws.Name)] = struct{}{}
		}
	}
	if _, ok := occupied[strings.ToLower(candidate)]; !ok {
		return candidate
	}
	for i := 2; i < 1000; i++ {
		gen := fmt.Sprintf("%s (%d)", candidate, i)
		if _, ok := occupied[strings.ToLower(gen)]; !ok {
			return gen
		}
	}
	return candidate
}

func (s *workspaceStore) List() []workspaceProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]workspaceProfile, len(s.workspaces))
	copy(out, s.workspaces)
	return out
}

func (s *workspaceStore) ListDismissed() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.dismissed))
	copy(out, s.dismissed)
	return out
}

func (s *workspaceStore) Create(input saveWorkspaceRequest) (workspaceProfile, error) {
	folders, err := validateWorkspaceFolders(input.Folders)
	if err != nil {
		return workspaceProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	primaryPath := folders[0].Path
	name := s.resolveNameLocked(input.Name, primaryPath, "")
	color := normalizeWorkspaceIdentityColor(input.Color)
	if color == "" {
		color = defaultWorkspaceIdentityColor
	}
	now := time.Now().UTC().Format(time.RFC3339)
	wsFolders := make([]workspaceFolder, len(folders))
	for idx, f := range folders {
		wsFolders[idx] = workspaceFolder{
			ID:        generateWorkspaceUUID(),
			Path:      f.Path,
			IsPrimary: f.IsPrimary,
			Position:  idx,
		}
	}
	created := workspaceProfile{
		ID:        generateWorkspaceUUID(),
		Name:      name,
		Color:     color,
		Folders:   wsFolders,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.workspaces = append(s.workspaces, created)
	for _, f := range created.Folders {
		s.removeDismissedLocked(f.Path)
	}
	s.removeDismissedLocked(created.ID)
	s.persistLocked()
	return created, nil
}

func (s *workspaceStore) Update(id string, input saveWorkspaceRequest) (workspaceProfile, error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return workspaceProfile{}, errors.New("workspace ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	targetIdx := -1
	for i, ws := range s.workspaces {
		if ws.ID == cleanID {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		compTarget := comparableFolderPath(cleanID)
		for i, ws := range s.workspaces {
			for _, f := range ws.Folders {
				if comparableFolderPath(f.Path) == compTarget {
					targetIdx = i
					break
				}
			}
			if targetIdx != -1 {
				break
			}
		}
	}
	if targetIdx == -1 {
		folders, err := validateWorkspaceFolders(input.Folders)
		if err != nil {
			return workspaceProfile{}, err
		}
		primaryPath := folders[0].Path
		name := s.resolveNameLocked(input.Name, primaryPath, "")
		color := normalizeWorkspaceIdentityColor(input.Color)
		if color == "" {
			color = defaultWorkspaceIdentityColor
		}
		now := time.Now().UTC().Format(time.RFC3339)
		wsFolders := make([]workspaceFolder, len(folders))
		for idx, f := range folders {
			wsFolders[idx] = workspaceFolder{
				ID:        generateWorkspaceUUID(),
				Path:      f.Path,
				IsPrimary: f.IsPrimary,
				Position:  idx,
			}
		}
		created := workspaceProfile{
			ID:        generateWorkspaceUUID(),
			Name:      name,
			Color:     color,
			Folders:   wsFolders,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.workspaces = append(s.workspaces, created)
		for _, f := range created.Folders {
			s.removeDismissedLocked(f.Path)
		}
		s.removeDismissedLocked(created.ID)
		s.persistLocked()
		return created, nil
	}

	before := s.workspaces[targetIdx]
	folders, err := validateWorkspaceFolders(input.Folders)
	if err != nil {
		return workspaceProfile{}, err
	}
	primaryPath := folders[0].Path
	name := s.resolveNameLocked(input.Name, primaryPath, before.ID)
	color := normalizeWorkspaceIdentityColor(input.Color)
	if color == "" {
		color = before.Color
	}
	if color == "" {
		color = defaultWorkspaceIdentityColor
	}
	wsFolders := make([]workspaceFolder, len(folders))
	existingMap := make(map[string]workspaceFolder, len(before.Folders))
	for _, f := range before.Folders {
		existingMap[comparableFolderPath(f.Path)] = f
	}
	for idx, f := range folders {
		foundID := generateWorkspaceUUID()
		if old, ok := existingMap[comparableFolderPath(f.Path)]; ok && old.ID != "" {
			foundID = old.ID
		}
		wsFolders[idx] = workspaceFolder{
			ID:        foundID,
			Path:      f.Path,
			IsPrimary: f.IsPrimary,
			Position:  idx,
		}
	}
	updated := workspaceProfile{
		ID:        before.ID,
		Name:      name,
		Color:     color,
		Folders:   wsFolders,
		CreatedAt: before.CreatedAt,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.workspaces[targetIdx] = updated
	for _, f := range updated.Folders {
		s.removeDismissedLocked(f.Path)
	}
	s.removeDismissedLocked(updated.ID)
	s.persistLocked()
	return updated, nil
}

func (s *workspaceStore) Remove(idOrPath string) bool {
	target := strings.TrimSpace(idOrPath)
	if target == "" {
		return false
	}
	compTarget := comparableFolderPath(target)
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make([]workspaceProfile, 0, len(s.workspaces))
	removed := false
	for _, ws := range s.workspaces {
		matchPath := false
		for _, f := range ws.Folders {
			if comparableFolderPath(f.Path) == compTarget {
				matchPath = true
				break
			}
		}
		if ws.ID == target || matchPath {
			removed = true
			s.addDismissedLocked(ws.ID)
			for _, f := range ws.Folders {
				s.addDismissedLocked(f.Path)
			}
			continue
		}
		next = append(next, ws)
	}

	s.addDismissedLocked(target)
	s.workspaces = next
	s.persistLocked()
	return removed
}

func (s *workspaceStore) registerRoutes(mux *http.ServeMux) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		pathID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/hoxi/workspaces"))
		pathID = strings.TrimPrefix(pathID, "/")
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{
				"workspaces": s.List(),
				"dismissed":  s.ListDismissed(),
			})
		case http.MethodPost:
			var req saveWorkspaceRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			ws, err := s.Create(req)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, ws)
		case http.MethodPut, http.MethodPatch:
			var req saveWorkspaceRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			id := pathID
			if id == "" {
				id = req.ID
			}
			ws, err := s.Update(id, req)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, ws)
		case http.MethodDelete:
			id := pathID
			if id == "" {
				id = strings.TrimSpace(r.URL.Query().Get("id"))
			}
			if id == "" {
				id = strings.TrimSpace(r.URL.Query().Get("path"))
			}
			deleted := s.Remove(id)
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"deleted":   deleted,
				"dismissed": s.ListDismissed(),
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	mux.HandleFunc("/api/hoxi/workspaces", handler)
	mux.HandleFunc("/api/hoxi/workspaces/", handler)
}
