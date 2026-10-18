package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// TokenStore 管理本地 ~/.hoxi/auth.json 文件存储
type TokenStore struct {
	mu       sync.RWMutex
	filePath string
}

func NewTokenStore() *TokenStore {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return &TokenStore{
		filePath: filepath.Join(home, ".hoxi", "auth.json"),
	}
}

// Load 读取本地存储的设备凭证
func (s *TokenStore) Load() (*StoredAuth, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var stored StoredAuth
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}

	if stored.DeviceToken == "" {
		return nil, nil
	}

	return &stored, nil
}

// Save 将设备凭证安全持久化到磁盘
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

	return os.WriteFile(s.filePath, data, 0o600)
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
