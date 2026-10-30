package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (g *Service) provisionProviderWithMCPToken(ctx context.Context, sess *mcpOAuthSession, accessToken string) (string, string, []mcpOAuthModelItem) {
	siteOrigin := sess.siteOrigin
	var gwPayload struct {
		URL     string `json:"url"`
		BaseURL string `json:"baseUrl"`
	}
	var baseURL string
	if g.fetchMCPAuthedJSON(ctx, http.MethodGet, siteOrigin+"/api/proxy/gateway-url", accessToken, nil, &gwPayload) == nil {
		if rawGW := firstNonEmptyString(gwPayload.URL, gwPayload.BaseURL); rawGW != "" {
			baseURL = normalizeGatewayAPIBaseURL(rawGW)
		}
	}
	if baseURL == "" && sess.hintBaseURL != "" {
		baseURL = normalizeGatewayAPIBaseURL(sess.hintBaseURL)
	}
	if baseURL == "" {
		baseURL = deriveGatewayAPIBaseURLFromSite(siteOrigin)
	}

	apiKey := g.fetchOrMintSiteAPIKey(ctx, siteOrigin, accessToken)
	if apiKey == "" {
		apiKey = accessToken
	}
	models := g.fetchSiteModels(ctx, siteOrigin, baseURL, accessToken, apiKey)
	return baseURL, apiKey, models
}

// persistMCPOAuthProvisioning atomically persists the provider API key, model catalog,
// and remote MCP server configuration on the backend as soon as OAuth succeeds.
func (g *Service) persistMCPOAuthProvisioning(
	sess *mcpOAuthSession,
	mcpURL string,
	accessToken string,
	baseURL string,
	apiKey string,
	models []mcpOAuthModelItem,
) ([]mcpOAuthModelItem, error) {
	pid := strings.TrimSpace(sess.providerID)
	if pid == "" {
		pid = "hoxi-gateway"
	}
	resolvedBaseURL := strings.TrimSpace(baseURL)
	if resolvedBaseURL == "" {
		resolvedBaseURL = strings.TrimSpace(sess.hintBaseURL)
	}
	if resolvedBaseURL == "" {
		resolvedBaseURL = "https://api.hoxi.ai/v1"
	}
	resolvedKey := strings.TrimSpace(apiKey)
	if resolvedKey == "" {
		resolvedKey = strings.TrimSpace(accessToken)
	}

	if resolvedKey != "" {
		g.persistEngineProviderAuth(pid, resolvedKey)
	}

	if len(models) == 0 {
		fallbackIDs := sess.recommendedModels
		if len(fallbackIDs) == 0 {
			fallbackIDs = []string{"claude-sonnet-4-6", "gpt-5.4", "gemini-3.1-pro", "deepseek-v3.2"}
		}
		for _, rawID := range fallbackIDs {
			mid := strings.TrimSpace(rawID)
			if mid == "" {
				continue
			}
			models = append(models, mcpOAuthModelItem{ID: mid, Name: mid})
		}
	}

	g.configPersistMu.Lock()
	doc := g.loadHoxiConfigDoc()
	hoxiMap, _ := doc["provider"].(map[string]interface{})
	if hoxiMap == nil {
		hoxiMap = map[string]interface{}{}
	}
	existingProv, _ := hoxiMap[pid].(map[string]interface{})
	nextProv := map[string]interface{}{}
	for k, v := range existingProv {
		nextProv[k] = v
	}

	name := strings.TrimSpace(sess.providerName)
	if name == "" {
		if existingName, ok := existingProv["name"].(string); ok && strings.TrimSpace(existingName) != "" {
			name = strings.TrimSpace(existingName)
		} else {
			name = "Hoxi Gateway"
		}
	}
	nextProv["name"] = name

	npmPkg, _ := existingProv["npm"].(string)
	if strings.TrimSpace(npmPkg) == "" {
		npmPkg = "@ai-sdk/openai-compatible"
	}
	nextProv["npm"] = npmPkg

	nextOpts := map[string]interface{}{}
	if existingOpts, ok := existingProv["options"].(map[string]interface{}); ok {
		for k, v := range existingOpts {
			nextOpts[k] = v
		}
	}
	nextOpts["baseURL"] = resolvedBaseURL
	if resolvedKey != "" {
		nextOpts["apiKey"] = resolvedKey
	}
	nextProv["options"] = nextOpts

	modelsMap := map[string]interface{}{}
	if existingModels, ok := existingProv["models"].(map[string]interface{}); ok {
		for k, v := range existingModels {
			modelsMap[k] = v
		}
	}
	for _, m := range models {
		mid := strings.TrimSpace(m.ID)
		if mid == "" {
			continue
		}
		mName := strings.TrimSpace(m.Name)
		if mName == "" {
			mName = mid
		}
		isChat := !isNonChatUtilityModel(mid)
		inputs := []interface{}{"text"}
		if isChat {
			inputs = []interface{}{"text", "image"}
		}
		mEntry := map[string]interface{}{
			"name":       mName,
			"attachment": isChat,
			"reasoning":  isChat,
			"modalities": map[string]interface{}{
				"input":  inputs,
				"output": []interface{}{"text"},
			},
		}
		if m.InputPrice > 0 || m.OutputPrice > 0 {
			mEntry["cost"] = map[string]interface{}{
				"input":  m.InputPrice,
				"output": m.OutputPrice,
			}
		}
		modelsMap[mid] = mEntry
	}
	nextProv["models"] = modelsMap

	g.normalizeAndStoreMultiKeys(pid, nextProv)
	hoxiMap[pid] = nextProv
	doc["provider"] = hoxiMap

	if strings.TrimSpace(mcpURL) != "" && strings.TrimSpace(accessToken) != "" {
		mcpMap, _ := doc["mcp"].(map[string]interface{})
		if mcpMap == nil {
			mcpMap = map[string]interface{}{}
		}
		mcpMap[pid] = map[string]interface{}{
			"type":    "remote",
			"url":     strings.TrimSpace(mcpURL),
			"enabled": true,
			"headers": map[string]interface{}{
				"Authorization": "Bearer " + strings.TrimSpace(accessToken),
			},
		}
		doc["mcp"] = mcpMap
	}

	err := g.persistHoxiConfigFiles(doc)
	g.configPersistMu.Unlock()
	if err != nil {
		return models, err
	}
	g.reloadOpenCodeGlobalConfig()
	return models, nil
}

