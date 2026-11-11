package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

var (
	localTransport    = &http.Transport{Proxy: nil, IdleConnTimeout: 90 * time.Second}
	providerTransport = &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
		IdleConnTimeout: 90 * time.Second,
	}
	titleTransport = &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
		IdleConnTimeout: 90 * time.Second,
	}
)

func isLoopbackURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// localBridgePathPrefix is where every Hoxi gateway serves its CLI bridges.
const localBridgePathPrefix = "/api/hoxi/bridge/"

// localizeBridgeURL rewrites a Hoxi bridge route on any loopback port to this
// process's own gateway. models.json is shared by release, dev and headless
// processes and keeps only a neutral address; another process's port may
// belong to a gateway that has already exited.
func (s *Service) localizeBridgeURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !isLoopbackURL(trimmed) {
		return raw
	}
	u, err := url.Parse(trimmed)
	if err != nil || !strings.HasPrefix(u.Path, localBridgePathPrefix) {
		return raw
	}
	s.relayMu.Lock()
	base := s.relayBaseURL
	s.relayMu.Unlock()
	own, err := url.Parse(base)
	if base == "" || err != nil || own.Host == "" {
		return raw
	}
	u.Scheme, u.Host = own.Scheme, own.Host
	return u.String()
}

type llmCompletionRequest struct {
	ProviderID      string
	ModelID         string
	BaseURL         string
	APIKey          string
	NPM             string
	Headers         map[string]string
	Prompt          string
	MaxTokens       int
	Temperature     float64
	ReasoningEffort string
	Timeout         time.Duration
	Transport       *http.Transport
	Stats           *modelStatsStore
	Breaker         *breakerStore
}

type llmAttemptResult struct {
	text       string
	status     int
	readFailed bool
	retryAfter time.Duration
	err        error
	// quotaExhausted marks a 429/403 that is a billing limit, not a rate limit.
	quotaExhausted bool
}

func callSimpleLLM(parentCtx context.Context, req llmCompletionRequest) (text string, resultErr error) {
	started := time.Now()
	budget := req.Timeout
	if budget <= 0 {
		budget = 15 * time.Second
	}
	deadline := started.Add(budget)
	status := 0
	readFailed := false
	attempts := 0
	quotaExhausted := false
	defer func() {
		level, message := "info", "internal request completed"
		if resultErr != nil {
			level, message = "error", "internal request failed"
			var netErr net.Error
			switch {
			case errors.Is(resultErr, context.Canceled):
				level, message = "info", "internal request canceled"
			case errors.Is(resultErr, context.DeadlineExceeded), errors.As(resultErr, &netErr) && netErr.Timeout():
				message = "internal request timed out"
			case readFailed:
				message = "internal request response read failed"
			case status != 0 && (status < 200 || status >= 300):
				message = "internal request HTTP error"
			}
		}
		diagnostics.Record(diagnostics.Entry{Kind: "request", Level: level, Source: "provider", Message: message, ProviderID: req.ProviderID, ModelID: req.ModelID, Status: status, DurationMs: time.Since(started).Milliseconds()})
		if req.Stats != nil {
			req.Stats.recordLLMRequest(req.ProviderID, req.ModelID, attempts, resultErr)
		}
		if req.Breaker != nil && req.ProviderID != "" && req.ModelID != "" {
			if resultErr == nil {
				req.Breaker.RecordSuccess(req.ProviderID, req.ModelID, 0)
			} else {
				class := classifyCandidateFailure(status, resultErr, nil)
				if quotaExhausted {
					class = breakerClassQuota
				}
				req.Breaker.RecordFailure(req.ProviderID, req.ModelID, class, status, 0, resultErr.Error())
			}
		}
	}()
	for attempt := 1; ; attempt++ {
		attempts = attempt
		remaining := time.Until(deadline)
		if remaining <= 0 {
			resultErr = context.DeadlineExceeded
			break
		}
		res := attemptSimpleLLM(parentCtx, req, llmAttemptWindow(remaining, llmMaxAttempts-attempt+1))
		status, readFailed = res.status, res.readFailed
		quotaExhausted = quotaExhausted || res.quotaExhausted
		if res.err == nil {
			return res.text, nil
		}
		resultErr = res.err
		if parentCtx.Err() != nil || attempt >= llmMaxAttempts || res.quotaExhausted || res.retryAfter > llmRetryMaxWait || !isTransientLLMError(res.err, res.status) {
			break
		}
		wait := llmRetryWait(attempt, res.retryAfter)
		if time.Until(deadline) <= wait {
			break
		}
		diagnostics.Record(diagnostics.Entry{Kind: "request", Level: "warn", Source: "provider", Message: "internal request retrying", ProviderID: req.ProviderID, ModelID: req.ModelID, Status: res.status, DurationMs: time.Since(started).Milliseconds()})
		timer := time.NewTimer(wait)
		select {
		case <-parentCtx.Done():
			timer.Stop()
			return "", parentCtx.Err()
		case <-timer.C:
		}
	}
	return "", resultErr
}

