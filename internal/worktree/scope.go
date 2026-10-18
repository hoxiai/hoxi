package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SessionBinding is the directory authority for a session known to Hoxi.
// Directory is canonical and only points at an existing directory.
type SessionBinding struct {
	Directory string
	Isolated  bool
}

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

// BindingForSession finds a binding across this manager's workspace roots.
// It deliberately ignores OpenCode's session.directory value: Hoxi owns the
// binding after sandbox creation and can recover it from the filesystem.
func (m *worktreeManager) BindingForSession(sessionID string) (SessionBinding, bool, error) {
	sessionID = sanitizeSessionID(sessionID)
	if sessionID == "" || sessionID == "__root__" {
		return SessionBinding{}, false, nil
	}

	m.roots.Lock()
	managers := make([]*worktreeManager, 0, len(m.roots.managers))
	for _, scoped := range m.roots.managers {
		managers = append(managers, scoped)
	}
	m.roots.Unlock()

	var matches []SessionBinding
	for _, scoped := range managers {
		if meta, ok := scoped.sessionMeta(sessionID); ok && meta.Isolated {
			binding, err := bindingFromDirectory(meta.Directory)
			if err != nil {
				return SessionBinding{}, true, err
			}
			matches = append(matches, binding)
			continue
		}
		candidate := filepath.Join(scoped.rootDir, ".hoxi", "worktrees", sessionID)
		if _, err := os.Stat(candidate); err == nil {
			binding, err := bindingFromDirectory(candidate)
			if err != nil {
				return SessionBinding{}, true, err
			}
			matches = append(matches, SessionBinding{Directory: binding.Directory, Isolated: true})
		} else if !os.IsNotExist(err) {
			return SessionBinding{}, true, err
		}
	}
	if len(matches) == 0 {
		return SessionBinding{}, false, nil
	}
	first := matches[0]
	for _, match := range matches[1:] {
		if match.Directory != first.Directory {
			return SessionBinding{}, true, errors.New("session has ambiguous isolated worktree bindings")
		}
	}
	return first, true, nil
}

func (m *worktreeManager) sessionMeta(sessionID string) (worktreeMeta, bool) {
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	return meta, ok
}

func bindingFromDirectory(directory string) (SessionBinding, error) {
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return SessionBinding{}, fmt.Errorf("resolve session sandbox: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("path is not a directory")
		}
		return SessionBinding{}, fmt.Errorf("invalid session sandbox: %w", err)
	}
	abs, err := filepath.Abs(canonical)
	if err != nil {
		return SessionBinding{}, fmt.Errorf("resolve session sandbox: %w", err)
	}
	return SessionBinding{Directory: filepath.Clean(abs), Isolated: true}, nil
}
