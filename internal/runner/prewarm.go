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
	// projectPrewarmInterval is the steady-state cadence. The OpenCode daemon
	// keeps a directory's instance warm for several minutes, so re-touching
	// every root well inside that window keeps workspace expands instant.
	projectPrewarmInterval = 60 * time.Second
	// projectPrewarmRetry is used while the daemon is still coming up (or was
	// restarted), so the first expand after launch is not left cold.
	projectPrewarmRetry = 3 * time.Second
	// projectPrewarmTimeout bounds a single directory's warm request.
	projectPrewarmTimeout = 5 * time.Second
	// indexPrewarmTimeout bounds a single directory's background index warm call.
	indexPrewarmTimeout = 15 * time.Second
	// maxPrewarmIndexRoots caps automatic startup index prewarming to match the
	// in-memory code-index LRU capacity so idle projects do not thrash cache or disk.
	maxPrewarmIndexRoots = 8
)

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

// StartProjectPrewarm keeps the OpenCode daemon's per-directory caches warm
// and schedules a decoupled one-shot index prewarm for active project roots.
//
// The daemon lazily boots an instance per directory (about half a second even
// for an empty directory), and the unified /api/hoxi/sessions listing always
// asks it for un-adopted sessions — so without warming, the first expand of a
// workspace stalls on that cold start. Warming every known project root in the
// background moves the cost off the interactive path.
//
// The host calls this once with its lifetime context; the loop stops when the
// context is cancelled and is a no-op until both the engine client and a
// project enumerator are wired.
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
	enumerator, ok := s.projectResolver.(projectEnumerator)
	if !ok {
		return
	}
	for _, root := range enumerator.AllProjectRoots() {
		if ctx.Err() != nil {
			return
		}
		dir := strings.TrimSpace(root)
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
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
			delay = projectPrewarmInterval
		} else {
			delay = projectPrewarmRetry
		}
	}
}

// prewarmProjectRoots touches the engine for every project folder. It reports
// whether the engine answered (or there was nothing to do), which lets the loop
// settle into its steady cadence instead of retrying while the daemon is down.
func (s *Service) prewarmProjectRoots(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if s.openCode == nil {
		return true
	}
	enumerator, ok := s.projectResolver.(projectEnumerator)
	if !ok {
		return true
	}
	answered, attempted := false, false
	for _, root := range enumerator.AllProjectRoots() {
		dir := strings.TrimSpace(root)
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		attempted = true
		warmCtx, cancel := context.WithTimeout(ctx, projectPrewarmTimeout)
		_, _, err := s.openCodeRaw(warmCtx, http.MethodGet, "/session", directoryQuery(dir, url.Values{"limit": {"200"}}), nil)
		cancel()
		if err == nil {
			answered = true
		}
		if ctx.Err() != nil {
			break
		}
	}
	return answered || !attempted
}
