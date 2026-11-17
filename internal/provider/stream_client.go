package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

var (
	_ llm.StreamClient            = (*Service)(nil)
	_ llm.SubagentEconomyResolver = (*Service)(nil)
)

// DefaultStreamStallTimeout bounds how long an open model SSE body may remain
// completely silent between reads before the request is cancelled as stalled.
const DefaultStreamStallTimeout = 180 * time.Second

type stallReadCloser struct {
	body    io.ReadCloser
	limit   time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	stalled *atomic.Bool
	once    sync.Once
}

func newStallReadCloser(body io.ReadCloser, limit time.Duration, cancel context.CancelFunc, stalled *atomic.Bool, timer *time.Timer) io.ReadCloser {
	if limit <= 0 {
		return body
	}
	return &stallReadCloser{
		body:    body,
		limit:   limit,
		timer:   timer,
		cancel:  cancel,
		stalled: stalled,
	}
}

func (r *stallReadCloser) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if r.stalled.Load() {
		return n, fmt.Errorf("model stream stalled after %s without data: %w", r.limit, context.DeadlineExceeded)
	}
	if n > 0 {
		r.timer.Reset(r.limit)
	}
	return n, err
}

func (r *stallReadCloser) Close() error {
	var closeErr error
	r.once.Do(func() {
		r.timer.Stop()
		r.cancel()
		closeErr = r.body.Close()
	})
	return closeErr
}

// ResolveStreamTarget resolves provider connection metadata and token limits
// without exposing credentials outside internal/provider.
func (s *Service) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	pid, mid := s.resolveBackendAutoCandidate(strings.TrimSpace(providerID), strings.TrimSpace(modelID))
	target := llm.StreamTarget{
		ProviderID: pid,
		ModelID:    mid,
	}
	if pid == "" || mid == "" {
		return target
	}
	baseURL, _, npm, _ := s.resolveProviderConnection(pid, "", "")
	target.BaseURL = baseURL
	target.NPM = npm
	target.HasEndpoint = isHTTPEndpoint(baseURL)

	switch effectiveProviderProtocol(pid, npm, baseURL) {
	case "anthropic":
		target.Protocol = llm.ProtocolAnthropic
	case "google":
		target.Protocol = llm.ProtocolGoogle
	default:
		if isOpenAIResponsesProtocol(npm, baseURL) {
			target.Protocol = llm.ProtocolOpenAIResponses
		} else {
			target.Protocol = llm.ProtocolOpenAI
		}
	}

	if lim, ok := knownModelTokenLimit(canonicalModelID(mid)); ok {
		target.ContextLimit = lim.Context
		target.OutputLimit = lim.Output
	} else {
		target.ContextLimit = 200_000
		target.OutputLimit = 16_384
	}
	// Reuse the model options exposed to OpenCode so native turns honor the
	// same legacy/adaptive thinking modes and explicit non-reasoning models.
	target.Variants = defaultModelVariants(pid, npm, mid)
	if target.Protocol == llm.ProtocolGoogle && isNonThinkingGeminiChatModel(mid) {
		target.Variants = nil
	}
	providers := s.loadOpenCodeImportedProviderMap()
	for id, cfg := range s.loadHoxiProvidersMap() {
		providers[id] = cfg
	}
	if cfg, ok := providers[pid].(map[string]any); ok {
		models, _ := cfg["models"].(map[string]any)
		model, _ := models[mid].(map[string]any)
		if limits, ok := model["limit"].(map[string]any); ok {
			if n := positiveNumber(limits["context"]); n > 0 {
				target.ContextLimit = n
			}
			if n := positiveNumber(limits["output"]); n > 0 {
				target.OutputLimit = n
			}
		}
		if variants, ok := model["variants"].(map[string]any); ok {
			target.Variants = variants
		}
		if reasoning, ok := model["reasoning"].(bool); ok && !reasoning {
			target.Variants = nil
		}
	}
	return target
}

func resolveStreamEndpointURL(baseURL string, target llm.StreamTarget) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch target.Protocol {
	case llm.ProtocolAnthropic:
		if strings.HasSuffix(trimmedBase, "/v1") {
			return trimmedBase + "/messages"
		}
		return trimmedBase + "/v1/messages"
	case llm.ProtocolGoogle:
		cleanModel := strings.TrimPrefix(strings.TrimSpace(target.ModelID), "models/")
		return googleModelsBaseURL(trimmedBase) + "/" + url.PathEscape(cleanModel) + ":streamGenerateContent?alt=sse"
	case llm.ProtocolOpenAIResponses:
		if strings.HasSuffix(strings.ToLower(trimmedBase), "/responses") {
			return trimmedBase
		}
		return trimmedBase + "/responses"
	default:
		return trimmedBase + "/chat/completions"
	}
}

