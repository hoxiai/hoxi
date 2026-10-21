package promptassist

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
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
	titlePrefixRegex = regexp.MustCompile(`(?i)^(会话标题|会话名称|标题|主题|总结|session\s*title|title|summary|topic)\s*[:：\-]\s*`)
)

// HandleSummarizeTitle names a session from its first prompt, falling back to
// a heuristic title when no model answers. It is served at the public
// /api/hoxi/provider/summarize-title route.
func (s *Service) HandleSummarizeTitle(w http.ResponseWriter, r *http.Request) {
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

	if s.llm != nil {
		target := s.llm.Resolve(req.ProviderID, req.ModelID, req.BaseURL, req.NPM)
		if llmTitle, err := s.invokeTitleSummarizerLLM(r.Context(), target, rawPrompt, locale); err == nil && llmTitle != "" {
			writeJSON(w, http.StatusOK, summarizeTitleResponse{
				OK:    true,
				Title: llmTitle,
			})
			return
		}
	}

	fallback := fallbackSummarizeTitle(rawPrompt, locale)
	// The heuristic echoes the prompt, so a title that looks like the first
	// message usually means the configured role model cannot serve HTTP calls.
	slog.Warn("session title summarizer fell back to prompt",
		"providerID", req.ProviderID,
		"modelID", req.ModelID,
	)
	writeJSON(w, http.StatusOK, summarizeTitleResponse{
		OK:    true,
		Title: fallback,
	})
}

func (s *Service) invokeTitleSummarizerLLM(
	parentCtx context.Context,
	target llm.Target,
	userPrompt, locale string,
) (string, error) {
	if !target.Ready() {
		return "", llm.ErrUnavailable
	}
	isZh := strings.HasPrefix(strings.ToLower(locale), "zh")
	snippet := llm.TruncateRunes(strings.TrimSpace(llm.StripCodeFences(userPrompt, " ")), 400)
	if snippet == "" {
		snippet = llm.TruncateRunes(userPrompt, 400)
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

	rawText, err := s.llm.Complete(parentCtx, target, llm.Request{
		Prompt:      instruction,
		MaxTokens:   512,
		Temperature: 0.2,
		Timeout:     12 * time.Second,
		Purpose:     llm.PurposeInteractive,
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
	text := strings.TrimSpace(llm.StripThinking(raw))
	if text == "" || llm.LooksLikeRawJSON(text) {
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
		if llm.HasMeaningfulRune(l) {
			text = l
			found = true
			break
		}
	}
	if !found || !llm.HasMeaningfulRune(text) {
		return ""
	}

	isZh := strings.HasPrefix(strings.ToLower(locale), "zh")
	if isZh {
		return llm.TruncateRunes(text, 22)
	}
	return llm.TruncateRunes(text, 50)
}

func fallbackSummarizeTitle(prompt, locale string) string {
	stripped := strings.TrimSpace(llm.StripCodeFences(prompt, " "))
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
