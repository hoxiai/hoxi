package provider

import (
	"encoding/json"
	"os"
	"strings"
)

type builtinModelTemplate struct {
	ID             string
	Name           string
	Context        int
	Output         int
	InputPerM      float64
	OutputPerM     float64
	CacheReadPerM  float64
	CacheWritePerM float64
	Reasoning      bool
}

type builtinProviderTemplate struct {
	ID     string
	Name   string
	NPM    string
	Env    []string
	Models []builtinModelTemplate
}

// builtinProviderTemplates provides curated offline presets for mainstream LLM
// providers so Hoxi's Model Manager, PUT /auth/<provider>, and native turns work
// out-of-the-box without requiring the external opencode serve sidecar.
var builtinProviderTemplates = []builtinProviderTemplate{
	{
		ID:   "anthropic",
		Name: "Anthropic",
		NPM:  "@ai-sdk/anthropic",
		Env:  []string{"ANTHROPIC_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Context: 200_000, Output: 64_000, InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75, Reasoning: true},
			{ID: "claude-opus-4-5", Name: "Claude Opus 4.5", Context: 200_000, Output: 64_000, InputPerM: 5.0, OutputPerM: 25.0, CacheReadPerM: 0.50, CacheWritePerM: 6.25, Reasoning: true},
			{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", Context: 200_000, Output: 64_000, InputPerM: 1.0, OutputPerM: 5.0, CacheReadPerM: 0.10, CacheWritePerM: 1.25, Reasoning: true},
		},
	},
	{
		ID:   "openai",
		Name: "OpenAI",
		NPM:  "@ai-sdk/openai",
		Env:  []string{"OPENAI_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "gpt-5", Name: "GPT-5", Context: 400_000, Output: 128_000, InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.125, CacheWritePerM: 1.25, Reasoning: true},
			{ID: "gpt-5-mini", Name: "GPT-5 Mini", Context: 400_000, Output: 128_000, InputPerM: 0.25, OutputPerM: 2.0, CacheReadPerM: 0.025, CacheWritePerM: 0.25, Reasoning: true},
			{ID: "gpt-4.1", Name: "GPT-4.1", Context: 1_047_576, Output: 32_768, InputPerM: 2.0, OutputPerM: 8.0, CacheReadPerM: 0.50, CacheWritePerM: 2.0},
			{ID: "gpt-4o", Name: "GPT-4o", Context: 128_000, Output: 16_384, InputPerM: 2.50, OutputPerM: 10.0, CacheReadPerM: 1.25, CacheWritePerM: 2.50},
			{ID: "o4-mini", Name: "o4-mini", Context: 200_000, Output: 100_000, InputPerM: 1.10, OutputPerM: 4.40, CacheReadPerM: 0.275, CacheWritePerM: 1.10, Reasoning: true},
		},
	},
	{
		ID:   "google",
		Name: "Google Gemini",
		NPM:  "@ai-sdk/google",
		Env:  []string{"GEMINI_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Context: 1_048_576, Output: 65_536, InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.3125, CacheWritePerM: 1.25, Reasoning: true},
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Context: 1_048_576, Output: 65_536, InputPerM: 0.30, OutputPerM: 2.50, CacheReadPerM: 0.075, CacheWritePerM: 0.30, Reasoning: true},
			{ID: "gemini-2.5-flash-lite", Name: "Gemini 2.5 Flash-Lite", Context: 1_048_576, Output: 65_536, InputPerM: 0.10, OutputPerM: 0.40, CacheReadPerM: 0.025, CacheWritePerM: 0.10},
		},
	},
	{
		ID:   "deepseek",
		Name: "DeepSeek",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"DEEPSEEK_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "deepseek-chat", Name: "DeepSeek V3", Context: 128_000, Output: 8_192, InputPerM: 0.27, OutputPerM: 1.10, CacheReadPerM: 0.07, CacheWritePerM: 0.27},
			{ID: "deepseek-reasoner", Name: "DeepSeek R1", Context: 128_000, Output: 32_768, InputPerM: 0.55, OutputPerM: 2.19, CacheReadPerM: 0.14, CacheWritePerM: 0.55, Reasoning: true},
		},
	},
	{
		ID:   "openrouter",
		Name: "OpenRouter",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"OPENROUTER_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet 4.5 (OpenRouter)", Context: 200_000, Output: 64_000, InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75, Reasoning: true},
			{ID: "openai/gpt-5", Name: "GPT-5 (OpenRouter)", Context: 400_000, Output: 128_000, InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.125, CacheWritePerM: 1.25, Reasoning: true},
			{ID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro (OpenRouter)", Context: 1_048_576, Output: 65_536, InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.3125, CacheWritePerM: 1.25, Reasoning: true},
			{ID: "deepseek/deepseek-chat-v3-0324", Name: "DeepSeek V3 (OpenRouter)", Context: 128_000, Output: 16_384, InputPerM: 0.27, OutputPerM: 1.10, CacheReadPerM: 0.07, CacheWritePerM: 0.27},
		},
	},
	{
		ID:   "siliconflow",
		Name: "SiliconFlow",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"SILICONFLOW_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "deepseek-ai/DeepSeek-V3", Name: "DeepSeek V3", Context: 128_000, Output: 16_384, InputPerM: 0.27, OutputPerM: 1.10, CacheReadPerM: 0.07, CacheWritePerM: 0.27},
			{ID: "deepseek-ai/DeepSeek-R1", Name: "DeepSeek R1", Context: 128_000, Output: 32_768, InputPerM: 0.55, OutputPerM: 2.19, CacheReadPerM: 0.14, CacheWritePerM: 0.55, Reasoning: true},
			{ID: "Qwen/Qwen3-Coder-480B-A35B-Instruct", Name: "Qwen3 Coder 480B", Context: 256_000, Output: 32_768, InputPerM: 1.20, OutputPerM: 6.0, CacheReadPerM: 0.24, CacheWritePerM: 1.20},
		},
	},
	{
		ID:   "alibaba",
		Name: "Alibaba Bailian (DashScope)",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"DASHSCOPE_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "qwen3-coder-plus", Name: "Qwen3 Coder Plus", Context: 1_000_000, Output: 65_536, InputPerM: 1.20, OutputPerM: 6.0, CacheReadPerM: 0.24, CacheWritePerM: 1.20},
			{ID: "qwen-max", Name: "Qwen Max", Context: 131_072, Output: 8_192, InputPerM: 1.20, OutputPerM: 6.0, CacheReadPerM: 0.24, CacheWritePerM: 1.20},
			{ID: "qwen-plus", Name: "Qwen Plus", Context: 131_072, Output: 8_192, InputPerM: 0.40, OutputPerM: 1.20, CacheReadPerM: 0.10, CacheWritePerM: 0.40},
		},
	},
	{
		ID:   "zai",
		Name: "Z.AI (GLM)",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"ZHIPU_API_KEY", "ZAI_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "glm-4.6", Name: "GLM-4.6", Context: 200_000, Output: 65_536, InputPerM: 0.60, OutputPerM: 2.20, CacheReadPerM: 0.15, CacheWritePerM: 0.60, Reasoning: true},
			{ID: "glm-4.5", Name: "GLM-4.5", Context: 128_000, Output: 32_768, InputPerM: 0.60, OutputPerM: 2.20, CacheReadPerM: 0.15, CacheWritePerM: 0.60, Reasoning: true},
			{ID: "glm-4.5-air", Name: "GLM-4.5 Air", Context: 128_000, Output: 16_384, InputPerM: 0.14, OutputPerM: 0.28, CacheReadPerM: 0.035, CacheWritePerM: 0.14},
		},
	},
	{
		ID:   "moonshotai",
		Name: "Moonshot AI (Kimi)",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"MOONSHOT_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "kimi-k2-0905-preview", Name: "Kimi K2", Context: 256_000, Output: 32_768, InputPerM: 0.60, OutputPerM: 2.50, CacheReadPerM: 0.15, CacheWritePerM: 0.60},
			{ID: "kimi-k2-turbo-preview", Name: "Kimi K2 Turbo", Context: 256_000, Output: 32_768, InputPerM: 0.60, OutputPerM: 2.50, CacheReadPerM: 0.15, CacheWritePerM: 0.60},
		},
	},
	{
		ID:   "xai",
		Name: "xAI (Grok)",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"XAI_API_KEY"},
		Models: []builtinModelTemplate{
			{ID: "grok-4", Name: "Grok 4", Context: 256_000, Output: 32_768, InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.75, CacheWritePerM: 3.0, Reasoning: true},
			{ID: "grok-4-fast", Name: "Grok 4 Fast", Context: 2_000_000, Output: 32_768, InputPerM: 0.20, OutputPerM: 0.50, CacheReadPerM: 0.05, CacheWritePerM: 0.20, Reasoning: true},
			{ID: "grok-3-mini", Name: "Grok 3 Mini", Context: 131_072, Output: 16_384, InputPerM: 0.20, OutputPerM: 0.50, CacheReadPerM: 0.05, CacheWritePerM: 0.20, Reasoning: true},
		},
	},
	{
		ID:   "ollama",
		Name: "Ollama (Local)",
		NPM:  "@ai-sdk/openai-compatible",
		Env:  []string{"OLLAMA_HOST"},
		Models: []builtinModelTemplate{
			{ID: "qwen2.5-coder:32b", Name: "Qwen 2.5 Coder 32B", Context: 131_072, Output: 16_384},
			{ID: "llama3.3:70b", Name: "Llama 3.3 70B", Context: 131_072, Output: 16_384},
			{ID: "deepseek-r1:32b", Name: "DeepSeek R1 32B", Context: 131_072, Output: 16_384, Reasoning: true},
		},
	},
}

