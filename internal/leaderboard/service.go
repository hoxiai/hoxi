package leaderboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type cachedEntry struct {
	data      any
	expiresAt time.Time
}

type Service struct {
	mu       sync.RWMutex
	client   *http.Client
	baseURL  string
	cache    map[string]cachedEntry
	cacheTTL time.Duration
}

func NewService(baseURL string, client *http.Client) *Service {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = hoxipaths.ServerBaseURL()
	}
	if client == nil {
		client = &http.Client{
			Timeout: 8 * time.Second,
		}
	}
	return &Service{
		client:   client,
		baseURL:  baseURL,
		cache:    make(map[string]cachedEntry),
		cacheTTL: 5 * time.Minute,
	}
}

func normalizeLang(lang ...string) string {
	if len(lang) > 0 && (lang[0] == "zh" || lang[0] == "zh-CN" || lang[0] == "zh_CN") {
		return "zh"
	}
	return "en"
}

// isSelfOperatedGateway identifies the official one-click gateway by its
// server-issued id. The gateway endpoint itself is supplied by the official
// site API, so no domain is hardcoded here.
func isSelfOperatedGateway(item GatewayItem) bool {
	id := strings.ToLower(strings.TrimSpace(item.ID))
	return id == "hoxi-gateway" || strings.HasPrefix(id, "hoxi-gateway-")
}

// GetConnectableGateways always fetches the live recommendation directory from
// the official site. It is deliberately never cached, locally or in memory, so
// the list the desktop shows cannot drift from what the server recommends.
func (s *Service) GetConnectableGateways(lang ...string) (*GatewaysResponse, error) {
	normLang := normalizeLang(lang...)
	if hoxipaths.AuxiliaryNetworkDisabled() {
		return &GatewaysResponse{
			Code:    200,
			Message: "ok (offline mode)",
			Data:    &GatewaysData{Items: []GatewayItem{}},
		}, nil
	}

	endpoint := fmt.Sprintf("%s/api/hoxi/gateways/connectable?lang=%s", s.baseURL, url.QueryEscape(normLang))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway directory returned status %d", resp.StatusCode)
	}

	var result GatewaysResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Data == nil {
		return nil, fmt.Errorf("gateway directory returned no data")
	}
	// Only self-operated gateways support desktop one-click OAuth & automatic key provisioning.
	for i := range result.Data.Items {
		item := &result.Data.Items[i]
		if isSelfOperatedGateway(*item) {
			item.IsSelfOperated = true
			item.CanOneClickConnect = true
			continue
		}
		if item.IsSelfOperated {
			item.Badge = ""
			item.Caveat = ""
		}
		item.IsSelfOperated = false
		item.CanOneClickConnect = false
		item.AuthURL = ""
	}

	return &result, nil
}

func pricingDiskCachePath() string {
	return hoxipaths.Join("model-pricing-cache.json")
}

func (s *Service) GetModelPricing(forceRefresh bool) (*ModelPricingResponse, error) {
	const cacheKey = "model_pricing_catalog"

	if !forceRefresh {
		s.mu.RLock()
		if entry, ok := s.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
			s.mu.RUnlock()
			return entry.data.(*ModelPricingResponse), nil
		}
		s.mu.RUnlock()
	}

	endpoint := fmt.Sprintf("%s/api/hoxi/models", s.baseURL)
	var req *http.Request
	var err error
	if !hoxipaths.AuxiliaryNetworkDisabled() {
		req, err = http.NewRequest(http.MethodGet, endpoint, nil)
	}
	if !hoxipaths.AuxiliaryNetworkDisabled() && err == nil {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", buildinfo.UserAgent())
		if resp, err := s.client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var items []ModelPricingItem
				if err := json.NewDecoder(resp.Body).Decode(&items); err == nil && len(items) > 0 {
					updatedAt := time.Now().Format("2006-01-02")
					for _, it := range items {
						if it.UpdatedAt != "" {
							updatedAt = it.UpdatedAt
							break
						}
					}
					result := &ModelPricingResponse{
						Code:    200,
						Message: "ok",
						Data: &ModelPricingData{
							UpdatedAt: updatedAt,
							Total:     len(items),
							Items:     items,
						},
					}
					s.mu.Lock()
					s.cache[cacheKey] = cachedEntry{
						data:      result,
						expiresAt: time.Now().Add(30 * time.Minute),
					}
					s.mu.Unlock()
					if p := pricingDiskCachePath(); p != "" {
						if raw, err := json.Marshal(result); err == nil {
							_ = hoxipaths.WriteFileAtomic(p, raw, 0o600)
						}
					}
					return result, nil
				}
			}
		}
	}

	// Try reading last synced catalog from ~/.hoxi/model-pricing-cache.json
	if p := pricingDiskCachePath(); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			var cached ModelPricingResponse
			if json.Unmarshal(raw, &cached) == nil && cached.Data != nil && len(cached.Data.Items) > 0 {
				s.mu.Lock()
				s.cache[cacheKey] = cachedEntry{
					data:      &cached,
					expiresAt: time.Now().Add(5 * time.Minute),
				}
				s.mu.Unlock()
				return &cached, nil
			}
		}
	}

	return s.fallbackModelPricing(), nil
}

