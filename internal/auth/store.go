package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

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

func (s *TokenStore) workAppsPath() string {
	dir := filepath.Dir(s.filePath)
	return filepath.Join(dir, "work-apps.json")
}

// LoadWorkState 读取本地持久化的工作模式应用安装、自定义应用、MCP 连接及技能绑定状态 (~/.hoxi/work-apps.json)
func (s *TokenStore) LoadWorkState() (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.workAppsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return map[string]any{}, nil
	}
	if state == nil {
		state = map[string]any{}
	}
	return state, nil
}

// SaveWorkState 保存工作模式应用安装、自定义应用、MCP 连接及技能绑定状态 (~/.hoxi/work-apps.json)
func (s *TokenStore) SaveWorkState(state map[string]any) error {
	if state == nil {
		state = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.workAppsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
