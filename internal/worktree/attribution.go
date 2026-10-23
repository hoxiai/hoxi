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
// attribution applies to the repository being committed.
const HoxiCoAuthorTrailer = "Co-authored-by: Hoxi <code@hoxi.ai>"

// AttributionFile is the ~/.hoxi state file holding the git attribution
// preference. It is read once at gateway startup and on every change.
const AttributionFile = "git-attribution.json"

// AttributionMode decides which repositories get the Hoxi co-author trailer.
type AttributionMode string

const (
	// AttributionAuto is the default: only Hoxi-created session worktrees
	// (<project>/.hoxi/worktrees/<session>) carry the trailer. Commits made
	// directly in the user's own repository stay untouched, so work intended
	// for an upstream never gains an unexpected trailer.
	AttributionAuto AttributionMode = "auto"
	// AttributionAlways adds the trailer to every commit Hoxi creates.
	AttributionAlways AttributionMode = "always"
	// AttributionNever disables the trailer everywhere.
	AttributionNever AttributionMode = "never"
)

// AttributionState is the persisted git attribution preference. Enabled is a
// compatibility mirror of the pre-mode toggle: it reports whether a commit
// without repository context would carry the trailer.
type AttributionState struct {
	Mode    AttributionMode `json:"mode"`
	Enabled bool            `json:"enabled"`
}

const (
	modeAuto int32 = iota
	modeAlways
	modeNever
)

var (
	// The zero value is AttributionAuto, so tests and callers that never load
	// the preference get the default (session worktrees only).
	attributionMode atomic.Int32
	attributionMu   sync.Mutex
)

// ParseAttributionMode validates a stored or submitted mode.
func ParseAttributionMode(raw string) (AttributionMode, bool) {
	switch AttributionMode(strings.ToLower(strings.TrimSpace(raw))) {
	case AttributionAuto:
		return AttributionAuto, true
	case AttributionAlways:
		return AttributionAlways, true
	case AttributionNever:
		return AttributionNever, true
	}
	return "", false
}

// CurrentAttributionMode reports the in-memory preference.
func CurrentAttributionMode() AttributionMode {
	switch attributionMode.Load() {
	case modeAlways:
		return AttributionAlways
	case modeNever:
		return AttributionNever
	}
	return AttributionAuto
}

// SetAttributionMode updates the in-memory preference used by commits.
func SetAttributionMode(mode AttributionMode) {
	switch mode {
	case AttributionAlways:
		attributionMode.Store(modeAlways)
	case AttributionNever:
		attributionMode.Store(modeNever)
	default:
		attributionMode.Store(modeAuto)
	}
}

// AttributionEnabled reports whether a commit without repository context
// carries the trailer. AttributionAuto counts as enabled here because callers
// that omit a directory are Hoxi-internal (snapshots, merges of session
// branches). Prefer AttributionEnabledFor for real repository paths.
func AttributionEnabled() bool { return CurrentAttributionMode() != AttributionNever }

// SetAttributionEnabled maps the legacy boolean toggle onto the mode:
// true means "always", false means "never".
func SetAttributionEnabled(enabled bool) {
	if enabled {
		SetAttributionMode(AttributionAlways)
		return
	}
	SetAttributionMode(AttributionNever)
}

// IsSessionWorktree reports whether dir lives inside a Hoxi-created session
// worktree, including a nested repository within one
// (<project>/.hoxi/worktrees/<session-id>/...).
func IsSessionWorktree(dir string) bool {
	slash := filepath.ToSlash(filepath.Clean(strings.TrimSpace(dir)))
	if slash == "" || slash == "." {
		return false
	}
	marker := "/" + hoxipaths.ProjectMemoryDirName + "/worktrees/"
	idx := strings.Index(slash, marker)
	if idx < 0 {
		return false
	}
	rest := strings.Trim(slash[idx+len(marker):], "/")
	if rest == "" {
		return false
	}
	first := rest
	if cut := strings.IndexByte(rest, '/'); cut >= 0 {
		first = rest[:cut]
	}
	return first != "" && first != "." && first != ".."
}

// AttributionEnabledFor resolves the preference for one repository directory.
// Under AttributionAuto only Hoxi session worktrees opt in.
func AttributionEnabledFor(dir string) bool {
	switch CurrentAttributionMode() {
	case AttributionAlways:
		return true
	case AttributionNever:
		return false
	}
	return IsSessionWorktree(dir)
}

// AttributionPath returns ~/.hoxi/git-attribution.json, or "" when the Hoxi
// home is unknown.
func AttributionPath() string { return hoxipaths.Join(AttributionFile) }

// parseAttributionJSON reads the current mode, or the legacy enabled boolean
// written before modes existed. A missing field is reported as not ok.
func parseAttributionJSON(data []byte) (AttributionMode, bool) {
	var stored struct {
		Mode    *string `json:"mode"`
		Enabled *bool   `json:"enabled"`
	}
	if json.Unmarshal(data, &stored) != nil {
		return "", false
	}
	if stored.Mode != nil {
		return ParseAttributionMode(*stored.Mode)
	}
	if stored.Enabled != nil {
		if *stored.Enabled {
			return AttributionAlways, true
		}
		return AttributionNever, true
	}
	return "", false
}

func backupCorruptAttributionLocked(path string, data []byte) {
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	backup := fmt.Sprintf("%s.corrupt-%s", path, time.Now().UTC().Format("20060102T150405.000000000"))
	_ = os.Rename(path, backup)
}

// LoadAttribution reads the stored preference. A missing, unreadable or
// malformed file keeps the default (auto) and backs up any corrupt file.
func LoadAttribution(path string) AttributionState {
	if strings.TrimSpace(path) == "" {
		return AttributionState{Mode: AttributionAuto, Enabled: true}
	}
	attributionMu.Lock()
	defer attributionMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return AttributionState{Mode: AttributionAuto, Enabled: true}
	}
	mode, ok := parseAttributionJSON(data)
	if !ok {
		backupCorruptAttributionLocked(path, data)
		return AttributionState{Mode: AttributionAuto, Enabled: true}
	}
	return AttributionState{Mode: mode, Enabled: mode != AttributionNever}
}

// SaveAttribution atomically persists the preference (0600) and applies it.
func SaveAttribution(path string, mode AttributionMode) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("git attribution settings path unavailable")
	}
	if _, ok := ParseAttributionMode(string(mode)); !ok {
		return errors.New("unsupported git attribution mode")
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
	// Enabled mirrors the pre-mode toggle so an older Hoxi build reading this
	// file sees the closest behaviour: never -> off, auto/always -> on.
	if err := json.NewEncoder(f).Encode(AttributionState{Mode: mode, Enabled: mode != AttributionNever}); err != nil {
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
	SetAttributionMode(mode)
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

// appendHoxiCoAuthor applies the current preference without repository
// context, which resolves to enabled for Hoxi-internal commits.
func appendHoxiCoAuthor(msg string) string {
	return withCoAuthor(msg, AttributionEnabled())
}

// appendHoxiCoAuthorFor applies the preference to a commit made in dir.
func appendHoxiCoAuthorFor(dir, msg string) string {
	return withCoAuthor(msg, AttributionEnabledFor(dir))
}
