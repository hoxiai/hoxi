package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type fetchProviderModelsRequest struct {
	BaseURL    string            `json:"baseURL"`
	APIKey     string            `json:"apiKey,omitempty"`
	ProviderID string            `json:"providerID,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type fetchedModelItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fetchProviderModelsResponse struct {
	Models []fetchedModelItem `json:"models,omitempty"`
	Error  string             `json:"error,omitempty"`
	Auth   bool               `json:"auth,omitempty"`
}

func (g *desktopGateway) handleFetchProviderModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req fetchProviderModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: "Invalid request payload"})
		return
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: "Base URL must start with http:// or https://"})
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "********" {
		apiKey = ""
	}
	// Resolve {env:VAR_NAME} syntax if used
	if strings.HasPrefix(apiKey, "{env:") && strings.HasSuffix(apiKey, "}") {
		envName := strings.TrimSpace(apiKey[5 : len(apiKey)-1])
		apiKey = os.Getenv(envName)
	}
	// If editing an existing provider with untouched key, look up stored key
	if apiKey == "" && strings.TrimSpace(req.ProviderID) != "" {
		apiKey = g.resolveStoredProviderKey(strings.TrimSpace(req.ProviderID), baseURL)
	}

	modelsURL := strings.TrimRight(baseURL, "/") + "/models"
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: err.Error()})
		return
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
	if apiKey != "" {
		outReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 15 * time.Second,
			}).DialContext,
		},
	}

	// For loopback addresses, bypass proxy to avoid 502 from local proxy tools
	if strings.Contains(modelsURL, "://127.0.0.1") || strings.Contains(modelsURL, "://localhost") {
		client.Transport = &http.Transport{Proxy: nil}
	}

	resp, err := client.Do(outReq)
	if err != nil {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(bodyBytes))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		isAuth := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		msg := "HTTP " + strconv.Itoa(resp.StatusCode)
		if snippet != "" {
			msg += ": " + snippet
		}
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{
			Error: msg,
			Auth:  isAuth,
		})
		return
	}

	models := parseOpenAIModelsPayload(bodyBytes)
	writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Models: models})
}

func parseOpenAIModelsPayload(raw []byte) []fetchedModelItem {
	type rawModel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var wrapper struct {
		Data   []rawModel `json:"data"`
		Models []rawModel `json:"models"`
	}

	var list []rawModel
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if len(wrapper.Data) > 0 {
			list = wrapper.Data
		} else if len(wrapper.Models) > 0 {
			list = wrapper.Models
		}
	}
	if len(list) == 0 {
		_ = json.Unmarshal(raw, &list)
	}

	seen := make(map[string]struct{}, len(list))
	result := make([]fetchedModelItem, 0, len(list))
	for _, item := range list {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = id
		}
		result = append(result, fetchedModelItem{ID: id, Name: name})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func (g *desktopGateway) resolveStoredProviderKey(providerID, requestedBaseURL string) string {
	// 1. Check OpenCode /provider endpoint first
	if g.openCodePort > 0 {
		u := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/provider"
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err == nil {
			req.SetBasicAuth("opencode", g.password)
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
			if resp, err := client.Do(req); err == nil {
				defer resp.Body.Close()
				var payload struct {
					All []struct {
						ID      string         `json:"id"`
						Key     string         `json:"key"`
						Options map[string]any `json:"options"`
					} `json:"all"`
				}
				if json.NewDecoder(resp.Body).Decode(&payload) == nil {
					for _, p := range payload.All {
						if p.ID == providerID {
							if p.Key != "" {
								return p.Key
							}
							if optKey, ok := p.Options["apiKey"].(string); ok && optKey != "" {
								return optKey
							}
						}
					}
				}
			}
		}
	}

	// 2. Fallback to ~/.local/share/opencode/auth.json
	if home, err := os.UserHomeDir(); err == nil {
		authPath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
		if data, err := os.ReadFile(authPath); err == nil {
			var authMap map[string]struct {
				Key string `json:"key"`
			}
			if json.Unmarshal(data, &authMap) == nil {
				if entry, ok := authMap[providerID]; ok && entry.Key != "" {
					return entry.Key
				}
			}
		}
	}
	return ""
}

type testProviderModelRequest struct {
	ProviderID string            `json:"providerID,omitempty"`
	BaseURL    string            `json:"baseURL,omitempty"`
	APIKey     string            `json:"apiKey,omitempty"`
	NPM        string            `json:"npm,omitempty"`
	ModelID    string            `json:"modelID"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type testProviderModelResponse struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

var defaultProviderBaseURLs = map[string]string{
	"deepseek":   "https://api.deepseek.com/v1",
	"openai":     "https://api.openai.com/v1",
	"anthropic":  "https://api.anthropic.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"xai":        "https://api.x.ai/v1",
	"groq":       "https://api.groq.com/openai/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"moonshotai": "https://api.moonshot.cn/v1",
	"zai":        "https://open.bigmodel.cn/api/paas/v4",
	"alibaba":    "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"ollama":     "http://127.0.0.1:11434/v1",
}

func (g *desktopGateway) handleTestProviderModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req testProviderModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, testProviderModelResponse{OK: false, Error: "Invalid request payload"})
		return
	}

	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		writeJSON(w, http.StatusOK, testProviderModelResponse{OK: false, Error: "Model ID is required"})
		return
	}

	pid := strings.TrimSpace(req.ProviderID)
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" && pid != "" {
		baseURL = defaultProviderBaseURLs[pid]
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		writeJSON(w, http.StatusOK, testProviderModelResponse{OK: false, Error: "Base URL must start with http:// or https://"})
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "********" {
		apiKey = ""
	}
	if strings.HasPrefix(apiKey, "{env:") && strings.HasSuffix(apiKey, "}") {
		envName := strings.TrimSpace(apiKey[5 : len(apiKey)-1])
		apiKey = os.Getenv(envName)
	}
	if apiKey == "" && pid != "" {
		apiKey = g.resolveStoredProviderKey(pid, baseURL)
	}

	isAnthropic := req.NPM == "@ai-sdk/anthropic" || pid == "anthropic"
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
			"max_tokens": 1,
			"messages": []map[string]string{
				{"role": "user", "content": "hi"},
			},
		}
	} else {
		targetURL = trimmedBase + "/chat/completions"
		bodyMap = map[string]any{
			"model":      modelID,
			"max_tokens": 1,
			"messages": []map[string]string{
				{"role": "user", "content": "hi"},
			},
		}
	}

	payloadBytes, _ := json.Marshal(bodyMap)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		writeJSON(w, http.StatusOK, testProviderModelResponse{OK: false, Error: err.Error()})
		return
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
	if apiKey != "" {
		if isAnthropic {
			outReq.Header.Set("x-api-key", apiKey)
			outReq.Header.Set("anthropic-version", "2023-06-01")
		} else {
			outReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 15 * time.Second,
			}).DialContext,
		},
	}
	if strings.Contains(targetURL, "://127.0.0.1") || strings.Contains(targetURL, "://localhost") {
		client.Transport = &http.Transport{Proxy: nil}
	}

	start := time.Now()
	resp, err := client.Do(outReq)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, testProviderModelResponse{OK: false, LatencyMs: latency, Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := extractAPIErrorMessage(resp.StatusCode, respBytes)
		writeJSON(w, http.StatusOK, testProviderModelResponse{
			OK:        false,
			LatencyMs: latency,
			Error:     errMsg,
		})
		return
	}

	writeJSON(w, http.StatusOK, testProviderModelResponse{
		OK:        true,
		LatencyMs: latency,
	})
}

func extractAPIErrorMessage(statusCode int, raw []byte) string {
	var parsed struct {
		Error any    `json:"error"`
		Msg   string `json:"message"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		switch v := parsed.Error.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return "HTTP " + strconv.Itoa(statusCode) + ": " + strings.TrimSpace(v)
			}
		case map[string]any:
			if m, ok := v["message"].(string); ok && strings.TrimSpace(m) != "" {
				return "HTTP " + strconv.Itoa(statusCode) + ": " + strings.TrimSpace(m)
			}
		}
		if strings.TrimSpace(parsed.Msg) != "" {
			return "HTTP " + strconv.Itoa(statusCode) + ": " + strings.TrimSpace(parsed.Msg)
		}
	}
	snippet := strings.TrimSpace(string(raw))
	if len(snippet) > 180 {
		snippet = snippet[:180]
	}
	if snippet != "" {
		return "HTTP " + strconv.Itoa(statusCode) + ": " + snippet
	}
	return "HTTP " + strconv.Itoa(statusCode)
}
