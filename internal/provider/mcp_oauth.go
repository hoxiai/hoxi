package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	mcpOAuthClientID   = "hoxi-desktop"
	mcpOAuthClientName = "Hoxi"
	mcpOAuthScopes     = "mcp:tools mcp:profile:read mcp:theme:read"
	mcpOAuthSessionTTL = 5 * time.Minute
)

type mcpOAuthStartRequest struct {
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName,omitempty"`
	SiteURL      string `json:"siteUrl,omitempty"`
	BaseURL      string `json:"baseUrl,omitempty"`
}

type mcpOAuthStartResponse struct {
	State      string `json:"state"`
	AuthURL    string `json:"authUrl"`
	SiteOrigin string `json:"siteOrigin"`
}

type mcpOAuthModelItem struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	InputPrice  float64 `json:"inputPrice,omitempty"`
	OutputPrice float64 `json:"outputPrice,omitempty"`
}

type mcpOAuthPollResponse struct {
	Status       string              `json:"status"` // "pending" | "authorized" | "expired" | "error"
	ProviderID   string              `json:"providerId,omitempty"`
	ProviderName string              `json:"providerName,omitempty"`
	SiteOrigin   string              `json:"siteOrigin,omitempty"`
	MCPURL       string              `json:"mcpUrl,omitempty"`
	AccessToken  string              `json:"accessToken,omitempty"`
	BaseURL      string              `json:"baseUrl,omitempty"`
	APIKey       string              `json:"apiKey,omitempty"`
	AccountLabel string              `json:"accountLabel,omitempty"`
	Models       []mcpOAuthModelItem `json:"models,omitempty"`
	Error        string              `json:"error,omitempty"`
}

type mcpOAuthSession struct {
	mu           sync.Mutex
	state        string
	codeVerifier string
	redirectURI  string
	siteOrigin   string
	providerID   string
	providerName string
	hintBaseURL  string
	authURL      string
	createdAt    time.Time
	polling      bool
	result       mcpOAuthPollResponse
}

