package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const providerOAuthTTL = 10 * time.Minute

type providerAuthMethodItem struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

type providerOAuthAuthResponse struct {
	URL          string `json:"url"`
	Method       string `json:"method"` // "auto" | "code"
	Instructions string `json:"instructions"`
}

type pendingProviderOAuthFlow struct {
	ProviderID   string
	MethodIndex  int
	FlowKind     string // "openrouter_pkce" | "github_device" | "custom_pkce"
	CodeVerifier string
	DeviceCode   string
	RedirectURI  string
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       string
	State        string
	CreatedAt    time.Time
}

var (
	providerOAuthFlowsMu sync.Mutex
	providerOAuthFlows   = map[string]*pendingProviderOAuthFlow{}
)

// handleProviderSubroutes handles /api/hoxi/provider/auth-methods and
// /api/hoxi/provider/oauth/:id/{authorize|callback} (plus legacy /provider/auth
// and /provider/:id/oauth/{authorize|callback}) natively when OpenCode is absent.
func (g *Service) handleProviderSubroutes(w http.ResponseWriter, r *http.Request) {
	isAuthMethods := false
	var oauthPID, oauthAction string

	switch {
	case r.URL.Path == "/api/hoxi/provider/auth-methods":
		isAuthMethods = true
	case strings.HasPrefix(r.URL.Path, "/api/hoxi/provider/oauth/"):
		rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/provider/oauth/"), "/")
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && (parts[1] == "authorize" || parts[1] == "callback") {
			pid, _ := url.PathUnescape(parts[0])
			oauthPID = strings.TrimSpace(pid)
			oauthAction = parts[1]
		}
	default:
		clean := strings.TrimPrefix(r.URL.Path, "/api")
		clean = strings.TrimPrefix(clean, "/provider/")
		clean = strings.Trim(clean, "/")
		if clean == "auth" {
			isAuthMethods = true
		} else {
			parts := strings.Split(clean, "/")
			if len(parts) == 3 && parts[1] == "oauth" && (parts[2] == "authorize" || parts[2] == "callback") {
				pid, _ := url.PathUnescape(parts[0])
				oauthPID = strings.TrimSpace(pid)
				oauthAction = parts[2]
			}
		}
	}

	if g.openCode != nil && g.openCode.Available() && g.proxy != nil {
		if strings.HasPrefix(r.URL.Path, "/api/hoxi/") {
			r2 := r.Clone(r.Context())
			r2.URL.RawPath = ""
			switch {
			case isAuthMethods:
				r2.URL.Path = "/provider/auth"
			case oauthPID != "" && oauthAction != "":
				r2.URL.Path = "/provider/" + url.PathEscape(oauthPID) + "/oauth/" + oauthAction
			default:
				writeJSONError(w, http.StatusNotFound, "provider route not found")
				return
			}
			g.proxy.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.StripPrefix("/api", g.proxy).ServeHTTP(w, r)
			return
		}
		g.proxy.ServeHTTP(w, r)
		return
	}

	if isAuthMethods && r.Method == http.MethodGet {
		g.handleNativeProviderAuthMethods(w, r)
		return
	}
	if oauthPID != "" && r.Method == http.MethodPost {
		switch oauthAction {
		case "authorize":
			g.handleNativeProviderOAuthAuthorize(w, r, oauthPID)
			return
		case "callback":
			g.handleNativeProviderOAuthCallback(w, r, oauthPID)
			return
		}
	}
	writeJSONError(w, http.StatusNotFound, "provider route not found")
}

