package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonHTTPResponse(status int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNativeProviderOAuthLifecycleRefreshRestartAndRevocation(t *testing.T) {
	homeDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", homeDir)

	var refreshCalls atomic.Int32
	var forceRevoke atomic.Bool

	SetProviderOAuthTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rawBody, _ := io.ReadAll(r.Body)
		switch r.URL.String() {
		case "https://oauth.acme.test/token":
			var req map[string]string
			_ = json.Unmarshal(rawBody, &req)
			if req["grant_type"] == "authorization_code" {
				if req["code"] != "code_initial_1" || req["code_verifier"] == "" {
					return jsonHTTPResponse(http.StatusBadRequest, `{"error":"invalid_request"}`), nil
				}
				return jsonHTTPResponse(http.StatusOK, `{"access_token":"tok_initial_v1","refresh_token":"ref_v1","expires_in":3600}`), nil
			}
			if req["grant_type"] == "refresh_token" {
				refreshCalls.Add(1)
				if forceRevoke.Load() {
					return jsonHTTPResponse(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"user revoked access"}`), nil
				}
				if req["refresh_token"] != "ref_v1" {
					return jsonHTTPResponse(http.StatusUnauthorized, `{"error":"invalid_grant"}`), nil
				}
				return jsonHTTPResponse(http.StatusOK, `{"access_token":"tok_refreshed_v2","refresh_token":"ref_v2","expires_in":7200}`), nil
			}
		case "https://openrouter.ai/api/v1/auth/keys":
			return jsonHTTPResponse(http.StatusOK, `{"key":"sk-or-pkce-minted"}`), nil
		}
		return jsonHTTPResponse(http.StatusNotFound, `{"error":"not_found"}`), nil
	}))
	defer SetProviderOAuthTransportForTest(nil)

	// OpenCode port 0 -> completely offline / daemonless!
	svc := NewService(0, "", wsDir, nil)
	if err := svc.PersistHoxiConfigDoc(map[string]any{
		"provider": map[string]any{
			"acme-oauth": map[string]any{
				"name": "Acme Cloud",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": "https://api.acme.test/v1",
				},
				"oauth": map[string]any{
					"authorizeURL": "https://oauth.acme.test/authorize",
					"tokenURL":     "https://oauth.acme.test/token",
					"clientID":     "acme-cli",
					"scopes":       "models:inference",
				},
				"models": map[string]any{
					"acme-coder-1": map[string]any{"name": "Acme Coder 1"},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Verify GET /provider/auth works without OpenCode.
	wMethods := httptest.NewRecorder()
	mux.ServeHTTP(wMethods, httptest.NewRequest(http.MethodGet, "/provider/auth", nil))
	if wMethods.Code != http.StatusOK {
		t.Fatalf("GET /provider/auth = %d %s", wMethods.Code, wMethods.Body.String())
	}
	var methods map[string][]providerAuthMethodItem
	if err := json.Unmarshal(wMethods.Body.Bytes(), &methods); err != nil {
		t.Fatal(err)
	}
	if len(methods["acme-oauth"]) == 0 || methods["acme-oauth"][0].Type != "oauth" {
		t.Fatalf("expected oauth method for acme-oauth, got %+v", methods["acme-oauth"])
	}
	if len(methods["openrouter"]) == 0 || methods["openrouter"][0].Type != "oauth" {
		t.Fatalf("expected oauth method for openrouter, got %+v", methods["openrouter"])
	}

	// 2. First-time login: authorize + callback exchanges PKCE code and persists token!
	wAuth := httptest.NewRecorder()
	mux.ServeHTTP(wAuth, httptest.NewRequest(http.MethodPost, "/provider/acme-oauth/oauth/authorize", bytes.NewReader([]byte(`{"method":0}`))))
	if wAuth.Code != http.StatusOK {
		t.Fatalf("authorize = %d %s", wAuth.Code, wAuth.Body.String())
	}
	var authResp providerOAuthAuthResponse
	if err := json.Unmarshal(wAuth.Body.Bytes(), &authResp); err != nil || !strings.Contains(authResp.URL, "code_challenge=") {
		t.Fatalf("unexpected authorize response: %+v err=%v", authResp, err)
	}

	wCb := httptest.NewRecorder()
	mux.ServeHTTP(wCb, httptest.NewRequest(http.MethodPost, "/provider/acme-oauth/oauth/callback", bytes.NewReader([]byte(`{"method":0,"code":"code_initial_1"}`))))
	if wCb.Code != http.StatusOK {
		t.Fatalf("callback = %d %s", wCb.Code, wCb.Body.String())
	}
	if got := svc.ResolveStoredProviderKey("acme-oauth", "https://api.acme.test/v1"); got != "tok_initial_v1" {
		t.Fatalf("expected tok_initial_v1 after initial OAuth login, got %q", got)
	}

	// 3. Expired token auto-refresh + restart persistence!
	if err := svc.persistEngineProviderAuthMap("acme-oauth", map[string]any{
		"type":     "oauth",
		"access":   "tok_expired_v1",
		"refresh":  "ref_v1",
		"expires":  time.Now().Add(-5 * time.Minute).UnixMilli(),
		"tokenURL": "https://oauth.acme.test/token",
		"clientID": "acme-cli",
	}); err != nil {
		t.Fatal(err)
	}
	if got := svc.ResolveStoredProviderKey("acme-oauth", "https://api.acme.test/v1"); got != "tok_refreshed_v2" {
		t.Fatalf("expected auto-refreshed tok_refreshed_v2, got %q", got)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected 1 refresh call, got %d", refreshCalls.Load())
	}

	// Simulate Hoxi restart: new Service instance reads rotated token without hitting refresh endpoint again!
	svcAfterRestart := NewService(0, "", wsDir, nil)
	if got := svcAfterRestart.ResolveStoredProviderKey("acme-oauth", "https://api.acme.test/v1"); got != "tok_refreshed_v2" {
		t.Fatalf("expected tok_refreshed_v2 persisted across restart, got %q", got)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected 0 extra refresh calls after restart with fresh expiry, got %d", refreshCalls.Load()-1)
	}

	// 4. Revocation: expire token again and simulate upstream invalid_grant -> marks disconnected & requires re-auth!
	forceRevoke.Store(true)
	if err := svcAfterRestart.persistEngineProviderAuthMap("acme-oauth", map[string]any{
		"type":     "oauth",
		"access":   "tok_stale",
		"refresh":  "ref_v1",
		"expires":  time.Now().Add(-2 * time.Minute).UnixMilli(),
		"tokenURL": "https://oauth.acme.test/token",
		"clientID": "acme-cli",
	}); err != nil {
		t.Fatal(err)
	}
	if got := svcAfterRestart.ResolveStoredProviderKey("acme-oauth", "https://api.acme.test/v1"); got != "" {
		t.Fatalf("expected empty credential after revocation, got %q", got)
	}
	_, revokedFlag, revErr := svcAfterRestart.resolveAuthFileCredential(nil, "acme-oauth")
	if !revokedFlag || revErr == nil || !strings.Contains(revErr.Error(), "revoked") {
		t.Fatalf("expected explicit revocation diagnostic, got revoked=%v err=%v", revokedFlag, revErr)
	}
	offlineList := svcAfterRestart.buildOfflineProviderListPayload()
	for _, id := range offlineList["connected"].([]string) {
		if id == "acme-oauth" {
			t.Fatalf("revoked OAuth provider must not appear in connected list: %+v", offlineList["connected"])
		}
	}
}
