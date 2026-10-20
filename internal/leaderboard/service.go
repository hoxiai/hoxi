package leaderboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
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
	if err != nil {
		return s.fallbackGateways(normLang), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return s.fallbackGateways(normLang), nil
	}

	var result GatewaysResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Data == nil {
		return s.fallbackGateways(normLang), nil
	}
	// This is a directory, not Hoxi-operated inference or account authorization.
	for i := range result.Data.Items {
		item := &result.Data.Items[i]
		if item.IsSelfOperated {
			item.Badge = ""
			item.Caveat = ""
		}
		item.IsSelfOperated = false
		item.CanOneClickConnect = false
		item.AuthURL = ""
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
	return &GatewaysResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &GatewaysData{
			Total: 0,
			Items: []GatewayItem{},
		},
	}
}

func pricingDiskCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "model-pricing-cache.json")
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
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err == nil {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "HoxiDesktop/1.0")
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
						_ = os.MkdirAll(filepath.Dir(p), 0o755)
						if raw, err := json.Marshal(result); err == nil {
							_ = os.WriteFile(p, raw, 0o644)
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
	lb := s.fallbackLeaderboard("overall", 50, "en")
	var items []ModelPricingItem
	if lb != nil && lb.Data != nil {
		for _, m := range lb.Data.Items {
			items = append(items, ModelPricingItem{
				Slug:          m.Slug,
				Name:          m.Name,
				Vendor:        m.Vendor,
				APIName:       m.APIName,
				Price:         m.Price,
				ContextWindow: m.ContextWindow,
				Reasoning:     m.Reasoning,
				IsFree:        m.IsFree,
			})
		}
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
	req.Header.Set("User-Agent", "HoxiDesktop/1.0")

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

	items := []FreeTierGuideItem{
		{
			ID:         "google",
			ProviderID: "google",
			Name:       "Google AI Studio (Gemini)",
			Badge:      pick("Generous Daily Free", "每日高频免费"),
			QuotaLabel: pick("500–1,500 free RPD on Gemini 2.5 Flash with 1M context window", "Gemini 2.5 Flash 每日 500~1500 次免费请求，1M 超长上下文"),
			Steps: pickSlice(
				[]string{
					"Sign in to Google AI Studio (aistudio.google.com/apikey) with your Google account",
					"Click Get API key -> Create API key (no credit card required)",
					"Select Google in Hoxi Provider Settings and paste your API key(s)",
				},
				[]string{
					"使用 Google 账号登录 Google AI Studio (aistudio.google.com/apikey)",
					"点击右上角 Get API key -> Create API key 一键生成密钥（无需绑定信用卡）",
					"在 Hoxi 供应商设置中选择 Google，粘贴 API Key（可粘贴多个 Key 用逗号分隔实现自动轮询）",
				},
			),
			SignupURL:         "https://aistudio.google.com/apikey",
			BaseURL:           "https://generativelanguage.googleapis.com/v1beta",
			CustomMode:        false,
			RecommendedModels: []string{"gemini-2.5-flash", "gemini-2.0-flash"},
		},
		{
			ID:         "groq",
			ProviderID: "groq",
			Name:       pick("Groq Cloud (Ultra-Fast LPU)", "Groq Cloud (LPU 极速推理)"),
			Badge:      pick("Free Developer Tier", "注册即送免费层"),
			QuotaLabel: pick("1,000–14,400 RPD free across Llama 3.3 70B, Qwen & DeepSeek", "Llama 3.3 70B / Qwen / DeepSeek 每日最高 1,000~14,400 次免费请求"),
			Steps: pickSlice(
				[]string{
					"Sign in at console.groq.com with GitHub or Google",
					"Go to API Keys -> Create API Key (gsk_...)",
					"Add a Custom Endpoint in Hoxi with Base URL https://api.groq.com/openai/v1",
				},
				[]string{
					"访问 console.groq.com 使用 GitHub 或 Google 账号一键登录",
					"左侧菜单进入 API Keys -> Create API Key 生成 gsk_... 密钥",
					"在 Hoxi 自定义端点中添加：https://api.groq.com/openai/v1 并填入 API Key",
				},
			),
			SignupURL:         "https://console.groq.com/keys",
			BaseURL:           "https://api.groq.com/openai/v1",
			CustomMode:        true,
			RecommendedModels: []string{"llama-3.3-70b-versatile", "qwen-qwq-32b"},
		},
		{
			ID:         "openrouter",
			ProviderID: "openrouter",
			Name:       pick("OpenRouter (:free Models)", "OpenRouter (:free 免费模型专区)"),
			Badge:      pick("Multi-Model Free Pool", "多模型聚合白嫖"),
			QuotaLabel: pick("Dozens of :free models (DeepSeek R1/V3, Gemma 3, Qwen3) at $0 cost", "数十款带 :free 后缀的开源旗舰模型（DeepSeek R1/V3、Gemma 3、Qwen3）免费调用"),
			Steps: pickSlice(
				[]string{
					"Visit openrouter.ai/settings/keys and click Create Key",
					"Select OpenRouter in Hoxi Providers and paste your sk-or-v1-... key",
					"Add :free models to hoxi-auto/free for automatic 429 rotation",
				},
				[]string{
					"登录 openrouter.ai/settings/keys，点击 Create Key 创建免费密钥",
					"在 Hoxi 供应商列表选择 OpenRouter 并填入 sk-or-v1-... 密钥",
					"将带 :free 后缀的模型加入 hoxi-auto/free 候选池，触发 429 自动切换下一节点",
				},
			),
			SignupURL:         "https://openrouter.ai/settings/keys",
			BaseURL:           "https://openrouter.ai/api/v1",
			CustomMode:        false,
			RecommendedModels: []string{"deepseek/deepseek-chat-v3-0324:free", "deepseek/deepseek-r1:free", "qwen/qwen3-235b-a22b:free"},
		},
		{
			ID:         "siliconflow",
			ProviderID: "siliconflow",
			Name:       pick("SiliconFlow Cloud", "SiliconFlow 硅基流动"),
			Badge:      pick("Permanent Free Models", "国内直连 + 永久免费模型"),
			QuotaLabel: pick("Signup bonus credits + permanent ¥0 free models (Qwen2.5-7B, DeepSeek-R1-Distill-7B)", "注册赠送体验金 + Qwen2.5-7B / DeepSeek-R1-Distill-7B 等多款模型 ¥0 永久免费"),
			Steps: pickSlice(
				[]string{
					"Register at cloud.siliconflow.cn",
					"Create a new API key (sk-...) under API Keys",
					"Add a Custom Endpoint in Hoxi with Base URL https://api.siliconflow.cn/v1",
				},
				[]string{
					"访问 cloud.siliconflow.cn 注册账号（国内网络直连免代理）",
					"在「API 密钥」页面新建密钥 sk-...",
					"在 Hoxi 自定义端点中添加 Base URL：https://api.siliconflow.cn/v1",
				},
			),
			SignupURL:         "https://cloud.siliconflow.cn/account/ak",
			BaseURL:           "https://api.siliconflow.cn/v1",
			CustomMode:        true,
			RecommendedModels: []string{"Qwen/Qwen2.5-Coder-7B-Instruct", "deepseek-ai/DeepSeek-R1-Distill-Qwen-7B"},
		},
	}

	return &FreeTiersResponse{
		Code:    200,
		Message: "ok (cached offline fallback)",
		Data: &FreeTiersData{
			UpdatedAt: "2026-10-20",
			Total:     len(items),
			Items:     items,
		},
	}
}
