package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// HoxiCoAuthorTrailer is appended to commits Hoxi creates while git
// attribution is enabled (the default).
const HoxiCoAuthorTrailer = "Co-authored-by: Hoxi <code@hoxi.ai>"

// AttributionFile is the ~/.hoxi state file holding the git attribution
// preference. It is read once at gateway startup and on every change.
const AttributionFile = "git-attribution.json"

// AttributionState is the persisted git attribution preference.
type AttributionState struct {
	Enabled bool `json:"enabled"`
}

var (
	// attributionOff is inverted so the zero value keeps attribution on: tests
	// and callers that never load the preference keep today's behavior.
	attributionOff atomic.Bool
	attributionMu  sync.Mutex
)

// AttributionEnabled reports whether Hoxi-created commits carry the trailer.
func AttributionEnabled() bool { return !attributionOff.Load() }

// SetAttributionEnabled updates the in-memory preference used by commits.
func SetAttributionEnabled(enabled bool) { attributionOff.Store(!enabled) }

// AttributionPath returns ~/.hoxi/git-attribution.json, or "" when the Hoxi
// home is unknown.
func AttributionPath() string { return hoxipaths.Join(AttributionFile) }

func parseAttributionJSON(data []byte) (bool, bool) {
	var stored struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(data, &stored) != nil || stored.Enabled == nil {
		return false, false
	}
	return *stored.Enabled, true
}

func backupCorruptAttributionLocked(path string, data []byte) {
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	backup := fmt.Sprintf("%s.corrupt-%s", path, time.Now().UTC().Format("20060102T150405.000000000"))
	_ = os.Rename(path, backup)
}

// LoadAttribution reads the stored preference. A missing, unreadable or
// malformed file keeps the default (enabled) and backs up any corrupt file.
func LoadAttribution(path string) AttributionState {
	if strings.TrimSpace(path) == "" {
		return AttributionState{Enabled: true}
	}
	attributionMu.Lock()
	defer attributionMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return AttributionState{Enabled: true}
	}
	enabled, ok := parseAttributionJSON(data)
	if !ok {
		backupCorruptAttributionLocked(path, data)
		return AttributionState{Enabled: true}
	}
	return AttributionState{Enabled: enabled}
}

// SaveAttribution atomically persists the preference (0600) and applies it.
func SaveAttribution(path string, enabled bool) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("git attribution settings path unavailable")
	}
	attributionMu.Lock()
	defer attributionMu.Unlock()
	if data, err := os.ReadFile(path); err == nil {
		if _, ok := parseAttributionJSON(data); !ok {
			backupCorruptAttributionLocked(path, data)
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".git-attribution-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(AttributionState{Enabled: enabled}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	SetAttributionEnabled(enabled)
	return nil
}

// withCoAuthor trims msg and appends the Hoxi trailer when enabled, unless the
// message already carries it.
func withCoAuthor(msg string, enabled bool) string {
	msg = strings.TrimSpace(msg)
	if msg == "" || !enabled {
		return msg
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "co-authored-by:") && strings.Contains(lower, "code@hoxi.ai") {
		return msg
	}
	return msg + "\n\n" + HoxiCoAuthorTrailer
}

// appendHoxiCoAuthor applies the current attribution preference to msg.
func appendHoxiCoAuthor(msg string) string {
	return withCoAuthor(msg, AttributionEnabled())
}
