package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultWorkspaceIdentityColor = "#5EA0F7"

var (
	shortHexColorRE = regexp.MustCompile(`^#([0-9a-fA-F]{3})$`)
	longHexColorRE  = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)
)

type workspaceFolderInput struct {
	Path      string `json:"path"`
	IsPrimary bool   `json:"isPrimary"`
}

type workspaceFolder struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	IsPrimary bool   `json:"isPrimary"`
	Position  int    `json:"position"`
}

type workspaceProfile struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Color     string            `json:"color,omitempty"`
	Folders   []workspaceFolder `json:"folders"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}

type saveWorkspaceRequest struct {
	ID      string                 `json:"id,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Color   string                 `json:"color,omitempty"`
	Folders []workspaceFolderInput `json:"folders"`
}

type workspaceStore struct {
	mu          sync.RWMutex
	storagePath string
	workspaces  []workspaceProfile
}

func newWorkspaceStore(rootDir string) *workspaceStore {
	storagePath := userHoxiWorkspacesPath()
	if storagePath == "" && strings.TrimSpace(rootDir) != "" {
		storagePath = filepath.Join(strings.TrimSpace(rootDir), ".hoxi", "workspaces.json")
	}
	s := &workspaceStore{
		storagePath: storagePath,
		workspaces:  []workspaceProfile{},
	}
	s.loadFromDisk(rootDir)
	return s
}

func userHoxiWorkspacesPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "workspaces.json")
}

func normalizeWorkspaceIdentityColor(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if m := shortHexColorRE.FindStringSubmatch(trimmed); len(m) == 2 {
		d := strings.ToUpper(m[1])
		return fmt.Sprintf("#%c%c%c%c%c%c", d[0], d[0], d[1], d[1], d[2], d[2])
	}
	if m := longHexColorRE.FindStringSubmatch(trimmed); len(m) == 2 {
		return "#" + strings.ToUpper(m[1])
	}
	return ""
}

func comparableFolderPath(p string) string {
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func canonicalHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(home)
}

func generateWorkspaceUUID() string {
	if hexStr, err := generateRandomHex(16); err == nil && len(hexStr) == 32 {
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
	}
	return fmt.Sprintf("ws_%d", time.Now().UnixNano())
}

