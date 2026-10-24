package handoff

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
	"sort"
	"strings"
	"time"
)

const grokUpstreamBaseURL = "https://cli-chat-proxy.grok.com"

func resolveGrokUpstreamBaseURL() string {
	for _, key := range []string{"HOXI_GROK_UPSTREAM_URL", "GROK_BASE_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(key)), "/"); strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			return v
		}
	}
	if gDir := grokHomeDir(); gDir != "" {
		if setBytes, err := os.ReadFile(filepath.Join(gDir, "settings.json")); err == nil {
			var setDoc map[string]interface{}
			if json.Unmarshal(setBytes, &setDoc) == nil {
				if u := strings.TrimRight(firstNonEmpty(asString(setDoc["baseURL"]), asString(setDoc["base_url"]), asString(setDoc["apiBase"])), "/"); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					return u
				}
			}
		}
	}
	return grokUpstreamBaseURL
}

var grokBridgeTransport http.RoundTripper = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	IdleConnTimeout: 90 * time.Second,
}

type grokLocalCredentials struct {
	APIKey       string
	Version      string
	DefaultModel string
	Models       []string
}

func appendUniqueString(list []string, items ...string) []string {
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		exists := false
		for _, cur := range list {
			if cur == it {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, it)
		}
	}
	return list
}

func loadGrokLocalCredentials() grokLocalCredentials {
	out := grokLocalCredentials{}
	gDir := grokHomeDir()
	if gDir == "" {
		return out
	}

	out.APIKey = readGrokAPIKey(filepath.Join(gDir, "auth.json"), time.Now())

	if verData, err := os.ReadFile(filepath.Join(gDir, "version.json")); err == nil {
		var verObj struct {
			LatestVersion string `json:"latest_version"`
		}
		if json.Unmarshal(verData, &verObj) == nil && strings.TrimSpace(verObj.LatestVersion) != "" {
			out.Version = strings.TrimSpace(verObj.LatestVersion)
		}
	}

	if setBytes, err := os.ReadFile(filepath.Join(gDir, "settings.json")); err == nil {
		var setDoc map[string]interface{}
		if json.Unmarshal(setBytes, &setDoc) == nil {
			out.DefaultModel = firstNonEmpty(asString(setDoc["model"]), asString(setDoc["default_model"]), asString(setDoc["current_model_id"]))
		}
	}
	if tomlBytes, err := os.ReadFile(filepath.Join(gDir, "config.toml")); err == nil {
		for _, line := range strings.Split(string(tomlBytes), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				k := strings.TrimSpace(parts[0])
				v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				if (k == "model" || k == "default_model" || k == "fork_secondary_model") && v != "" {
					out.Models = appendUniqueString(out.Models, v)
					if k != "fork_secondary_model" && out.DefaultModel == "" {
						out.DefaultModel = v
					}
				}
			}
		}
	}

	if modData, err := os.ReadFile(filepath.Join(gDir, "models_cache.json")); err == nil {
		var rawDoc map[string]interface{}
		if json.Unmarshal(modData, &rawDoc) == nil {
			if out.Version == "" {
				out.Version = strings.TrimSpace(asString(rawDoc["grok_version"]))
			}
			if dm := strings.TrimSpace(asString(rawDoc["default_model"])); dm != "" && out.DefaultModel == "" {
				out.DefaultModel = dm
			}
			switch mv := rawDoc["models"].(type) {
			case []interface{}:
				for _, item := range mv {
					if s := strings.TrimSpace(asString(item)); s != "" {
						out.Models = appendUniqueString(out.Models, s)
					}
				}
			case map[string]interface{}:
				var mapModels []string
				for k, v := range mv {
					if vm, ok := v.(map[string]interface{}); ok {
						if info, ok := vm["info"].(map[string]interface{}); ok {
							if hidden, _ := info["hidden"].(bool); hidden {
								continue
							}
							if id := strings.TrimSpace(asString(info["id"])); id != "" {
								mapModels = appendUniqueString(mapModels, id)
								continue
							}
						}
					}
					if strings.TrimSpace(k) != "" {
						mapModels = appendUniqueString(mapModels, strings.TrimSpace(k))
					}
				}
				sort.Slice(mapModels, func(i, j int) bool { return mapModels[i] > mapModels[j] })
				out.Models = appendUniqueString(out.Models, mapModels...)
			}
		}
	}
	if out.DefaultModel == "" && len(out.Models) > 0 {
		out.DefaultModel = out.Models[0]
	}
	if out.DefaultModel != "" {
		out.Models = appendUniqueString([]string{out.DefaultModel}, out.Models...)
	}
	return out
}

