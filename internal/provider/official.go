package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// AuthSource exposes the signed-in Hoxi account so the provider plane can
// provision the official gateway channel without importing internal/auth.
// It mirrors the tool / workapp auth seams.
type AuthSource interface {
	CurrentDeviceToken() string
	DeviceTokenSnapshot() (token, baseURL string)
	InvalidateDeviceToken(token, baseURL string)
	WithCurrentDeviceToken(token, baseURL string, commit func() error) (bool, error)
}

// SetAuthSource wires the desktop account used by the official gateway connect
// flow. When unset the official routes answer 401.
func (s *Service) SetAuthSource(a AuthSource) {
	signedIn := a != nil && a.CurrentDeviceToken() != ""
	s.authMu.Lock()
	s.authSource = a
	s.authMu.Unlock()
	s.officialBlocked.Store(!signedIn)
	if signedIn {
		s.migrateLegacyOfficialProvider()
	}
}

// migrateLegacyOfficialProvider repairs the upgrade path where the official
// channel was persisted under the shared "ainode" provider id before it got a
// dedicated id. Without this, the model picker still shows the account catalog
// while the engine answers "Model not found: hoxi-official/<model>" because the
// provider never reached ~/.hoxi/models.json.
//
// It only runs while the account is signed in, never overwrites an existing
// official provider, and only renames a provider that still carries the
// official display name, so a user-defined "ainode" gateway is left untouched.
func (s *Service) migrateLegacyOfficialProvider() {
	s.officialMu.Lock()
	defer s.officialMu.Unlock()

	s.configPersistMu.Lock()
	doc := s.loadHoxiConfigDoc()
	providers, _ := doc["provider"].(map[string]interface{})
	legacy, ok := providers["ainode"].(map[string]interface{})
	if !ok {
		s.configPersistMu.Unlock()
		return
	}
	if _, exists := providers[officialProviderID]; exists {
		s.configPersistMu.Unlock()
		return
	}
	if name, _ := legacy["name"].(string); strings.TrimSpace(name) != officialProviderName {
		s.configPersistMu.Unlock()
		return
	}

	migrated := make(map[string]interface{}, len(legacy))
	for k, v := range legacy {
		migrated[k] = v
	}
	if npm, _ := migrated["npm"].(string); strings.TrimSpace(npm) == "" {
		migrated["npm"] = "@ai-sdk/openai-compatible"
	}
	providers[officialProviderID] = migrated
	delete(providers, "ainode")
	doc["provider"] = providers
	persistErr := s.persistHoxiConfigFiles(doc)
	s.configPersistMu.Unlock()
	if persistErr != nil {
		return
	}

	if key := providerAPIKey(migrated); key != "" {
		_ = s.persistEngineProviderAuthMap(officialProviderID, map[string]interface{}{"type": "api", "key": key})
	}
	s.officialBlocked.Store(false)
	s.reloadOpenCodeGlobalConfig()
}

func providerAPIKey(provider map[string]interface{}) string {
	opts, _ := provider["options"].(map[string]interface{})
	key, _ := opts["apiKey"].(string)
	return strings.TrimSpace(key)
}

const (
	officialProviderID         = "hoxi-official"
	officialProviderName       = "Hoxi Official"
	officialPointsPerUsd       = 10_000
	officialSiteRequestTimeout = 12 * time.Second
)

type officialModelView struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Vendor           string  `json:"vendor"`
	BillingMode      string  `json:"billingMode"`
	InputPointsPerM  float64 `json:"inputPointsPerM"`
	OutputPointsPerM float64 `json:"outputPointsPerM"`
	PerCallPoints    float64 `json:"perCallPoints"`
}

type officialModelsResponse struct {
	Authenticated    bool                `json:"authenticated"`
	GatewayAvailable bool                `json:"gatewayAvailable"`
	BaseURL          string              `json:"baseUrl"`
	Models           []officialModelView `json:"models"`
	Error            string              `json:"error,omitempty"`
}

type officialConnectResponse struct {
	Authenticated bool                `json:"authenticated"`
	ProviderID    string              `json:"providerId"`
	BaseURL       string              `json:"baseUrl"`
	ModelCount    int                 `json:"modelCount"`
	Models        []officialModelView `json:"models"`
}

func (s *Service) officialAuth() (AuthSource, string, string) {
	s.authMu.RLock()
	auth := s.authSource
	s.authMu.RUnlock()
	if auth == nil {
		return nil, "", ""
	}
	token, baseURL := auth.DeviceTokenSnapshot()
	site := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if site == "" {
		return nil, "", ""
	}
	return auth, site, strings.TrimSpace(token)
}

