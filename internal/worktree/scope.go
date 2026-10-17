package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type managerRoots struct {
	sync.Mutex
	managers map[string]*worktreeManager
}

// ForDirectory returns a stable manager without changing any other request's root.
func (m *worktreeManager) ForDirectory(dir string) *Manager {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return m
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return m
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return m
	}
	m.roots.Lock()
	defer m.roots.Unlock()
	if scoped := m.roots.managers[abs]; scoped != nil {
		return scoped
	}
	scoped := &worktreeManager{rootDir: abs, sessions: make(map[string]worktreeMeta), roots: m.roots}
	m.roots.managers[abs] = scoped
	return scoped
}

func (m *worktreeManager) scoped(dir, sessionID string) *worktreeManager {
	sessionID = sanitizeSessionID(sessionID)
	if strings.TrimSpace(dir) != "" {
		// Callers may supply the sandbox itself rather than its parent workspace.
		abs, _ := filepath.Abs(dir)
		m.roots.Lock()
		for _, scoped := range m.roots.managers {
			scoped.mu.RLock()
			meta, ok := scoped.sessions[sessionID]
			scoped.mu.RUnlock()
			if ok && meta.Directory == abs {
				m.roots.Unlock()
				return scoped
			}
		}
		m.roots.Unlock()
		return m.ForDirectory(dir)
	}
	if sessionID == "" || sessionID == "__root__" {
		return m
	}
	// Session-only endpoints must keep using the workspace that created the session.
	m.roots.Lock()
	defer m.roots.Unlock()
	for _, scoped := range m.roots.managers {
		scoped.mu.RLock()
		_, ok := scoped.sessions[sessionID]
		scoped.mu.RUnlock()
		if ok {
			return scoped
		}
	}
	for _, scoped := range m.roots.managers {
		candidate := filepath.Join(scoped.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return scoped
		}
	}
	return m
}

func (m *worktreeManager) storeMeta(meta worktreeMeta) {
	m.mu.Lock()
	m.sessions[meta.SessionID] = meta
	m.mu.Unlock()
}