// effectiveProviderProtocol resolves "anthropic", "google", or "openai" by
// inspecting unambiguous BaseURL shapes first (e.g. /v1beta/openai vs
// generativelanguage.googleapis.com / /v1beta), then npm, then providerID.
func effectiveProviderProtocol(providerID, npm, baseURL string) string {
	trimmedURL := strings.TrimSpace(baseURL)
	if trimmedURL != "" {
		if u, err := url.Parse(trimmedURL); err == nil {
			host := strings.ToLower(u.Hostname())
			p := strings.TrimRight(strings.ToLower(u.Path), "/")
			if strings.HasSuffix(p, "/openai") || strings.HasSuffix(p, "/openai/v1") || strings.Contains(p, "/openai/") {
				return "openai"
			}
			if host == "generativelanguage.googleapis.com" {
				return "google"
			}
			if strings.HasSuffix(p, "/v1beta") || strings.HasSuffix(p, "/v1alpha") ||
				strings.HasSuffix(p, "/v1beta/models") || strings.HasSuffix(p, "/v1alpha/models") {
				return "google"
			}
		}
	}
	lowerNPM := strings.ToLower(strings.TrimSpace(npm))
	if lowerNPM != "" {
		if strings.Contains(lowerNPM, "anthropic") {
			return "anthropic"
		}
		if strings.Contains(lowerNPM, "google") || strings.Contains(lowerNPM, "gemini") {
			return "google"
		}
		return "openai"
	}
	lowerPID := strings.ToLower(strings.TrimSpace(providerID))
	if lowerPID == "anthropic" {
		return "anthropic"
	}
	if lowerPID == "google" || lowerPID == "gemini" {
		return "google"
	}
	return "openai"
}

func isAnthropicProtocol(providerID, npm string) bool {
	return effectiveProviderProtocol(providerID, npm, "") == "anthropic"
}

func isGoogleProtocol(providerID, npm string) bool {
	return effectiveProviderProtocol(providerID, npm, "") == "google"
}

func googleModelsBaseURL(trimmedBase string) string {
	trimmedBase = strings.TrimRight(strings.TrimSpace(trimmedBase), "/")
	if strings.HasSuffix(trimmedBase, "/models") {
		return trimmedBase
	}
	if strings.HasSuffix(trimmedBase, "/v1beta") || strings.HasSuffix(trimmedBase, "/v1") || strings.HasSuffix(trimmedBase, "/v1alpha") {
		return trimmedBase + "/models"
	}
	return trimmedBase + "/v1beta/models"
}

func isOfficialGoogleHost(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "generativelanguage.googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
}

func isOpenAIResponsesProtocol(npm, baseURL string) bool {
	if strings.EqualFold(strings.TrimSpace(npm), "@ai-sdk/openai") {
		return true
	}
	trimmed := strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
	return strings.HasSuffix(trimmed, "/responses")
}

// shouldSendBearerOnGoogleRetry avoids attaching Authorization: Bearer AIza...
// when retrying custom Gemini reverse proxies that transparently forward
// headers to Google's upstream (which rejects Bearer AIza... as a bad OAuth2 token).
func shouldSendBearerOnGoogleRetry(apiKey string) bool {
	k := strings.TrimSpace(apiKey)
	return k != "" && !strings.HasPrefix(k, "AIza")
}

func appendGoogleQueryKey(rawURL, apiKey string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return rawURL
	}
	q := u.Query()
	if q.Get("key") == "" {
		q.Set("key", apiKey)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func redactSecretInText(text, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" || len(secret) < 4 {
		return text
	}
	out := strings.ReplaceAll(text, secret, "***")
	if enc := url.QueryEscape(secret); enc != "" && enc != secret {
		out = strings.ReplaceAll(out, enc, "***")
	}
	return out
}

func buildOpenAIChatCompletionBody(req llmCompletionRequest, maxTokens int) map[string]any {
	bodyMap := map[string]any{
		"model":      req.ModelID,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
	}
	if req.Temperature > 0 {
		bodyMap["temperature"] = req.Temperature
	}
	if eff := strings.TrimSpace(req.ReasoningEffort); eff != "" {
		bodyMap["reasoning_effort"] = eff
	}
	return bodyMap
}