func (m builtinModelTemplate) toConfigMap() map[string]any {
	out := map[string]any{
		"id":   m.ID,
		"name": m.Name,
		"limit": map[string]any{
			"context": m.Context,
			"output":  m.Output,
		},
		"reasoning": m.Reasoning,
	}
	if m.InputPerM > 0 || m.OutputPerM > 0 {
		out["cost"] = map[string]any{
			"input":       m.InputPerM,
			"output":      m.OutputPerM,
			"cache_read":  m.CacheReadPerM,
			"cache_write": m.CacheWritePerM,
		}
	} else {
		out["cost"] = map[string]any{
			"input":  0.0,
			"output": 0.0,
		}
	}
	return out
}

func findBuiltinProviderTemplate(providerID string) (builtinProviderTemplate, bool) {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	for _, tpl := range builtinProviderTemplates {
		if tpl.ID == pid {
			return tpl, true
		}
	}
	return builtinProviderTemplate{}, false
}

func builtinProviderConfigMap(providerID string) (map[string]any, string, bool) {
	tpl, ok := findBuiltinProviderTemplate(providerID)
	if !ok {
		return nil, "", false
	}
	models := make(map[string]any, len(tpl.Models))
	defaultModelID := ""
	for _, m := range tpl.Models {
		if defaultModelID == "" {
			defaultModelID = m.ID
		}
		models[m.ID] = m.toConfigMap()
	}
	envCopy := make([]string, len(tpl.Env))
	copy(envCopy, tpl.Env)
	return map[string]any{
		"id":     tpl.ID,
		"name":   tpl.Name,
		"npm":    tpl.NPM,
		"env":    envCopy,
		"source": "builtin",
		"models": models,
	}, defaultModelID, true
}