func (s *workspaceStore) loadFromDisk(seedRootDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := false
	if s.storagePath != "" {
		if data, err := os.ReadFile(s.storagePath); err == nil {
			var parsed struct {
				Workspaces []workspaceProfile `json:"workspaces"`
			}
			if err := json.Unmarshal(data, &parsed); err == nil && parsed.Workspaces != nil {
				s.workspaces = parsed.Workspaces
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
	payload := map[string]any{"workspaces": s.workspaces}
	if raw, err := json.MarshalIndent(payload, "", "  "); err == nil {
		_ = os.WriteFile(s.storagePath, append(raw, '\n'), 0o644)
	}
}

func validateWorkspaceFolders(inputs []workspaceFolderInput) ([]workspaceFolderInput, error) {
	if len(inputs) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	home := comparableFolderPath(canonicalHomeDir())
	seen := make(map[string]struct{}, len(inputs))
	canonical := make([]workspaceFolderInput, 0, len(inputs))
	primaryCount := 0

	for _, item := range inputs {
		rawPath := strings.TrimSpace(item.Path)
		if rawPath == "" {
			continue
		}
		if strings.HasPrefix(rawPath, "~/") {
			if h := canonicalHomeDir(); h != "" {
				rawPath = filepath.Join(h, strings.TrimPrefix(rawPath, "~/"))
			}
		}
		if !filepath.IsAbs(rawPath) {
			return nil, errors.New("Workspace folders must use absolute paths.")
		}
		resolved, err := filepath.EvalSymlinks(rawPath)
		if err != nil {
			resolved = filepath.Clean(rawPath)
		} else {
			resolved = filepath.Clean(resolved)
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("Workspace folder does not exist or is not a directory: %s", item.Path)
		}
		comp := comparableFolderPath(resolved)
		vol := filepath.VolumeName(resolved)
		if resolved == vol+string(filepath.Separator) || (home != "" && comp == home) {
			return nil, errors.New("Filesystem roots and the home directory cannot be workspace folders.")
		}
		if _, exists := seen[comp]; exists {
			return nil, errors.New("Workspace folders must be unique.")
		}
		seen[comp] = struct{}{}
		if item.IsPrimary {
			primaryCount++
		}
		canonical = append(canonical, workspaceFolderInput{Path: resolved, IsPrimary: item.IsPrimary})
	}

	if len(canonical) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	if primaryCount == 0 {
		canonical[0].IsPrimary = true
	}

	var primary workspaceFolderInput
	others := make([]workspaceFolderInput, 0, len(canonical)-1)
	foundPrimary := false
	for _, f := range canonical {
		if f.IsPrimary && !foundPrimary {
			primary = workspaceFolderInput{Path: f.Path, IsPrimary: true}
			foundPrimary = true
		} else {
			others = append(others, workspaceFolderInput{Path: f.Path, IsPrimary: false})
		}
	}
	return append([]workspaceFolderInput{primary}, others...), nil
}

func (s *workspaceStore) resolveNameLocked(rawName, primaryPath, excludeID string) string {
	if supplied := strings.TrimSpace(rawName); supplied != "" {
		return supplied
	}
	baseName := filepath.Base(primaryPath)
	if baseName == "" || baseName == "." || baseName == string(filepath.Separator) {
		baseName = "Workspace"
	}
	occupied := make(map[string]struct{}, len(s.workspaces))
	for _, ws := range s.workspaces {
		if ws.ID != excludeID {
			occupied[strings.ToLower(ws.Name)] = struct{}{}
		}
	}
	if _, exists := occupied[strings.ToLower(baseName)]; !exists {
		return baseName
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)", baseName, suffix)
		if _, exists := occupied[strings.ToLower(candidate)]; !exists {
			return candidate
		}
	}
}

func resolveColorOrDefault(raw, fallback string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if fallback != "" {
			return fallback, nil
		}
		return defaultWorkspaceIdentityColor, nil
	}
	if hex := normalizeWorkspaceIdentityColor(trimmed); hex != "" {
		return hex, nil
	}
	return "", errors.New("Invalid identity color.")
}

func (s *workspaceStore) List() []workspaceProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]workspaceProfile, len(s.workspaces))
	copy(out, s.workspaces)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (s *workspaceStore) Create(input saveWorkspaceRequest) (workspaceProfile, error) {
	folders, err := validateWorkspaceFolders(input.Folders)
	if err != nil {
		return workspaceProfile{}, err
	}
	color, err := resolveColorOrDefault(input.Color, defaultWorkspaceIdentityColor)
	if err != nil {
		return workspaceProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	name := s.resolveNameLocked(input.Name, folders[0].Path, "")
	now := time.Now().UTC().Format(time.RFC3339)
	wsFolders := make([]workspaceFolder, len(folders))
	for idx, f := range folders {
		wsFolders[idx] = workspaceFolder{
			ID:        generateWorkspaceUUID(),
			Path:      f.Path,
			IsPrimary: idx == 0,
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
	s.persistLocked()
	return created, nil
}

func (s *workspaceStore) Update(id string, input saveWorkspaceRequest) (workspaceProfile, error) {
	folders, err := validateWorkspaceFolders(input.Folders)
	if err != nil {
		return workspaceProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	targetIdx := -1
	for i, ws := range s.workspaces {
		if ws.ID == id {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 && len(folders) > 0 {
		primaryComp := comparableFolderPath(folders[0].Path)
		for i, ws := range s.workspaces {
			if len(ws.Folders) > 0 && comparableFolderPath(ws.Folders[0].Path) == primaryComp {
				targetIdx = i
				break
			}
		}
	}
	if targetIdx < 0 {
		color, err := resolveColorOrDefault(input.Color, defaultWorkspaceIdentityColor)
		if err != nil {
			return workspaceProfile{}, err
		}
		name := s.resolveNameLocked(input.Name, folders[0].Path, "")
		now := time.Now().UTC().Format(time.RFC3339)
		wsFolders := make([]workspaceFolder, len(folders))
		for idx, f := range folders {
			wsFolders[idx] = workspaceFolder{
				ID:        generateWorkspaceUUID(),
				Path:      f.Path,
				IsPrimary: idx == 0,
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
		s.persistLocked()
		return created, nil
	}

	before := s.workspaces[targetIdx]
	color, err := resolveColorOrDefault(input.Color, before.Color)
	if err != nil {
		return workspaceProfile{}, err
	}
	name := before.Name
	if strings.TrimSpace(input.Name) != "" {
		name = s.resolveNameLocked(input.Name, folders[0].Path, before.ID)
	}
	retainedIDs := make(map[string]string, len(before.Folders))
	for _, f := range before.Folders {
		retainedIDs[comparableFolderPath(f.Path)] = f.ID
	}
	wsFolders := make([]workspaceFolder, len(folders))
	for idx, f := range folders {
		fID := retainedIDs[comparableFolderPath(f.Path)]
		if fID == "" {
			fID = generateWorkspaceUUID()
		}
		wsFolders[idx] = workspaceFolder{
			ID:        fID,
			Path:      f.Path,
			IsPrimary: idx == 0,
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
		matchPath := len(ws.Folders) > 0 && comparableFolderPath(ws.Folders[0].Path) == compTarget
		if ws.ID == target || matchPath {
			removed = true
			continue
		}
		next = append(next, ws)
	}
	if removed {
		s.workspaces = next
		s.persistLocked()
	}
	return removed
}

func (s *workspaceStore) registerRoutes(mux *http.ServeMux) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		pathID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/hoxi/workspaces"))
		pathID = strings.TrimPrefix(pathID, "/")
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"workspaces": s.List()})
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
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	mux.HandleFunc("/api/hoxi/workspaces", handler)
	mux.HandleFunc("/api/hoxi/workspaces/", handler)
}
