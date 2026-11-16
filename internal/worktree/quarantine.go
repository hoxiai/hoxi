package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	HealthOK               = "ok"
	HealthAbsentRestorable = "absent-restorable"
	HealthUnregistered     = "unregistered"
	HealthQuarantined      = "quarantined"

	quarantineThreshold  = 3
	baseQuarantineWindow = 15 * time.Second
	maxQuarantineWindow  = 2 * time.Minute
	tempDeletePrefix     = ".hoxi-delete-"
)

type quarantineEntry struct {
	failures int
	until    time.Time
}

// quarantineTracker isolates repeatedly failing worktrees during status/diff
// polling so one broken worktree does not spawn failing git subprocesses on
// every poll cycle.
type quarantineTracker struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]quarantineEntry
}

func newQuarantineTracker() *quarantineTracker {
	return &quarantineTracker{
		now:     time.Now,
		entries: make(map[string]quarantineEntry),
	}
}

func quarantineWindowForFailures(failures int) time.Duration {
	if failures < quarantineThreshold {
		return 0
	}
	shift := failures - quarantineThreshold
	if shift > 4 {
		shift = 4
	}
	win := baseQuarantineWindow * time.Duration(1<<shift)
	if win > maxQuarantineWindow {
		return maxQuarantineWindow
	}
	return win
}

// RecordFailure increments the consecutive failure count for key and returns
// true when the key enters or extends a quarantine window.
func (q *quarantineTracker) RecordFailure(key string) bool {
	if q == nil || key == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.entries[key]
	entry.failures++
	if entry.failures >= quarantineThreshold {
		entry.until = q.now().Add(quarantineWindowForFailures(entry.failures))
		q.entries[key] = entry
		return true
	}
	q.entries[key] = entry
	return false
}

// Clear resets the failure history for key after a successful operation or
// explicit mutation.
func (q *quarantineTracker) Clear(key string) {
	if q == nil || key == "" {
		return
	}
	q.mu.Lock()
	delete(q.entries, key)
	q.mu.Unlock()
}

// Blocked reports whether polling for key is currently suppressed. When the
// quarantine window expires, it consumes the timer to allow a single half-open
// probe attempt through.
func (q *quarantineTracker) Blocked(key string) bool {
	if q == nil || key == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, ok := q.entries[key]
	if !ok || entry.until.IsZero() {
		return false
	}
	if q.now().Before(entry.until) {
		return true
	}
	entry.until = time.Time{}
	q.entries[key] = entry
	return false
}

// Peek reports whether key is currently in an active quarantine window without
// consuming the half-open retry probe.
func (q *quarantineTracker) Peek(key string) bool {
	if q == nil || key == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, ok := q.entries[key]
	if !ok || entry.until.IsZero() {
		return false
	}
	return q.now().Before(entry.until)
}

// classifyWorktreeHealth inspects an isolated session's worktree directory and
// branch state to distinguish healthy, restorable, and unregistered directories.
func classifyWorktreeHealth(repoRoot string, meta worktreeMeta) string {
	if !meta.Isolated || strings.TrimSpace(meta.Directory) == "" {
		return HealthOK
	}
	info, err := os.Stat(meta.Directory)
	if err != nil || !info.IsDir() {
		br := strings.TrimSpace(meta.Branch)
		if br == "" && meta.SessionID != "" {
			br = "hoxi/" + meta.SessionID
		}
		if br != "" && strings.TrimSpace(repoRoot) != "" {
			if _, refErr := runGit(repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+br); refErr == nil {
				return HealthAbsentRestorable
			}
		}
		return HealthUnregistered
	}
	if !hasGitMarker(meta.Directory) {
		return HealthUnregistered
	}
	return HealthOK
}

// detachAndRemoveDir renames dir to a temporary sibling name before removing it
// so the original worktree path is released immediately even on large trees.
func detachAndRemoveDir(dir string) {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "" || cleaned == "." || cleaned == "/" {
		return
	}
	if _, err := os.Stat(cleaned); os.IsNotExist(err) {
		return
	}
	parent := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)
	tempPath := filepath.Join(parent, fmt.Sprintf("%s%s-%d", tempDeletePrefix, base, time.Now().UnixNano()))
	if err := os.Rename(cleaned, tempPath); err == nil {
		_ = os.RemoveAll(tempPath)
		return
	}
	_ = os.RemoveAll(cleaned)
}