func buildOpenAIResponsesBody(req llmCompletionRequest, maxTokens int) map[string]any {
	// OpenAI /v1/responses enforces max_output_tokens >= 16 on GPT-5 / o-series models.
	respMax := maxTokens
	if respMax < 32 {
		respMax = 32
	}
	bodyMap := map[string]any{
		"model":             req.ModelID,
		"input":             req.Prompt,
		"max_output_tokens": respMax,
		"store":             false,
	}
	if req.Temperature > 0 {
		bodyMap["temperature"] = req.Temperature
	}
	if eff := strings.TrimSpace(req.ReasoningEffort); eff != "" {
		bodyMap["reasoning"] = map[string]string{"effort": eff}
	}
	return bodyMap
}

func attemptSimpleLLM(parentCtx context.Context, req llmCompletionRequest, window time.Duration) llmAttemptResult {
	proto := effectiveProviderProtocol(req.ProviderID, req.NPM, req.BaseURL)
	isAnthropic := proto == "anthropic"
	isGoogle := proto == "google"
	isResponses := !isAnthropic && !isGoogle && isOpenAIResponsesProtocol(req.NPM, req.BaseURL)
	trimmedBase := strings.TrimRight(req.BaseURL, "/")
	var targetURL string
	var bodyMap map[string]any

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	if isAnthropic {
		if strings.HasSuffix(trimmedBase, "/v1") {
			targetURL = trimmedBase + "/messages"
		} else {
			targetURL = trimmedBase + "/v1/messages"
		}
		bodyMap = map[string]any{
			"model":      req.ModelID,
			"max_tokens": maxTokens,
			"messages": []map[string]string{
				{"role": "user", "content": req.Prompt},
			},
		}
	} else if isGoogle {
		cleanModel := strings.TrimPrefix(strings.TrimSpace(req.ModelID), "models/")
		targetURL = googleModelsBaseURL(trimmedBase) + "/" + url.PathEscape(cleanModel) + ":generateContent"
		// Gemini 2.5 Pro / Gemini 3 thinking models count internal thought tokens
		// toward maxOutputTokens and reject tiny values like maxOutputTokens=1 with HTTP 400.
		googleMaxTokens := maxTokens
		if googleMaxTokens < 256 {
			googleMaxTokens = 256
		}
		genCfg := map[string]any{
			"maxOutputTokens": googleMaxTokens,
		}
		if req.Temperature > 0 {
			genCfg["temperature"] = req.Temperature
		}
		bodyMap = map[string]any{
			"contents": []map[string]any{
				{
					"role": "user",
					"parts": []map[string]string{
						{"text": req.Prompt},
					},
				},
			},
			"generationConfig": genCfg,
		}
	} else if isResponses {
		if strings.HasSuffix(strings.ToLower(trimmedBase), "/responses") {
			targetURL = trimmedBase
		} else {
			targetURL = trimmedBase + "/responses"
		}
		bodyMap = buildOpenAIResponsesBody(req, maxTokens)
	} else {
		targetURL = trimmedBase + "/chat/completions"
		bodyMap = buildOpenAIChatCompletionBody(req, maxTokens)
	}

	timeout := window
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	doOnce := func(reqURL string, reqBody map[string]any, includeBearerWithQueryKey bool) (*http.Response, []byte, error) {
		payloadBytes, _ := json.Marshal(reqBody)
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
		if err != nil {
			return nil, nil, err
		}
		outReq.Header.Set("Accept", "application/json")
		outReq.Header.Set("Content-Type", "application/json")
		hasCustomAuth := false
		for k, v := range req.Headers {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "x-goog-api-key") || strings.EqualFold(k, "x-api-key") {
					hasCustomAuth = true
				}
				outReq.Header.Set(k, v)
			}
		}
		if req.APIKey != "" && !hasCustomAuth {
			if isAnthropic {
				outReq.Header.Set("x-api-key", req.APIKey)
				outReq.Header.Set("anthropic-version", "2023-06-01")
			} else if isGoogle {
				outReq.Header.Set("x-goog-api-key", req.APIKey)
				if includeBearerWithQueryKey {
					outReq.Header.Set("Authorization", "Bearer "+req.APIKey)
				}
			} else {
				outReq.Header.Set("Authorization", "Bearer "+req.APIKey)
			}
		}

		transport := req.Transport
		if transport == nil {
			transport = providerTransport
		}
		if isLoopbackURL(reqURL) {
			transport = localTransport
		}
		client := &http.Client{
			Timeout:   timeout,
			Transport: transport,
		}
		resp, err := client.Do(outReq)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		if readErr != nil {
			return resp, nil, readErr
		}
		return resp, respBytes, nil
	}

	resp, respBytes, err := doOnce(targetURL, bodyMap, false)
	if err != nil {
		if resp != nil {
			return llmAttemptResult{status: resp.StatusCode, readFailed: true, err: fmt.Errorf("read provider response: %w", err)}
		}
		return llmAttemptResult{err: err}
	}
	// Third-party Gemini relays (OneAPI/NewAPI/custom gateways) sometimes require
	// ?key= or Authorization: Bearer instead of x-goog-api-key.
	if isGoogle && req.APIKey != "" && !isOfficialGoogleHost(targetURL) &&
		(resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		if retryResp, retryBytes, retryErr := doOnce(appendGoogleQueryKey(targetURL, req.APIKey), bodyMap, shouldSendBearerOnGoogleRetry(req.APIKey)); retryErr == nil && retryResp != nil {
			resp, respBytes = retryResp, retryBytes
		}
	}
	// Dual-direction fallback between /v1/responses and /v1/chat/completions for
	// OpenAI gateways where only one of the two routes is exposed for the model.
	if !isAnthropic && !isGoogle && resp.StatusCode >= 400 && resp.StatusCode < 500 {
		lowerBody := strings.ToLower(string(respBytes))
		if isResponses && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) &&
			!strings.HasSuffix(strings.ToLower(trimmedBase), "/responses") {
			if retryResp, retryBytes, retryErr := doOnce(trimmedBase+"/chat/completions", buildOpenAIChatCompletionBody(req, maxTokens), false); retryErr == nil && retryResp != nil && retryResp.StatusCode < 400 {
				resp, respBytes = retryResp, retryBytes
			}
		} else if !isResponses && (resp.StatusCode == http.StatusNotFound || strings.Contains(lowerBody, "/responses")) {
			if retryResp, retryBytes, retryErr := doOnce(trimmedBase+"/responses", buildOpenAIResponsesBody(req, maxTokens), false); retryErr == nil && retryResp != nil && retryResp.StatusCode < 400 {
				resp, respBytes = retryResp, retryBytes
			}
		}
	}

	res := llmAttemptResult{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText := redactSecretInText(extractAPIErrorMessage(resp.StatusCode, respBytes), req.APIKey)
		res.err = fmt.Errorf("%s", errText)
		res.quotaExhausted = isQuotaExhausted(respBytes)
		return res
	}
	res.text = extractLLMTextContent(respBytes)
	return res
}

