package provider

import (
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func floatFromCostField(m map[string]any, keys ...string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	for _, k := range keys {
		raw, exists := m[k]
		if !exists || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			if v >= 0 {
				return v, true
			}
		case int:
			if v >= 0 {
				return float64(v), true
			}
		case int64:
			if v >= 0 {
				return float64(v), true
			}
		}
	}
	return 0, false
}

// resolveModelPricing resolves USD-per-1M-token rates from explicit model
// configuration first, falling back to public standard rates for known model
// families while keeping local/free endpoints at zero.
func resolveModelPricing(providerID, modelID, baseURL string, modelCfg map[string]any) llm.ModelPricing {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	if modelCfg != nil {
		if costMap, ok := modelCfg["cost"].(map[string]any); ok && len(costMap) > 0 {
			inRate, hasIn := floatFromCostField(costMap, "input")
			outRate, hasOut := floatFromCostField(costMap, "output")
			reasonRate, _ := floatFromCostField(costMap, "reasoning")
			cacheRead, hasCR := floatFromCostField(costMap, "cache_read", "cacheRead")
			cacheWrite, hasCW := floatFromCostField(costMap, "cache_write", "cacheWrite")
			if (hasIn && inRate > 0) || (hasOut && outRate > 0) {
				p := llm.ModelPricing{
					InputPerM:      inRate,
					OutputPerM:     outRate,
					ReasoningPerM:  reasonRate,
					CacheReadPerM:  cacheRead,
					CacheWritePerM: cacheWrite,
				}
				if !hasCR && inRate > 0 {
					p.CacheReadPerM = defaultCacheReadMultiplier(pid, modelID) * inRate
				}
				if !hasCW && inRate > 0 {
					p.CacheWritePerM = defaultCacheWriteMultiplier(pid, modelID) * inRate
				}
				return p
			}
			if (pid == "opencode" || pid == "opencode-go" || pid == "ollama") && hasIn && inRate == 0 {
				return llm.ModelPricing{}
			}
		}
	}

	if pid == "ollama" || pid == "opencode" || pid == "opencode-go" {
		return llm.ModelPricing{}
	}
	if isLoopbackURL(baseURL) && pid != "grok-cli" {
		return llm.ModelPricing{}
	}

	canon := canonicalModelID(modelID)
	if known, ok := knownModelPricing(canon); ok {
		return known
	}
	return llm.ModelPricing{}
}

func defaultCacheReadMultiplier(providerID, modelID string) float64 {
	canon := canonicalModelID(modelID)
	switch {
	case providerID == "anthropic" || strings.HasPrefix(canon, "claude-"):
		return 0.1
	case providerID == "deepseek" || strings.HasPrefix(canon, "deepseek-"):
		return 0.1
	case providerID == "google" || providerID == "gemini" || strings.HasPrefix(canon, "gemini-"):
		return 0.25
	default:
		return 0.25
	}
}

func defaultCacheWriteMultiplier(providerID, modelID string) float64 {
	canon := canonicalModelID(modelID)
	if providerID == "anthropic" || strings.HasPrefix(canon, "claude-") {
		return 1.25
	}
	return 1.0
}

