package leaderboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type cachedEntry struct {
	data      any
	expiresAt time.Time
}

type Service struct {
	mu         sync.RWMutex
	client     *http.Client
	baseURL    string
	cache      map[string]cachedEntry
	cacheTTL   time.Duration
}

func NewService(baseURL string, client *http.Client) *Service {
	if baseURL == "" {
		baseURL = "https://hoxi.ai"
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

func (s *Service) GetLeaderboard(dimension string, limit int) (*LeaderboardResponse, error) {
	if dimension == "" {
		dimension = "overall"
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	cacheKey := fmt.Sprintf("leaderboard_%s_%d", dimension, limit)

	s.mu.RLock()
	if entry, ok := s.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.data.(*LeaderboardResponse), nil
	}
	s.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/api/models/leaderboard?sort=%s&limit=%s",
		s.baseURL,
		url.QueryEscape(dimension),
		strconv.Itoa(limit),
	)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return s.fallbackLeaderboard(dimension, limit), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

	resp, err := s.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return s.fallbackLeaderboard(dimension, limit), nil
	}
	defer resp.Body.Close()

	var result LeaderboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Data == nil {
		return s.fallbackLeaderboard(dimension, limit), nil
	}

	s.mu.Lock()
	s.cache[cacheKey] = cachedEntry{
		data:      &result,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return &result, nil
}

func (s *Service) GetConnectableGateways() (*GatewaysResponse, error) {
	cacheKey := "connectable_gateways"

	s.mu.RLock()
	if entry, ok := s.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.data.(*GatewaysResponse), nil
	}
	s.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/api/gateways/connectable", s.baseURL)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return s.fallbackGateways(), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

	resp, err := s.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return s.fallbackGateways(), nil
	}
	defer resp.Body.Close()

	var result GatewaysResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Data == nil {
		return s.fallbackGateways(), nil
	}

	s.mu.Lock()
	s.cache[cacheKey] = cachedEntry{
		data:      &result,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return &result, nil
}

func (s *Service) fallbackLeaderboard(dimension string, limit int) *LeaderboardResponse {
	items := []ModelItem{
		{
			Rank: 1, Slug: "claude-3-7-sonnet", Name: "Claude 3.7 Sonnet", Vendor: "anthropic", APIName: "claude-3-7-sonnet-20250219",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 3.0, Output: 15.0, CacheRead: 0.3},
			ContextWindow: 200000,
			Scores:        map[string]any{"overall": 72, "coding": 94},
			Perf:          map[string]any{"tps": 82, "ttft": 0.8},
			Badges:        []string{"编程榜首", "混合思考"},
			Scenes:        []string{"写代码", "复杂推理", "Agent工具调用"},
			Summary:       "当前最强大的混合思考代码模型，架构设计与 Bug 排查首选",
			Reasoning:     true,
		},
		{
			Rank: 2, Slug: "deepseek-r1", Name: "DeepSeek R1", Vendor: "deepseek", APIName: "deepseek-reasoner",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 0.55, Output: 2.19, CacheRead: 0.14},
			ContextWindow: 64000,
			Scores:        map[string]any{"overall": 70, "coding": 92},
			Perf:          map[string]any{"tps": 65, "ttft": 1.2},
			Badges:        []string{"性价比之王", "开源满血"},
			Scenes:        []string{"复杂推理", "写代码", "数学算法"},
			Summary:       "超高性价比的深度推理大模型，代码能力比肩顶尖商业模型",
			Reasoning:     true,
		},
		{
			Rank: 3, Slug: "gpt-4o", Name: "GPT-4o", Vendor: "openai", APIName: "gpt-4o",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 2.5, Output: 10.0, CacheRead: 1.25},
			ContextWindow: 128000,
			Scores:        map[string]any{"overall": 68, "coding": 88},
			Perf:          map[string]any{"tps": 110, "ttft": 0.5},
			Badges:        []string{"极速全能"},
			Scenes:        []string{"写代码", "多模态", "Agent工具调用"},
			Summary:       "全能极速旗舰，高并发吞吐与多模态识别极佳",
		},
		{
			Rank: 4, Slug: "gemini-2-5-pro", Name: "Gemini 2.5 Pro", Vendor: "google", APIName: "gemini-2.5-pro",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 1.25, Output: 5.0, CacheRead: 0.3},
			ContextWindow: 2000000,
			Scores:        map[string]any{"overall": 67, "coding": 89},
			Perf:          map[string]any{"tps": 75, "ttft": 0.9},
			Badges:        []string{"200万上下文"},
			Scenes:        []string{"超长项目", "写代码", "全库分析"},
			Summary:       "200万超大上下文窗口，适合巨型代码库一次性跨文件检索",
		},
	}
	if limit < len(items) {
		items = items[:limit]
	}
	return &LeaderboardResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &LeaderboardData{
			Dimension: dimension,
			UpdatedAt: "2026-08-27",
			Total:     len(items),
			Items:     items,
		},
	}
}

func (s *Service) fallbackGateways() *GatewaysResponse {
	return &GatewaysResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &GatewaysData{
			Total: 1,
			Items: []GatewayItem{
				{
					ID:                 "ainode-pro",
					Name:               "AINode 极速专线",
					Domain:             "ainode.run",
					URL:                "https://api.ainode.run",
					BaseURL:            "https://api.ainode.run/v1",
					IsSelfOperated:     true,
					CanOneClickConnect: true,
					Badge:              "自营推荐 · 一键直连",
					SavingsPercent:     87,
					PriceLabel:         "¥2.80 / M",
					LatencyMs:          280,
					Uptime:             "99.99%",
					Models:             []string{"Claude 3.7 Sonnet", "DeepSeek R1", "GPT-4o", "Gemini 2.5 Flash"},
					Features:           []string{"国内双线直连", "100% 满血思考模式", "微信/支付宝充值", "余额永不过期"},
					Caveat:             "高质量企业专线，支持一键免密授权至 Hoxi 桌面端",
					AuthURL:            "https://hoxi.ai/auth/authorize-desktop?gateway=ainode",
				},
			},
		},
	}
}
