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

type generateCommitMessageRequest struct {
	ProviderID    string `json:"providerID"`
	ModelID       string `json:"modelID"`
	BaseURL       string `json:"baseURL,omitempty"`
	NPM           string `json:"npm,omitempty"`
	SessionDigest string `json:"sessionDigest,omitempty"`
	SessionTitle  string `json:"sessionTitle,omitempty"`
	LastPrompt    string `json:"lastPrompt,omitempty"`
	DiffSummary   string `json:"diffSummary"`
	Locale        string `json:"locale,omitempty"`
}

type generateCommitMessageResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

var (
	commitPrefixRegex       = regexp.MustCompile(`(?i)^(commit\s*message|git\s*commit|subject|summary|提交信息|提交说明|信息)\s*[:：\-]\s*`)
	conventionalCommitRegex = regexp.MustCompile(`^(feat|fix|refactor|perf|style|docs|test|build|ci|chore|revert)(\([a-zA-Z0-9_.\-,/]+\))?!?:\s+.+`)
)

func (g *Service) handleGenerateCommitMessage(w http.ResponseWriter, r *http.Request) {
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
	if diffSummary == "" && strings.TrimSpace(req.SessionDigest) == "" && strings.TrimSpace(req.LastPrompt) == "" {
		writeJSON(w, http.StatusOK, generateCommitMessageResponse{OK: false, Error: "Diff summary or session context is required"})
		return
	}

	pid, mid := g.resolveBackendAutoCandidate(strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ModelID))
	baseURL, apiKey, npm, headers := g.resolveProviderConnection(pid, req.BaseURL, req.NPM)

	if pid != "" && mid != "" && (strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://")) {
		if msg, err := g.invokeCommitMessageLLM(r.Context(), pid, mid, baseURL, apiKey, npm, headers, req); err == nil && msg != "" {
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

func (g *Service) invokeCommitMessageLLM(
	parentCtx context.Context,
	providerID, modelID, baseURL, apiKey, npm string,
	headers map[string]string,
	req generateCommitMessageRequest,
) (string, error) {
	diffSnippet := truncateRunes(strings.TrimSpace(req.DiffSummary), 2400)
	digestSnippet := truncateRunes(strings.TrimSpace(codeFenceRegex.ReplaceAllString(req.SessionDigest, " ")), 1200)
	if digestSnippet == "" {
		digestSnippet = truncateRunes(strings.TrimSpace(codeFenceRegex.ReplaceAllString(req.LastPrompt, " ")), 500)
	}

	instruction := fmt.Sprintf(`You are an expert software engineer generating a Git commit message for the changes completed in the current session.
Analyze the Current Session Conversation Context and the Git Diff Summary below, then write a concise, accurate single-line English Conventional Commit message.

Strict requirements:
1. Format MUST be: <type>(<scope>): <concise imperative English description> (e.g., "feat(timeline): add one-click git commit and push in composer dropdown").
2. MUST be written strictly in English — even if the session conversation is in Chinese or another language, translate the technical intent into natural, idiomatic English.
3. <type> must be one of: feat, fix, refactor, perf, style, docs, test, build, chore.
4. Summarize WHAT code change was implemented and WHY based on the conversation and diff; NEVER copy conversational phrases or raw user questions.
5. Keep the line under 72 characters, imperative mood ("add", "fix", "refactor", not "added" or "adds"), no trailing period.
6. Output ONLY the single-line commit message itself — no quotes, no markdown fences, no explanation.

Current Session Conversation Context:
%s

Git Diff Summary:
%s`, digestSnippet, diffSnippet)

	// Use 2048 max_tokens so reasoning models (DeepSeek, Qwen, GLM, o-series) can finish their
	// internal reasoning_content without hitting finish_reason="length" before emitting content.
	rawText, err := callSimpleLLM(parentCtx, llmCompletionRequest{
		ProviderID:  providerID,
		ModelID:     modelID,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		NPM:         npm,
		Headers:     headers,
		Prompt:      instruction,
		MaxTokens:   2048,
		Temperature: 0.2,
		Timeout:     20 * time.Second,
		Transport:   titleTransport,
		Telemetry:   g.telemetry,
	})
	if err != nil {
		return "", err
	}

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
	if text == "" || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || strings.Contains(text, `"choices"`) {
		return ""
	}

	var firstCandidate string
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "{") || strings.HasPrefix(l, "[") || strings.Contains(l, `"finish_reason"`) || strings.Contains(l, `"choices"`) {
			continue
		}
		l = strings.TrimLeft(l, "#*-•> \t")
		l = commitPrefixRegex.ReplaceAllString(l, "")
		l = strings.Trim(l, "\"'`“”‘’「」『』.。;； \t")
		if !hasMeaningfulRune(l) {
			continue
		}
		if conventionalCommitRegex.MatchString(l) {
			return truncateRunes(l, 88)
		}
		if firstCandidate == "" {
			firstCandidate = truncateRunes(l, 88)
		}
	}
	return firstCandidate
}
