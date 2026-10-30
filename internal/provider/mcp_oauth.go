package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	mcpOAuthClientID   = "hoxi-desktop"
	mcpOAuthClientName = "Hoxi"
	mcpOAuthScopes     = "mcp:tools mcp:profile:read mcp:theme:read"
	mcpOAuthSessionTTL = 5 * time.Minute
)

type mcpOAuthStartRequest struct {
	ProviderID        string   `json:"providerId"`
	ProviderName      string   `json:"providerName,omitempty"`
	SiteURL           string   `json:"siteUrl,omitempty"`
	BaseURL           string   `json:"baseUrl,omitempty"`
	RecommendedModels []string `json:"recommendedModels,omitempty"`
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
	BaseURL      string              `json:"baseUrl,omitempty"`
	AccountLabel string              `json:"accountLabel,omitempty"`
	Models       []mcpOAuthModelItem `json:"models,omitempty"`
	Error        string              `json:"error,omitempty"`
}

type mcpOAuthSession struct {
	mu                sync.Mutex
	state             string
	codeVerifier      string
	redirectURI       string
	siteOrigin        string
	providerID        string
	providerName      string
	hintBaseURL       string
	recommendedModels []string
	authURL           string
	createdAt         time.Time
	polling           bool
	result            mcpOAuthPollResponse
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
		state:             state,
		codeVerifier:      codeVerifier,
		redirectURI:       redirectURI,
		siteOrigin:        siteOrigin,
		providerID:        providerID,
		providerName:      providerName,
		hintBaseURL:       strings.TrimSpace(req.BaseURL),
		recommendedModels: append([]string(nil), req.RecommendedModels...),
		authURL:           authURL,
		createdAt:         time.Now(),
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
	persistedModels, persistErr := g.persistMCPOAuthProvisioning(sess, mcpURL, accessToken, baseURL, apiKey, models)
	if persistErr != nil {
		sess.mu.Lock()
		sess.result.Status = "error"
		sess.result.Error = persistErr.Error()
		sess.mu.Unlock()
		return true
	}

	sess.mu.Lock()
	sess.result = mcpOAuthPollResponse{
		Status:       "authorized",
		ProviderID:   sess.providerID,
		ProviderName: sess.providerName,
		SiteOrigin:   sess.siteOrigin,
		MCPURL:       mcpURL,
		BaseURL:      baseURL,
		AccountLabel: accountLabel,
		Models:       persistedModels,
	}
	sess.mu.Unlock()
	return true
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
		cmd = subproc.Command("open", targetURL)
	case "windows":
		cmd = subproc.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = subproc.Command("xdg-open", targetURL)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