// OpenStream opens an authenticated SSE streaming HTTP response for req.
func (s *Service) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	if !req.Target.HasEndpoint {
		return nil, llm.ErrUnavailable
	}
	baseURL, apiKey, npm, headers := s.resolveProviderConnection(req.Target.ProviderID, req.Target.BaseURL, req.Target.NPM)
	if !isHTTPEndpoint(baseURL) {
		return nil, llm.ErrUnavailable
	}
	targetURL := resolveStreamEndpointURL(baseURL, req.Target)
	proto := effectiveProviderProtocol(req.Target.ProviderID, npm, baseURL)

	stallLimit := DefaultStreamStallTimeout
	if s.streamStallTimeout > 0 {
		stallLimit = s.streamStallTimeout
	}

	doOpen := func(reqURL string, includeBearerWithQueryKey bool) (*http.Response, error) {
		reqCtx, cancelReq := context.WithCancel(ctx)
		stalled := &atomic.Bool{}
		timer := time.AfterFunc(stallLimit, func() {
			stalled.Store(true)
			cancelReq()
		})

		outReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, bytes.NewReader(req.Body))
		if err != nil {
			timer.Stop()
			cancelReq()
			return nil, err
		}
		outReq.Header.Set("Accept", "text/event-stream")
		outReq.Header.Set("Content-Type", "application/json")

		hasCustomAuth := false
		for k, v := range headers {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "x-goog-api-key") || strings.EqualFold(k, "x-api-key") {
					hasCustomAuth = true
				}
				outReq.Header.Set(k, v)
			}
		}
		for k, v := range req.ExtraHeaders {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				outReq.Header.Set(k, v)
			}
		}

		if apiKey != "" && !hasCustomAuth {
			switch proto {
			case "anthropic":
				outReq.Header.Set("x-api-key", apiKey)
				outReq.Header.Set("anthropic-version", "2023-06-01")
			case "google":
				outReq.Header.Set("x-goog-api-key", apiKey)
				if includeBearerWithQueryKey {
					outReq.Header.Set("Authorization", "Bearer "+apiKey)
				}
			default:
				outReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}

		transport := providerTransport
		if isLoopbackURL(reqURL) {
			transport = localTransport
		}
		client := &http.Client{Transport: transport}
		resp, doErr := client.Do(outReq)
		if doErr != nil {
			timer.Stop()
			cancelReq()
			if stalled.Load() {
				return nil, fmt.Errorf("model stream stalled after %s without response headers: %w", stallLimit, context.DeadlineExceeded)
			}
			return nil, doErr
		}
		resp.Body = newStallReadCloser(resp.Body, stallLimit, cancelReq, stalled, timer)
		return resp, nil
	}

	started := time.Now()
	resp, err := doOpen(targetURL, false)
	if err != nil {
		if s.breaker != nil {
			s.breaker.RecordFailure(req.Target.ProviderID, req.Target.ModelID, classifyCandidateFailure(0, err, nil), 0, 0, err.Error())
		}
		return nil, err
	}
	if proto == "google" && apiKey != "" && !isOfficialGoogleHost(targetURL) &&
		(resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		if retryResp, retryErr := doOpen(appendGoogleQueryKey(targetURL, apiKey), shouldSendBearerOnGoogleRetry(apiKey)); retryErr == nil && retryResp != nil {
			_ = resp.Body.Close()
			resp = retryResp
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		_ = resp.Body.Close()
		errText := redactSecretInText(extractAPIErrorMessage(resp.StatusCode, bodyBytes), apiKey)
		if s.breaker != nil {
			class := classifyCandidateFailure(resp.StatusCode, fmt.Errorf("%s", errText), nil)
			if isQuotaExhausted(bodyBytes) {
				class = breakerClassQuota
			}
			s.breaker.RecordFailure(req.Target.ProviderID, req.Target.ModelID, class, resp.StatusCode, 0, errText)
		}
		diagnostics.Record(diagnostics.Entry{
			Kind:       "request",
			Level:      "error",
			Source:     "provider",
			Message:    "native stream HTTP error",
			ProviderID: req.Target.ProviderID,
			ModelID:    req.Target.ModelID,
			Status:     resp.StatusCode,
			DurationMs: time.Since(started).Milliseconds(),
		})
		return nil, fmt.Errorf("%s", errText)
	}
	if s.breaker != nil {
		s.breaker.RecordSuccess(req.Target.ProviderID, req.Target.ModelID, time.Since(started).Milliseconds())
	}
	return resp, nil
}
