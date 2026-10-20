package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type enhancePromptRequest struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	BaseURL    string `json:"baseURL,omitempty"`
	NPM        string `json:"npm,omitempty"`
	Prompt     string `json:"prompt"`
	Locale     string `json:"locale,omitempty"`
}

type enhancePromptResponse struct {
	OK     bool   `json:"ok"`
	Prompt string `json:"prompt"`
	Error  string `json:"error,omitempty"`
}

var enhancePrefixRegex = compilePrefixRegex(
	"enhanced prompt", "improved prompt", "optimized prompt", "enhance prompt",
	"增强后的提示词", "优化后的提示词", "提示词",
)

func compilePrefixRegex(prefixes ...string) *regexp.Regexp {
	escaped := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		escaped[i] = regexp.QuoteMeta(prefix)
	}
	return regexp.MustCompile(`(?i)^(` + strings.Join(escaped, "|") + `)\s*[:：\-]\s*`)
}

func (g *Service) handleEnhancePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req enhancePromptRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, enhancePromptResponse{OK: false, Error: "Invalid request payload"})
		return
	}
	rawPrompt := strings.TrimSpace(req.Prompt)
	if rawPrompt == "" {
		writeJSON(w, http.StatusOK, enhancePromptResponse{OK: false, Error: "Prompt is required"})
		return
	}
	if len([]rune(rawPrompt)) > 12000 {
		writeJSON(w, http.StatusOK, enhancePromptResponse{OK: false, Error: "Prompt is too long"})
		return
	}

	locale := strings.TrimSpace(req.Locale)
	if locale == "" {
		locale = "en-US"
	}
	pid, mid := g.resolveBackendAutoCandidate(strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ModelID))
	baseURL, apiKey, npm, headers := g.resolveProviderConnection(pid, req.BaseURL, req.NPM)
	if pid == "" || mid == "" || (!strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://")) {
		writeJSON(w, http.StatusOK, enhancePromptResponse{OK: false, Error: "No model is available for prompt enhancement"})
		return
	}

	enhanced, err := g.invokePromptEnhancerLLM(r.Context(), pid, mid, baseURL, apiKey, npm, headers, rawPrompt, locale)
	if err != nil || enhanced == "" {
		writeJSON(w, http.StatusOK, enhancePromptResponse{OK: false, Error: "Could not enhance the prompt"})
		return
	}
	writeJSON(w, http.StatusOK, enhancePromptResponse{OK: true, Prompt: enhanced})
}

func (g *Service) invokePromptEnhancerLLM(
	parentCtx context.Context,
	providerID, modelID, baseURL, apiKey, npm string,
	headers map[string]string,
	rawPrompt, locale string,
) (string, error) {
	language := "English"
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		language = "Simplified Chinese"
	}
	instruction := fmt.Sprintf(`You improve prompts for an AI coding agent inside a desktop workspace.
Rewrite the user's request so it is clearer, more specific, and easier to execute.

Rules:
1. Preserve the user's intent, constraints, names, paths, and technical terms. Do not invent files, APIs, or requirements.
2. Add helpful structure only when it reduces ambiguity: goal, scope, constraints, and a concrete definition of done.
3. Keep it concise. Do not add generic filler, role-play, or explanations.
4. Output in %s, matching the UI language.
5. Output ONLY the improved prompt. Do not include quotes, prefixes like "Enhanced prompt:", markdown fences around the whole answer, or commentary.

User request:
%s`, language, rawPrompt)

	rawText, err := callSimpleLLM(parentCtx, llmCompletionRequest{
		ProviderID:  providerID,
		ModelID:     modelID,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		NPM:         npm,
		Headers:     headers,
		Prompt:      instruction,
		MaxTokens:   1536,
		Temperature: 0.3,
		Timeout:     20 * time.Second,
		Transport:   titleTransport,
		Telemetry:   g.telemetry,
	})
	if err != nil {
		return "", err
	}
	return cleanEnhancedPrompt(rawText), nil
}

func cleanEnhancedPrompt(raw string) string {
	text := thinkBlockRegex.ReplaceAllString(raw, "")
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || strings.Contains(text, `"choices"`) {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		last := len(lines) - 1
		for last > 0 && strings.TrimSpace(lines[last]) == "" {
			last--
		}
		if strings.HasPrefix(strings.TrimSpace(lines[last]), "```") {
			lines = lines[1:last]
		}
	}
	text = strings.TrimSpace(strings.Join(lines, "\n"))
	text = enhancePrefixRegex.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if !hasMeaningfulRune(text) {
		return ""
	}
	return truncateRunes(text, 12000)
}
