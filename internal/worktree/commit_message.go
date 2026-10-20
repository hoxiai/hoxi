package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
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

// HandleGenerateCommitMessage drafts a Conventional Commit subject from the
// session context and diff summary. It is served at the public
// /api/hoxi/provider/generate-commit-message route.
func (m *Manager) HandleGenerateCommitMessage(w http.ResponseWriter, r *http.Request) {
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

	if client, _ := m.reviewLLM(); client != nil {
		target := client.Resolve(req.ProviderID, req.ModelID, req.BaseURL, req.NPM)
		if msg, err := invokeCommitMessageLLM(r.Context(), client, target, req); err == nil && msg != "" {
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

func invokeCommitMessageLLM(
	parentCtx context.Context,
	client llm.Client,
	target llm.Target,
	req generateCommitMessageRequest,
) (string, error) {
	if !target.Ready() {
		return "", llm.ErrUnavailable
	}
	diffSnippet := llm.TruncateRunes(strings.TrimSpace(req.DiffSummary), 2400)
	digestSnippet := llm.TruncateRunes(strings.TrimSpace(llm.StripCodeFences(req.SessionDigest, " ")), 1200)
	if digestSnippet == "" {
		digestSnippet = llm.TruncateRunes(strings.TrimSpace(llm.StripCodeFences(req.LastPrompt, " ")), 500)
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
	rawText, err := client.Complete(parentCtx, target, llm.Request{
		Prompt:      instruction,
		MaxTokens:   2048,
		Temperature: 0.2,
		Timeout:     20 * time.Second,
		Purpose:     llm.PurposeInteractive,
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
	text := llm.StripThinking(raw)
	text = llm.StripCodeFences(text, "")
	text = strings.TrimSpace(text)
	if text == "" || llm.LooksLikeRawJSON(text) {
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
		if !llm.HasMeaningfulRune(l) {
			continue
		}
		if conventionalCommitRegex.MatchString(l) {
			return llm.TruncateRunes(l, 88)
		}
		if firstCandidate == "" {
			firstCandidate = llm.TruncateRunes(l, 88)
		}
	}
	return firstCandidate
}
