package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const snapshotVersion = 1

// Snapshot is the per-session confinement authority shared with the OpenCode
// plugin. It mirrors Kilo Code's SandboxStore snapshot file.
type Snapshot struct {
	Enabled     bool        `json:"enabled"`
	Network     NetworkMode `json:"network"`
	Workspace   string      `json:"workspace"`
	ProfilePath string      `json:"profilePath"`
	Version     int         `json:"version"`
	UpdatedAt   int64       `json:"updatedAt"`
}

type Manager struct {
	root string
	mu   sync.Mutex
}

func NewManager(root string) *Manager {
	root = strings.TrimSpace(root)
	if root == "" {
		root = DefaultRoot()
	}
	return &Manager{root: root}
}

func (m *Manager) Root() string {
	if m == nil {
		return ""
	}
	return m.root
}

func (m *Manager) Set(sessionID, workspace string, enabled bool, network NetworkMode) (Snapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Snapshot{}, errors.New("sandbox session id is required")
	}
	if network == "" {
		network = NetworkDeny
	}
	if network != NetworkAllow && network != NetworkDeny {
		return Snapshot{}, errors.New("sandbox network mode must be allow or deny")
	}
	if !enabled {
		return Snapshot{}, m.Remove(sessionID)
	}
	profile, err := DefaultProfile(workspace, network, GitMetadataDir(workspace))
	if err != nil {
		return Snapshot{}, err
	}
	text, err := ProfileText(profile)
	if err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	profileDir := filepath.Join(m.root, "profiles")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create sandbox profile directory: %w", err)
	}
	profilePath := filepath.Join(profileDir, hash(sessionID)+".sb")
	if err := writeAtomic(profilePath, []byte(text), 0o600); err != nil {
		return Snapshot{}, fmt.Errorf("write sandbox profile: %w", err)
	}
	snapshot := Snapshot{
		Enabled:     true,
		Network:     network,
		Workspace:   profileWorkspace(profile),
		ProfilePath: profilePath,
		Version:     snapshotVersion,
		UpdatedAt:   time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create sandbox policy directory: %w", err)
	}
	if err := writeAtomic(m.snapshotPath(sessionID), raw, 0o600); err != nil {
		return Snapshot{}, fmt.Errorf("write sandbox policy: %w", err)
	}
	return snapshot, nil
}

func profileWorkspace(profile Profile) string {
	if len(profile.Filesystem.AllowWrite) == 0 {
		return ""
	}
	return profile.Filesystem.AllowWrite[0].Path
}

func (m *Manager) Get(sessionID string) (Snapshot, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || m == nil {
		return Snapshot{}, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, err := os.ReadFile(m.snapshotPath(sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, false, fmt.Errorf("invalid sandbox policy: %w", err)
	}
	if snapshot.Version != snapshotVersion {
		return Snapshot{}, false, fmt.Errorf("unsupported sandbox policy version: %d", snapshot.Version)
	}
	return snapshot, snapshot.Enabled, nil
}

func (m *Manager) Remove(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.Remove(m.snapshotPath(sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Join(m.root, "profiles", hash(sessionID)+".sb")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) snapshotPath(sessionID string) string {
	return filepath.Join(m.root, hash(sessionID)+".json")
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sandbox-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
