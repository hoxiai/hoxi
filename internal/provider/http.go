package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	ProviderID  string
	ModelID     string
	BaseURL     string
	APIKey      string
	NPM         string
	Headers     map[string]string
	Prompt      string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
	Transport   *http.Transport
}

func callSimpleLLM(parentCtx context.Context, req llmCompletionRequest) (string, error) {
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
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	payloadBytes, _ := json.Marshal(bodyMap)
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", err
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
		return "", err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s", extractAPIErrorMessage(resp.StatusCode, respBytes))
	}

	return extractLLMTextContent(respBytes), nil
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
