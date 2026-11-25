package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

var (
	_ llm.StreamClient            = (*Service)(nil)
	_ llm.StreamOutcomeReporter   = (*Service)(nil)
	_ llm.SubagentEconomyResolver = (*Service)(nil)
)

// DefaultStreamStallTimeout bounds how long an open model SSE body may remain
// completely silent between reads before the request is cancelled as stalled.
const DefaultStreamStallTimeout = 180 * time.Second

var (
	streamTransportMu   sync.Mutex
	streamHTTPTransport http.RoundTripper
)

// SetStreamTransportForTest overrides the HTTP transport used by OpenStream in tests.
func SetStreamTransportForTest(rt http.RoundTripper) {
	streamTransportMu.Lock()
	streamHTTPTransport = rt
	streamTransportMu.Unlock()
}

type stallReadCloser struct {
	body    io.ReadCloser
	limit   time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	stalled *atomic.Bool
	onClose func()
	once    sync.Once
}

func newStallReadCloser(body io.ReadCloser, limit time.Duration, cancel context.CancelFunc, stalled *atomic.Bool, timer *time.Timer, onClose func()) io.ReadCloser {
	if limit <= 0 && onClose == nil {
		return body
	}
	return &stallReadCloser{
		body:    body,
		limit:   limit,
		timer:   timer,
		cancel:  cancel,
		stalled: stalled,
		onClose: onClose,
	}
}

func (r *stallReadCloser) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if r.stalled != nil && r.stalled.Load() {
		return n, fmt.Errorf("model stream stalled after %s without data: %w", r.limit, context.DeadlineExceeded)
	}
	if n > 0 && r.timer != nil && r.limit > 0 {
		r.timer.Reset(r.limit)
	}
	return n, err
}

