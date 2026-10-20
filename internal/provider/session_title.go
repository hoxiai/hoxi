package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type summarizeTitleRequest struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	BaseURL    string `json:"baseURL,omitempty"`
	NPM        string `json:"npm,omitempty"`
	Prompt     string `json:"prompt"`
	Locale     string `json:"locale"`
}

type summarizeTitleResponse struct {
	OK    bool   `json:"ok"`
	Title string `json:"title"`
	Error string `json:"error,omitempty"`
}

var (
	thinkBlockRegex  = regexp.MustCompile(`(?is)<think>.*?</think>`)
	codeFenceRegex   = regexp.MustCompile("(?s)```.*?```")
	titlePrefixRegex = regexp.MustCompile(`(?i)^(会话标题|会话名称|标题|主题|总结|session\s*title|title|summary|topic)\s*[:：\-]\s*`)
)

func (g *Service) handleSummarizeSessionTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req summarizeTitleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, summarizeTitleResponse{OK: false, Error: "Invalid request payload"})
		return
	}

	rawPrompt := strings.TrimSpace(req.Prompt)
	if rawPrompt == "" {
		writeJSON(w, http.StatusOK, summarizeTitleResponse{OK: false, Error: "Prompt is required"})
		return
	}

	locale := strings.TrimSpace(req.Locale)
	if locale == "" {
		locale = "zh-CN"
	}

	pid, mid := g.resolveBackendAutoCandidate(strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ModelID))
	baseURL, apiKey, npm, headers := g.resolveProviderConnection(pid, req.BaseURL, req.NPM)

	if pid != "" && mid != "" && (strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://")) {
		if llmTitle, err := g.invokeTitleSummarizerLLM(r.Context(), pid, mid, baseURL, apiKey, npm, headers, rawPrompt, locale); err == nil && llmTitle != "" {
			writeJSON(w, http.StatusOK, summarizeTitleResponse{
				OK:    true,
				Title: llmTitle,
			})
			return
		}
	}

	fallback := fallbackSummarizeTitle(rawPrompt, locale)
	writeJSON(w, http.StatusOK, summarizeTitleResponse{
		OK:    true,
		Title: fallback,
	})
}

func (g *Service) invokeTitleSummarizerLLM(
	parentCtx context.Context,
	providerID, modelID, baseURL, apiKey, npm string,
	headers map[string]string,
	userPrompt, locale string,
) (string, error) {
	isZh := strings.HasPrefix(strings.ToLower(locale), "zh")
	snippet := truncateRunes(strings.TrimSpace(codeFenceRegex.ReplaceAllString(userPrompt, " ")), 400)
	if snippet == "" {
		snippet = truncateRunes(userPrompt, 400)
	}

	var instruction string
	if isZh {
		instruction = fmt.Sprintf(`请根据以下用户的任务请求，总结一个简短精炼的中文会话标题（4 到 14 个汉字以内）。
严格要求：
1. 必须按照当前界面语言（简体中文）输出会话名称（专有技术名词如 Git、SSE、CORS、SolidJS 等可保留英文）。
2. 只输出最终标题本身，严禁包含引号、书名号、句号、“标题：”前缀、Markdown 符号或任何解释。

用户任务请求：
%s`, snippet)
	} else {
		instruction = fmt.Sprintf(`Summarize the user's task request below into a concise English session title (3 to 7 words, max 48 characters).
Strict rules:
1. You MUST output the session title in English (current UI language), even if the user prompt is in another language.
2. Output ONLY the title text itself — no quotes, no trailing period, no "Title:" prefix, and no markdown formatting.

User task request:
%s`, snippet)
	}

	rawText, err := callSimpleLLM(parentCtx, llmCompletionRequest{
		ProviderID:  providerID,
		ModelID:     modelID,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		NPM:         npm,
		Headers:     headers,
		Prompt:      instruction,
		MaxTokens:   512,
		Temperature: 0.2,
		Timeout:     12 * time.Second,
		Transport:   titleTransport,
		Telemetry:   g.telemetry,
	})
	if err != nil {
		return "", err
	}

	cleaned := cleanSummarizedTitle(rawText, locale)
	if cleaned == "" {
		return "", fmt.Errorf("empty summarized title")
	}
	return cleaned, nil
}

func cleanSummarizedTitle(raw, locale string) string {
	text := thinkBlockRegex.ReplaceAllString(raw, "")
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || strings.Contains(text, `"choices"`) {
		return ""
	}

	found := false
	// Take the first non-empty line with meaningful content
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "{") || strings.HasPrefix(l, "[") || strings.Contains(l, `"finish_reason"`) {
			continue
		}
		l = strings.TrimLeft(l, "#*-•> \t")
		l = titlePrefixRegex.ReplaceAllString(l, "")
		l = strings.Trim(l, "\"'`“”‘’「」『』《》【】[]()（）.。!！?？;；,，:： \t")
		if hasMeaningfulRune(l) {
			text = l
			found = true
			break
		}
	}
	if !found || !hasMeaningfulRune(text) {
		return ""
	}

	isZh := strings.HasPrefix(strings.ToLower(locale), "zh")
	if isZh {
		return truncateRunes(text, 22)
	}
	return truncateRunes(text, 50)
}

func hasMeaningfulRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func fallbackSummarizeTitle(prompt, locale string) string {
	stripped := strings.TrimSpace(codeFenceRegex.ReplaceAllString(prompt, " "))
	if stripped == "" {
		stripped = strings.TrimSpace(prompt)
	}
	firstLine := stripped
	for _, line := range strings.Split(stripped, "\n") {
		l := strings.TrimSpace(line)
		if l != "" {
			firstLine = l
			break
		}
	}

	// Split on common sentence delimiters to grab the primary intent clause
	for _, sep := range []string{"。", "；", "！", "？", ". ", "? ", "! "} {
		if idx := strings.Index(firstLine, sep); idx > 6 {
			firstLine = firstLine[:idx]
			break
		}
	}

	cleaned := cleanSummarizedTitle(firstLine, locale)
	if cleaned != "" {
		return cleaned
	}
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		return "新任务会话"
	}
	return "New Task Session"
}

func truncateRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes]))
}
