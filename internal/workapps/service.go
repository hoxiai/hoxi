package workapps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
		return map[string]any{"localState": sanitizeStateForClient(localState)}, nil
	}
	req.Header.Set("Accept", "application/json")
	if tok := s.deviceToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		localState, _ := s.store.Load()
		return map[string]any{"localState": sanitizeStateForClient(localState)}, nil
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
	payload["localState"] = sanitizeStateForClient(localState)
	return payload, nil
}

// SaveState validates and merges patch keys into ~/.hoxi/work-apps.json atomically.
func (s *Service) SaveState(patch map[string]any) error {
	if err := ValidateStatePatch(patch); err != nil {
		return err
	}
	_, err := s.store.Update(func(st map[string]any) error {
		if err := restoreRedactedProxies(st, patch); err != nil {
			return err
		}
		for k, v := range patch {
			st[k] = v
		}
		return nil
	})
	return err
}

// ListStores returns credential-scrubbed StoreProfiles across all apps and storeConfigs.
func (s *Service) ListStores() []StoreProfile {
	if s == nil || s.store == nil {
		return nil
	}
	state, _ := s.store.Load()
	rawList := collectStoresFromState(state)
	out := make([]StoreProfile, 0, len(rawList))
	for _, item := range rawList {
		out = append(out, item.PublicCopy())
	}
	return out
}

// GetStore returns the full StoreProfile (including ProxyURL for internal callers) by storeID.
func (s *Service) GetStore(storeID string) (*StoreProfile, bool) {
	storeID = strings.TrimSpace(storeID)
	if s == nil || s.store == nil || !isSafeStoreProfileKey(storeID) {
		return nil, false
	}
	state, _ := s.store.Load()
	return findStoreInState(state, storeID, "")
}

// ResolveSessionStoreProfile returns the bound StoreProfile for sessionID, or nil if unbound.
func (s *Service) ResolveSessionStoreProfile(sessionID string) *StoreProfile {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s == nil || s.store == nil {
		return nil
	}
	state, _ := s.store.Load()
	storeID, appID := resolveSessionStoreTarget(state, sessionID)
	if !isSafeStoreProfileKey(storeID) {
		return nil
	}
	if prof, ok := findStoreInState(state, storeID, appID); ok {
		return prof
	}
	if strings.HasPrefix(sessionID, "workstore_") {
		if storeID == "default" {
			return nil
		}
		for _, item := range collectStoresFromState(state) {
			if item.BrowserProfileKey == storeID {
				cp := item
				return &cp
			}
		}
	}
	profKey := lookupStoreProfileKey(state, storeID, appID)
	if profKey == "" {
		profKey = storeID
	}
	return &StoreProfile{
		ID:                storeID,
		AppID:             appID,
		Platform:          strings.ToLower(appID),
		BrowserProfileKey: profKey,
	}
}

// ForgetSession removes any persisted store binding for a deleted session without
// touching disk when the session had no binding.
func (s *Service) ForgetSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s == nil || s.store == nil {
		return
	}
	state, err := s.store.Load()
	if err != nil {
		return
	}
	bindings, ok := state["sessionStoreBindings"].(map[string]any)
	if !ok {
		return
	}
	if _, exists := bindings[sessionID]; !exists {
		return
	}
	_, _ = s.store.Update(func(cur map[string]any) error {
		if b, ok := cur["sessionStoreBindings"].(map[string]any); ok {
			delete(b, sessionID)
		}
		return nil
	})
}

// FormatSessionStoreContext renders the <hoxi_work_store_profile> system prompt block
// for sessionID when bound to a store, or returns "" when unbound.
func (s *Service) FormatSessionStoreContext(sessionID string) string {
	return s.ResolveSessionStoreProfile(sessionID).FormatPromptBlock()
}

