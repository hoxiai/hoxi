package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// AuthSource exposes the signed-in Hoxi account so the provider plane can
// provision the official AINode channel without importing internal/auth.
// It mirrors the tool / workapp auth seams.
type AuthSource interface {
	ServerBaseURL() string
	CurrentDeviceToken() string
	InvalidateDeviceToken(token, baseURL string)
}

// SetAuthSource wires the desktop account used by the official AINode connect
// flow. When unset the official routes answer 401.
func (s *Service) SetAuthSource(a AuthSource) {
	s.authMu.Lock()
	s.authSource = a
	s.authMu.Unlock()
}

const (
	officialAinodeProviderID     = "ainode"
	officialAinodeProviderName   = "Hoxi Official"
	officialAinodeDefaultBaseURL = "https://api.ainode.run/v1"
	officialPointsPerUsd         = 10_000
	officialSiteRequestTimeout   = 12 * time.Second
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
	site := strings.TrimRight(strings.TrimSpace(auth.ServerBaseURL()), "/")
	if site == "" {
		return nil, "", ""
	}
	return auth, site, strings.TrimSpace(auth.CurrentDeviceToken())
}

// handleOfficialModels previews the models the signed-in Hoxi account may call
// on the official AINode channel, resolved through the tenant path.
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
	models, errMsg := parseOfficialModels(payload)
	writeJSON(w, http.StatusOK, officialModelsResponse{
		Authenticated:    true,
		GatewayAvailable: baseURL != "",
		BaseURL:          baseURL,
		Models:           models,
		Error:            errMsg,
	})
}

// handleOfficialConnect provisions (or refreshes) the official AINode provider
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

	apiKey := s.fetchOrMintSiteAPIKey(r.Context(), site, token)
	if apiKey == "" {
		writeJSONError(w, http.StatusBadGateway, "official channel credential unavailable")
		return
	}

	offlineModels, _ := parseOfficialModels(payload)
	engineModels := s.fetchSiteModels(r.Context(), site, baseURL, token, apiKey)
	persisted, persistErr := s.persistMCPOAuthProvisioning(
		&mcpOAuthSession{siteOrigin: site, providerID: officialAinodeProviderID, providerName: officialAinodeProviderName},
		"",
		token,
		baseURL,
		apiKey,
		engineModels,
	)
	if persistErr != nil {
		writeJSONError(w, http.StatusInternalServerError, persistErr.Error())
		return
	}

	models := offlineModels
	if len(models) == 0 {
		models = modelItemsToViews(persisted)
	}
	writeJSON(w, http.StatusOK, officialConnectResponse{
		Authenticated: true,
		ProviderID:    officialAinodeProviderID,
		BaseURL:       baseURL,
		ModelCount:    len(models),
		Models:        models,
	})
}

func (s *Service) resolveOfficialGateway(ctx context.Context, site, token string) (string, map[string]any, int, error) {
	status, payload, err := s.fetchOfficialSiteJSON(ctx, http.MethodGet, site+"/api/hoxi/member/models", token, nil)
	if err != nil {
		return "", nil, 0, err
	}
	if status == http.StatusUnauthorized {
		return "", payload, status, nil
	}
	baseURL := officialGatewayBaseURL(payload, site)
	if baseURL == "" {
		if st, gw, gwErr := s.fetchOfficialSiteJSON(ctx, http.MethodGet, site+"/api/proxy/gateway-url", token, nil); gwErr == nil && st >= 200 && st < 300 {
			baseURL = normalizeGatewayAPIBaseURL(firstNonEmptyString(gw["url"], gw["baseUrl"]))
		}
	}
	if baseURL == "" {
		baseURL = officialAinodeDefaultBaseURL
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

func modelItemsToViews(items []mcpOAuthModelItem) []officialModelView {
	views := make([]officialModelView, 0, len(items))
	for _, m := range items {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		views = append(views, officialModelView{
			ID:               id,
			Name:             firstNonEmptyString(m.Name, id),
			BillingMode:      "token",
			InputPointsPerM:  round4(m.InputPrice * officialPointsPerUsd),
			OutputPointsPerM: round4(m.OutputPrice * officialPointsPerUsd),
		})
	}
	return views
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
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload)
	return resp.StatusCode, payload, nil
}
