package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config is the long-lived tunnel credential issued by GoPanel pairing.
// The token authenticates this desktop to GoPanel; it is written with 0600
// permissions and never returned by the HTTP API.
type Config struct {
	ControllerURL string `json:"controllerUrl"`
	NodeID        uint   `json:"nodeId"`
	ClientID      string `json:"clientId"`
	Token         string `json:"token"`
	Name          string `json:"name"`
}

func (c Config) valid() bool {
	return c.ControllerURL != "" && c.ClientID != "" && c.Token != ""
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
		if len(bytes.TrimSpace(data)) > 0 {
			backupCorruptTunnelFile(s.path)
		}
		return Config{}, fmt.Errorf("read tunnel config: %w", err)
	}
	if !cfg.valid() {
		if len(bytes.TrimSpace(data)) > 0 {
			backupCorruptTunnelFile(s.path)
		}
		return Config{}, os.ErrNotExist
	}
	return cfg, nil
}

func backupCorruptTunnelFile(path string) {
	if path == "" {
		return
	}
	backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UTC().UnixNano())
	_ = os.Rename(path, backup)
}

func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("tunnel config path is unknown")
	}
	if existing, err := os.ReadFile(s.path); err == nil && len(bytes.TrimSpace(existing)) > 0 {
		var prev Config
		if json.Unmarshal(existing, &prev) != nil || !prev.valid() {
			backupCorruptTunnelFile(s.path)
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tunnel-*.json")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Store) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// normalizeControllerURL requires HTTPS for public controllers; plain HTTP is
// accepted only for loopback (local development), matching GoPanel's client.
func normalizeControllerURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("GoPanel URL is invalid")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return "", errors.New("public GoPanel must use HTTPS")
		}
	default:
		return "", errors.New("GoPanel URL must start with https://")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func connectURL(controllerURL string) (string, error) {
	normalized, err := normalizeControllerURL(controllerURL)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(normalized)
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path += "/api/tunnel/connect"
	return parsed.String(), nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
