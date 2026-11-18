package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const oauthRefreshSkewMs = 60 * 1000

type storedProviderAuthEntry struct {
	Type          string `json:"type,omitempty"`
	Key           string `json:"key,omitempty"`
	Access        string `json:"access,omitempty"`
	Refresh       string `json:"refresh,omitempty"`
	Expires       int64  `json:"expires,omitempty"`
	AccountID     string `json:"accountId,omitempty"`
	EnterpriseURL string `json:"enterpriseUrl,omitempty"`
	TokenURL      string `json:"tokenURL,omitempty"`
	ClientID      string `json:"clientID,omitempty"`
	Revoked       bool   `json:"revoked,omitempty"`
	RevokedReason string `json:"revokedReason,omitempty"`
}

var (
	oauthRefreshMu     sync.Mutex
	oauthHTTPTransport http.RoundTripper
)

// SetProviderOAuthTransportForTest overrides the HTTP transport used for OAuth token exchange/refresh in tests.
func SetProviderOAuthTransportForTest(rt http.RoundTripper) {
	oauthRefreshMu.Lock()
	oauthHTTPTransport = rt
	oauthRefreshMu.Unlock()
}

func (g *Service) oauthClient(targetURL string, timeout time.Duration) *http.Client {
	oauthRefreshMu.Lock()
	override := oauthHTTPTransport
	oauthRefreshMu.Unlock()
	if override != nil {
		return &http.Client{Timeout: timeout, Transport: override}
	}
	transport := providerTransport
	if isLoopbackURL(targetURL) {
		transport = localTransport
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func normalizeExpiresMs(raw int64) int64 {
	if raw <= 0 {
		return 0
	}
	if raw < 1_000_000_000_000 {
		return raw * 1000
	}
	return raw
}

// resolveAuthFileCredential reads API keys and OAuth credentials from Hoxi's
// auth.json (and legacy OpenCode auth.json), automatically refreshing expired
// OAuth tokens, persisting rotated tokens for post-restart reuse, and marking
// revoked credentials so they never silently fail without re-auth diagnostics.
func (g *Service) resolveAuthFileCredential(ctx context.Context, providerID string) (string, bool, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return "", false, nil
	}
	for _, authPath := range candidateProviderAuthPaths() {
		entry, ok := readProviderAuthEntry(authPath, providerID)
		if !ok {
			continue
		}
		if entry.Revoked {
			return "", true, fmt.Errorf("provider %q OAuth authorization was revoked (%s); please re-authorize in Settings", providerID, firstNonEmptyString(entry.RevokedReason, "token revoked"))
		}
		if k := strings.TrimSpace(entry.Key); k != "" {
			return k, false, nil
		}
		access := strings.TrimSpace(entry.Access)
		refresh := strings.TrimSpace(entry.Refresh)
		expMs := normalizeExpiresMs(entry.Expires)
		nowMs := time.Now().UnixMilli()

		if access != "" && (expMs == 0 || nowMs+oauthRefreshSkewMs < expMs) {
			return access, false, nil
		}
		if refresh == "" {
			if access != "" && expMs > 0 && nowMs >= expMs {
				return "", true, fmt.Errorf("provider %q OAuth access token expired and has no refresh token; please re-authorize", providerID)
			}
			continue
		}

		refreshed, revoked, refErr := g.refreshProviderOAuthCredential(ctx, providerID, entry)
		if refErr == nil && strings.TrimSpace(refreshed.Access) != "" {
			return strings.TrimSpace(refreshed.Access), false, nil
		}
		if revoked {
			return "", true, refErr
		}
		if access != "" && (expMs == 0 || nowMs < expMs) {
			return access, false, nil
		}
		if refErr != nil {
			return "", false, refErr
		}
	}
	return "", false, nil
}

func candidateProviderAuthPaths() []string {
	var out []string
	if p := strings.TrimSpace(hoxipaths.EngineAuthPath()); p != "" {
		out = append(out, p)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		legacy := filepath.Join(home, ".local", "share", "opencode", "auth.json")
		if len(out) == 0 || filepath.Clean(out[0]) != filepath.Clean(legacy) {
			out = append(out, legacy)
		}
	}
	return out
}

func readProviderAuthEntry(authPath, providerID string) (storedProviderAuthEntry, bool) {
	raw, err := os.ReadFile(authPath)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return storedProviderAuthEntry{}, false
	}
	var all map[string]storedProviderAuthEntry
	if json.Unmarshal(raw, &all) != nil {
		return storedProviderAuthEntry{}, false
	}
	e, ok := all[providerID]
	return e, ok
}