func extractLLMTextContent(raw []byte) string {
	var parsed struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					Thought    bool   `json:"thought"`
					InlineData *struct {
						Data string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		if t := strings.TrimSpace(parsed.OutputText); t != "" {
			return t
		}
		if len(parsed.Output) > 0 {
			for _, item := range parsed.Output {
				for _, block := range item.Content {
					if (block.Type == "" || block.Type == "output_text" || block.Type == "text") && strings.TrimSpace(block.Text) != "" {
						return strings.TrimSpace(block.Text)
					}
				}
				if (item.Type == "message" || item.Type == "output_text" || item.Type == "text") && strings.TrimSpace(item.Text) != "" {
					return strings.TrimSpace(item.Text)
				}
			}
			return ""
		}
		if len(parsed.Choices) > 0 {
			if c := strings.TrimSpace(parsed.Choices[0].Message.Content); c != "" {
				return c
			}
			if t := strings.TrimSpace(parsed.Choices[0].Text); t != "" {
				return t
			}
			return ""
		}
		if len(parsed.Content) > 0 {
			for _, block := range parsed.Content {
				if (block.Type == "" || block.Type == "text") && strings.TrimSpace(block.Text) != "" {
					return strings.TrimSpace(block.Text)
				}
			}
			return ""
		}
		if len(parsed.Candidates) > 0 {
			var fallback string
			hasInlineImage := false
			for _, part := range parsed.Candidates[0].Content.Parts {
				if part.InlineData != nil && strings.TrimSpace(part.InlineData.Data) != "" {
					hasInlineImage = true
				}
				t := strings.TrimSpace(part.Text)
				if t == "" {
					continue
				}
				if !part.Thought {
					return t
				}
				if fallback == "" {
					fallback = t
				}
			}
			if fallback != "" {
				return fallback
			}
			if hasInlineImage {
				return "[image]"
			}
			return ""
		}
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return ""
	}
	return trimmed
}