func (s *Service) fallbackModelPricing() *ModelPricingResponse {
	items := []ModelPricingItem{
		{
			Slug: "claude-opus-5", Name: "Claude Opus 5", Vendor: "anthropic", APIName: "claude-opus-5",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 5.0, Output: 25.0, CacheRead: 0.5},
			ContextWindow: 1000000,
			Reasoning:     true,
		},
		{
			Slug: "gpt-5-6-sol", Name: "GPT-5.6 Sol", Vendor: "openai", APIName: "gpt-5.6-sol",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 4.0, Output: 20.0, CacheRead: 0.4},
			ContextWindow: 1050000,
			Reasoning:     true,
		},
		{
			Slug: "glm-5-3-flash", Name: "GLM-5.3-Flash", Vendor: "zhipu", APIName: "glm-5.3-flash",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "CNY", Input: 0.8, Output: 2.8, CacheRead: 0.23},
			ContextWindow: 1000000,
			Reasoning:     true,
		},
		{
			Slug: "gemini-3-7-flash", Name: "Gemini 3.7 Flash", Vendor: "google", APIName: "gemini-3.7-flash",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 0.75, Output: 3.75, CacheRead: 0.075},
			ContextWindow: 1048576,
			Reasoning:     true,
		},
	}
	return &ModelPricingResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &ModelPricingData{
			UpdatedAt: "2026-08-27",
			Total:     len(items),
			Items:     items,
		},
	}
}

func (s *Service) GetFreeTiers(lang ...string) (*FreeTiersResponse, error) {
	resolvedLang := normalizeLang(lang...)
	if hoxipaths.AuxiliaryNetworkDisabled() {
		return s.fallbackFreeTiers(resolvedLang), nil
	}
	cacheKey := fmt.Sprintf("free_tiers:%s", resolvedLang)

	s.mu.RLock()
	if entry, ok := s.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.data.(*FreeTiersResponse), nil
	}
	s.mu.RUnlock()

	u, err := url.Parse(s.baseURL + "/api/hoxi/models/free-tiers")
	if err != nil {
		return s.fallbackFreeTiers(resolvedLang), nil
	}
	q := u.Query()
	q.Set("lang", resolvedLang)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return s.fallbackFreeTiers(resolvedLang), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", resolvedLang)
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := s.client.Do(req)
	if err != nil {
		return s.fallbackFreeTiers(resolvedLang), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return s.fallbackFreeTiers(resolvedLang), nil
	}

	var result FreeTiersResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || result.Data == nil || len(result.Data.Items) == 0 {
		return s.fallbackFreeTiers(resolvedLang), nil
	}

	s.mu.Lock()
	s.cache[cacheKey] = cachedEntry{
		data:      &result,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return &result, nil
}

func (s *Service) fallbackFreeTiers(lang string) *FreeTiersResponse {
	return &FreeTiersResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &FreeTiersData{
			UpdatedAt: "",
			Total:     0,
			Items:     []FreeTierGuideItem{},
		},
	}
}