// handleOfficialModels previews the models the signed-in Hoxi account may call
// on the official gateway channel, resolved through the tenant path.
func (s *Service) handleOfficialModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	auth, site, token := s.officialAuth()
	if token == "" {
		writeJSON(w, http.StatusOK, officialModelsResponse{Authenticated: false, Models: []officialModelView{}})
		return
	}

	status, payload, err := s.fetchOfficialSiteJSON(r.Context(), http.MethodGet, site+"/api/hoxi/member/models", token, nil)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	if status == http.StatusUnauthorized {
		if auth != nil {
			auth.InvalidateDeviceToken(token, site)
		}
		writeJSON(w, http.StatusOK, officialModelsResponse{Authenticated: false, Models: []officialModelView{}})
		return
	}
	if status < 200 || status >= 300 {
		writeJSONError(w, http.StatusBadGateway, "official model catalog unavailable")
		return
	}

	baseURL := officialGatewayBaseURL(payload, site)
	if baseURL == "" {
		baseURL = deriveGatewayAPIBaseURLFromSite(site)
	}
	models, errMsg := parseOfficialModels(payload)
	writeJSON(w, http.StatusOK, officialModelsResponse{
		Authenticated:    true,
		GatewayAvailable: baseURL != "",
		BaseURL:          baseURL,
		Models:           models,
		Error:            errMsg,
	})
}