func knownModelPricing(canon string) (llm.ModelPricing, bool) {
	if canon == "" {
		return llm.ModelPricing{}, false
	}
	switch {
	// Anthropic Claude families
	case strings.HasPrefix(canon, "claude-opus-4-5"), strings.HasPrefix(canon, "claude-opus-4-6"),
		strings.HasPrefix(canon, "claude-4-5-opus"), strings.HasPrefix(canon, "claude-4-6-opus"):
		return llm.ModelPricing{InputPerM: 5.0, OutputPerM: 25.0, CacheReadPerM: 0.50, CacheWritePerM: 6.25}, true
	case strings.HasPrefix(canon, "claude-opus-4"), strings.HasPrefix(canon, "claude-4-opus"), strings.HasPrefix(canon, "claude-3-opus"):
		return llm.ModelPricing{InputPerM: 15.0, OutputPerM: 75.0, CacheReadPerM: 1.50, CacheWritePerM: 18.75}, true
	case strings.HasPrefix(canon, "claude-sonnet-4"), strings.HasPrefix(canon, "claude-4-sonnet"),
		strings.HasPrefix(canon, "claude-3-7-sonnet"), strings.HasPrefix(canon, "claude-3-5-sonnet"):
		return llm.ModelPricing{InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75}, true
	case strings.HasPrefix(canon, "claude-haiku-4"), strings.HasPrefix(canon, "claude-4-haiku"):
		return llm.ModelPricing{InputPerM: 1.0, OutputPerM: 5.0, CacheReadPerM: 0.10, CacheWritePerM: 1.25}, true
	case strings.HasPrefix(canon, "claude-3-5-haiku"):
		return llm.ModelPricing{InputPerM: 0.80, OutputPerM: 4.0, CacheReadPerM: 0.08, CacheWritePerM: 1.0}, true

	// OpenAI GPT-5 / GPT-4.1 / GPT-4o / o-series
	case strings.Contains(canon, "gpt-5") && strings.Contains(canon, "nano"):
		return llm.ModelPricing{InputPerM: 0.05, OutputPerM: 0.40, CacheReadPerM: 0.005, CacheWritePerM: 0.05}, true
	case strings.Contains(canon, "gpt-5") && strings.Contains(canon, "mini"):
		return llm.ModelPricing{InputPerM: 0.25, OutputPerM: 2.0, CacheReadPerM: 0.025, CacheWritePerM: 0.25}, true
	case strings.HasPrefix(canon, "gpt-5"):
		return llm.ModelPricing{InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.125, CacheWritePerM: 1.25}, true
	case strings.HasPrefix(canon, "gpt-4-1-nano"):
		return llm.ModelPricing{InputPerM: 0.10, OutputPerM: 0.40, CacheReadPerM: 0.025, CacheWritePerM: 0.10}, true
	case strings.HasPrefix(canon, "gpt-4-1-mini"):
		return llm.ModelPricing{InputPerM: 0.40, OutputPerM: 1.60, CacheReadPerM: 0.10, CacheWritePerM: 0.40}, true
	case strings.HasPrefix(canon, "gpt-4-1"):
		return llm.ModelPricing{InputPerM: 2.0, OutputPerM: 8.0, CacheReadPerM: 0.50, CacheWritePerM: 2.0}, true
	case strings.HasPrefix(canon, "gpt-4o-mini"):
		return llm.ModelPricing{InputPerM: 0.15, OutputPerM: 0.60, CacheReadPerM: 0.075, CacheWritePerM: 0.15}, true
	case strings.HasPrefix(canon, "gpt-4o"):
		return llm.ModelPricing{InputPerM: 2.50, OutputPerM: 10.0, CacheReadPerM: 1.25, CacheWritePerM: 2.50}, true
	case strings.HasPrefix(canon, "o4-mini"), strings.HasPrefix(canon, "o3-mini"):
		return llm.ModelPricing{InputPerM: 1.10, OutputPerM: 4.40, CacheReadPerM: 0.275, CacheWritePerM: 1.10}, true
	case canon == "o3" || strings.HasPrefix(canon, "o3-"):
		return llm.ModelPricing{InputPerM: 2.0, OutputPerM: 8.0, CacheReadPerM: 0.50, CacheWritePerM: 2.0}, true

	// Google Gemini families
	case strings.HasPrefix(canon, "gemini-") && strings.Contains(canon, "flash-lite"):
		return llm.ModelPricing{InputPerM: 0.10, OutputPerM: 0.40, CacheReadPerM: 0.025, CacheWritePerM: 0.10}, true
	case strings.HasPrefix(canon, "gemini-") && strings.Contains(canon, "flash"):
		return llm.ModelPricing{InputPerM: 0.30, OutputPerM: 2.50, CacheReadPerM: 0.075, CacheWritePerM: 0.30}, true
	case strings.HasPrefix(canon, "gemini-") && strings.Contains(canon, "pro"):
		return llm.ModelPricing{InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.3125, CacheWritePerM: 1.25}, true

	// DeepSeek V3 / R1
	case strings.HasPrefix(canon, "deepseek-reasoner"), strings.HasPrefix(canon, "deepseek-r1"):
		return llm.ModelPricing{InputPerM: 0.55, OutputPerM: 2.19, CacheReadPerM: 0.14, CacheWritePerM: 0.55}, true
	case strings.HasPrefix(canon, "deepseek-"):
		return llm.ModelPricing{InputPerM: 0.27, OutputPerM: 1.10, CacheReadPerM: 0.07, CacheWritePerM: 0.27}, true

	// Kimi / GLM / Qwen / Grok
	case strings.HasPrefix(canon, "kimi-k2"):
		return llm.ModelPricing{InputPerM: 0.60, OutputPerM: 2.50, CacheReadPerM: 0.15, CacheWritePerM: 0.60}, true
	case strings.HasPrefix(canon, "glm-4-5-air"), strings.HasPrefix(canon, "glm-4-flash"):
		return llm.ModelPricing{InputPerM: 0.14, OutputPerM: 0.28, CacheReadPerM: 0.035, CacheWritePerM: 0.14}, true
	case strings.HasPrefix(canon, "glm-4"), strings.HasPrefix(canon, "glm-5"):
		return llm.ModelPricing{InputPerM: 0.60, OutputPerM: 2.20, CacheReadPerM: 0.15, CacheWritePerM: 0.60}, true
	case strings.HasPrefix(canon, "qwen3-coder"), strings.HasPrefix(canon, "qwen-max"), strings.HasPrefix(canon, "qwen3-max"):
		return llm.ModelPricing{InputPerM: 1.20, OutputPerM: 6.0, CacheReadPerM: 0.24, CacheWritePerM: 1.20}, true
	case strings.HasPrefix(canon, "qwen-plus"), strings.HasPrefix(canon, "qwen3-plus"):
		return llm.ModelPricing{InputPerM: 0.40, OutputPerM: 1.20, CacheReadPerM: 0.10, CacheWritePerM: 0.40}, true
	case strings.HasPrefix(canon, "grok-4-fast"), strings.HasPrefix(canon, "grok-3-mini"):
		return llm.ModelPricing{InputPerM: 0.20, OutputPerM: 0.50, CacheReadPerM: 0.05, CacheWritePerM: 0.20}, true
	case strings.HasPrefix(canon, "grok-3"), strings.HasPrefix(canon, "grok-4"):
		return llm.ModelPricing{InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.75, CacheWritePerM: 3.0}, true
	}
	return llm.ModelPricing{}, false
}