func (r *stallReadCloser) Close() error {
	var closeErr error
	r.once.Do(func() {
		if r.timer != nil {
			r.timer.Stop()
		}
		if r.cancel != nil {
			r.cancel()
		}
		closeErr = r.body.Close()
		if r.onClose != nil {
			r.onClose()
		}
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
	baseURL, _, npm, _ := s.resolveEffectiveProviderConnection(pid, "", "")
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
		} else if s.shouldPromoteClaudeToAnthropicMessages(pid, mid, baseURL) {
			target.Protocol = llm.ProtocolAnthropic
			target.FallbackOpenAI = true
		} else if s.shouldPromoteOpenAIToResponses(pid, mid, baseURL) {
			target.Protocol = llm.ProtocolOpenAIResponses
			target.FallbackOpenAI = true
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
	if target.FallbackOpenAI && target.Protocol == llm.ProtocolAnthropic {
		if !hasProxyBakedEffortSuffix(mid) {
			target.Variants = defaultModelVariants(pid, "@ai-sdk/anthropic", mid)
		}
	} else if target.FallbackOpenAI && target.Protocol == llm.ProtocolOpenAIResponses {
		if !hasProxyBakedEffortSuffix(mid) {
			target.Variants = defaultModelVariants(pid, "@ai-sdk/openai", mid)
		}
	} else {
		target.Variants = defaultModelVariants(pid, npm, mid)
	}
	if target.Protocol == llm.ProtocolGoogle && isNonThinkingGeminiChatModel(mid) {
		target.Variants = nil
	}
	providers := s.loadOpenCodeImportedProviderMap()
	for id, cfg := range s.loadHoxiProvidersMap() {
		providers[id] = cfg
	}
	var modelCfg map[string]any
	if cfg, ok := providers[pid].(map[string]any); ok {
		models, _ := cfg["models"].(map[string]any)
		model, _ := models[mid].(map[string]any)
		modelCfg = model
		if limits, ok := model["limit"].(map[string]any); ok {
			if n := positiveNumber(limits["context"]); n > 0 {
				target.ContextLimit = n
			}
			if n := positiveNumber(limits["output"]); n > 0 {
				target.OutputLimit = n
			}
		}
		if variants, ok := model["variants"].(map[string]any); ok {
			if !(target.FallbackOpenAI && target.Protocol == llm.ProtocolAnthropic) || variantsContainThinkingMap(variants) {
				target.Variants = variants
			}
		}
		if reasoning, ok := model["reasoning"].(bool); ok && !reasoning {
			target.Variants = nil
		}
	}
	if modelCfg == nil {
		modelCfg = builtinModelConfigMap(pid, mid)
		if limits, ok := modelCfg["limit"].(map[string]any); ok {
			if n := positiveNumber(limits["context"]); n > 0 {
				target.ContextLimit = n
			}
			if n := positiveNumber(limits["output"]); n > 0 {
				target.OutputLimit = n
			}
		}
	}
	target.Cost = resolveModelPricing(pid, mid, baseURL, modelCfg)
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
	baseURL, apiKey, npm, headers := s.resolveEffectiveProviderConnection(req.Target.ProviderID, req.Target.BaseURL, req.Target.NPM)
	if !isHTTPEndpoint(baseURL) {
		return nil, llm.ErrUnavailable
	}
	targetURL := resolveStreamEndpointURL(baseURL, req.Target)
	reqBody := req.Body
	if isCodexChatGPTBackendURL(targetURL) {
		reqBody = normalizeCodexBackendResponsesPayload(reqBody)
	}
	proto := effectiveProviderProtocol(req.Target.ProviderID, npm, baseURL)
	if req.Target.Protocol == llm.ProtocolAnthropic {
		proto = "anthropic"
	} else if req.Target.Protocol == llm.ProtocolOpenAI || req.Target.Protocol == llm.ProtocolOpenAIResponses {
		proto = "openai"
	}

	stallLimit := DefaultStreamStallTimeout
	if s.streamStallTimeout > 0 {
		stallLimit = s.streamStallTimeout
	}

	doOpen := func(reqURL string, includeBearerWithQueryKey, forceHTTP1 bool) (*http.Response, error) {
		reqCtx, cancelReq := context.WithCancel(ctx)
		stalled := &atomic.Bool{}
		timer := time.AfterFunc(stallLimit, func() {
			stalled.Store(true)
			cancelReq()
		})

		outReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
		if err != nil {
			timer.Stop()
			cancelReq()
			return nil, err
		}
		outReq.Header.Set("Accept", "text/event-stream")
		outReq.Header.Set("Content-Type", "application/json")
		if sid := strings.TrimSpace(req.SessionID); sid != "" {
			if isCodexChatGPTBackendURL(reqURL) {
				outReq.Header.Set("Session-Id", sid)
			} else {
				outReq.Header.Set("x-session-affinity", sid)
				outReq.Header.Set("X-Session-Id", sid)
			}
		}

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
				if !isOfficialAnthropicHost(reqURL) {
					outReq.Header.Set("Authorization", "Bearer "+apiKey)
				}
			case "google":
				outReq.Header.Set("x-goog-api-key", apiKey)
				if includeBearerWithQueryKey {
					outReq.Header.Set("Authorization", "Bearer "+apiKey)
				}
			default:
				outReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}

		var transport http.RoundTripper = providerTransport
		if isLoopbackURL(reqURL) {
			transport = localTransport
		}
		streamTransportMu.Lock()
		if streamHTTPTransport != nil {
			transport = streamHTTPTransport
		}
		streamTransportMu.Unlock()
		var onClose func()
		if forceHTTP1 {
			if baseHT, ok := transport.(*http.Transport); ok && baseHT != nil {
				h1 := newHTTP1FallbackTransport(baseHT)
				transport = h1
				onClose = h1.CloseIdleConnections
			}
		}
		client := &http.Client{Transport: transport}
		resp, doErr := client.Do(outReq)
		if doErr != nil {
			timer.Stop()
			cancelReq()
			if onClose != nil {
				onClose()
			}
			if stalled.Load() {
				return nil, fmt.Errorf("model stream stalled after %s without response headers: %w", stallLimit, context.DeadlineExceeded)
			}
			return nil, doErr
		}
		resp.Body = newStallReadCloser(resp.Body, stallLimit, cancelReq, stalled, timer, onClose)
		return resp, nil
	}

	started := time.Now()
	usedHTTP1 := req.ForceHTTP1
	resp, err := doOpen(targetURL, false, usedHTTP1)
	if err != nil && !usedHTTP1 && ctx.Err() == nil && llm.IsHTTP2ProtocolError(err) {
		usedHTTP1 = true
		resp, err = doOpen(targetURL, false, true)
	}
	if err != nil {
		if s.breaker != nil {
			s.breaker.RecordFailure(req.Target.ProviderID, req.Target.ModelID, classifyCandidateFailure(0, err, nil), 0, time.Since(started).Milliseconds(), err.Error())
		}
		return nil, err
	}
	if proto == "google" && apiKey != "" && !isOfficialGoogleHost(targetURL) &&
		(resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		if retryResp, retryErr := doOpen(appendGoogleQueryKey(targetURL, apiKey), shouldSendBearerOnGoogleRetry(apiKey), usedHTTP1); retryErr == nil && retryResp != nil {
			_ = resp.Body.Close()
			resp = retryResp
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		_ = resp.Body.Close()
		now := time.Now()
		latencyMs := now.Sub(started).Milliseconds()
		retryAfter := parseStreamRetryHeaders(resp.Header, resp.StatusCode, now)
		secrets := make([]string, 0, 1+len(headers)+len(req.ExtraHeaders))
		if apiKey != "" {
			secrets = append(secrets, apiKey)
		}
		for _, v := range headers {
			if trimmed := strings.TrimSpace(v); len(trimmed) >= 6 {
				secrets = append(secrets, trimmed)
			}
		}
		for _, v := range req.ExtraHeaders {
			if trimmed := strings.TrimSpace(v); len(trimmed) >= 6 {
				secrets = append(secrets, trimmed)
			}
		}
		upstream := llm.BuildRedactedUpstreamDetails(
			resp.StatusCode,
			req.Target.Protocol,
			targetURL,
			resp.Header,
			bodyBytes,
			latencyMs,
			secrets...,
		)
		errText := redactSecretInText(extractAPIErrorMessage(resp.StatusCode, bodyBytes), apiKey)
		if req.Target.FallbackOpenAI && isPromotedProtocolUnsupportedResponse(req.Target.Protocol, resp.StatusCode, errText) {
			memoKey := anthropicMessagesKey(req.Target.ProviderID, baseURL)
			if req.Target.Protocol == llm.ProtocolOpenAIResponses {
				s.noOpenAIResponses.Store(memoKey, true)
			} else {
				s.noAnthropicMessages.Store(memoKey, true)
			}
			return nil, &llm.StreamHTTPError{
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfter,
				Message:    errText,
				Upstream:   upstream,
			}
		}
		if s.breaker != nil {
			class := classifyCandidateFailure(resp.StatusCode, fmt.Errorf("%s", errText), nil)
			if isQuotaExhausted(bodyBytes) {
				class = breakerClassQuota
			}
			s.breaker.RecordFailureWithRetryAfter(req.Target.ProviderID, req.Target.ModelID, class, resp.StatusCode, latencyMs, retryAfter, errText)
		}
		diagMsg := "native stream HTTP error"
		if upstream != nil && upstream.RequestID != "" {
			diagMsg += " (req=" + upstream.RequestID + ")"
		}
		diagnostics.Record(diagnostics.Entry{
			Kind:       "request",
			Level:      "error",
			Source:     "provider",
			Message:    diagMsg,
			ProviderID: req.Target.ProviderID,
			ModelID:    req.Target.ModelID,
			Status:     resp.StatusCode,
			DurationMs: latencyMs,
		})
		return nil, &llm.StreamHTTPError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Message:    errText,
			Upstream:   upstream,
		}
	}
	return resp, nil
}

// RecordStreamOutcome records the terminal outcome of an opened 2xx SSE stream
// once the protocol driver has verified a clean terminal frame (or hit a stream
// error/EOF/stall), avoiding premature circuit-breaker resets on 200 headers.
func (s *Service) RecordStreamOutcome(providerID, modelID string, latencyMs int64, streamErr error) {
	if s == nil || s.breaker == nil {
		return
	}
	if streamErr == nil {
		s.breaker.RecordSuccess(providerID, modelID, latencyMs)
		return
	}
	if errors.Is(streamErr, context.Canceled) {
		return
	}
	class := classifyCandidateFailure(0, streamErr, nil)
	if isQuotaExhausted([]byte(streamErr.Error())) {
		class = breakerClassQuota
	}
	s.breaker.RecordFailure(providerID, modelID, class, 0, latencyMs, streamErr.Error())
}

func anthropicMessagesKey(providerID, baseURL string) string {
	return strings.ToLower(strings.TrimSpace(providerID)) + "|" + strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
}

func isOfficialAnthropicHost(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.anthropic.com" || strings.HasSuffix(host, ".anthropic.com")
}

func isOpenAIOnlyChatHost(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	p := strings.TrimRight(strings.ToLower(u.Path), "/")
	if strings.HasSuffix(p, "/chat/completions") || strings.HasSuffix(p, "/openai") || strings.Contains(p, "/openai/") {
		return true
	}
	for _, suffix := range []string{
		"openai.com",
		"openai.azure.com",
		"openrouter.ai",
		"siliconflow.cn",
		"siliconflow.com",
		"githubcopilot.com",
		"inference.ai.azure.com",
		"dashscope.aliyuncs.com",
		"groq.com",
		"together.xyz",
		"fireworks.ai",
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func hasProxyBakedEffortSuffix(modelID string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	for _, suf := range []string{"-low", "-medium", "-high", "-xhigh", "-max", ":thinking"} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

func variantsContainThinkingMap(variants map[string]any) bool {
	for _, raw := range variants {
		if m, ok := raw.(map[string]any); ok && m["thinking"] != nil {
			return true
		}
	}
	return false
}

func (s *Service) shouldPromoteClaudeToAnthropicMessages(providerID, modelID, baseURL string) bool {
	if !isHTTPEndpoint(baseURL) || isOpenAIOnlyChatHost(baseURL) {
		return false
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(modelID)), "claude") {
		return false
	}
	if s != nil {
		if _, blocked := s.noAnthropicMessages.Load(anthropicMessagesKey(providerID, baseURL)); blocked {
			return false
		}
	}
	return true
}

var openAIResponsesPromotableModelRe = regexp.MustCompile(`(?i)(?:^|[/:_-])(?:codex|gpt-(?:5|6|7)(?:$|[._:-])|o[34](?:$|[._:-]))`)

func isOpenAIResponsesPromotableModel(modelID string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" || strings.Contains(lower, "gpt-oss") || strings.Contains(lower, "oss-") {
		return false
	}
	return openAIResponsesPromotableModelRe.MatchString(lower)
}

func isResponsesPromotionExcludedHost(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	p := strings.TrimRight(strings.ToLower(u.Path), "/")
	if strings.HasSuffix(p, "/chat/completions") || strings.HasSuffix(p, "/openai") || strings.Contains(p, "/openai/") {
		return true
	}
	for _, suffix := range []string{
		"openai.azure.com",
		"openrouter.ai",
		"siliconflow.cn",
		"siliconflow.com",
		"githubcopilot.com",
		"inference.ai.azure.com",
		"dashscope.aliyuncs.com",
		"groq.com",
		"together.xyz",
		"fireworks.ai",
		"deepseek.com",
		"cerebras.ai",
		"mistral.ai",
		"x.ai",
		"googleapis.com",
		"anthropic.com",
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func isPromotedProtocolUnsupportedResponse(proto llm.StreamProtocol, statusCode int, errText string) bool {
	if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed || statusCode == http.StatusNotImplemented {
		return true
	}
	if proto != llm.ProtocolOpenAIResponses || (statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(errText))
	if !strings.Contains(lower, "responses") && !strings.Contains(lower, "endpoint") && !strings.Contains(lower, "path") && !strings.Contains(lower, "route") {
		return false
	}
	return strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "not implemented") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "invalid url") ||
		strings.Contains(lower, "unknown path")
}

func (s *Service) shouldPromoteOpenAIToResponses(providerID, modelID, baseURL string) bool {
	if !isHTTPEndpoint(baseURL) || isResponsesPromotionExcludedHost(baseURL) {
		return false
	}
	if !isOpenAIResponsesPromotableModel(modelID) {
		return false
	}
	if s != nil {
		if _, blocked := s.noOpenAIResponses.Load(anthropicMessagesKey(providerID, baseURL)); blocked {
			return false
		}
	}
	return true
}

func parseStreamRetryHeaders(h http.Header, statusCode int, now time.Time) time.Duration {
	if h == nil {
		return 0
	}
	var best time.Duration
	if rawMs := strings.TrimSpace(h.Get("Retry-After-Ms")); rawMs != "" {
		if ms, err := strconv.ParseFloat(rawMs, 64); err == nil && ms > 0 {
			best = time.Duration(ms * float64(time.Millisecond))
		}
	}
	if d := parseRetryAfter(h.Get("Retry-After"), now); d > best {
		best = d
	}
	// Explicit Retry-After / Retry-After-Ms headers are authoritative for this
	// rejected request; do not let long-window X-Ratelimit-Reset-* headers
	// inflate a short burst retry into a quota-exhaustion cooldown.
	if best > 0 {
		if best > llm.MaxResetWait {
			return llm.MaxResetWait
		}
		return best
	}
	if statusCode == http.StatusTooManyRequests || statusCode >= 500 {
		reqExhausted, hasRemReq := isRateLimitBucketExhausted(h.Get("X-Ratelimit-Remaining-Requests"))
		var dReq time.Duration
		if !hasRemReq || reqExhausted {
			dReq = parseRateLimitResetDuration(h.Get("X-Ratelimit-Reset-Requests"), now)
		}
		dTok := parseRateLimitResetDuration(h.Get("X-Ratelimit-Reset-Tokens"), now)
		switch {
		case dReq > 0 && dTok > 0:
			if hasRemReq && reqExhausted {
				best = max(dReq, dTok)
			} else if dTok < llm.QuotaResetPromoteAfter && dReq >= llm.QuotaResetPromoteAfter {
				best = dTok
			} else if dReq < llm.QuotaResetPromoteAfter && dTok >= llm.QuotaResetPromoteAfter {
				best = dReq
			} else {
				best = max(dReq, dTok)
			}
		case dTok > 0:
			best = dTok
		case dReq > 0:
			best = dReq
		}
	}
	if best > llm.MaxResetWait {
		best = llm.MaxResetWait
	}
	return best
}

func isRateLimitBucketExhausted(rawRemaining string) (exhausted bool, present bool) {
	v := strings.TrimSpace(rawRemaining)
	if v == "" {
		return false, false
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return false, false
	}
	return n <= 0, true
}

func parseRateLimitResetDuration(raw string, now time.Time) time.Duration {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return 0
		}
		if d > llm.MaxResetWait {
			return llm.MaxResetWait
		}
		return d
	}
	if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
		if d := ts.Sub(now); d > 0 {
			if d > llm.MaxResetWait {
				return llm.MaxResetWait
			}
			return d
		}
		return 0
	}
	return llm.ParseRetryDelay("retry after "+v, now, llm.MaxResetWait)
}
