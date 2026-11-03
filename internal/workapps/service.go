package workapps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// AuthSource provides the active Hoxi server base URL and optional device token.
type AuthSource interface {
	ServerBaseURL() string
	CurrentDeviceToken() string
}

type Service struct {
	auth       AuthSource
	store      *Store
	httpClient *http.Client
}

func NewService(auth AuthSource, store *Store) *Service {
	if store == nil {
		store = NewStore("")
	}
	return &Service{
		auth:  auth,
		store: store,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *Service) baseURL() string {
	if s.auth != nil {
		if u := strings.TrimRight(strings.TrimSpace(s.auth.ServerBaseURL()), "/"); u != "" {
			return u
		}
	}
	return hoxipaths.ServerBaseURL()
}

func (s *Service) deviceToken() string {
	if s.auth == nil {
		return ""
	}
	return strings.TrimSpace(s.auth.CurrentDeviceToken())
}

// FetchApps fetches the remote Work apps catalog from hoxi.ai and merges
// cachedItems into ~/.hoxi/work-apps.json under a single write lock AFTER the
// network call finishes, avoiding any Lost Update race with SaveState.
func (s *Service) FetchApps(ctx context.Context) (map[string]any, error) {
	targetURL := fmt.Sprintf("%s/api/hoxi/work/apps", s.baseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		localState, _ := s.store.Load()
		return map[string]any{"localState": localState}, nil
	}
	req.Header.Set("Accept", "application/json")
	if tok := s.deviceToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		localState, _ := s.store.Load()
		return map[string]any{"localState": localState}, nil
	}
	defer resp.Body.Close()

	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload)
	if payload == nil {
		payload = map[string]any{}
	}

	var localState map[string]any
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var items any
		if dataMap, ok := payload["data"].(map[string]any); ok && dataMap["items"] != nil {
			items = dataMap["items"]
		} else if payload["items"] != nil {
			items = payload["items"]
		} else if payload["apps"] != nil {
			items = payload["apps"]
		}
		if items != nil {
			localState, _ = s.store.Update(func(st map[string]any) error {
				st["cachedItems"] = items
				return nil
			})
		}
	}
	if localState == nil {
		localState, _ = s.store.Load()
	}
	payload["localState"] = localState
	return payload, nil
}

// SaveState merges patch keys into ~/.hoxi/work-apps.json atomically.
func (s *Service) SaveState(patch map[string]any) error {
	_, err := s.store.Update(func(st map[string]any) error {
		for k, v := range patch {
			st[k] = v
		}
		return nil
	})
	return err
}

// ResolveBrowserProfileKey maps a browser session ID (either a direct
// "workstore_<storeId>" / "workapp_<appId>" scope or a Hoxi session bound in
// sessionStoreBindings) to a sanitized persistent profile key so bound stores
// and unbound app sessions share a stable Chrome login profile.
func (s *Service) ResolveBrowserProfileKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s == nil || s.store == nil {
		return ""
	}
	state, _ := s.store.Load()
	if strings.HasPrefix(sessionID, "workstore_") {
		candidate := strings.TrimSpace(strings.TrimPrefix(sessionID, "workstore_"))
		if isSafeStoreProfileKey(candidate) {
			if mapped := lookupStoreProfileKey(state, candidate, ""); mapped != "" {
				return mapped
			}
			return candidate
		}
		return ""
	}
	if strings.HasPrefix(sessionID, "workapp_") {
		candidate := strings.TrimSpace(strings.TrimPrefix(sessionID, "workapp_"))
		if isSafeStoreProfileKey(candidate) {
			if activeKey := lookupActiveAppStoreProfileKey(state, candidate); activeKey != "" {
				return activeKey
			}
			return "default"
		}
		return ""
	}
	if state == nil {
		return ""
	}
	bindings, ok := state["sessionStoreBindings"].(map[string]any)
	if !ok {
		return ""
	}
	entry, ok := bindings[sessionID].(map[string]any)
	if !ok {
		return ""
	}
	appID, _ := entry["appId"].(string)
	appID = strings.TrimSpace(appID)
	storeID, _ := entry["storeId"].(string)
	storeID = strings.TrimSpace(storeID)
	if isSafeStoreProfileKey(storeID) {
		if mapped := lookupStoreProfileKey(state, storeID, appID); mapped != "" {
			return mapped
		}
		return storeID
	}
	if isSafeStoreProfileKey(appID) {
		if activeKey := lookupActiveAppStoreProfileKey(state, appID); activeKey != "" {
			return activeKey
		}
		return "default"
	}
	return ""
}

func lookupActiveAppStoreProfileKey(state map[string]any, appID string) string {
	if state == nil || !isSafeStoreProfileKey(appID) {
		return ""
	}
	if activeByApp, ok := state["activeStoreIdByApp"].(map[string]any); ok {
		if activeStoreID, _ := activeByApp[appID].(string); isSafeStoreProfileKey(strings.TrimSpace(activeStoreID)) {
			activeStoreID = strings.TrimSpace(activeStoreID)
			if mapped := lookupStoreProfileKey(state, activeStoreID, appID); mapped != "" {
				return mapped
			}
			return activeStoreID
		}
	}
	return ""
}

func lookupStoreProfileKey(state map[string]any, storeID, appID string) string {
	if state == nil || !isSafeStoreProfileKey(storeID) {
		return ""
	}
	storesByApp, ok := state["storesByApp"].(map[string]any)
	if !ok {
		return ""
	}
	checkList := func(raw any) string {
		items, ok := raw.([]any)
		if !ok {
			return ""
		}
		for idx, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if strings.TrimSpace(id) != storeID {
				continue
			}
			if profKey, _ := m["browserProfileKey"].(string); isSafeStoreProfileKey(strings.TrimSpace(profKey)) {
				profKey = strings.TrimSpace(profKey)
				if idx == 0 && profKey == storeID {
					return "default"
				}
				return profKey
			}
			if idx == 0 {
				return "default"
			}
			return storeID
		}
		return ""
	}
	if appID != "" {
		if found := checkList(storesByApp[appID]); found != "" {
			return found
		}
	}
	for _, list := range storesByApp {
		if found := checkList(list); found != "" {
			return found
		}
	}
	return ""
}

func isSafeStoreProfileKey(raw string) bool {
	if raw == "" || raw == "unbound" || len(raw) > 96 {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 && !isAlphaNum {
			return false
		}
		if !isAlphaNum && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return !strings.Contains(raw, "..")
}
