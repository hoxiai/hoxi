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

func normalizeLang(lang ...string) string {
	if len(lang) > 0 && (lang[0] == "zh" || lang[0] == "zh-CN" || lang[0] == "zh_CN") {
		return "zh"
	}
	return "en"
}

func (s *Service) GetLeaderboard(dimension string, limit int, lang ...string) (*LeaderboardResponse, error) {
	if dimension == "" {
		dimension = "overall"
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	normLang := normalizeLang(lang...)
	cacheKey := fmt.Sprintf("leaderboard_%s_%d_%s", dimension, limit, normLang)

	s.mu.RLock()
	if entry, ok := s.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.data.(*LeaderboardResponse), nil
	}
	s.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/api/hoxi/models/leaderboard?sort=%s&limit=%s&lang=%s",
		s.baseURL,
		url.QueryEscape(dimension),
		strconv.Itoa(limit),
		url.QueryEscape(normLang),
	)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return s.fallbackLeaderboard(dimension, limit, normLang), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

	resp, err := s.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return s.fallbackLeaderboard(dimension, limit, normLang), nil
	}
	defer resp.Body.Close()

	var result LeaderboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Data == nil {
		return s.fallbackLeaderboard(dimension, limit, normLang), nil
	}

	s.mu.Lock()
	s.cache[cacheKey] = cachedEntry{
		data:      &result,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return &result, nil
}

func (s *Service) GetConnectableGateways(lang ...string) (*GatewaysResponse, error) {
	normLang := normalizeLang(lang...)
	cacheKey := fmt.Sprintf("connectable_gateways_%s", normLang)

	s.mu.RLock()
	if entry, ok := s.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.data.(*GatewaysResponse), nil
	}
	s.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/api/hoxi/gateways/connectable?lang=%s", s.baseURL, url.QueryEscape(normLang))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return s.fallbackGateways(normLang), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

	resp, err := s.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return s.fallbackGateways(normLang), nil
	}
	defer resp.Body.Close()

	var result GatewaysResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Data == nil {
		return s.fallbackGateways(normLang), nil
	}

	s.mu.Lock()
	s.cache[cacheKey] = cachedEntry{
		data:      &result,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return &result, nil
}

