// Package cloudflared supervises a Cloudflare Tunnel connector so the mobile app
// can reach this desktop through Cloudflare when it is not on the local network.
//
// The connector token is the tunnel's credential: it is written 0600 and never
// returned by the HTTP API. Cloudflare terminates TLS at its edge, so this path
// only becomes private once an end-to-end encrypted envelope is added (see
// docs/remote-access-e2e-contract.md).
package cloudflared

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Config is the persisted Cloudflare Tunnel connector settings.
type Config struct {
	Enabled  bool   `json:"enabled"`
	Token    string `json:"token"`
	Hostname string `json:"hostname,omitempty"`
}

// Store persists Config atomically (temp file + rename) under a mutex.
type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return Config{}, os.ErrNotExist
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("cloudflared config path is unknown")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cloudflared-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
