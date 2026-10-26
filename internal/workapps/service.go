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
