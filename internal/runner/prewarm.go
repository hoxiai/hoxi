package runner

import (
	"context"
	"net/http"
	"net/url"
	"os"
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
)

// StartProjectPrewarm keeps the OpenCode daemon's per-directory caches warm.
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
	go s.prewarmLoop(ctx)
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