// grokAuthEntry is one credential in ~/.grok/auth.json. The Grok CLI refreshes
// the file itself; Hoxi only reads it and must never write a token back.
type grokAuthEntry struct {
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at"`
}

// readGrokAPIKey returns the newest credential that has not expired. A key
// without expires_at never expires. When every key is expired the newest one
// is returned anyway: the CLI may not have refreshed yet, and a stale key
// fails the request instead of silently dropping the provider.
func readGrokAPIKey(path string, now time.Time) string {
	authData, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var authMap map[string]grokAuthEntry
	if json.Unmarshal(authData, &authMap) != nil {
		return ""
	}
	var bestKey string
	var bestExpiry time.Time
	var fallbackKey string
	var fallbackExpiry time.Time
	keys := make([]string, 0, len(authMap))
	for k := range authMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry := authMap[k]
		key := strings.TrimSpace(entry.Key)
		if key == "" {
			continue
		}
		expiry, hasExpiry := parseGrokExpiry(entry.ExpiresAt)
		if !hasExpiry {
			return key
		}
		if fallbackKey == "" || expiry.After(fallbackExpiry) {
			fallbackKey = key
			fallbackExpiry = expiry
		}
		if !expiry.After(now) {
			continue
		}
		if bestKey == "" || expiry.After(bestExpiry) {
			bestKey = key
			bestExpiry = expiry
		}
	}
	if bestKey != "" {
		return bestKey
	}
	return fallbackKey
}

func parseGrokExpiry(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func detectGrokCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	creds := loadGrokLocalCredentials()
	sessions, stats := scanGrokSessions("", "")
	for _, s := range sessions {
		if s.Model != "" {
			creds.Models = appendUniqueString(creds.Models, s.Model)
			if creds.DefaultModel == "" {
				creds.DefaultModel = s.Model
			}
		}
	}
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

func (g *Service) importGrokCLIProvider() LocalCLIImportResponse {
	creds := loadGrokLocalCredentials()
	if creds.APIKey == "" {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolGrok),
			Error:  "Grok CLI credentials not found in ~/.grok/auth.json",
		}
	}
	sessions, _ := scanGrokSessions("", "")
	for _, s := range sessions {
		if s.Model != "" {
			creds.Models = appendUniqueString(creds.Models, s.Model)
		}
	}

	modelsMap := map[string]interface{}{}
	for _, mID := range creds.Models {
		modelsMap[mID] = map[string]interface{}{
			"name":       mID + " (Grok CLI)",
			"tool_call":  true,
			"reasoning":  true,
			"attachment": true,
			"modalities": map[string]interface{}{
				"input":  []string{"text", "image"},
				"output": []string{"text"},
			},
			"limit": map[string]interface{}{
				"context": 256000,
				"output":  32768,
			},
		}
	}

	if err := g.updateHoxiConfigDoc(func(doc map[string]interface{}) error {
		provMap, _ := doc["provider"].(map[string]interface{})
		if provMap == nil {
			provMap = map[string]interface{}{}
		}
		provMap["grok-cli"] = map[string]interface{}{
			"name": "Grok CLI (Local)",
			"npm":  "@ai-sdk/openai-compatible",
			"options": map[string]interface{}{
				"baseURL": grokCLIStoredBridgeURL,
				"apiKey":  "hoxi-local-grok-cli-bridge",
			},
			"models": modelsMap,
		}
		doc["provider"] = provMap
		return nil
	}); err != nil {
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

func (g *Service) handleGrokCLIBridge(w http.ResponseWriter, r *http.Request) {
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
		if json.Unmarshal(rawBody, &payload) == nil && payload != nil {
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

	upstreamURL := resolveGrokUpstreamBaseURL() + subPath
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
		Transport: grokBridgeTransport,
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
