package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderMCPOAuthPKCEAndProvisioning(t *testing.T) {
	var approved atomic.Bool
	var capturedChallenge atomic.Value
	var capturedRedirectURI atomic.Value

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/authorize":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"client_id":"hoxi-desktop"}`))

		case "/api/oauth/token":
			var body struct {
				GrantType    string `json:"grant_type"`
				CodeVerifier string `json:"code_verifier"`
				ClientID     string `json:"client_id"`
				RedirectURI  string `json:"redirect_uri"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !approved.Load() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"Waiting for user approval"}`))
				return
			}
			sum := sha256.Sum256([]byte(body.CodeVerifier))
			computedChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			wantChallenge, _ := capturedChallenge.Load().(string)
			if wantChallenge != "" && computedChallenge != wantChallenge {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"PKCE verification failed"}`))
				return
			}
			wantRedirect, _ := capturedRedirectURI.Load().(string)
			if wantRedirect != "" && body.RedirectURI != wantRedirect {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"redirect_uri mismatch"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "apay_mcp_test_token_123",
				"token_type":   "Bearer",
				"expires_in":   7776000,
				"scope":        mcpOAuthScopes,
				"mcp_url":      server.URL + "/api/mcp",
				"user": map[string]any{
					"id":       42,
					"email":    "dev@ainode.run",
					"nickname": "DevUser",
				},
			})

		case "/api/proxy/gateway-url":
			if r.Header.Get("Authorization") != "Bearer apay_mcp_test_token_123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"url": "https://api.ainode.run",
			})

		case "/api/proxy/external":
			if r.Header.Get("Authorization") != "Bearer apay_mcp_test_token_123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			targetPath := r.URL.Query().Get("path")
			if targetPath == "/api/site/api-keys/list" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"data": []map[string]any{
						{
							"id":     1,
							"name":   "Default Key",
							"rawKey": "sk-ainode-live-99887766",
							"status": "active",
						},
					},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)

		case "/api/ainode/public-models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"groups": []map[string]any{
					{
						"id":   1,
						"name": "Default Group",
						"models": []map[string]any{
							{
								"code":        "claude-sonnet-4-6",
								"name":        "Claude Sonnet 4.6",
								"inputPrice":  3.0,
								"outputPrice": 15.0,
							},
							{
								"code":        "gpt-5.4",
								"name":        "GPT-5.4",
								"inputPrice":  2.5,
								"outputPrice": 10.0,
							},
						},
					},
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	svc.mcpOAuthPollInterval = 1 * time.Hour // manual poll in test
	openedCh := make(chan string, 1)
	svc.openBrowserFunc = func(u string) {
		select {
		case openedCh <- u:
		default:
		}
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Start MCP OAuth flow
	startBody, _ := json.Marshal(mcpOAuthStartRequest{
		ProviderID:   "ainode",
		ProviderName: "AINode",
		SiteURL:      server.URL,
		BaseURL:      "https://api.ainode.run/v1",
	})
	startReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/mcp-oauth/start", bytes.NewReader(startBody))
	startRec := httptest.NewRecorder()
	mux.ServeHTTP(startRec, startReq)

	if startRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on start, got %d: %s", startRec.Code, startRec.Body.String())
	}
	var startResp mcpOAuthStartResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("failed to decode start response: %v", err)
	}
	if startResp.State == "" || startResp.AuthURL == "" {
		t.Fatalf("expected non-empty state and authUrl, got %+v", startResp)
	}

	parsedAuthURL, err := url.Parse(startResp.AuthURL)
	if err != nil {
		t.Fatalf("invalid authUrl: %v", err)
	}
	capturedChallenge.Store(parsedAuthURL.Query().Get("code_challenge"))
	redirectURI := parsedAuthURL.Query().Get("redirect_uri")
	capturedRedirectURI.Store(redirectURI)
	if parsedAuthURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("expected S256 challenge method, got %q", parsedAuthURL.Query().Get("code_challenge_method"))
	}
	if redirectURI != server.URL+"/user/mcp-authorize" {
		t.Fatalf("unexpected redirect_uri: %q", redirectURI)
	}
	select {
	case <-openedCh:
	case <-time.After(time.Second):
		t.Fatalf("expected browser launch callback to be invoked")
	}

	// 2. Poll while still pending
	pollReq1 := httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/mcp-oauth/poll?state="+url.QueryEscape(startResp.State), nil)
	pollRec1 := httptest.NewRecorder()
	mux.ServeHTTP(pollRec1, pollReq1)
	if pollRec1.Code != http.StatusOK {
		t.Fatalf("expected 200 on poll 1, got %d", pollRec1.Code)
	}
	var pollResp1 mcpOAuthPollResponse
	_ = json.Unmarshal(pollRec1.Body.Bytes(), &pollResp1)
	if pollResp1.Status != "pending" {
		t.Fatalf("expected status pending before approval, got %q", pollResp1.Status)
	}

	// 3. Approve in browser and poll again
	approved.Store(true)
	pollReq2 := httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/mcp-oauth/poll?state="+url.QueryEscape(startResp.State), nil)
	pollRec2 := httptest.NewRecorder()
	mux.ServeHTTP(pollRec2, pollReq2)
	if pollRec2.Code != http.StatusOK {
		t.Fatalf("expected 200 on poll 2, got %d", pollRec2.Code)
	}
	var pollResp2 mcpOAuthPollResponse
	if err := json.Unmarshal(pollRec2.Body.Bytes(), &pollResp2); err != nil {
		t.Fatalf("failed to decode poll 2 response: %v", err)
	}
	if pollResp2.Status != "authorized" {
		t.Fatalf("expected status authorized after approval, got %q (error=%q)", pollResp2.Status, pollResp2.Error)
	}
	if pollResp2.BaseURL != "https://api.ainode.run/v1" {
		t.Fatalf("expected normalized baseUrl https://api.ainode.run/v1, got %q", pollResp2.BaseURL)
	}
	if pollResp2.APIKey != "sk-ainode-live-99887766" {
		t.Fatalf("expected apiKey sk-ainode-live-99887766, got %q", pollResp2.APIKey)
	}
	if pollResp2.AccessToken != "apay_mcp_test_token_123" {
		t.Fatalf("expected accessToken apay_mcp_test_token_123, got %q", pollResp2.AccessToken)
	}
	if pollResp2.AccountLabel != "dev@ainode.run" {
		t.Fatalf("expected accountLabel dev@ainode.run, got %q", pollResp2.AccountLabel)
	}
	if len(pollResp2.Models) != 2 || pollResp2.Models[0].ID != "claude-sonnet-4-6" {
		t.Fatalf("expected 2 models from public-models, got %+v", pollResp2.Models)
	}
}
