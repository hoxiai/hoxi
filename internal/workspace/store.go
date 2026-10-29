package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type workspaceStore struct {
	mu            sync.RWMutex
	storagePath   string
	workspaces    []workspaceProfile
	dismissed     []string
	removed       []removedProject
	relinkHandler func(projectID, oldPath, newPath string) error
	// persistBlocked is set when the existing file could not be read or
	// backed up; writing would destroy data the user may still recover.
	persistBlocked error
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

// loadFromDisk reads the store file. A corrupt file is moved aside before the
// store reseeds, and an unreadable file blocks writes, so a parse error never
// silently replaces the user's projects.
func (s *workspaceStore) loadFromDisk(seedRootDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := false
	if s.storagePath != "" {
		data, err := os.ReadFile(s.storagePath)
		switch {
		case err == nil:
			var parsed workspaceStoreFile
			if parseErr := json.Unmarshal(data, &parsed); parseErr == nil {
				if parsed.Workspaces != nil {
					s.workspaces = parsed.Workspaces
				}
				if parsed.Dismissed != nil {
					s.dismissed = parsed.Dismissed
				}
				s.removed = parsed.Removed
				loaded = true
			} else if backup, backupErr := s.backupCorruptFileLocked(); backupErr != nil {
				s.persistBlocked = fmt.Errorf("workspaces file is corrupt and could not be backed up: %w", backupErr)
				log.Printf("workspace store: %v (parse error: %v)", s.persistBlocked, parseErr)
				return
			} else {
				log.Printf("workspace store: corrupt %s moved to %s: %v", s.storagePath, backup, parseErr)
			}
		case !errors.Is(err, os.ErrNotExist):
			s.persistBlocked = fmt.Errorf("workspaces file is unreadable: %w", err)
			log.Printf("workspace store: %v", s.persistBlocked)
			return
		}
	}

	if !loaded && strings.TrimSpace(seedRootDir) != "" {
		if ws, err := s.buildSeededWorkspace(seedRootDir); err == nil {
			s.workspaces = []workspaceProfile{ws}
			if err := s.persistLocked(); err != nil {
				log.Printf("workspace store: persist seed: %v", err)
			}
		}
	}
}

func (s *workspaceStore) backupCorruptFileLocked() (string, error) {
	backup := fmt.Sprintf("%s.corrupt-%s", s.storagePath, time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Rename(s.storagePath, backup); err != nil {
		return "", err
	}
	return backup, nil
}

func (s *workspaceStore) buildSeededWorkspace(rootDir string) (workspaceProfile, error) {
	if CleanValidProjectDir(rootDir) == "" {
		return workspaceProfile{}, errors.New("invalid seed rootDir")
	}
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

// persistLocked atomically replaces the store file: temp file in the same
// directory, fsync, then rename. Callers hold s.mu.
func (s *workspaceStore) persistLocked() error {
	if s.storagePath == "" {
		return nil
	}
	if s.persistBlocked != nil {
		return s.persistBlocked
	}
	payload := workspaceStoreFile{
		Workspaces: s.workspaces,
		Dismissed:  s.dismissed,
		Removed:    s.removed,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.storagePath, append(raw, '\n'), 0o644)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// storeState is an in-memory snapshot used to roll back a mutation whose
// persistence failed, so memory never diverges from disk.
type storeState struct {
	workspaces []workspaceProfile
	dismissed  []string
	removed    []removedProject
}

func (s *workspaceStore) snapshotLocked() storeState {
	ws := make([]workspaceProfile, len(s.workspaces))
	for i, profile := range s.workspaces {
		ws[i] = cloneProfile(profile)
	}
	return storeState{
		workspaces: ws,
		dismissed:  append([]string(nil), s.dismissed...),
		removed:    append([]removedProject(nil), s.removed...),
	}
}

// commitLocked persists the current state or restores prev on failure.
func (s *workspaceStore) commitLocked(prev storeState) error {
	if err := s.persistLocked(); err != nil {
		s.workspaces, s.dismissed, s.removed = prev.workspaces, prev.dismissed, prev.removed
		log.Printf("workspace store: persist failed: %v", err)
		return fmt.Errorf("save workspaces: %w", err)
	}
	return nil
}

func cloneProfile(profile workspaceProfile) workspaceProfile {
	profile.Folders = append([]workspaceFolder(nil), profile.Folders...)
	profile.Aliases = append([]string(nil), profile.Aliases...)
	return profile
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

func (s *workspaceStore) PrimaryRootDir(fallback string) string {
	if valid := CleanValidProjectDir(fallback); valid != "" {
		return valid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	savedRoot := func(includeHidden bool) string {
		for _, ws := range s.workspaces {
			if ws.Hidden && !includeHidden {
				continue
			}
			for _, f := range ws.Folders {
				if f.IsPrimary {
					if valid := CleanValidProjectDir(f.Path); valid != "" {
						return valid
					}
				}
			}
			for _, f := range ws.Folders {
				if valid := CleanValidProjectDir(f.Path); valid != "" {
					return valid
				}
			}
		}
		return ""
	}
	if root := savedRoot(false); root != "" {
		return root
	}
	return savedRoot(true)
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
	for i, profile := range s.workspaces {
		out[i] = cloneProfile(profile)
	}
	return out
}

func (s *workspaceStore) ListDismissed() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.dismissed))
	copy(out, s.dismissed)
	return out
}

// Remove drops every profile matching an ID or folder path. Each folder
// leaves a tombstone so re-adding it later reuses the project ID.
func (s *workspaceStore) Remove(idOrPath string) (bool, error) {
	target := strings.TrimSpace(idOrPath)
	if target == "" {
		return false, nil
	}
	compTarget := comparableFolderPath(target)
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.snapshotLocked()

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
				s.addTombstoneLocked(f.Path, ws)
			}
			continue
		}
		next = append(next, ws)
	}

	s.addDismissedLocked(target)
	s.workspaces = next
	if err := s.commitLocked(prev); err != nil {
		return false, err
	}
	return removed, nil
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
			deleted, err := s.Remove(id)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
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
	mux.HandleFunc("/api/hoxi/projects/resolve", s.handleResolveProject)
}
