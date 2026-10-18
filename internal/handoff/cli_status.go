package handoff

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func (g *desktopGateway) handleLocalCLIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	hoxiMap := g.loadHoxiProvidersMap()
	detections := []func() LocalCLIStatusItem{
		func() LocalCLIStatusItem { return detectGrokCLIStatus(hoxiMap) },
		func() LocalCLIStatusItem { return detectClaudeCLIStatus(hoxiMap) },
		func() LocalCLIStatusItem { return detectCodexCLIStatus(g, hoxiMap) },
		func() LocalCLIStatusItem { return detectGeminiCLIStatus(g, hoxiMap) },
	}
	tools := make([]LocalCLIStatusItem, len(detections))
	var scans sync.WaitGroup
	for i, detect := range detections {
		scans.Add(1)
		go func(i int, detect func() LocalCLIStatusItem) {
			defer scans.Done()
			tools[i] = detect()
		}(i, detect)
	}
	scans.Wait()
	writeJSON(w, http.StatusOK, LocalCLIStatusResponse{Tools: tools})
}

func readClaudeSettingsEnv() (baseURL, apiKey, defaultModel string, models []string) {
	baseURL = strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	apiKey = firstNonEmpty(strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")), strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")))
	cDir := claudeHomeDir()
	if cDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cDir, "settings.json")); err == nil {
			var doc map[string]interface{}
			if json.Unmarshal(raw, &doc) == nil {
				defaultModel = strings.TrimSpace(asString(doc["model"]))
				if defaultModel != "" {
					models = appendUniqueString(models, defaultModel)
				}
				if envMap, ok := doc["env"].(map[string]interface{}); ok {
					if u := strings.TrimSpace(asString(envMap["ANTHROPIC_BASE_URL"])); u != "" {
						baseURL = u
					}
					if k := firstNonEmpty(asString(envMap["ANTHROPIC_AUTH_TOKEN"]), asString(envMap["ANTHROPIC_API_KEY"])); k != "" {
						apiKey = k
					}
					for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
						if m := strings.TrimSpace(asString(envMap[k])); m != "" {
							models = appendUniqueString(models, m)
							if defaultModel == "" {
								defaultModel = m
							}
						}
					}
				}
			}
		}
		if apiKey == "" {
			if raw, err := os.ReadFile(filepath.Join(cDir, ".credentials.json")); err == nil {
				var credDoc struct {
					ClaudeAiOauth struct {
						AccessToken string `json:"accessToken"`
					} `json:"claudeAiOauth"`
				}
				if json.Unmarshal(raw, &credDoc) == nil && strings.TrimSpace(credDoc.ClaudeAiOauth.AccessToken) != "" {
					apiKey = strings.TrimSpace(credDoc.ClaudeAiOauth.AccessToken)
				}
			}
		}
		if raw, err := os.ReadFile(filepath.Join(filepath.Dir(cDir), ".claude.json")); err == nil {
			var rootDoc map[string]interface{}
			if json.Unmarshal(raw, &rootDoc) == nil {
				if extra, ok := rootDoc["additionalModelOptionsCache"].([]interface{}); ok {
					for _, item := range extra {
						if im, ok := item.(map[string]interface{}); ok {
							if v := strings.TrimSpace(asString(im["value"])); v != "" {
								models = appendUniqueString(models, v)
							}
						}
					}
				}
				if apiKey == "" && rootDoc["oauthAccount"] != nil {
					apiKey = "claude-code-local-oauth"
				}
			}
		}
	}
	sessions, _ := scanClaudeSessions("", "")
	for _, s := range sessions {
		if m := strings.TrimSpace(s.Model); m != "" && !strings.HasPrefix(m, "<") {
			models = appendUniqueString(models, m)
			if defaultModel == "" {
				defaultModel = m
			}
		}
	}
	if len(models) == 0 {
		models = []string{"claude-sonnet-4-6", "claude-opus-4-6"}
	}
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0]
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return baseURL, apiKey, defaultModel, models
}

func detectClaudeCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	_, stats := scanClaudeSessions("", "")
	_, apiKey, defaultModel, models := readClaudeSettingsEnv()
	_, connected := hoxiMap["claude-code"]
	return LocalCLIStatusItem{
		ID:            ExternalToolClaude,
		Name:          "Claude Code",
		Installed:     stats.Installed,
		Authenticated: apiKey != "" || stats.SessionCount > 0,
		Connected:     connected,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "claude-settings",
		ProviderID:    "claude-code",
	}
}

func (g *desktopGateway) importClaudeCLIProvider() LocalCLIImportResponse {
	baseURL, apiKey, _, models := readClaudeSettingsEnv()
	if apiKey == "" {
		apiKey = firstNonEmpty(g.resolveStoredProviderKey("claude-code", ""), g.resolveStoredProviderKey("anthropic", ""))
	}
	if apiKey == "" {
		if _, stats := scanClaudeSessions("", ""); stats.Installed && stats.SessionCount > 0 {
			apiKey = "claude-code-local-oauth"
		}
	}
	if apiKey == "" {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolClaude),
			Error:  "No ANTHROPIC_AUTH_TOKEN, ANTHROPIC_API_KEY, or Claude OAuth credentials found in ~/.claude",
		}
	}
	modelsMap := map[string]interface{}{}
	for _, m := range models {
		modelsMap[m] = map[string]interface{}{"name": m + " (Claude Code)", "tool_call": true, "reasoning": true}
	}
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		provMap = map[string]interface{}{}
	}
	provMap["claude-code"] = map[string]interface{}{
		"name":    "Claude Code (Local)",
		"npm":     "@ai-sdk/anthropic",
		"options": map[string]interface{}{"baseURL": baseURL, "apiKey": apiKey},
		"models":  modelsMap,
	}
	doc["provider"] = provMap
	if err := g.persistHoxiConfigDoc(doc); err != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolClaude), Error: err.Error()}
	}
	return LocalCLIImportResponse{OK: true, ToolID: string(ExternalToolClaude), ProviderID: "claude-code", Models: models}
}
