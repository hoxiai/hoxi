package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// TokenStore 管理本地 ~/.hoxi/auth.json 文件存储
type TokenStore struct {
	mu       sync.RWMutex
	filePath string
}

type persistedAuthFile struct {
	DeviceToken    string                 `json:"deviceToken,omitempty"`
	User           UserProfile            `json:"user,omitempty"`
	ExpiresAt      string                 `json:"expiresAt,omitempty"`
	LastVerifiedAt time.Time              `json:"lastVerifiedAt,omitempty"`
	ServerBaseURL  string                 `json:"serverBaseUrl,omitempty"`
	ByServer       map[string]*StoredAuth `json:"byServer,omitempty"`
}

func normalizeServerBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return strings.TrimRight(hoxipaths.DefaultServerBaseURL, "/")
	}
	return trimmed
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

func (s *TokenStore) readPersistedLocked() (*persistedAuthFile, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var file persistedAuthFile
	if err := json.Unmarshal(data, &file); err != nil {
		backupPath := s.filePath + ".corrupt." + strconv.FormatInt(time.Now().Unix(), 10)
		_ = os.Rename(s.filePath, backupPath)
		return nil, err
	}
	return &file, nil
}

// Load 读取本地存储的设备凭证
func (s *TokenStore) Load() (*StoredAuth, error) {
	s.mu.Lock()
	file, err := s.readPersistedLocked()
	s.mu.Unlock()
	if err != nil || file == nil {
		return nil, err
	}
	if file.DeviceToken != "" {
		return &StoredAuth{
			DeviceToken:    file.DeviceToken,
			User:           file.User,
			ExpiresAt:      file.ExpiresAt,
			LastVerifiedAt: file.LastVerifiedAt,
			ServerBaseURL:  file.ServerBaseURL,
		}, nil
	}
	for _, entry := range file.ByServer {
		if entry != nil && entry.DeviceToken != "" {
			copied := *entry
			return &copied, nil
		}
	}
	return nil, nil
}

// LoadForServer 读取指定服务端的设备凭证，避免 dev (127.0.0.1:5400) 与正式环境 (https://hoxi.ai) 互相串用或误删。
func (s *TokenStore) LoadForServer(baseURL string) (*StoredAuth, error) {
	normBase := normalizeServerBaseURL(baseURL)
	defaultBase := normalizeServerBaseURL(hoxipaths.DefaultServerBaseURL)

	s.mu.Lock()
	file, err := s.readPersistedLocked()
	s.mu.Unlock()
	if err != nil || file == nil {
		return nil, err
	}

	if entry := file.ByServer[normBase]; entry != nil && entry.DeviceToken != "" {
		copied := *entry
		if copied.ServerBaseURL == "" {
			copied.ServerBaseURL = normBase
		}
		return &copied, nil
	}

	if file.DeviceToken == "" {
		return nil, nil
	}
	topServer := strings.TrimRight(strings.TrimSpace(file.ServerBaseURL), "/")
	if (topServer != "" && topServer == normBase) || (topServer == "" && normBase == defaultBase) {
		return &StoredAuth{
			DeviceToken:    file.DeviceToken,
			User:           file.User,
			ExpiresAt:      file.ExpiresAt,
			LastVerifiedAt: file.LastVerifiedAt,
			ServerBaseURL:  normBase,
		}, nil
	}
	return nil, nil
}

// Save 将设备凭证安全原子持久化到磁盘
func (s *TokenStore) Save(auth *StoredAuth) error {
	baseURL := ""
	if auth != nil {
		baseURL = auth.ServerBaseURL
	}
	return s.SaveForServer(baseURL, auth)
}

