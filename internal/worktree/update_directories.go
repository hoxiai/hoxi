package worktree

import (
	"fmt"
	"os"
	"path/filepath"
)

// UpdateDirectories includes in-memory bindings and sandboxes from earlier boots.
// roots also covers workspace folders not yet visited by this manager.
func (m *worktreeManager) UpdateDirectories(roots []string) ([]string, error) {
	dirs := make(map[string]bool)
	for _, root := range roots {
		if root != "" {
			dirs[root] = true
		}
	}
	m.roots.Lock()
	for _, scoped := range m.roots.managers {
		dirs[scoped.rootDir] = true
		scoped.mu.RLock()
		for _, meta := range scoped.sessions {
			dirs[meta.Directory] = true
		}
		scoped.mu.RUnlock()
	}
	m.roots.Unlock()
	// Snapshot before adding children so we only inspect each root once.
	parents := make([]string, 0, len(dirs))
	for dir := range dirs {
		parents = append(parents, dir)
	}
	for _, root := range parents {
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		wtDir := filepath.Join(root, ".hoxi", "worktrees")
		wtInfo, err := os.Stat(wtDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !wtInfo.IsDir() {
			return nil, fmt.Errorf("worktrees path is not a directory: %s", wtDir)
		}
		entries, err := os.ReadDir(wtDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			info, err := os.Stat(filepath.Join(root, ".hoxi", "worktrees", entry.Name()))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				dirs[filepath.Join(root, ".hoxi", "worktrees", entry.Name())] = true
			}
		}
	}
	result := make([]string, 0, len(dirs))
	for dir := range dirs {
		if dir != "" {
			info, err := os.Stat(dir)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				continue
			}
			result = append(result, dir)
		}
	}
	return result, nil
}
