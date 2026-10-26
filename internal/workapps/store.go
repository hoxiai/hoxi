package workapps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Store manages atomic persistence for ~/.hoxi/work-apps.json.
type Store struct {
	mu       sync.RWMutex
	filePath string
}

func NewStore(filePath string) *Store {
	if filePath == "" {
		filePath = hoxipaths.Join("work-apps.json")
	}
	if filePath == "" {
		filePath = filepath.Join(".", ".hoxi", "work-apps.json")
	}
	return &Store{filePath: filePath}
}

func (s *Store) Load() (map[string]any, error) {
	s.mu.RLock()
	data, err := os.ReadFile(s.filePath)
	s.mu.RUnlock()
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}

	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		s.mu.Lock()
		backupPath := s.filePath + ".corrupt." + strconv.FormatInt(time.Now().Unix(), 10)
		_ = os.Rename(s.filePath, backupPath)
		s.mu.Unlock()
		return map[string]any{}, nil
	}
	if state == nil {
		state = map[string]any{}
	}
	return state, nil
}

// Update executes fn under a single write lock against the freshest on-disk
// state and atomically writes the updated state via temp file + rename.
func (s *Store) Update(fn func(state map[string]any) error) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := map[string]any{}
	if data, err := os.ReadFile(s.filePath); err == nil {
		if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
			backupPath := s.filePath + ".corrupt." + strconv.FormatInt(time.Now().Unix(), 10)
			_ = os.Rename(s.filePath, backupPath)
			state = map[string]any{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if state == nil {
		state = map[string]any{}
	}

	if err := fn(state); err != nil {
		return nil, err
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(s.filePath, encoded, 0o600); err != nil {
		return nil, err
	}
	return state, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