type mcpOAuthTokenPayload struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	MCPURL           string `json:"mcp_url"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Message          string `json:"message"`
	StatusMessage    string `json:"statusMessage"`
	User             struct {
		ID       any    `json:"id"`
		Email    string `json:"email"`
		Nickname string `json:"nickname"`
	} `json:"user"`
}

func (g *Service) handleProviderMCPOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req mcpOAuthStartRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		providerID = "ainode"
	}
	providerName := strings.TrimSpace(req.ProviderName)
	if providerName == "" {
		providerName = providerID
	}

	siteOrigin := g.resolveMCPOAuthSiteOrigin(r.Context(), req.SiteURL, req.BaseURL)
	if siteOrigin == "" {
		writeJSONError(w, http.StatusBadRequest, "Unable to resolve provider OAuth portal URL")
		return
	}

	codeVerifier, codeChallenge, state, err := generatePKCEParameters()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to generate PKCE parameters")
		return
	}

	redirectURI := siteOrigin + "/user/mcp-authorize"
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", mcpOAuthClientID)
	query.Set("client_name", mcpOAuthClientName)
	query.Set("redirect_uri", redirectURI)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	query.Set("scope", mcpOAuthScopes)

	authURL := siteOrigin + "/api/oauth/authorize?" + query.Encode()

	sess := &mcpOAuthSession{
		state:        state,
		codeVerifier: codeVerifier,
		redirectURI:  redirectURI,
		siteOrigin:   siteOrigin,
		providerID:   providerID,
		providerName: providerName,
		hintBaseURL:  strings.TrimSpace(req.BaseURL),
		authURL:      authURL,
		createdAt:    time.Now(),
		result: mcpOAuthPollResponse{
			Status:       "pending",
			ProviderID:   providerID,
			ProviderName: providerName,
			SiteOrigin:   siteOrigin,
		},
	}

	g.mcpOAuthMu.Lock()
	if g.mcpOAuthSessions == nil {
		g.mcpOAuthSessions = make(map[string]*mcpOAuthSession)
	}
	now := time.Now()
	for k, existing := range g.mcpOAuthSessions {
		if now.Sub(existing.createdAt) > mcpOAuthSessionTTL {
			delete(g.mcpOAuthSessions, k)
		}
	}
	g.mcpOAuthSessions[state] = sess
	browserFn := g.openBrowserFunc
	g.mcpOAuthMu.Unlock()

	if browserFn == nil {
		browserFn = openProviderBrowser
	}
	go browserFn(authURL)
	go g.runMCPOAuthPollLoop(sess)

	writeJSON(w, http.StatusOK, mcpOAuthStartResponse{State: state, AuthURL: authURL, SiteOrigin: siteOrigin})
}

func (g *Service) handleProviderMCPOAuthPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		writeJSONError(w, http.StatusBadRequest, "state parameter is required")
		return
	}
	g.mcpOAuthMu.Lock()
	sess := g.mcpOAuthSessions[state]
	g.mcpOAuthMu.Unlock()
	if sess == nil {
		writeJSON(w, http.StatusOK, mcpOAuthPollResponse{Status: "expired", Error: "Authorization session expired or not found"})
		return
	}

	sess.mu.Lock()
	if time.Since(sess.createdAt) > mcpOAuthSessionTTL && sess.result.Status == "pending" {
		sess.result.Status = "expired"
		sess.result.Error = "Authorization timed out"
	}
	needStep := sess.result.Status == "pending" && !sess.polling
	if needStep {
		sess.polling = true
	}
	snapshot := sess.result
	sess.mu.Unlock()

	if needStep {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		g.stepMCPOAuthSession(ctx, sess)
		cancel()
		sess.mu.Lock()
		sess.polling = false
		snapshot = sess.result
		sess.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (g *Service) runMCPOAuthPollLoop(sess *mcpOAuthSession) {
	g.mcpOAuthMu.Lock()
	interval := g.mcpOAuthPollInterval
	g.mcpOAuthMu.Unlock()
	if interval <= 0 {
		interval = 1800 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := sess.createdAt.Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		<-ticker.C
		sess.mu.Lock()
		if sess.result.Status != "pending" {
			sess.mu.Unlock()
			return
		}
		if sess.polling {
			sess.mu.Unlock()
			continue
		}
		sess.polling = true
		sess.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		done := g.stepMCPOAuthSession(ctx, sess)
		cancel()
		sess.mu.Lock()
		sess.polling = false
		sess.mu.Unlock()
		if done {
			return
		}
	}
	sess.mu.Lock()
	if sess.result.Status == "pending" {
		sess.result.Status = "expired"
		sess.result.Error = "Authorization timed out"
	}
	sess.mu.Unlock()
}

func (g *Service) stepMCPOAuthSession(ctx context.Context, sess *mcpOAuthSession) bool {
	tokenURL := sess.siteOrigin + "/api/oauth/token"
	bodyBytes, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code_verifier": sess.codeVerifier,
		"client_id":     mcpOAuthClientID,
		"redirect_uri":  sess.redirectURI,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := g.mcpOAuthHTTPClient(tokenURL, 8*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload mcpOAuthTokenPayload
	_ = json.Unmarshal(raw, &payload)

	if resp.StatusCode == http.StatusBadRequest {
		errCode := strings.TrimSpace(payload.Error)
		if errCode == "authorization_pending" || errCode == "" {
			return false
		}
		if errCode == "invalid_grant" || errCode == "expired_token" || errCode == "access_denied" {
			msg := strings.TrimSpace(payload.ErrorDescription)
			if msg == "" {
				msg = errCode
			}
			sess.mu.Lock()
			sess.result.Status = "error"
			sess.result.Error = msg
			sess.mu.Unlock()
			return true
		}
		return false
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}

	accessToken := strings.TrimSpace(payload.AccessToken)
	if accessToken == "" {
		return false
	}

	mcpURL := strings.TrimSpace(payload.MCPURL)
	if mcpURL == "" {
		mcpURL = sess.siteOrigin + "/api/mcp"
	}

	accountLabel := strings.TrimSpace(payload.User.Email)
	if accountLabel == "" {
		accountLabel = strings.TrimSpace(payload.User.Nickname)
	}

	provCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	baseURL, apiKey, models := g.provisionProviderWithMCPToken(provCtx, sess, accessToken)

	sess.mu.Lock()
	sess.result = mcpOAuthPollResponse{
		Status:       "authorized",
		ProviderID:   sess.providerID,
		ProviderName: sess.providerName,
		SiteOrigin:   sess.siteOrigin,
		MCPURL:       mcpURL,
		AccessToken:  accessToken,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		AccountLabel: accountLabel,
		Models:       models,
	}
	sess.mu.Unlock()
	return true
}

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

	memberKeysURL := siteOrigin + "/api/hoxi/member/keys"
	var memberResp map[string]any
	if g.fetchMCPAuthedJSON(ctx, http.MethodGet, memberKeysURL, accessToken, nil, &memberResp) == nil {
		if key := extractRawAPIKeyFromPayload(memberResp); key != "" {
			return key
		}
		if keyID := extractFirstActiveKeyID(memberResp); keyID > 0 {
			var revealResp map[string]any
			if g.fetchMCPAuthedJSON(ctx, http.MethodPost, memberKeysURL, accessToken, map[string]any{"action": "reveal", "id": keyID}, &revealResp) == nil {
				if key := extractRawAPIKeyFromPayload(revealResp); key != "" {
					return key
				}
			}
		}
		var createResp map[string]any
		if g.fetchMCPAuthedJSON(ctx, http.MethodPost, memberKeysURL, accessToken, map[string]any{"name": "Hoxi Desktop"}, &createResp) == nil {
			if key := extractRawAPIKeyFromPayload(createResp); key != "" {
				return key
			}
		}
	}
	return ""
}

func (g *Service) fetchSiteModels(ctx context.Context, siteOrigin, baseURL, accessToken, apiKey string) []mcpOAuthModelItem {
	for _, endpoint := range []string{
		siteOrigin + "/api/ainode/public-models",
		siteOrigin + "/api/proxy/external?path=" + url.QueryEscape("/api/site/models/groups"),
		siteOrigin + "/api/hoxi/member/models",
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

func extractFirstActiveKeyID(payload map[string]any) int64 {
	for _, listField := range []string{"keys", "data", "items"} {
		list, ok := payload[listField].([]any)
		if !ok {
			continue
		}
		for _, raw := range list {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			status := strings.ToLower(firstNonEmptyString(item["status"]))
			if status != "" && status != "active" && status != "enabled" {
				continue
			}
			if id := int64(toFloat64(item["id"])); id > 0 {
				return id
			}
		}
	}
	return 0
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

func (g *Service) resolveMCPOAuthSiteOrigin(ctx context.Context, rawSiteURL, rawBaseURL string) string {
	var candidates []string
	addCandidate := func(u string) {
		parsed, err := url.Parse(strings.TrimSpace(u))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return
		}
		origin := parsed.Scheme + "://" + parsed.Host
		if strings.HasPrefix(strings.ToLower(parsed.Hostname()), "api.") {
			portalHost := strings.TrimPrefix(strings.TrimPrefix(parsed.Host, "api."), "API.")
			candidates = appendUniqueString(candidates, parsed.Scheme+"://"+portalHost)
		}
		candidates = appendUniqueString(candidates, origin)
	}
	addCandidate(rawSiteURL)
	addCandidate(rawBaseURL)
	addCandidate(hoxipaths.ServerBaseURL())
	if len(candidates) == 0 {
		return ""
	}
	if isLoopbackURL(candidates[0]) || len(candidates) == 1 {
		return candidates[0]
	}
	for _, candidate := range candidates {
		probeURL := candidate + "/api/oauth/authorize?mode=inspect&client_id=" + mcpOAuthClientID +
			"&redirect_uri=" + url.QueryEscape(candidate+"/user/mcp-authorize")
		probeCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("Accept", "application/json")
		resp, err := g.mcpOAuthHTTPClient(probeURL, 2500*time.Millisecond).Do(req)
		if err == nil {
			_ = resp.Body.Close()
			cancel()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return candidate
			}
			continue
		}
		cancel()
	}
	return candidates[0]
}

func normalizeGatewayAPIBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return trimmed
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1"
	}
	return strings.TrimRight(u.String(), "/")
}

func deriveGatewayAPIBaseURLFromSite(siteOrigin string) string {
	u, err := url.Parse(strings.TrimSpace(siteOrigin))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if isLoopbackURL(siteOrigin) {
		return strings.TrimRight(siteOrigin, "/") + "/v1"
	}
	if !strings.HasPrefix(strings.ToLower(u.Hostname()), "api.") {
		return u.Scheme + "://api." + u.Hostname() + "/v1"
	}
	return u.Scheme + "://" + u.Host + "/v1"
}

func generatePKCEParameters() (string, string, string, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", "", err
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	digest := sha256.Sum256([]byte(codeVerifier))
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", "", "", err
	}
	return codeVerifier, base64.RawURLEncoding.EncodeToString(digest[:]), hex.EncodeToString(stateBytes), nil
}

func appendUniqueString(slice []string, val string) []string {
	for _, item := range slice {
		if strings.EqualFold(item, val) {
			return slice
		}
	}
	return append(slice, val)
}

func firstNonEmptyString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func toFloat64(values ...any) float64 {
	for _, v := range values {
		switch n := v.(type) {
		case float64:
			if n != 0 {
				return n
			}
		case int:
			if n != 0 {
				return float64(n)
			}
		case int64:
			if n != 0 {
				return float64(n)
			}
		case json.Number:
			if f, err := n.Float64(); err == nil && f != 0 {
				return f
			}
		}
	}
	return 0
}

func openProviderBrowser(targetURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