func (g *Service) persistEngineProviderAuth(providerID, apiKey string) {
	_ = g.persistEngineProviderAuthMap(providerID, map[string]interface{}{
		"type": "api",
		"key":  apiKey,
	})
}

func (g *Service) fetchOrMintSiteAPIKey(ctx context.Context, siteOrigin, accessToken string) string {
	listURL := siteOrigin + "/api/proxy/external?path=" + url.QueryEscape("/api/site/api-keys/list")
	var listResp map[string]any
	if g.fetchMCPAuthedJSON(ctx, http.MethodGet, listURL, accessToken, nil, &listResp) == nil {
		if key := extractRawAPIKeyFromPayload(listResp); key != "" {
			return key
		}
		createURL := siteOrigin + "/api/proxy/external?path=" + url.QueryEscape("/api/site/api-keys/create")
		var createResp map[string]any
		if g.fetchMCPAuthedJSON(ctx, http.MethodPost, createURL, accessToken, map[string]any{"name": "Hoxi Desktop", "groupId": 0}, &createResp) == nil {
			if key := extractRawAPIKeyFromPayload(createResp); key != "" {
				return key
			}
			var retryResp map[string]any
			if g.fetchMCPAuthedJSON(ctx, http.MethodGet, listURL, accessToken, nil, &retryResp) == nil {
				if key := extractRawAPIKeyFromPayload(retryResp); key != "" {
					return key
				}
			}
		}
	}
	return ""
}