// SaveForServer 按服务端隔离保存设备凭证，同时保留其他服务端的已登录凭证。
func (s *TokenStore) SaveForServer(baseURL string, auth *StoredAuth) error {
	if auth == nil || auth.DeviceToken == "" {
		return errors.New("cannot save empty auth credentials")
	}

	normBase := normalizeServerBaseURL(baseURL)
	defaultBase := normalizeServerBaseURL(hoxipaths.DefaultServerBaseURL)

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var file persistedAuthFile
	if existing, err := os.ReadFile(s.filePath); err == nil {
		_ = json.Unmarshal(existing, &file)
	}
	if file.ByServer == nil {
		file.ByServer = make(map[string]*StoredAuth)
	}
	// Migrate legacy top-level production credential into ByServer before updating.
	if file.DeviceToken != "" {
		existingTopServer := normalizeServerBaseURL(file.ServerBaseURL)
		if _, ok := file.ByServer[existingTopServer]; !ok {
			file.ByServer[existingTopServer] = &StoredAuth{
				DeviceToken:    file.DeviceToken,
				User:           file.User,
				ExpiresAt:      file.ExpiresAt,
				LastVerifiedAt: file.LastVerifiedAt,
				ServerBaseURL:  existingTopServer,
			}
		}
	}

	entry := *auth
	entry.ServerBaseURL = normBase
	file.ByServer[normBase] = &entry

	topServer := strings.TrimRight(strings.TrimSpace(file.ServerBaseURL), "/")
	if normBase == defaultBase || file.DeviceToken == "" || topServer == normBase {
		file.DeviceToken = entry.DeviceToken
		file.User = entry.User
		file.ExpiresAt = entry.ExpiresAt
		file.LastVerifiedAt = entry.LastVerifiedAt
		file.ServerBaseURL = normBase
	}

	data, err := json.MarshalIndent(&file, "", "  ")
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

// ClearForServer 仅清理指定服务端（或指定 token）的本地凭据，不误删其他环境（如正式环境 https://hoxi.ai）的凭据。
func (s *TokenStore) ClearForServer(baseURL string, token ...string) error {
	normBase := normalizeServerBaseURL(baseURL)
	defaultBase := normalizeServerBaseURL(hoxipaths.DefaultServerBaseURL)
	targetToken := ""
	if len(token) > 0 {
		targetToken = strings.TrimSpace(token[0])
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var file persistedAuthFile
	if err := json.Unmarshal(raw, &file); err != nil {
		if rmErr := os.Remove(s.filePath); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
		return nil
	}

	if file.ByServer != nil {
		delete(file.ByServer, normBase)
		if targetToken != "" {
			for k, v := range file.ByServer {
				if v != nil && v.DeviceToken == targetToken {
					delete(file.ByServer, k)
				}
			}
		}
	}

	topServer := strings.TrimRight(strings.TrimSpace(file.ServerBaseURL), "/")
	if (topServer != "" && topServer == normBase) ||
		(topServer == "" && normBase == defaultBase) ||
		(targetToken != "" && file.DeviceToken == targetToken) {
		file.DeviceToken = ""
		file.User = UserProfile{}
		file.ExpiresAt = ""
		file.LastVerifiedAt = time.Time{}
		file.ServerBaseURL = ""
	} else if file.DeviceToken != "" && topServer == "" {
		// Preserve legacy top-level production credential when a non-default server clears its own state.
		file.ServerBaseURL = defaultBase
		if file.ByServer == nil {
			file.ByServer = make(map[string]*StoredAuth)
		}
		file.ByServer[defaultBase] = &StoredAuth{
			DeviceToken:    file.DeviceToken,
			User:           file.User,
			ExpiresAt:      file.ExpiresAt,
			LastVerifiedAt: file.LastVerifiedAt,
			ServerBaseURL:  defaultBase,
		}
	}

	if file.DeviceToken == "" {
		if fallback := file.ByServer[defaultBase]; fallback != nil && fallback.DeviceToken != "" {
			file.DeviceToken = fallback.DeviceToken
			file.User = fallback.User
			file.ExpiresAt = fallback.ExpiresAt
			file.LastVerifiedAt = fallback.LastVerifiedAt
			file.ServerBaseURL = fallback.ServerBaseURL
		} else {
			for k, candidate := range file.ByServer {
				if candidate != nil && candidate.DeviceToken != "" {
					file.DeviceToken = candidate.DeviceToken
					file.User = candidate.User
					file.ExpiresAt = candidate.ExpiresAt
					file.LastVerifiedAt = candidate.LastVerifiedAt
					file.ServerBaseURL = k
					break
				}
			}
		}
	}

	if file.DeviceToken == "" && len(file.ByServer) == 0 {
		if rmErr := os.Remove(s.filePath); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
		return nil
	}

	data, err := json.MarshalIndent(&file, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.filePath, data, 0o600)
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
