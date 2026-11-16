package runner

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// projectPrewarmRetry is used while the daemon is still coming up (or was
	// restarted), so the active workspace root is warmed as soon as OpenCode listens.
	projectPrewarmRetry = 3 * time.Second
	// projectPrewarmTimeout bounds a single directory's warm request.
	projectPrewarmTimeout = 5 * time.Second
	// indexPrewarmTimeout bounds a single directory's background index warm call.
	indexPrewarmTimeout = 15 * time.Second
	// maxPrewarmIndexRoots limits automatic startup index prewarming to the
	// active primary workspace root; other projects warm lazily on demand.
	maxPrewarmIndexRoots = 1
)

// prewarmState tracks workspace directory prewarming hooks and warmed roots.
type prewarmState struct {
	directoryWarmer func(ctx context.Context, directory string)
	prewarmCtx      context.Context
	warmedDirs      syncMap[string, bool]
}

// SetDirectoryWarmer registers an optional hook that warms workspace indexes in
// the background when project roots or new session worktrees are provisioned.
func (s *Service) SetDirectoryWarmer(fn func(directory string)) {
	if fn == nil {
		s.SetDirectoryWarmerContext(nil)
		return
	}
	s.SetDirectoryWarmerContext(func(ctx context.Context, directory string) {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		fn(directory)
	})
}

// SetDirectoryWarmerContext registers a context-aware directory warmer hook
// (such as codeindex.Service.StatusContext) bounded by indexPrewarmTimeout.
func (s *Service) SetDirectoryWarmerContext(fn func(ctx context.Context, directory string)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.directoryWarmer = fn
	s.mu.Unlock()
	s.warmedDirs.Range(func(dir string, _ bool) bool {
		s.warmedDirs.Delete(dir)
		return true
	})
}

func (s *Service) claimIndexPrewarm(directory string, enforceRootCap bool) bool {
	clean := filepath.Clean(strings.TrimSpace(directory))
	if clean == "" || clean == "." {
		return false
	}
	if _, already := s.warmedDirs.Load(clean); already {
		return false
	}
	if enforceRootCap {
		count := 0
		s.warmedDirs.Range(func(_ string, _ bool) bool {
			count++
			return count < maxPrewarmIndexRoots
		})
		if count >= maxPrewarmIndexRoots {
			return false
		}
	}
	_, loaded := s.warmedDirs.LoadOrStore(clean, true)
	return !loaded
}

func (s *Service) forgetWarmedDirectory(directory string) {
	if s == nil {
		return
	}
	clean := filepath.Clean(strings.TrimSpace(directory))
	if clean == "" || clean == "." {
		return
	}
	s.warmedDirs.Delete(clean)
}

func (s *Service) runDirectoryWarmerOnce(ctx context.Context, directory string, enforceRootCap bool) {
	dir := strings.TrimSpace(directory)
	if s == nil || dir == "" || ctx == nil || ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	warmer := s.directoryWarmer
	s.mu.Unlock()
	if warmer == nil {
		return
	}
	if !s.claimIndexPrewarm(dir, enforceRootCap) {
		return
	}
	warmCtx, cancel := context.WithTimeout(ctx, indexPrewarmTimeout)
	defer cancel()
	warmer(warmCtx, dir)
}

func (s *Service) warmDirectoryAsync(directory string) {
	dir := strings.TrimSpace(directory)
	if s == nil || dir == "" {
		return
	}
	s.mu.Lock()
	warmer := s.directoryWarmer
	ctx := s.prewarmCtx
	s.mu.Unlock()
	if warmer == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	go s.runDirectoryWarmerOnce(ctx, dir, false)
}

// candidatePrewarmRoots returns existing project directories ordered with the
// active primary workspace root first so startup prewarming targets only what
// the user actually has open.
func (s *Service) candidatePrewarmRoots() []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	add := func(raw string) {
		dir := strings.TrimSpace(raw)
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if clean == "" || clean == "." || seen[clean] {
			return
		}
		if info, err := os.Stat(clean); err != nil || !info.IsDir() {
			return
		}
		seen[clean] = true
		out = append(out, clean)
	}
	add(s.rootDir)
	if enumerator, ok := s.projectResolver.(projectEnumerator); ok {
		for _, root := range enumerator.AllProjectRoots() {
			add(root)
		}
	}
	return out
}

// StartProjectPrewarm performs a one-shot startup warm of the active project
// root (both OpenCode daemon instance and local lexical code index) without
// polling idle historical workspaces in a permanent keep-alive loop. Other
// workspaces warm lazily on demand when opened.
func (s *Service) StartProjectPrewarm(ctx context.Context) {
	if ctx == nil {
		return
	}
	s.mu.Lock()
	s.prewarmCtx = ctx
	s.mu.Unlock()
	go s.prewarmIndexRoots(ctx)
	go s.prewarmLoop(ctx)
}

func (s *Service) prewarmIndexRoots(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	warmer := s.directoryWarmer
	s.mu.Unlock()
	if warmer == nil {
		return
	}
	for _, dir := range s.candidatePrewarmRoots() {
		if ctx.Err() != nil {
			return
		}
		s.runDirectoryWarmerOnce(ctx, dir, true)
	}
}

func (s *Service) prewarmLoop(ctx context.Context) {
	delay := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if s.prewarmProjectRoots(ctx) {
			// Stop once the active project root is warmed so idle historical
			// OpenCode instances can sleep and release memory naturally.
			return
		}
		delay = projectPrewarmRetry
	}
}

// prewarmProjectRoots touches the OpenCode engine once for the primary active
// workspace root. It returns true once the engine answers (or when there is no
// valid root to warm), and false while the daemon is still booting.
func (s *Service) prewarmProjectRoots(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if s.openCode == nil {
		return true
	}
	roots := s.candidatePrewarmRoots()
	if len(roots) == 0 {
		return true
	}
	dir := roots[0]
	warmCtx, cancel := context.WithTimeout(ctx, projectPrewarmTimeout)
	_, _, err := s.openCodeRaw(warmCtx, http.MethodGet, "/session", directoryQuery(dir, url.Values{"limit": {"50"}}), nil)
	cancel()
	return err == nil
}