func (s *Service) fallbackLeaderboard(dimension string, limit int, lang string) *LeaderboardResponse {
	isZh := lang == "zh"
	pick := func(en, zh string) string {
		if isZh {
			return zh
		}
		return en
	}
	pickSlice := func(en, zh []string) []string {
		if isZh {
			return zh
		}
		return en
	}

	items := []ModelItem{
		{
			Rank: 1, Slug: "claude-opus-5", Name: "Claude Opus 5", Vendor: "anthropic", APIName: "claude-opus-5",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 5.0, Output: 25.0, CacheRead: 0.5},
			ContextWindow: 1000000,
			Scores:        map[string]any{"overall": 63, "coding": 95},
			Perf:          map[string]any{"tps": 55, "ttft": 1.2},
			Badges:        pickSlice([]string{"#1 Overall", "Coding Leader"}, []string{"综合分第一", "编程榜首"}),
			Scenes:        pickSlice([]string{"Coding", "Deep Reasoning", "Agent Tool Use"}, []string{"写代码", "复杂推理", "Agent工具调用"}),
			Summary:       pick("Top-tier workhorse for multi-file refactoring and agentic tool use; rock-solid reasoning and first-pass accuracy.", "多文件复杂重构与 Agent 工具调用的战神；逻辑缜密代码一次过，日常攻坚首选。"),
			Reasoning:     true,
		},
		{
			Rank: 2, Slug: "gpt-5-6-sol", Name: "GPT-5.6 Sol", Vendor: "openai", APIName: "gpt-5.6-sol",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 4.0, Output: 20.0, CacheRead: 0.4},
			ContextWindow: 1050000,
			Scores:        map[string]any{"overall": 61, "coding": 92},
			Perf:          map[string]any{"tps": 74, "ttft": 1.1},
			Badges:        pickSlice([]string{"All-Rounder"}, []string{"全能旗舰"}),
			Scenes:        pickSlice([]string{"Deep Reasoning", "Coding", "Agent Tool Use"}, []string{"复杂推理", "写代码", "Agent工具调用"}),
			Summary:       pick("Broad domain knowledge and thorough system design; highly reliable logic and code generation.", "知识面极宽、复杂方案设计详尽；逻辑与代码极稳。"),
			Reasoning:     true,
		},
		{
			Rank: 3, Slug: "glm-5-3-flash", Name: "GLM-5.3-Flash", Vendor: "zhipu", APIName: "glm-5.3-flash",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "CNY", Input: 0.8, Output: 2.8, CacheRead: 0.23},
			ContextWindow: 1000000,
			Scores:        map[string]any{"overall": 57, "coding": 88},
			Perf:          map[string]any{"tps": 50, "ttft": 1.47},
			Badges:        pickSlice([]string{"Value King"}, []string{"性价比之王"}),
			Scenes:        pickSlice([]string{"Batch Processing", "Coding", "Vision"}, []string{"批量处理", "中文写作", "图片理解"}),
			Summary:       pick("Near-flagship benchmark score at a fraction of the price with 1.47s TTFT; ideal for everyday feature development.", "白菜价却有接近旗舰的能力表现，日常写小功能极度划算。"),
			Reasoning:     true,
		},
		{
			Rank: 4, Slug: "gemini-3-7-flash", Name: "Gemini 3.7 Flash", Vendor: "google", APIName: "gemini-3.7-flash",
			Price: struct {
				Currency   string  `json:"currency"`
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead,omitempty"`
				CacheWrite float64 `json:"cacheWrite,omitempty"`
			}{Currency: "USD", Input: 0.75, Output: 3.75, CacheRead: 0.075},
			ContextWindow: 1048576,
			Scores:        map[string]any{"overall": 56, "coding": 89},
			Perf:          map[string]any{"tps": 362, "ttft": 0.4},
			Badges:        pickSlice([]string{"Fastest TPS"}, []string{"最快"}),
			Scenes:        pickSlice([]string{"General Chat", "Batch Processing", "Long Context"}, []string{"日常问答", "批量处理", "长文档"}),
			Summary:       pick("Sub-second TTFT and 360+ TPS; unbeaten value for rapid code completion and multimodal parsing.", "首字毫秒级响应、极速无感；日常代码补全与常规 CRUD 生成利器。"),
			Reasoning:     true,
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

func (s *Service) fallbackGateways(lang string) *GatewaysResponse {
	isZh := lang == "zh"
	name := "AINode Direct Pro"
	badge := "Official Pick · 1-Click Auth"
	features := []string{"Low-latency direct routing", "100% full-weight thinking mode", "Instant top-up", "Never-expiring balance"}
	caveat := "Enterprise-grade dedicated upstream channels with 1-click OAuth to Hoxi Desktop"
	if isZh {
		name = "AINode 极速专线"
		badge = "自营推荐 · 一键直连"
		features = []string{"国内双线直连", "100% 满血思考模式", "微信/支付宝充值", "余额永不过期"}
		caveat = "高质量企业专线，支持一键免密授权至 Hoxi 桌面端"
	}
	return &GatewaysResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &GatewaysData{
			Total: 1,
			Items: []GatewayItem{
				{
					ID:                 "ainode-pro",
					Name:               name,
					Domain:             "ainode.run",
					URL:                "https://api.ainode.run",
					BaseURL:            "https://api.ainode.run/v1",
					IsSelfOperated:     true,
					CanOneClickConnect: true,
					Badge:              badge,
					SavingsPercent:     87,
					PriceLabel:         "¥2.80 / M",
					LatencyMs:          280,
					Uptime:             "99.99%",
					Models:             []string{"Claude Opus 5", "Claude Sonnet 5", "GPT-5.6 Sol", "Gemini 3.7 Flash"},
					Features:           features,
					Caveat:             caveat,
					AuthURL:            "https://hoxi.ai/auth/authorize-desktop?gateway=ainode",
				},
			},
		},
	}
}