// handleOfficialConnect provisions (or refreshes) the official gateway provider
// using the desktop account device token, without a browser OAuth round-trip.
func (s *Service) handleOfficialConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	auth, site, token := s.officialAuth()
	if token == "" {
		writeJSONError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	baseURL, payload, status, err := s.resolveOfficialGateway(r.Context(), site, token)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	if status == http.StatusUnauthorized {
		if auth != nil {
			auth.InvalidateDeviceToken(token, site)
		}
		writeJSONError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	models, catalogErr := parseOfficialModels(payload)
	if status < 200 || status >= 300 || catalogErr != "" {
		writeJSONError(w, http.StatusBadGateway, "official model catalog unavailable")
		return
	}
	apiKey, keyStatus := s.fetchOrMintOfficialMemberKey(r.Context(), site, token)
	if keyStatus == http.StatusUnauthorized {
		if auth != nil {
			auth.InvalidateDeviceToken(token, site)
		}
		writeJSONError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	if apiKey == "" {
		writeJSONError(w, http.StatusBadGateway, "official channel credential unavailable")
		return
	}

	committed, persistErr := auth.WithCurrentDeviceToken(token, site, func() error {
		return s.persistOfficialProvider(baseURL, apiKey, models)
	})
	if !committed {
		writeJSONError(w, http.StatusUnauthorized, "account changed during official connection")
		return
	}
	if persistErr != nil {
		writeJSONError(w, http.StatusInternalServerError, persistErr.Error())
		return
	}

	writeJSON(w, http.StatusOK, officialConnectResponse{
		Authenticated: true,
		ProviderID:    officialProviderID,
		BaseURL:       baseURL,
		ModelCount:    len(models),
		Models:        models,
	})
}

// ClearOfficialProvider removes persisted hoxi-official credentials and models
// from ~/.hoxi/models.json and engine auth.json when the desktop account signs
// out or its device token is revoked.
func (s *Service) ClearOfficialProvider() error {
	s.officialMu.Lock()
	defer s.officialMu.Unlock()
	// Remain blocked on any cleanup error, including across retries.
	s.officialBlocked.Store(true)
	authErr := s.removeEngineProviderAuth(officialProviderID)

	configErr := s.clearOfficialConfig()
	s.reloadOpenCodeGlobalConfig()
	return errors.Join(authErr, configErr)
}

func (s *Service) clearOfficialConfig() error {
	s.configPersistMu.Lock()
	defer s.configPersistMu.Unlock()
	for _, path := range []string{userHoxiModelsPath(), s.workspaceHoxiModelsPath()} {
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(bytes.TrimSpace(raw)) > 0 && readJSONMapFile(path) == nil {
			return fmt.Errorf("cannot clear official provider from invalid config %s", path)
		}
	}
	doc := s.loadHoxiConfigDoc()
	hoxiMap, _ := doc["provider"].(map[string]interface{})
	if _, exists := hoxiMap[officialProviderID]; !exists {
		return nil
	}
	delete(hoxiMap, officialProviderID)
	doc["provider"] = hoxiMap
	return s.persistHoxiConfigFiles(doc)
}

func (s *Service) fetchOrMintOfficialMemberKey(ctx context.Context, site, token string) (string, int) {
	memberKeysURL := site + "/api/hoxi/member/keys"
	status, memberResp, err := s.fetchOfficialSiteJSON(ctx, http.MethodGet, memberKeysURL, token, nil)
	if err != nil {
		return "", 0
	}
	if status == http.StatusUnauthorized {
		return "", http.StatusUnauthorized
	}
	if status < 200 || status >= 300 {
		return "", status
	}
	if key := extractRawAPIKeyFromPayload(memberResp); key != "" {
		return key, http.StatusOK
	}
	if keyID := extractFirstActiveKeyID(memberResp); keyID > 0 {
		st, revealResp, revErr := s.fetchOfficialSiteJSON(ctx, http.MethodPost, memberKeysURL, token, map[string]any{"action": "reveal", "id": keyID})
		if revErr == nil && st == http.StatusUnauthorized {
			return "", http.StatusUnauthorized
		}
		if revErr == nil && st >= 200 && st < 300 {
			if key := extractRawAPIKeyFromPayload(revealResp); key != "" {
				return key, http.StatusOK
			}
		}
	}
	st, createResp, createErr := s.fetchOfficialSiteJSON(ctx, http.MethodPost, memberKeysURL, token, map[string]any{"name": "Hoxi Desktop"})
	if createErr == nil && st == http.StatusUnauthorized {
		return "", http.StatusUnauthorized
	}
	if createErr == nil && st >= 200 && st < 300 {
		if key := extractRawAPIKeyFromPayload(createResp); key != "" {
			return key, http.StatusOK
		}
	}
	return "", status
}

func (s *Service) persistOfficialProvider(baseURL, apiKey string, models []officialModelView) error {
	s.officialMu.Lock()
	defer s.officialMu.Unlock()
	s.officialBlocked.Store(true)
	resolvedBaseURL := strings.TrimSpace(baseURL)
	resolvedKey := strings.TrimSpace(apiKey)
	if resolvedKey != "" {
		if err := s.persistEngineProviderAuthMap(officialProviderID, map[string]interface{}{"type": "api", "key": resolvedKey}); err != nil {
			return err
		}
	}

	s.configPersistMu.Lock()
	doc := s.loadHoxiConfigDoc()
	hoxiMap, _ := doc["provider"].(map[string]interface{})
	if hoxiMap == nil {
		hoxiMap = map[string]interface{}{}
	}
	existingProv, _ := hoxiMap[officialProviderID].(map[string]interface{})
	nextProv := map[string]interface{}{}
	for k, v := range existingProv {
		nextProv[k] = v
	}
	nextProv["name"] = officialProviderName

	npmPkg, _ := existingProv["npm"].(string)
	if strings.TrimSpace(npmPkg) == "" {
		npmPkg = "@ai-sdk/openai-compatible"
	}
	nextProv["npm"] = npmPkg

	nextOpts := map[string]interface{}{}
	if existingOpts, ok := existingProv["options"].(map[string]interface{}); ok {
		for k, v := range existingOpts {
			nextOpts[k] = v
		}
	}
	nextOpts["baseURL"] = resolvedBaseURL
	if resolvedKey != "" {
		nextOpts["apiKey"] = resolvedKey
	}
	nextProv["options"] = nextOpts

	modelsMap := make(map[string]interface{}, len(models))
	for _, m := range models {
		mid := strings.TrimSpace(m.ID)
		if mid == "" {
			continue
		}
		mName := firstNonEmptyString(m.Name, mid)
		isChat := !isNonChatUtilityModel(mid)
		inputs := []interface{}{"text"}
		if isChat {
			inputs = []interface{}{"text", "image"}
		}
		mEntry := map[string]interface{}{
			"name":       mName,
			"attachment": isChat,
			"reasoning":  isChat,
			"modalities": map[string]interface{}{
				"input":  inputs,
				"output": []interface{}{"text"},
			},
		}
		if m.InputPointsPerM > 0 || m.OutputPointsPerM > 0 {
			mEntry["cost"] = map[string]interface{}{
				"input":  round4(m.InputPointsPerM / officialPointsPerUsd),
				"output": round4(m.OutputPointsPerM / officialPointsPerUsd),
			}
		}
		modelsMap[mid] = mEntry
	}
	nextProv["models"] = modelsMap

	s.normalizeAndStoreMultiKeys(officialProviderID, nextProv)
	hoxiMap[officialProviderID] = nextProv
	doc["provider"] = hoxiMap

	err := s.persistHoxiConfigFiles(doc)
	s.configPersistMu.Unlock()
	if err != nil {
		return err
	}
	s.reloadOpenCodeGlobalConfig()
	s.officialBlocked.Store(false)
	return nil
}

func (s *Service) resolveOfficialGateway(ctx context.Context, site, token string) (string, map[string]any, int, error) {
	status, payload, err := s.fetchOfficialSiteJSON(ctx, http.MethodGet, site+"/api/hoxi/member/models", token, nil)
	if err != nil {
		return "", nil, 0, err
	}
	if status < 200 || status >= 300 {
		return "", payload, status, nil
	}
	if _, catalogErr := parseOfficialModels(payload); catalogErr != "" {
		return "", payload, status, errors.New("official model catalog unavailable")
	}
	baseURL := officialGatewayBaseURL(payload, site)
	if baseURL == "" {
		if st, gw, gwErr := s.fetchOfficialSiteJSON(ctx, http.MethodGet, site+"/api/proxy/gateway-url", token, nil); gwErr == nil && st >= 200 && st < 300 {
			baseURL = normalizeGatewayAPIBaseURL(firstNonEmptyString(gw["url"], gw["baseUrl"]))
		}
	}
	if baseURL == "" {
		baseURL = deriveGatewayAPIBaseURLFromSite(site)
	}
	if baseURL == "" {
		return "", payload, status, errors.New("official gateway endpoint unavailable")
	}
	return baseURL, payload, status, nil
}

func officialGatewayBaseURL(payload map[string]any, site string) string {
	if payload == nil {
		return ""
	}
	raw := firstNonEmptyString(payload["gatewayBaseUrl"], payload["baseUrl"])
	if raw == "" {
		return ""
	}
	if normalized := normalizeGatewayAPIBaseURL(raw); normalized != "" {
		return normalized
	}
	return deriveGatewayAPIBaseURLFromSite(site)
}

func parseOfficialModels(payload map[string]any) ([]officialModelView, string) {
	if payload == nil {
		return []officialModelView{}, "model catalog unavailable"
	}
	items, _ := payload["models"].([]any)
	views := make([]officialModelView, 0, len(items))
	seen := map[string]struct{}{}
	for _, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if enabled, ok := m["enabled"].(bool); ok && !enabled {
			continue
		}
		id := firstNonEmptyString(m["modelName"], m["name"], m["id"])
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		inputUsd := toFloat64(m["inputUsdPerM"], m["inputPrice"], m["priceInput"])
		outputUsd := toFloat64(m["outputUsdPerM"], m["outputPrice"], m["priceOutput"])
		perCallUsd := toFloat64(m["perCallUsd"], m["perCallPrice"])
		views = append(views, officialModelView{
			ID:               id,
			Name:             firstNonEmptyString(m["displayName"], m["label"], id),
			Vendor:           firstNonEmptyString(m["vendor"], m["provider"]),
			BillingMode:      firstNonEmptyString(m["billingMode"], m["pricingMode"], m["type"], "token"),
			InputPointsPerM:  officialPointsFrom(m, "inputPointsPerM", inputUsd),
			OutputPointsPerM: officialPointsFrom(m, "outputPointsPerM", outputUsd),
			PerCallPoints:    officialPointsFrom(m, "perCallPoints", perCallUsd),
		})
	}
	if len(views) == 0 {
		return views, "no callable models"
	}
	return views, ""
}

func officialPointsFrom(m map[string]any, pointsKey string, usdFallback float64) float64 {
	if raw, ok := m[pointsKey]; ok {
		if pts := toFloat64(raw); pts > 0 {
			return round4(pts)
		}
	}
	if usdFallback > 0 {
		return round4(usdFallback * officialPointsPerUsd)
	}
	return 0
}

func round4(v float64) float64 {
	return math.Round(v*10_000) / 10_000
}

func extractFirstActiveKeyID(payload map[string]any) int64 {
	for _, listField := range []string{"keys", "data", "items"} {
		list, ok := payload[listField].([]any)
		if !ok {
			continue
		}
		for _, raw := range list {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			status := strings.ToLower(firstNonEmptyString(item["status"]))
			if status != "" && status != "active" && status != "enabled" {
				continue
			}
			if id := int64(toFloat64(item["id"])); id > 0 {
				return id
			}
		}
	}
	return 0
}

func (s *Service) fetchOfficialSiteJSON(ctx context.Context, method, targetURL, token string, body any) (int, map[string]any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.mcpOAuthHTTPClient(targetURL, officialSiteRequestTimeout).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil, errors.New("invalid official channel response")
	}
	return resp.StatusCode, payload, nil
}
