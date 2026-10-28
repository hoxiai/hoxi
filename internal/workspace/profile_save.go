package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxTombstones bounds the removed-project list; the oldest entries go first.
const maxTombstones = 500

func (s *workspaceStore) Create(input saveWorkspaceRequest) (workspaceProfile, error) {
	folders, err := validateWorkspaceFolders(input.Folders)
	if err != nil {
		return workspaceProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(folders, input)
}

// createLocked saves a new visible profile. A hidden project that already
// owns one of the folders is promoted (keeping its ID); further hidden owners
// are merged into it as aliases. A folder owned by another visible workspace
// is rejected so no folder ever belongs to two projects.
func (s *workspaceStore) createLocked(folders []workspaceFolderInput, input saveWorkspaceRequest) (workspaceProfile, error) {
	hidden, err := s.hiddenOwnersLocked(folders, "")
	if err != nil {
		return workspaceProfile{}, err
	}
	prev := s.snapshotLocked()
	now := time.Now().UTC().Format(time.RFC3339)
	created := workspaceProfile{
		ID:        generateWorkspaceUUID(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	existingFolderIDs := map[string]string{}
	if len(hidden) > 0 {
		promoted := s.workspaces[s.indexOfLocked(hidden[0])]
		created.ID, created.Color, created.CreatedAt = promoted.ID, promoted.Color, promoted.CreatedAt
		created.Aliases = append(created.Aliases, promoted.Aliases...)
		for _, folder := range promoted.Folders {
			existingFolderIDs[comparableFolderPath(folder.Path)] = folder.ID
		}
		hidden = hidden[1:]
		s.dropProfileLocked(promoted.ID)
	} else if tomb, ok := s.tombstoneForLocked(folders); ok {
		created.ID = tomb.ID
		created.Aliases = append(created.Aliases, tomb.Aliases...)
	}
	created.Aliases = withoutID(mergeIDs(created.Aliases, s.absorbHiddenLocked(hidden, folders)), created.ID)

	created.Name = s.resolveNameLocked(input.Name, folders[0].Path, created.ID)
	if color := normalizeWorkspaceIdentityColor(input.Color); color != "" {
		created.Color = color
	}
	if created.Color == "" {
		created.Color = defaultWorkspaceIdentityColor
	}
	created.Folders = make([]workspaceFolder, len(folders))
	for idx, f := range folders {
		id := existingFolderIDs[comparableFolderPath(f.Path)]
		if id == "" {
			id = generateWorkspaceUUID()
		}
		created.Folders[idx] = workspaceFolder{ID: id, Path: f.Path, IsPrimary: f.IsPrimary, Position: idx}
	}
	s.workspaces = append(s.workspaces, created)
	s.afterSaveLocked(created)
	if err := s.commitLocked(prev); err != nil {
		return workspaceProfile{}, err
	}
	return cloneProfile(created), nil
}

func (s *workspaceStore) Update(id string, input saveWorkspaceRequest) (result workspaceProfile, err error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return workspaceProfile{}, errors.New("workspace ID is required")
	}
	s.mu.Lock()
	var relinks []folderRelink
	defer func() {
		s.mu.Unlock()
		if err == nil {
			s.dispatchFolderRelinks(relinks)
		}
	}()

	targetIdx := s.profileIndexLocked(cleanID)
	if targetIdx == -1 {
		folders, err := validateWorkspaceFolders(input.Folders)
		if err != nil {
			return workspaceProfile{}, err
		}
		return s.createLocked(folders, input)
	}
	result, relinks, err = s.updateLocked(targetIdx, input)
	return result, err
}

func (s *workspaceStore) profileIndexLocked(idOrPath string) int {
	for i, ws := range s.workspaces {
		if ws.ID == idOrPath {
			return i
		}
	}
	expanded := expandUserPath(idOrPath)
	compTarget := comparableFolderPath(expanded)
	resolvedTarget := compTarget
	if realPath, err := filepath.EvalSymlinks(expanded); err == nil && realPath != "" {
		resolvedTarget = comparableFolderPath(realPath)
	}
	for i, ws := range s.workspaces {
		for _, f := range ws.Folders {
			comp := comparableFolderPath(f.Path)
			if comp == compTarget || comp == resolvedTarget {
				return i
			}
		}
	}
	return -1
}

// updateLocked rewrites a profile. Folder identity follows the path; a
// client-sent folder ID with a new path is an explicit relocation and is the
// only way sessions are relinked. Saved folders that are missing on disk are
// kept unless the client omitted them.
func (s *workspaceStore) updateLocked(targetIdx int, input saveWorkspaceRequest) (workspaceProfile, []folderRelink, error) {
	before := s.workspaces[targetIdx]
	byPath := make(map[string]workspaceFolder, len(before.Folders))
	byID := make(map[string]workspaceFolder, len(before.Folders))
	for _, f := range before.Folders {
		byPath[comparableFolderPath(f.Path)] = f
		if f.ID != "" {
			byID[f.ID] = f
		}
	}
	folders, err := validateWorkspaceFoldersKeeping(input.Folders, func(comp string) bool {
		_, ok := byPath[comp]
		return ok
	})
	if err != nil {
		return workspaceProfile{}, nil, err
	}
	hidden, err := s.hiddenOwnersLocked(folders, before.ID)
	if err != nil {
		return workspaceProfile{}, nil, err
	}
	hiddenOwned := make(map[string]bool)
	for _, id := range hidden {
		for _, f := range s.workspaces[s.indexOfLocked(id)].Folders {
			hiddenOwned[comparableFolderPath(f.Path)] = true
		}
	}
	inInput := make(map[string]bool, len(folders))
	for _, f := range folders {
		inInput[comparableFolderPath(f.Path)] = true
	}

	prev := s.snapshotLocked()
	var relinks []folderRelink
	used := make(map[string]bool, len(folders))
	wsFolders := make([]workspaceFolder, len(folders))
	for idx, f := range folders {
		comp := comparableFolderPath(f.Path)
		id := ""
		if old, ok := byPath[comp]; ok && old.ID != "" && !used[old.ID] {
			id = old.ID
		} else if old, ok := byID[f.ID]; ok && !used[old.ID] && !inInput[comparableFolderPath(old.Path)] {
			// Explicit replace: the folder keeps its ID. Sessions follow only
			// when the old folder is really gone and the new one is not a
			// directory some other project already tracks.
			id = old.ID
			if _, statErr := os.Stat(old.Path); errors.Is(statErr, os.ErrNotExist) && !hiddenOwned[comp] {
				relinks = append(relinks, folderRelink{projectID: before.ID, oldPath: old.Path, newPath: f.Path})
			}
		}
		if id == "" {
			id = generateWorkspaceUUID()
		}
		used[id] = true
		wsFolders[idx] = workspaceFolder{ID: id, Path: f.Path, IsPrimary: f.IsPrimary, Position: idx}
	}

	color := normalizeWorkspaceIdentityColor(input.Color)
	if color == "" {
		color = before.Color
	}
	if color == "" {
		color = defaultWorkspaceIdentityColor
	}
	updated := workspaceProfile{
		ID:        before.ID,
		Name:      s.resolveNameLocked(input.Name, folders[0].Path, before.ID),
		Color:     color,
		Folders:   wsFolders,
		Aliases:   append([]string(nil), before.Aliases...),
		CreatedAt: before.CreatedAt,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	updated.Aliases = withoutID(mergeIDs(updated.Aliases, s.absorbHiddenLocked(hidden, folders)), updated.ID)
	for i, ws := range s.workspaces {
		if ws.ID == before.ID {
			s.workspaces[i] = updated
			break
		}
	}
	for _, oldFolder := range before.Folders {
		comp := comparableFolderPath(oldFolder.Path)
		if !inInput[comp] && !s.folderOwnedByOtherLocked(comp, updated.ID) {
			s.addDismissedLocked(oldFolder.Path)
		}
	}
	s.afterSaveLocked(updated)
	if err := s.commitLocked(prev); err != nil {
		return workspaceProfile{}, nil, err
	}
	return cloneProfile(updated), relinks, nil
}

func (s *workspaceStore) folderOwnedByOtherLocked(compPath, selfID string) bool {
	for _, ws := range s.workspaces {
		if ws.ID == selfID {
			continue
		}
		for _, f := range ws.Folders {
			if comparableFolderPath(f.Path) == compPath {
				return true
			}
		}
	}
	return false
}

// hiddenOwnersLocked returns the IDs of hidden projects that own any of
// folders, or an error when a visible workspace other than selfID owns one.
func (s *workspaceStore) hiddenOwnersLocked(folders []workspaceFolderInput, selfID string) ([]string, error) {
	var hidden []string
	seen := make(map[string]bool)
	for _, folder := range folders {
		comp := comparableFolderPath(folder.Path)
		for _, ws := range s.workspaces {
			if ws.ID == selfID || seen[ws.ID] {
				continue
			}
			for _, owned := range ws.Folders {
				if comparableFolderPath(owned.Path) != comp {
					continue
				}
				if !ws.Hidden {
					return nil, fmt.Errorf("Folder %s already belongs to workspace %q.", folder.Path, ws.Name)
				}
				seen[ws.ID] = true
				hidden = append(hidden, ws.ID)
				break
			}
		}
	}
	return hidden, nil
}

// absorbHiddenLocked strips folders from the hidden projects ids. A hidden
// project left without folders is removed and its IDs are returned so the
// absorbing profile can claim its sessions as aliases.
func (s *workspaceStore) absorbHiddenLocked(ids []string, folders []workspaceFolderInput) []string {
	if len(ids) == 0 {
		return nil
	}
	taken := make(map[string]bool, len(folders))
	for _, f := range folders {
		taken[comparableFolderPath(f.Path)] = true
	}
	var aliases []string
	for _, id := range ids {
		index := s.indexOfLocked(id)
		if index < 0 {
			continue
		}
		profile := cloneProfile(s.workspaces[index])
		remaining := profile.Folders[:0]
		for _, f := range profile.Folders {
			if !taken[comparableFolderPath(f.Path)] {
				remaining = append(remaining, f)
			}
		}
		if len(remaining) > 0 {
			profile.Folders = remaining
			s.workspaces[index] = profile
			continue
		}
		aliases = append(aliases, profile.ID)
		aliases = append(aliases, profile.Aliases...)
		s.dropProfileLocked(profile.ID)
	}
	return aliases
}

func (s *workspaceStore) dropProfileLocked(id string) {
	next := make([]workspaceProfile, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		if ws.ID != id {
			next = append(next, ws)
		}
	}
	s.workspaces = next
}

// afterSaveLocked clears dismissals and tombstones claimed by saved.
func (s *workspaceStore) afterSaveLocked(saved workspaceProfile) {
	for _, f := range saved.Folders {
		s.removeDismissedLocked(f.Path)
	}
	s.removeDismissedLocked(saved.ID)
	s.clearTombstonesLocked(saved)
}

func (s *workspaceStore) indexOfLocked(id string) int {
	for i, ws := range s.workspaces {
		if ws.ID == id {
			return i
		}
	}
	return -1
}

func (s *workspaceStore) liveIDLocked(id string) bool {
	return s.indexOfLocked(id) >= 0
}

// tombstoneForLocked finds the removed project of the first folder (primary
// first) whose ID is not currently in use.
func (s *workspaceStore) tombstoneForLocked(folders []workspaceFolderInput) (removedProject, bool) {
	for _, f := range folders {
		if tomb, ok := s.tombstoneForPathLocked(f.Path); ok {
			return tomb, true
		}
	}
	return removedProject{}, false
}

func (s *workspaceStore) tombstoneForPathLocked(path string) (removedProject, bool) {
	comp := comparableFolderPath(path)
	for i := len(s.removed) - 1; i >= 0; i-- {
		tomb := s.removed[i]
		if comparableFolderPath(tomb.Path) == comp && tomb.ID != "" && !s.liveIDLocked(tomb.ID) {
			return tomb, true
		}
	}
	return removedProject{}, false
}

func (s *workspaceStore) addTombstoneLocked(path string, ws workspaceProfile) {
	comp := comparableFolderPath(path)
	next := make([]removedProject, 0, len(s.removed)+1)
	for _, tomb := range s.removed {
		if comparableFolderPath(tomb.Path) != comp {
			next = append(next, tomb)
		}
	}
	next = append(next, removedProject{Path: path, ID: ws.ID, Aliases: append([]string(nil), ws.Aliases...)})
	if len(next) > maxTombstones {
		next = next[len(next)-maxTombstones:]
	}
	s.removed = next
}

func (s *workspaceStore) clearTombstonesLocked(saved workspaceProfile) {
	if len(s.removed) == 0 {
		return
	}
	paths := make(map[string]bool, len(saved.Folders))
	for _, f := range saved.Folders {
		paths[comparableFolderPath(f.Path)] = true
	}
	next := s.removed[:0:0]
	for _, tomb := range s.removed {
		if tomb.ID != saved.ID && !paths[comparableFolderPath(tomb.Path)] {
			next = append(next, tomb)
		}
	}
	s.removed = next
}

func withoutID(ids []string, id string) []string {
	out := ids[:0:0]
	for _, candidate := range ids {
		if candidate != id {
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeIDs(base []string, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, id := range append(append([]string(nil), base...), extra...) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
