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
				req.Breaker.RecordSuccess(req.ProviderID, req.ModelID, time.Since(started).Milliseconds())
			} else {
				class := classifyCandidateFailure(status, resultErr, nil)
				if quotaExhausted {
					class = breakerClassQuota
				}
				req.Breaker.RecordFailure(req.ProviderID, req.ModelID, class, status, time.Since(started).Milliseconds(), resultErr.Error())
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
		if parentCtx.Err() != nil || attempt >= llmMaxAttempts || res.quotaExhausted || !isTransientLLMError(res.err, res.status) {
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

func attemptSimpleLLM(parentCtx context.Context, req llmCompletionRequest, window time.Duration) llmAttemptResult {
	isAnthropic := req.NPM == "@ai-sdk/anthropic" || req.ProviderID == "anthropic"
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
	} else {
		targetURL = trimmedBase + "/chat/completions"
		bodyMap = map[string]any{
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
	}

	timeout := window
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	payloadBytes, _ := json.Marshal(bodyMap)
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return llmAttemptResult{err: err}
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", "application/json")
	for k, v := range req.Headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			outReq.Header.Set(k, v)
		}
	}
	if req.APIKey != "" {
		if isAnthropic {
			outReq.Header.Set("x-api-key", req.APIKey)
			outReq.Header.Set("anthropic-version", "2023-06-01")
		} else {
			outReq.Header.Set("Authorization", "Bearer "+req.APIKey)
		}
	}

	transport := req.Transport
	if transport == nil {
		transport = providerTransport
	}
	if isLoopbackURL(targetURL) {
		transport = localTransport
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	resp, err := client.Do(outReq)
	if err != nil {
		return llmAttemptResult{err: err}
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return llmAttemptResult{status: resp.StatusCode, readFailed: true, err: fmt.Errorf("read provider response: %w", err)}
	}
	res := llmAttemptResult{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.err = fmt.Errorf("%s", extractAPIErrorMessage(resp.StatusCode, respBytes))
		res.quotaExhausted = isQuotaExhausted(respBytes)
		return res
	}
	res.text = extractLLMTextContent(respBytes)
	return res
}

func extractLLMTextContent(raw []byte) string {
	var parsed struct {
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
	}
	if json.Unmarshal(raw, &parsed) == nil {
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
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return ""
	}
	return trimmed
}