func builtinModelConfigMap(providerID, modelID string) map[string]any {
	tpl, ok := findBuiltinProviderTemplate(providerID)
	if !ok {
		return nil
	}
	mid := strings.TrimSpace(modelID)
	for _, m := range tpl.Models {
		if m.ID == mid {
			return m.toConfigMap()
		}
	}
	return nil
}

// unrevokedAuthProviderIDs inspects Hoxi's engine auth.json (and legacy OpenCode
// auth.json) in priority order, returning provider IDs with unrevoked credentials.
func unrevokedAuthProviderIDs() map[string]bool {
	authIDs := map[string]bool{}
	decided := map[string]bool{}
	for _, authPath := range candidateProviderAuthPaths() {
		raw, err := os.ReadFile(authPath)
		if err != nil {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) != nil {
			continue
		}
		for k, v := range parsed {
			pid := strings.TrimSpace(k)
			if pid == "" || decided[pid] {
				continue
			}
			decided[pid] = true
			if isValidUnrevokedAuthEntry(v) {
				authIDs[pid] = true
			}
		}
	}
	return authIDs
}

// loadEffectiveConfiguredProvidersMap merges OpenCode-imported providers,
// Hoxi's .hoxi/models.json providers, and any built-in provider templates that
// have active credentials in auth.json (backfilling default models only when a
// configured built-in entry omitted its models block).
func (g *Service) loadEffectiveConfiguredProvidersMap() map[string]interface{} {
	merged := g.loadOpenCodeImportedProviderMap()
	for k, v := range g.loadHoxiProvidersMap() {
		merged[k] = v
	}
	for pid, raw := range merged {
		pObj, ok := raw.(map[string]interface{})
		if !ok || pObj == nil {
			continue
		}
		mMap, _ := pObj["models"].(map[string]interface{})
		if len(mMap) > 0 {
			continue
		}
		if tplMap, _, hasTpl := builtinProviderConfigMap(pid); hasTpl {
			cp := make(map[string]interface{}, len(pObj)+1)
			for k, v := range pObj {
				cp[k] = v
			}
			cp["models"] = tplMap["models"]
			merged[pid] = cp
		}
	}
	for pid, ok := range g.unrevokedAuthProviderIDs() {
		if !ok || merged[pid] != nil {
			continue
		}
		if g.officialBlocked.Load() && pid == officialProviderID {
			continue
		}
		if tplMap, _, hasTpl := builtinProviderConfigMap(pid); hasTpl {
			merged[pid] = tplMap
		}
	}
	return merged
}
