package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type generateCommitMessageRequest struct {
	ProviderID   string `json:"providerID"`
	ModelID      string `json:"modelID"`
	BaseURL      string `json:"baseURL,omitempty"`
	NPM          string `json:"npm,omitempty"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	LastPrompt   string `json:"lastPrompt,omitempty"`
	DiffSummary  string `json:"diffSummary"`
	Locale       string `json:"locale,omitempty"`
}

type generateCommitMessageResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

var commitPrefixRegex = regexp.MustCompile(`(?i)^(commit\s*message|git\s*commit|提交信息|提交说明|信息)\s*[:：\-]\s*`)

func (g *desktopGateway) handleGenerateCommitMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req generateCommitMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, generateCommitMessageResponse{OK: false, Error: "Invalid request payload"})
		return
	}

	diffSummary := strings.TrimSpace(req.DiffSummary)
	if diffSummary == "" && strings.TrimSpace(req.SessionTitle) == "" && strings.TrimSpace(req.LastPrompt) == "" {
		writeJSON(w, http.StatusOK, generateCommitMessageResponse{OK: false, Error: "Diff summary or session context is required"})
		return
	}

	locale := strings.TrimSpace(req.Locale)
	if locale == "" {
		locale = "zh-CN"
	}

	pid := strings.TrimSpace(req.ProviderID)
	mid := strings.TrimSpace(req.ModelID)
	baseURL, apiKey, npm, headers := g.resolveProviderConnection(pid, req.BaseURL, req.NPM)

	if pid != "" && mid != "" && (strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://")) {
		if msg, err := g.invokeCommitMessageLLM(r.Context(), pid, mid, baseURL, apiKey, npm, headers, req, locale); err == nil && msg != "" {
			writeJSON(w, http.StatusOK, generateCommitMessageResponse{
				OK:      true,
				Message: msg,
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, generateCommitMessageResponse{
		OK:    false,
		Error: "LLM commit synthesis unavailable",
	})
}

func (g *desktopGateway) invokeCommitMessageLLM(
	parentCtx context.Context,
	providerID, modelID, baseURL, apiKey, npm string,
	headers map[string]string,
	req generateCommitMessageRequest,
	locale string,
) (string, error) {
	isZh := strings.HasPrefix(strings.ToLower(locale), "zh")
	diffSnippet := truncateRunes(strings.TrimSpace(req.DiffSummary), 1800)
	titleSnippet := truncateRunes(strings.TrimSpace(req.SessionTitle), 120)
	promptSnippet := truncateRunes(strings.TrimSpace(codeFenceRegex.ReplaceAllString(req.LastPrompt, " ")), 300)

	var instruction string
	if isZh {
		instruction = fmt.Sprintf(`请根据以下任务上下文与 Git 变更摘要，生成一条精炼的单行 Conventional Commit 提交信息（格式：<type>(<scope>): <简明中文说明>，例如 feat(timeline): 新增底部 Git 一键提交并推送功能）。
严格要求：
1. <type> 必须从 feat, fix, refactor, perf, style, docs, test, build, chore 中选择。
2. 只输出单行提交信息本身（总长度控制在 60 字以内），严禁包含引号、代码块、句号、“提交信息：”前缀或任何额外解释。

会话主题：%s
用户指令：%s
Git 变更文件与摘要：
%s`, titleSnippet, promptSnippet, diffSnippet)
	} else {
		instruction = fmt.Sprintf(`Generate a concise single-line Conventional Commit message (<type>(<scope>): <summary>, max 72 characters) based on the task context and Git diff summary below.
Strict rules:
1. <type> must be one of: feat, fix, refactor, perf, style, docs, test, build, chore.
2. Output ONLY the single-line commit message itself — no quotes, no markdown fences, no trailing period, and no explanation.

Session title: %s
User prompt: %s
Git diff summary:
%s`, titleSnippet, promptSnippet, diffSnippet)
	}

	isAnthropic := npm == "@ai-sdk/anthropic" || providerID == "anthropic"
	trimmedBase := strings.TrimRight(baseURL, "/")
	var targetURL string
	var bodyMap map[string]any

	if isAnthropic {
		if strings.HasSuffix(trimmedBase, "/v1") {
			targetURL = trimmedBase + "/messages"
		} else {
			targetURL = trimmedBase + "/v1/messages"
		}
		bodyMap = map[string]any{
			"model":      modelID,
			"max_tokens": 140,
			"messages": []map[string]string{
				{"role": "user", "content": instruction},
			},
		}
	} else {
		targetURL = trimmedBase + "/chat/completions"
		bodyMap = map[string]any{
			"model":       modelID,
			"max_tokens":  140,
			"temperature": 0.2,
			"messages": []map[string]string{
				{"role": "user", "content": instruction},
			},
		}
	}

	payloadBytes, _ := json.Marshal(bodyMap)
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", err
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if k != "" && v != "" {
			outReq.Header.Set(k, v)
		}
	}
	if apiKey != "" {
		if isAnthropic {
			outReq.Header.Set("x-api-key", apiKey)
			outReq.Header.Set("anthropic-version", "2023-06-01")
		} else {
			outReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: titleTransport,
	}
	if strings.Contains(targetURL, "://127.0.0.1") || strings.Contains(targetURL, "://localhost") {
		client.Transport = localTransport
	}

	resp, err := client.Do(outReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	rawText := extractLLMTextContent(respBytes)
	cleaned := cleanGeneratedCommitMessage(rawText)
	if cleaned == "" {
		return "", fmt.Errorf("empty generated commit message")
	}
	return cleaned, nil
}

func cleanGeneratedCommitMessage(raw string) string {
	text := thinkBlockRegex.ReplaceAllString(raw, "")
	text = codeFenceRegex.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		l = strings.TrimLeft(l, "#*-•> \t")
		l = commitPrefixRegex.ReplaceAllString(l, "")
		l = strings.Trim(l, "\"'`“”‘’「」『』.。;； \t")
		if hasMeaningfulRune(l) {
			return truncateRunes(l, 88)
		}
	}
	return ""
}
