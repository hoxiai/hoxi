package provider

import (
	"context"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

var _ llm.Client = (*Service)(nil)

// Resolve implements llm.Client: empty or "auto" IDs pick the backend auto
// candidate, and the provider connection decides whether an HTTP endpoint exists.
func (s *Service) Resolve(providerID, modelID, baseURL, npm string) llm.Target {
	pid, mid := s.resolveBackendAutoCandidate(strings.TrimSpace(providerID), strings.TrimSpace(modelID))
	target := llm.Target{ProviderID: pid, ModelID: mid, BaseURL: baseURL, NPM: npm}
	if pid != "" {
		resolvedBase, _, _, _ := s.resolveProviderConnection(pid, baseURL, npm)
		target.HasEndpoint = isHTTPEndpoint(resolvedBase)
	}
	return target
}

// Complete implements llm.Client on top of callSimpleLLM, so retries,
// diagnostics and per-provider usage stats apply to every internal call.
func (s *Service) Complete(ctx context.Context, target llm.Target, req llm.Request) (string, error) {
	if !target.HasModel() {
		return "", llm.ErrUnavailable
	}
	baseURL, apiKey, npm, headers := s.resolveProviderConnection(target.ProviderID, target.BaseURL, target.NPM)
	if !isHTTPEndpoint(baseURL) {
		return "", llm.ErrUnavailable
	}
	transport := titleTransport
	if req.Purpose == llm.PurposeReview {
		transport = providerTransport
	}
	return callSimpleLLM(ctx, llmCompletionRequest{
		ProviderID:  target.ProviderID,
		ModelID:     target.ModelID,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		NPM:         npm,
		Headers:     headers,
		Prompt:      req.Prompt,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Timeout:     req.Timeout,
		Transport:   transport,
		Stats:       s.stats,
		Breaker:     s.breaker,
	})
}

// RecordAuditResult folds an observer verdict into the worker model's stats.
func (s *Service) RecordAuditResult(providerID, modelID string, score int, pass bool, summary string) {
	if s.stats != nil {
		s.stats.recordAuditResult(providerID, modelID, score, pass, summary)
	}
}

func isHTTPEndpoint(baseURL string) bool {
	return strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://")
}

// Assist actions keep their historical /api/hoxi/provider/<action> routes,
// while the handlers live in the domain packages that own each feature and are
// injected by the gateway through SetAssistHandler.
const (
	AssistAudit                 = "audit"
	AssistSummarizeTitle        = "summarize-title"
	AssistGenerateCommitMessage = "generate-commit-message"
	AssistEnhancePrompt         = "enhance-prompt"
)

var assistActions = []string{AssistAudit, AssistSummarizeTitle, AssistGenerateCommitMessage, AssistEnhancePrompt}

// SetAssistHandler mounts h behind /api/hoxi/provider/<action>.
func (s *Service) SetAssistHandler(action string, h http.HandlerFunc) {
	s.assistMu.Lock()
	defer s.assistMu.Unlock()
	if s.assist == nil {
		s.assist = make(map[string]http.HandlerFunc)
	}
	s.assist[action] = h
}

func (s *Service) serveAssist(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.assistMu.RLock()
		h := s.assist[action]
		s.assistMu.RUnlock()
		if h == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "assist handler is not configured")
			return
		}
		h(w, r)
	}
}