func defaultOAuthTokenEndpoint(providerID string, entry storedProviderAuthEntry) (tokenURL, clientID string, isGitHubCopilot bool) {
	if strings.TrimSpace(entry.TokenURL) != "" {
		return strings.TrimSpace(entry.TokenURL), strings.TrimSpace(entry.ClientID), false
	}
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "github-copilot":
		host := "api.github.com"
		if strings.TrimSpace(entry.EnterpriseURL) != "" {
			if u, err := url.Parse(strings.TrimSpace(entry.EnterpriseURL)); err == nil && u.Host != "" {
				host = "api." + u.Host
			}
		}
		return "https://" + host + "/copilot_internal/v2/token", "", true
	case "openai", "codex":
		return "https://auth.openai.com/oauth/token", "app_EMoamEEZ73f0CkXaXp7hrann", false
	case "anthropic":
		return "https://console.anthropic.com/v1/oauth/token", "9d1c250a-e61b-44d9-88ed-5944d1962f5e", false
	case "google", "gemini":
		return "https://oauth2.googleapis.com/token", strings.TrimSpace(entry.ClientID), false
	default:
		return "", strings.TrimSpace(entry.ClientID), false
	}
}

func (g *Service) refreshProviderOAuthCredential(ctx context.Context, providerID string, entry storedProviderAuthEntry) (storedProviderAuthEntry, bool, error) {
	oauthRefreshMu.Lock()
	if latest, ok := readProviderAuthEntry(hoxipaths.EngineAuthPath(), providerID); ok {
		expMs := normalizeExpiresMs(latest.Expires)
		if latest.Revoked {
			oauthRefreshMu.Unlock()
			return latest, true, fmt.Errorf("provider %q OAuth token was revoked (%s); please re-authorize", providerID, latest.RevokedReason)
		}
		if strings.TrimSpace(latest.Access) != "" && expMs > time.Now().UnixMilli()+oauthRefreshSkewMs {
			oauthRefreshMu.Unlock()
			return latest, false, nil
		}
	}
	oauthRefreshMu.Unlock()

	tokenURL, clientID, isCopilot := defaultOAuthTokenEndpoint(providerID, entry)
	if cfgTokenURL, cfgClientID := g.configuredProviderOAuthEndpoints(providerID); tokenURL == "" && cfgTokenURL != "" {
		tokenURL = cfgTokenURL
		if clientID == "" {
			clientID = cfgClientID
		}
	}
	if tokenURL == "" {
		return entry, false, fmt.Errorf("provider %q OAuth token expired and no token refresh endpoint is configured; please re-authorize", providerID)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	var httpReq *http.Request
	var err error
	if isCopilot {
		httpReq, err = http.NewRequestWithContext(reqCtx, http.MethodGet, tokenURL, nil)
		if err == nil {
			httpReq.Header.Set("Accept", "application/json")
			httpReq.Header.Set("Authorization", "token "+strings.TrimSpace(entry.Refresh))
			httpReq.Header.Set("User-Agent", "Hoxi-Native/1.0")
		}
	} else {
		payload := map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": strings.TrimSpace(entry.Refresh),
		}
		if clientID != "" {
			payload["client_id"] = clientID
		}
		rawBody, _ := json.Marshal(payload)
		httpReq, err = http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, bytes.NewReader(rawBody))
		if err == nil {
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Accept", "application/json")
		}
	}
	if err != nil {
		return entry, false, err
	}

	resp, err := g.oauthClient(tokenURL, 12*time.Second).Do(httpReq)
	if err != nil {
		return entry, false, err
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))

	var tokResp struct {
		AccessToken      string `json:"access_token"`
		Token            string `json:"token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		ExpiresAt        int64  `json:"expires_at"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(rawResp, &tokResp)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		tokResp.Error == "invalid_grant" || tokResp.Error == "access_denied" || tokResp.Error == "expired_token" {
		reason := firstNonEmptyString(tokResp.ErrorDescription, tokResp.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		_ = g.persistEngineProviderAuthMap(providerID, map[string]any{
			"type":          "oauth",
			"revoked":       true,
			"revokedReason": reason,
		})
		return storedProviderAuthEntry{Type: "oauth", Revoked: true, RevokedReason: reason}, true,
			fmt.Errorf("provider %q OAuth authorization expired or was revoked (%s); please reconnect in Settings", providerID, reason)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return entry, false, fmt.Errorf("provider %q OAuth refresh HTTP %d: %s", providerID, resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}

	newAccess := strings.TrimSpace(firstNonEmptyString(tokResp.AccessToken, tokResp.Token))
	if newAccess == "" {
		return entry, false, fmt.Errorf("provider %q OAuth refresh returned empty access token", providerID)
	}
	newRefresh := strings.TrimSpace(firstNonEmptyString(tokResp.RefreshToken, entry.Refresh))
	var newExpiresMs int64
	if tokResp.ExpiresAt > 0 {
		newExpiresMs = normalizeExpiresMs(tokResp.ExpiresAt)
	} else if tokResp.ExpiresIn > 0 {
		newExpiresMs = time.Now().UnixMilli() + tokResp.ExpiresIn*1000
	} else {
		newExpiresMs = time.Now().UnixMilli() + 3600*1000
	}

	updated := entry
	updated.Type = "oauth"
	updated.Access = newAccess
	updated.Refresh = newRefresh
	updated.Expires = newExpiresMs
	updated.Revoked = false
	updated.RevokedReason = ""

	saveMap := map[string]any{
		"type":    "oauth",
		"access":  updated.Access,
		"refresh": updated.Refresh,
		"expires": updated.Expires,
	}
	if updated.AccountID != "" {
		saveMap["accountId"] = updated.AccountID
	}
	if updated.EnterpriseURL != "" {
		saveMap["enterpriseUrl"] = updated.EnterpriseURL
	}
	if tokenURL != "" && !isCopilot {
		saveMap["tokenURL"] = tokenURL
	}
	if clientID != "" {
		saveMap["clientID"] = clientID
	}
	_ = g.persistEngineProviderAuthMap(providerID, saveMap)
	return updated, false, nil
}

func (g *Service) configuredProviderOAuthEndpoints(providerID string) (tokenURL, clientID string) {
	if rawProv, ok := g.loadHoxiProvidersMap()[providerID].(map[string]any); ok {
		if oauthMap, ok := rawProv["oauth"].(map[string]any); ok {
			tokenURL, _ = oauthMap["tokenURL"].(string)
			clientID, _ = oauthMap["clientID"].(string)
		}
	}
	return strings.TrimSpace(tokenURL), strings.TrimSpace(clientID)
}

func (g *Service) configuredProviderOAuthSpec(providerID string) (authURL, tokenURL, clientID, scopes string, ok bool) {
	if rawProv, exists := g.loadHoxiProvidersMap()[providerID].(map[string]any); exists {
		if oauthMap, has := rawProv["oauth"].(map[string]any); has {
			authURL, _ = oauthMap["authorizeURL"].(string)
			tokenURL, _ = oauthMap["tokenURL"].(string)
			clientID, _ = oauthMap["clientID"].(string)
			scopes, _ = oauthMap["scopes"].(string)
			if strings.TrimSpace(authURL) != "" && strings.TrimSpace(tokenURL) != "" {
				return strings.TrimSpace(authURL), strings.TrimSpace(tokenURL), strings.TrimSpace(clientID), strings.TrimSpace(scopes), true
			}
		}
	}
	return "", "", "", "", false
}

// handleConfigProvidersProxy serves GET /config/providers from OpenCode when
// running and synthesizes the equivalent provider list natively when offline.
func (g *Service) handleConfigProvidersProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if g.openCode != nil && g.openCode.Available() {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		status, body, err := g.openCode.Raw(ctx, http.MethodGet, "/config/providers", r.URL.Query(), nil)
		cancel()
		if err == nil && status >= 200 && status < 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
	}
	offline := g.buildOfflineProviderListPayload()
	connectedSet := map[string]bool{}
	if connList, ok := offline["connected"].([]string); ok {
		for _, id := range connList {
			connectedSet[id] = true
		}
	}
	var providers []map[string]any
	if allList, ok := offline["all"].([]interface{}); ok {
		for _, item := range allList {
			if pMap, ok := item.(map[string]interface{}); ok {
				pid, _ := pMap["id"].(string)
				if !connectedSet[pid] {
					continue
				}
				cp := make(map[string]any, len(pMap)+1)
				for k, v := range pMap {
					cp[k] = v
				}
				cp["key"] = "configured"
				providers = append(providers, cp)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": providers,
		"default":   offline["default"],
	})
}

func isValidUnrevokedAuthEntry(raw any) bool {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return false
	}
	if rev, _ := m["revoked"].(bool); rev {
		return false
	}
	for _, f := range []string{"key", "access", "refresh"} {
		if v, _ := m[f].(string); strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}
