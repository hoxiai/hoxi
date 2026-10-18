// Package privacy persists versioned consent for gateway-initiated AI execution.
package privacy

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const Version = "2026-10-18"

// Scope is intentionally not a promise that declining consent disables networking.
const Scope = "AI execution may send prompts, code, files, and session context to configured providers and external CLI tools, subject to their own policies. Declining blocks new AI execution through the Hoxi gateway; it does not stop existing work or disable other network activity such as authentication, model discovery, updates, or browsing."

type Status struct {
	Version  string `json:"version"`
	Accepted bool   `json:"accepted"`
}

type Service struct {
	mu          sync.Mutex
	path        string
	writeFailed bool
}

// New uses ~/.hoxi/privacy.json when statePath is empty. Failure to resolve the
// home directory leaves the service unavailable and therefore denied.
func New(statePath string) *Service {
	if statePath == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			statePath = filepath.Join(home, ".hoxi", "privacy.json")
		}
	}
	return &Service{path: statePath}
}

// Decode requires an explicit boolean, the current version, and exactly one JSON
// object. The same validation applies to HTTP input and persisted state.
func Decode(r io.Reader) (Status, error) {
	var input struct {
		Version  string `json:"version"`
		Accepted *bool  `json:"accepted"`
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return Status{}, err
	}
	if input.Version != Version || input.Accepted == nil {
		return Status{}, errors.New("current version and explicit accepted boolean required")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Status{}, errors.New("expected exactly one JSON object")
	}
	return Status{Version: Version, Accepted: *input.Accepted}, nil
}

// Status rechecks disk rather than caching acceptance: deleted, unreadable,
// insecure, corrupt, or outdated state must never authorize execution.
func (s *Service) Status() Status {
	denied := Status{Version: Version}
	if s == nil {
		return denied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" || s.writeFailed {
		return denied
	}
	info, err := os.Lstat(s.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return denied
	}
	f, err := os.Open(s.path)
	if err != nil {
		return denied
	}
	defer f.Close()
	status, err := Decode(io.LimitReader(f, 4096))
	if err != nil {
		return denied
	}
	return status
}

func (s *Service) Set(status Status) error {
	if status.Version != Version {
		return errors.New("unsupported privacy version")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeFailed = true
	if s.path == "" {
		return errors.New("privacy state path unavailable")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".privacy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(status); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.writeFailed = false
	return nil
}
