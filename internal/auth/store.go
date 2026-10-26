package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// TokenStore 管理本地 ~/.hoxi/auth.json 文件存储
type TokenStore struct {
	mu       sync.RWMutex
	filePath string
}

func NewTokenStore() *TokenStore {
	filePath := hoxipaths.Join("auth.json")
	if filePath == "" {
		filePath = filepath.Join(".", ".hoxi", "auth.json")
	}
	return &TokenStore{
		filePath: filePath,
	}
}

// Load 读取本地存储的设备凭证
func (s *TokenStore) Load() (*StoredAuth, error) {
	s.mu.RLock()
	data, err := os.ReadFile(s.filePath)
	s.mu.RUnlock()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var stored StoredAuth
	if err := json.Unmarshal(data, &stored); err != nil {
		s.mu.Lock()
		backupPath := s.filePath + ".corrupt." + strconv.FormatInt(time.Now().Unix(), 10)
		_ = os.Rename(s.filePath, backupPath)
		s.mu.Unlock()
		return nil, err
	}

	if stored.DeviceToken == "" {
		return nil, nil
	}

	return &stored, nil
}

// Save 将设备凭证安全原子持久化到磁盘
func (s *TokenStore) Save(auth *StoredAuth) error {
	if auth == nil || auth.DeviceToken == "" {
		return errors.New("cannot save empty auth credentials")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}

	return writeFileAtomic(s.filePath, data, 0o600)
}

// Clear 清理本地凭据（退出登录）
func (s *TokenStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