func (g *Service) nativeMethodsForProvider(pid string) []providerAuthMethodItem {
	methods := []providerAuthMethodItem{{Type: "api", Label: "API Key"}}
	switch strings.ToLower(strings.TrimSpace(pid)) {
	case "openrouter":
		methods = append([]providerAuthMethodItem{{Type: "oauth", Label: "Connect with OpenRouter (PKCE OAuth)"}}, methods...)
	case "github-copilot":
		methods = append([]providerAuthMethodItem{{Type: "oauth", Label: "Sign in with GitHub Copilot (Device Code)"}}, methods...)
	default:
		if _, _, _, _, ok := g.configuredProviderOAuthSpec(pid); ok {
			methods = append([]providerAuthMethodItem{{Type: "oauth", Label: "OAuth 2.0 PKCE"}}, methods...)
		}
	}
	return methods
}

func (g *Service) handleNativeProviderAuthMethods(w http.ResponseWriter, _ *http.Request) {
	out := map[string][]providerAuthMethodItem{
		"openrouter":     g.nativeMethodsForProvider("openrouter"),
		"github-copilot": g.nativeMethodsForProvider("github-copilot"),
	}
	for pid := range g.loadHoxiProvidersMap() {
		if g.officialBlocked.Load() && pid == officialProviderID {
			continue
		}
		out[pid] = g.nativeMethodsForProvider(pid)
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Service) handleNativeProviderOAuthAuthorize(w http.ResponseWriter, r *http.Request, providerID string) {
	var body struct {
		Method int               `json:"method"`
		Inputs map[string]string `json:"inputs"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)

	lower := strings.ToLower(strings.TrimSpace(providerID))
	verifier, challenge, state, err := generatePKCEParameters()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate OAuth PKCE parameters")
		return
	}

	switch lower {
	case "openrouter":
		callbackURL := "https://openrouter.ai/auth/callback"
		if v := strings.TrimSpace(body.Inputs["redirectURI"]); v != "" {
			callbackURL = v
		}
		q := url.Values{
			"callback_url":          {callbackURL},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
		}
		storePendingProviderOAuth(&pendingProviderOAuthFlow{
			ProviderID:   providerID,
			MethodIndex:  body.Method,
			FlowKind:     "openrouter_pkce",
			CodeVerifier: verifier,
			TokenURL:     "https://openrouter.ai/api/v1/auth/keys",
			CreatedAt:    time.Now(),
		})
		writeJSON(w, http.StatusOK, providerOAuthAuthResponse{
			URL:          "https://openrouter.ai/auth?" + q.Encode(),
			Method:       "code",
			Instructions: "Authorize Hoxi in OpenRouter and paste the returned authorization code.",
		})
		return

	case "github-copilot":
		const ghClientID = "Iv1.b507a08c87ecfe98"
		form := url.Values{"client_id": {ghClientID}, "scope": {"read:user"}}
		req, reqErr := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://github.com/login/device/code", strings.NewReader(form.Encode()))
		if reqErr != nil {
			writeJSONError(w, http.StatusInternalServerError, reqErr.Error())
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		resp, doErr := g.oauthClient("https://github.com/login/device/code", 10*time.Second).Do(req)
		if doErr != nil {
			writeJSONError(w, http.StatusBadGateway, doErr.Error())
			return
		}
		defer resp.Body.Close()
		var devResp struct {
			DeviceCode      string `json:"device_code"`
			UserCode        string `json:"user_code"`
			VerificationURI string `json:"verification_uri"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&devResp) != nil || devResp.DeviceCode == "" {
			writeJSONError(w, http.StatusBadGateway, "GitHub device authorization failed")
			return
		}
		storePendingProviderOAuth(&pendingProviderOAuthFlow{
			ProviderID:  providerID,
			MethodIndex: body.Method,
			FlowKind:    "github_device",
			DeviceCode:  devResp.DeviceCode,
			ClientID:    ghClientID,
			TokenURL:    "https://github.com/login/oauth/access_token",
			CreatedAt:   time.Now(),
		})
		writeJSON(w, http.StatusOK, providerOAuthAuthResponse{
			URL:          devResp.VerificationURI,
			Method:       "auto",
			Instructions: "Enter code: " + devResp.UserCode,
		})
		return
	}

	if authEP, tokenEP, clientID, scopes, ok := g.configuredProviderOAuthSpec(providerID); ok {
		redirectURI := strings.TrimSpace(body.Inputs["redirectURI"])
		if redirectURI == "" {
			redirectURI = "urn:ietf:wg:oauth:2.0:oob"
		}
		q := url.Values{
			"response_type":         {"code"},
			"client_id":             {clientID},
			"redirect_uri":          {redirectURI},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"state":                 {state},
		}
		if scopes != "" {
			q.Set("scope", scopes)
		}
		sep := "?"
		if strings.Contains(authEP, "?") {
			sep = "&"
		}
		storePendingProviderOAuth(&pendingProviderOAuthFlow{
			ProviderID:   providerID,
			MethodIndex:  body.Method,
			FlowKind:     "custom_pkce",
			CodeVerifier: verifier,
			RedirectURI:  redirectURI,
			AuthorizeURL: authEP,
			TokenURL:     tokenEP,
			ClientID:     clientID,
			Scopes:       scopes,
			State:        state,
			CreatedAt:    time.Now(),
		})
		writeJSON(w, http.StatusOK, providerOAuthAuthResponse{
			URL:          authEP + sep + q.Encode(),
			Method:       "code",
			Instructions: "Complete authorization in your browser and paste the authorization code.",
		})
		return
	}

	writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("provider %q requires an API Key or dedicated CLI login (e.g. claude / codex / gemini) when running without OpenCode plugins", providerID))
}

