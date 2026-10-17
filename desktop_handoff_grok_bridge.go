package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const grokUpstreamBaseURL = "https://cli-chat-proxy.grok.com"

type grokLocalCredentials struct {
	APIKey       string
	Version      string
	DefaultModel string
	Models       []string
}

func loadGrokLocalCredentials() grokLocalCredentials {
	out := grokLocalCredentials{
		Version:      "1.0.52",
		DefaultModel: "grok-4.7",
		Models:       []string{"grok-4.7", "grok-4.7-build-fast", "grok-4.6", "grok-4.5"},
	}
	gDir := grokHomeDir()
	if gDir == "" {
		return out
	}

	if authData, err := os.ReadFile(filepath.Join(gDir, "auth.json")); err == nil {
		var authMap map[string]struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(authData, &authMap) == nil {
			for _, entry := range authMap {
				if strings.TrimSpace(entry.Key) != "" {
					out.APIKey = strings.TrimSpace(entry.Key)
					break
				}
			}
		}
	}

	if verData, err := os.ReadFile(filepath.Join(gDir, "version.json")); err == nil {
		var verObj struct {
			LatestVersion string `json:"latest_version"`
		}
		if json.Unmarshal(verData, &verObj) == nil && strings.TrimSpace(verObj.LatestVersion) != "" {
			out.Version = strings.TrimSpace(verObj.LatestVersion)
		}
	}

	if modData, err := os.ReadFile(filepath.Join(gDir, "models_cache.json")); err == nil {
		var modObj struct {
			Models       []string `json:"models"`
			DefaultModel string   `json:"default_model"`
		}
		if json.Unmarshal(modData, &modObj) == nil {
			if len(modObj.Models) > 0 {
				out.Models = modObj.Models
			}
			if strings.TrimSpace(modObj.DefaultModel) != "" {
				out.DefaultModel = strings.TrimSpace(modObj.DefaultModel)
			}
		}
	}
	return out
}

func detectGrokCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	creds := loadGrokLocalCredentials()
	_, stats := scanGrokSessions("", "")
	_, connected := hoxiMap["grok-cli"]
	return LocalCLIStatusItem{
		ID:            ExternalToolGrok,
		Name:          "Grok CLI",
		Installed:     stats.Installed || creds.APIKey != "",
		Authenticated: creds.APIKey != "",
		Connected:     connected,
		Version:       creds.Version,
		DefaultModel:  creds.DefaultModel,
		Models:        creds.Models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "xai-grok-cli",
		ProviderID:    "grok-cli",
	}
}

func (g *desktopGateway) importGrokCLIProvider() LocalCLIImportResponse {
	creds := loadGrokLocalCredentials()
	if creds.APIKey == "" {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolGrok),
			Error:  "Grok CLI credentials not found in ~/.grok/auth.json",
		}
	}

	modelsMap := map[string]interface{}{}
	for _, mID := range creds.Models {
		modelsMap[mID] = map[string]interface{}{
			"name":       mID + " (Grok CLI)",
			"tool_call":  true,
			"reasoning":  true,
			"attachment": true,
			"limit": map[string]interface{}{
				"context": 256000,
				"output":  32768,
			},
		}
	}

	bridgeURL := g.httpBridgeBaseURL() + "/api/hoxi/bridge/grok-cli/v1"
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		provMap = map[string]interface{}{}
	}
	provMap["grok-cli"] = map[string]interface{}{
		"name": "Grok CLI (Local)",
		"npm":  "@ai-sdk/openai-compatible",
		"options": map[string]interface{}{
			"baseURL": bridgeURL,
			"apiKey":  "hoxi-local-grok-cli-bridge",
		},
		"models": modelsMap,
	}
	doc["provider"] = provMap
	if err := g.persistHoxiConfigDoc(doc); err != nil {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolGrok),
			Error:  err.Error(),
		}
	}

	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolGrok),
		ProviderID: "grok-cli",
		Models:     creds.Models,
	}
}

func (g *desktopGateway) syncLocalCLIBridgeURLs() {
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		return
	}
	grokProv, ok := provMap["grok-cli"].(map[string]interface{})
	if !ok {
		return
	}
	opts, _ := grokProv["options"].(map[string]interface{})
	if opts == nil {
		return
	}
	expected := g.httpBridgeBaseURL() + "/api/hoxi/bridge/grok-cli/v1"
	if cur, _ := opts["baseURL"].(string); strings.Contains(cur, "/api/hoxi/bridge/grok-cli/v1") && cur != expected {
		opts["baseURL"] = expected
		grokProv["options"] = opts
		provMap["grok-cli"] = grokProv
		doc["provider"] = provMap
		_ = g.persistHoxiConfigDoc(doc)
	}
}

func (g *desktopGateway) handleGrokCLIBridge(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/api/hoxi/bridge/grok-cli")
	if subPath == "/v1/models" && r.Method == http.MethodGet {
		creds := loadGrokLocalCredentials()
		items := make([]map[string]interface{}, 0, len(creds.Models))
		for _, m := range creds.Models {
			items = append(items, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"owned_by": "xai-grok-cli",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   items,
		})
		return
	}

	creds := loadGrokLocalCredentials()
	if creds.APIKey == "" {
		writeJSONError(w, http.StatusUnauthorized, "Missing Grok CLI credentials in ~/.grok/auth.json")
		return
	}

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	modelName := creds.DefaultModel
	callerWantsStream := true

	if len(rawBody) > 0 {
		var payload map[string]interface{}
		if json.Unmarshal(rawBody, &payload) == nil {
			if m, ok := payload["model"].(string); ok && strings.TrimSpace(m) != "" {
				modelName = strings.TrimSpace(m)
			}
			if st, ok := payload["stream"].(bool); ok {
				callerWantsStream = st
			} else {
				callerWantsStream = false
			}
			// Grok CLI proxy requires stream: true
			payload["stream"] = true
			if rewritten, err := json.Marshal(payload); err == nil {
				rawBody = rewritten
			}
		}
	}

	upstreamURL := grokUpstreamBaseURL + subPath
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, bytes.NewReader(rawBody))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	outReq.Header.Set("Content-Type", "application/json")
	outReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	outReq.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	outReq.Header.Set("x-grok-client-version", creds.Version)
	outReq.Header.Set("x-grok-model-override", modelName)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	resp, err := client.Do(outReq)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || callerWantsStream {
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, rErr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rErr != nil {
				break
			}
		}
		return
	}

	// Caller requested non-streaming JSON (e.g. test-model, audit, summarize-title): aggregate SSE chunks
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(dataStr), &chunk) == nil && len(chunk.Choices) > 0 {
			contentBuf.WriteString(chunk.Choices[0].Delta.Content)
			reasoningBuf.WriteString(chunk.Choices[0].Delta.ReasoningContent)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      "chatcmpl-grok-bridge",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelName,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           contentBuf.String(),
					"reasoning_content": reasoningBuf.String(),
				},
				"finish_reason": "stop",
			},
		},
	})
}