func (g *Service) fetchSiteModels(ctx context.Context, siteOrigin, baseURL, accessToken, apiKey string) []mcpOAuthModelItem {
	for _, endpoint := range []string{
		siteOrigin + "/api/hoxi/public-models",
		siteOrigin + "/api/proxy/external?path=" + url.QueryEscape("/api/site/models/groups"),
	} {
		var payload map[string]any
		if g.fetchMCPAuthedJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &payload) == nil {
			if items := parseSiteModelsPayload(payload); len(items) > 0 {
				return items
			}
		}
	}
	if baseURL != "" && apiKey != "" {
		modelsURL := strings.TrimRight(baseURL, "/") + "/models"
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil); err == nil {
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", "Bearer "+apiKey)
			if resp, err := g.mcpOAuthHTTPClient(modelsURL, 8*time.Second).Do(req); err == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
					if fetched := parseOpenAIModelsPayload(raw); len(fetched) > 0 {
						out := make([]mcpOAuthModelItem, 0, len(fetched))
						for _, m := range fetched {
							out = append(out, mcpOAuthModelItem{ID: m.ID, Name: m.Name})
						}
						return out
					}
				}
			}
		}
	}
	return nil
}

func parseSiteModelsPayload(payload map[string]any) []mcpOAuthModelItem {
	if payload == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []mcpOAuthModelItem
	parseModelList := func(items []any) {
		for _, raw := range items {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id := firstNonEmptyString(m["code"], m["modelName"], m["id"], m["model"])
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			name := firstNonEmptyString(m["name"], m["displayName"], id)
			out = append(out, mcpOAuthModelItem{
				ID:          id,
				Name:        name,
				InputPrice:  toFloat64(m["inputPrice"], m["inputUsdPerM"]),
				OutputPrice: toFloat64(m["outputPrice"], m["outputUsdPerM"]),
			})
		}
	}

	var groups []any
	if dataMap, ok := payload["data"].(map[string]any); ok {
		if gList, ok := dataMap["groups"].([]any); ok {
			groups = gList
		} else if mList, ok := dataMap["models"].([]any); ok {
			parseModelList(mList)
		}
	} else if dataList, ok := payload["data"].([]any); ok {
		parseModelList(dataList)
	}
	if len(groups) == 0 {
		if gList, ok := payload["groups"].([]any); ok {
			groups = gList
		}
	}
	for _, gRaw := range groups {
		if gMap, ok := gRaw.(map[string]any); ok {
			if mList, ok := gMap["models"].([]any); ok {
				parseModelList(mList)
			}
		}
	}
	if mList, ok := payload["models"].([]any); ok {
		parseModelList(mList)
	}
	return out
}

func extractRawAPIKeyFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	cleanKey := func(v any) string {
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if s == "" || strings.Contains(s, "***") || s == "********" {
			return ""
		}
		return s
	}
	for _, field := range []string{"rawKey", "key", "apiKey", "token"} {
		if k := cleanKey(payload[field]); k != "" {
			return k
		}
	}
	if dataMap, ok := payload["data"].(map[string]any); ok {
		for _, field := range []string{"rawKey", "key", "apiKey", "token"} {
			if k := cleanKey(dataMap[field]); k != "" {
				return k
			}
		}
		for _, listField := range []string{"list", "items", "keys"} {
			if list, ok := dataMap[listField].([]any); ok {
				if k := extractRawAPIKeyFromSlice(list); k != "" {
					return k
				}
			}
		}
	}
	for _, listField := range []string{"data", "keys", "items", "list"} {
		if list, ok := payload[listField].([]any); ok {
			if k := extractRawAPIKeyFromSlice(list); k != "" {
				return k
			}
		}
	}
	return ""
}

func extractRawAPIKeyFromSlice(items []any) string {
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		status := strings.ToLower(firstNonEmptyString(item["status"]))
		if status != "" && status != "active" && status != "enabled" && status != "1" {
			continue
		}
		for _, field := range []string{"rawKey", "key", "apiKey", "token"} {
			if k, ok := item[field].(string); ok {
				k = strings.TrimSpace(k)
				if k != "" && !strings.Contains(k, "***") && k != "********" {
					return k
				}
			}
		}
	}
	return ""
}

func (g *Service) fetchMCPAuthedJSON(ctx context.Context, method, targetURL, accessToken string, reqBody any, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := g.mcpOAuthHTTPClient(targetURL, 8*time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func (g *Service) mcpOAuthHTTPClient(targetURL string, timeout time.Duration) *http.Client {
	transport := providerTransport
	if isLoopbackURL(targetURL) {
		transport = localTransport
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
