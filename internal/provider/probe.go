package provider

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

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

func (g *Service) handleTestProviderModel(w http.ResponseWriter, r *http.Request) {
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

	start := time.Now()
	_, err := callSimpleLLM(r.Context(), llmCompletionRequest{
		ProviderID: pid,
		ModelID:    modelID,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		NPM:        req.NPM,
		Headers:    req.Headers,
		Prompt:     "hi",
		MaxTokens:  1,
		Timeout:    15 * time.Second,
		Transport:  providerTransport,
		Telemetry:  g.telemetry,
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		errMsg := err.Error()
		if g.telemetry != nil && pid != "" {
			g.telemetry.recordProbeResult(pid, false, latency, errMsg, modelID)
		}
		writeJSON(w, http.StatusOK, testProviderModelResponse{
			OK:        false,
			LatencyMs: latency,
			Error:     errMsg,
		})
		return
	}

	if g.telemetry != nil && pid != "" {
		g.telemetry.recordProbeResult(pid, true, latency, "", modelID)
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
