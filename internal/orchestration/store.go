package orchestration

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// maxPlanFileSize caps how much of a persisted plan file is read. Plan prompts
// are already limited to 120k characters, so a plan that exceeds this is
// corrupt rather than legitimate.
const maxPlanFileSize = 2 << 20

// Store persists convoy plans as one atomic JSON file per plan. Plans contain
// prompts and session references, so the directory is private to the user.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store {
	return &Store{dir: strings.TrimSpace(dir)}
}

func (s *Store) Save(plan Plan) error {
	if s == nil || s.dir == "" {
		return errors.New("orchestration store is not configured")
	}
	if !validID(plan.ID) {
		return errors.New("invalid plan id")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".plan-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path(plan.ID)); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (s *Store) Load() ([]Plan, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file, err := os.Open(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxPlanFileSize+1))
		_ = file.Close()
		if readErr != nil || len(data) > maxPlanFileSize {
			continue
		}
		var plan Plan
		if json.Unmarshal(data, &plan) != nil || !validID(plan.ID) {
			continue
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (s *Store) Delete(id string) error {
	if s == nil || s.dir == "" || !validID(id) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}