func (g *Service) handleNativeProviderOAuthCallback(w http.ResponseWriter, r *http.Request, providerID string) {
	var body struct {
		Method int    `json:"method"`
		Code   string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)

	flow := lookupPendingProviderOAuth(providerID)
	if flow == nil {
		writeJSONError(w, http.StatusBadRequest, "no pending OAuth authorization session for provider "+providerID)
		return
	}

	switch flow.FlowKind {
	case "openrouter_pkce":
		code := extractOAuthCodeInput(body.Code)
		if code == "" {
			writeJSONError(w, http.StatusBadRequest, "authorization code is required")
			return
		}
		reqBody, _ := json.Marshal(map[string]string{
			"code":                  code,
			"code_verifier":         flow.CodeVerifier,
			"code_challenge_method": "S256",
		})
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, flow.TokenURL, bytes.NewReader(reqBody))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.oauthClient(flow.TokenURL, 12*time.Second).Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		var keyResp struct {
			Key string `json:"key"`
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&keyResp) != nil || strings.TrimSpace(keyResp.Key) == "" {
			writeJSONError(w, http.StatusBadGateway, "OpenRouter code exchange failed")
			return
		}
		if err := g.persistEngineProviderAuthMap(providerID, map[string]any{"type": "api", "key": strings.TrimSpace(keyResp.Key)}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		clearPendingProviderOAuth(providerID)
		writeJSON(w, http.StatusOK, true)
		return

	case "github_device":
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			form := url.Values{
				"client_id":   {flow.ClientID},
				"device_code": {flow.DeviceCode},
				"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			}
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, flow.TokenURL, strings.NewReader(form.Encode()))
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")
			resp, err := g.oauthClient(flow.TokenURL, 10*time.Second).Do(req)
			if err != nil {
				writeJSONError(w, http.StatusBadGateway, err.Error())
				return
			}
			var ghTok struct {
				AccessToken string `json:"access_token"`
				Error       string `json:"error"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ghTok)
			_ = resp.Body.Close()
			if strings.TrimSpace(ghTok.AccessToken) != "" {
				entry := storedProviderAuthEntry{
					Type:    "oauth",
					Refresh: strings.TrimSpace(ghTok.AccessToken),
				}
				if _, _, refErr := g.refreshProviderOAuthCredential(r.Context(), providerID, entry); refErr != nil {
					_ = g.persistEngineProviderAuthMap(providerID, map[string]any{
						"type":    "oauth",
						"refresh": entry.Refresh,
						"access":  entry.Refresh,
						"expires": 0,
					})
				}
				clearPendingProviderOAuth(providerID)
				writeJSON(w, http.StatusOK, true)
				return
			}
			if ghTok.Error != "authorization_pending" && ghTok.Error != "slow_down" {
				writeJSONError(w, http.StatusBadRequest, firstNonEmptyString(ghTok.Error, "device authorization denied"))
				return
			}
			select {
			case <-r.Context().Done():
				writeJSONError(w, http.StatusRequestTimeout, "authorization cancelled")
				return
			case <-time.After(1500 * time.Millisecond):
			}
		}
		writeJSONError(w, http.StatusRequestTimeout, "GitHub device authorization timed out")
		return

	case "custom_pkce":
		code := extractOAuthCodeInput(body.Code)
		if code == "" {
			writeJSONError(w, http.StatusBadRequest, "authorization code is required")
			return
		}
		payload := map[string]string{
			"grant_type":    "authorization_code",
			"code":          code,
			"code_verifier": flow.CodeVerifier,
			"redirect_uri":  flow.RedirectURI,
		}
		if flow.ClientID != "" {
			payload["client_id"] = flow.ClientID
		}
		rawReq, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, flow.TokenURL, bytes.NewReader(rawReq))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := g.oauthClient(flow.TokenURL, 12*time.Second).Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		var tok struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
			Error        string `json:"error"`
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&tok) != nil || strings.TrimSpace(tok.AccessToken) == "" {
			writeJSONError(w, http.StatusBadRequest, firstNonEmptyString(tok.Error, "OAuth token exchange failed"))
			return
		}
		var expMs int64
		if tok.ExpiresIn > 0 {
			expMs = time.Now().UnixMilli() + tok.ExpiresIn*1000
		}
		authRecord := map[string]any{
			"type":     "oauth",
			"access":   strings.TrimSpace(tok.AccessToken),
			"refresh":  strings.TrimSpace(tok.RefreshToken),
			"expires":  expMs,
			"tokenURL": flow.TokenURL,
		}
		if flow.ClientID != "" {
			authRecord["clientID"] = flow.ClientID
		}
		if err := g.persistEngineProviderAuthMap(providerID, authRecord); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		clearPendingProviderOAuth(providerID)
		writeJSON(w, http.StatusOK, true)
		return
	}

	writeJSONError(w, http.StatusBadRequest, "unsupported OAuth callback flow")
}

func extractOAuthCodeInput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if u, err := url.Parse(trimmed); err == nil {
			if c := strings.TrimSpace(u.Query().Get("code")); c != "" {
				return c
			}
		}
	}
	if idx := strings.IndexByte(trimmed, '#'); idx > 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func storePendingProviderOAuth(flow *pendingProviderOAuthFlow) {
	providerOAuthFlowsMu.Lock()
	now := time.Now()
	for k, v := range providerOAuthFlows {
		if now.Sub(v.CreatedAt) > providerOAuthTTL {
			delete(providerOAuthFlows, k)
		}
	}
	providerOAuthFlows[flow.ProviderID] = flow
	providerOAuthFlowsMu.Unlock()
}

func lookupPendingProviderOAuth(providerID string) *pendingProviderOAuthFlow {
	providerOAuthFlowsMu.Lock()
	defer providerOAuthFlowsMu.Unlock()
	flow := providerOAuthFlows[providerID]
	if flow == nil || time.Since(flow.CreatedAt) > providerOAuthTTL {
		delete(providerOAuthFlows, providerID)
		return nil
	}
	return flow
}

func clearPendingProviderOAuth(providerID string) {
	providerOAuthFlowsMu.Lock()
	delete(providerOAuthFlows, providerID)
	providerOAuthFlowsMu.Unlock()
}