// ResolveBrowserEnvironment resolves and validates the per-store browser profile,
// proxy, timezone, and locale settings for sessionID. It fails closed whenever
// ProxyRequired lacks a proxyURL or stored network/fingerprint fields are invalid.
func (s *Service) ResolveBrowserEnvironment(sessionID string) (BrowserEnvironment, error) {
	profKey := s.ResolveBrowserProfileKey(sessionID)
	prof := s.ResolveSessionStoreProfile(sessionID)
	if prof == nil {
		return BrowserEnvironment{ProfileKey: profKey}, nil
	}
	if profKey == "" {
		profKey = prof.BrowserProfileKey
	}
	if err := ValidateTimezone(prof.Timezone); err != nil {
		return BrowserEnvironment{}, fmt.Errorf("store %q: %w", prof.ID, err)
	}
	if err := ValidateLocale(prof.Locale); err != nil {
		return BrowserEnvironment{}, fmt.Errorf("store %q: %w", prof.ID, err)
	}
	rawProxy := strings.TrimSpace(prof.ProxyURL)
	if rawProxy != "" {
		if _, err := ValidateProxyURL(rawProxy); err != nil {
			return BrowserEnvironment{}, fmt.Errorf("store %q: %w", prof.ID, err)
		}
		if isRedactedProxyPlaceholder(rawProxy) {
			return BrowserEnvironment{}, fmt.Errorf("store %q proxy credentials are missing", prof.ID)
		}
	} else if prof.ProxyRequired {
		return BrowserEnvironment{}, fmt.Errorf("store %q requires an isolated proxy, but no proxyURL is configured", prof.ID)
	}
	return BrowserEnvironment{
		StoreID:       prof.ID,
		ProfileKey:    profKey,
		ProxyURL:      rawProxy,
		RedactedProxy: RedactProxyURL(rawProxy),
		ProxyRequired: prof.ProxyRequired,
		Timezone:      prof.Timezone,
		Locale:        prof.Locale,
	}, nil
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

func resolveSessionStoreTarget(state map[string]any, sessionID string) (storeID, appID string) {
	if strings.HasPrefix(sessionID, "workstore_") {
		candidate := strings.TrimSpace(strings.TrimPrefix(sessionID, "workstore_"))
		if isSafeStoreProfileKey(candidate) {
			return candidate, ""
		}
		return "", ""
	}
	if strings.HasPrefix(sessionID, "workapp_") {
		candidate := strings.TrimSpace(strings.TrimPrefix(sessionID, "workapp_"))
		if isSafeStoreProfileKey(candidate) && state != nil {
			if activeByApp, ok := state["activeStoreIdByApp"].(map[string]any); ok {
				if activeID, _ := activeByApp[candidate].(string); isSafeStoreProfileKey(strings.TrimSpace(activeID)) {
					return strings.TrimSpace(activeID), candidate
				}
			}
		}
		return "", ""
	}
	if state == nil {
		return "", ""
	}
	bindings, ok := state["sessionStoreBindings"].(map[string]any)
	if !ok {
		return "", ""
	}
	entry, ok := bindings[sessionID].(map[string]any)
	if !ok {
		return "", ""
	}
	appID = strings.TrimSpace(stringField(entry, "appId"))
	storeID = strings.TrimSpace(stringField(entry, "storeId"))
	if isSafeStoreProfileKey(storeID) {
		return storeID, appID
	}
	if isSafeStoreProfileKey(appID) {
		if activeByApp, ok := state["activeStoreIdByApp"].(map[string]any); ok {
			if activeID, _ := activeByApp[appID].(string); isSafeStoreProfileKey(strings.TrimSpace(activeID)) {
				return strings.TrimSpace(activeID), appID
			}
		}
	}
	return "", ""
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

func lookupStoreExtraConfig(state map[string]any, storeID string) map[string]any {
	if state == nil || storeID == "" {
		return nil
	}
	cfgs, ok := state["storeConfigs"].(map[string]any)
	if !ok {
		return nil
	}
	extra, _ := cfgs[storeID].(map[string]any)
	return extra
}

func lookupStoreProfileKey(state map[string]any, storeID, appID string) string {
	if state == nil || !isSafeStoreProfileKey(storeID) {
		return ""
	}
	extra := lookupStoreExtraConfig(state, storeID)
	if storesByApp, ok := state["storesByApp"].(map[string]any); ok {
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
				isolated := hasIsolationSettings(m) || hasIsolationSettings(extra)
				profKey := strings.TrimSpace(stringField(m, "browserProfileKey"))
				if profKey == "" {
					profKey = strings.TrimSpace(stringField(extra, "browserProfileKey"))
				}
				if isSafeStoreProfileKey(profKey) {
					if idx == 0 && profKey == storeID && !isolated {
						return "default"
					}
					return profKey
				}
				if idx == 0 && !isolated {
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
	}
	if profKey := strings.TrimSpace(stringField(extra, "browserProfileKey")); isSafeStoreProfileKey(profKey) {
		return profKey
	}
	return ""
}

func collectStoresFromState(state map[string]any) []StoreProfile {
	if state == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []StoreProfile
	if storesByApp, ok := state["storesByApp"].(map[string]any); ok {
		appKeys := make([]string, 0, len(storesByApp))
		for k := range storesByApp {
			appKeys = append(appKeys, k)
		}
		sort.Strings(appKeys)
		for _, appID := range appKeys {
			items, ok := storesByApp[appID].([]any)
			if !ok {
				continue
			}
			for _, rawItem := range items {
				m, ok := rawItem.(map[string]any)
				if !ok {
					continue
				}
				id := strings.TrimSpace(stringField(m, "id"))
				if !isSafeStoreProfileKey(id) || seen[id] {
					continue
				}
				seen[id] = true
				prof := parseStoreProfile(appID, m, lookupStoreExtraConfig(state, id))
				if resolved := lookupStoreProfileKey(state, id, appID); resolved != "" {
					prof.BrowserProfileKey = resolved
				} else if prof.BrowserProfileKey == "" {
					prof.BrowserProfileKey = id
				}
				out = append(out, prof)
			}
		}
	}
	if cfgs, ok := state["storeConfigs"].(map[string]any); ok {
		cfgKeys := make([]string, 0, len(cfgs))
		for k := range cfgs {
			cfgKeys = append(cfgKeys, k)
		}
		sort.Strings(cfgKeys)
		for _, id := range cfgKeys {
			if !isSafeStoreProfileKey(id) || seen[id] {
				continue
			}
			extra, ok := cfgs[id].(map[string]any)
			if !ok {
				continue
			}
			seen[id] = true
			appID := strings.TrimSpace(stringField(extra, "appId", "platform"))
			prof := parseStoreProfile(appID, map[string]any{"id": id}, extra)
			if prof.BrowserProfileKey == "" {
				prof.BrowserProfileKey = id
			}
			out = append(out, prof)
		}
	}
	return out
}

func findStoreInState(state map[string]any, storeID, preferredAppID string) (*StoreProfile, bool) {
	if state == nil || !isSafeStoreProfileKey(storeID) {
		return nil, false
	}
	extra := lookupStoreExtraConfig(state, storeID)
	if storesByApp, ok := state["storesByApp"].(map[string]any); ok {
		searchApp := func(appID string, raw any) (*StoreProfile, bool) {
			items, ok := raw.([]any)
			if !ok {
				return nil, false
			}
			for _, rawItem := range items {
				m, ok := rawItem.(map[string]any)
				if !ok {
					continue
				}
				if strings.TrimSpace(stringField(m, "id")) != storeID {
					continue
				}
				prof := parseStoreProfile(appID, m, extra)
				if resolved := lookupStoreProfileKey(state, storeID, appID); resolved != "" {
					prof.BrowserProfileKey = resolved
				} else if prof.BrowserProfileKey == "" {
					prof.BrowserProfileKey = storeID
				}
				return &prof, true
			}
			return nil, false
		}
		if preferredAppID != "" {
			if prof, ok := searchApp(preferredAppID, storesByApp[preferredAppID]); ok {
				return prof, true
			}
		}
		appKeys := make([]string, 0, len(storesByApp))
		for k := range storesByApp {
			appKeys = append(appKeys, k)
		}
		sort.Strings(appKeys)
		for _, appID := range appKeys {
			if prof, ok := searchApp(appID, storesByApp[appID]); ok {
				return prof, true
			}
		}
	}
	if extra != nil {
		appID := strings.TrimSpace(stringField(extra, "appId", "platform"))
		if appID == "" {
			appID = preferredAppID
		}
		prof := parseStoreProfile(appID, map[string]any{"id": storeID}, extra)
		if prof.BrowserProfileKey == "" {
			prof.BrowserProfileKey = storeID
		}
		return &prof, true
	}
	return nil, false
}

func sanitizeStateForClient(state map[string]any) map[string]any {
	if state == nil {
		return nil
	}
	out := make(map[string]any, len(state))
	for k, v := range state {
		out[k] = v
	}
	if storesByApp, ok := state["storesByApp"].(map[string]any); ok {
		clonedApps := make(map[string]any, len(storesByApp))
		for appID, rawList := range storesByApp {
			items, ok := rawList.([]any)
			if !ok {
				clonedApps[appID] = rawList
				continue
			}
			clonedItems := make([]any, len(items))
			for i, rawItem := range items {
				if m, ok := rawItem.(map[string]any); ok {
					clonedItems[i] = sanitizeStoreMapProxy(m)
				} else {
					clonedItems[i] = rawItem
				}
			}
			clonedApps[appID] = clonedItems
		}
		out["storesByApp"] = clonedApps
	}
	if storeConfigs, ok := state["storeConfigs"].(map[string]any); ok {
		clonedCfgs := make(map[string]any, len(storeConfigs))
		for storeID, rawCfg := range storeConfigs {
			if m, ok := rawCfg.(map[string]any); ok {
				clonedCfgs[storeID] = sanitizeStoreMapProxy(m)
			} else {
				clonedCfgs[storeID] = rawCfg
			}
		}
		out["storeConfigs"] = clonedCfgs
	}
	return out
}

func sanitizeStoreMapProxy(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	for _, key := range []string{"proxyURL", "proxyUrl", "proxy"} {
		if raw, ok := cp[key].(string); ok && strings.TrimSpace(raw) != "" {
			cp[key] = RedactProxyURL(raw)
		}
	}
	return cp
}

func restoreRedactedProxies(existing, patch map[string]any) error {
	restoreInMap := func(storeID string, m map[string]any) error {
		for _, key := range []string{"proxyURL", "proxyUrl", "proxy"} {
			raw, ok := m[key].(string)
			if !ok || !isRedactedProxyPlaceholder(raw) {
				continue
			}
			prev, found := findStoreInState(existing, storeID, "")
			if !found || prev.ProxyURL == "" || RedactProxyURL(prev.ProxyURL) != strings.TrimSpace(raw) {
				return fmt.Errorf("%w: proxy credentials must be re-entered when setting or changing proxy host", ErrInvalidStoreConfig)
			}
			m[key] = prev.ProxyURL
		}
		return nil
	}
	if storesByApp, ok := patch["storesByApp"].(map[string]any); ok {
		for _, rawList := range storesByApp {
			items, _ := rawList.([]any)
			for _, rawItem := range items {
				if m, ok := rawItem.(map[string]any); ok {
					if err := restoreInMap(strings.TrimSpace(stringField(m, "id")), m); err != nil {
						return err
					}
				}
			}
		}
	}
	if storeConfigs, ok := patch["storeConfigs"].(map[string]any); ok {
		for storeID, rawCfg := range storeConfigs {
			if m, ok := rawCfg.(map[string]any); ok {
				if err := restoreInMap(strings.TrimSpace(storeID), m); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
