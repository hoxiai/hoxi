package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type mockAuthSource struct {
	mu      sync.Mutex
	baseURL string
	token   string
}

func (m *mockAuthSource) ServerBaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseURL
}

func (m *mockAuthSource) CurrentDeviceToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token
}

func (m *mockAuthSource) InvalidateDeviceToken(token, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == token {
		m.token = ""
	}
}

func TestToolsGatewayBridgeAndExpiredTokenFallback(t *testing.T) {
	authSrc := &mockAuthSource{token: "expired_token"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer expired_token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"token expired"}`))
			return
		}
		switch r.URL.Path {
		case "/api/hoxi/tools/discover":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"totalToolsMatched": 2},
			})
		case "/api/hoxi/tools/inspect":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"provider": "apify", "endpoint": "/apidojo/tweet-scraper"},
			})
		case "/api/hoxi/tools/run":
			if authHeader != "Bearer valid_token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"ok": true, "status": "COMPLETED"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	authSrc.baseURL = srv.URL

	svc := NewService(authSrc)
	svc.httpClient = srv.Client()

	// Discover with an expired token must invalidate it and succeed via anonymous retry.
	discRes, err := svc.Discover(context.Background(), []byte(`{"query":"twitter"}`))
	if err != nil || discRes["code"] != float64(0) {
		t.Fatalf("Discover failed on expired token: %v, %#v", err, discRes)
	}
	if got := authSrc.CurrentDeviceToken(); got != "" {
		t.Fatalf("expected expired token to be invalidated, got %q", got)
	}

	// Inspect with an expired token must also invalidate and succeed via anonymous retry.
	authSrc.token = "expired_token"
	inspRes, err := svc.Inspect(context.Background(), []byte(`{"provider":"apify"}`))
	if err != nil || inspRes["code"] != float64(0) {
		t.Fatalf("Inspect failed on expired token: %v, %#v", err, inspRes)
	}
	if got := authSrc.CurrentDeviceToken(); got != "" {
		t.Fatalf("expected expired token to be invalidated, got %q", got)
	}

	// Run while logged out must fail with ErrNotLoggedIn.
	if _, err := svc.Run(context.Background(), []byte(`{"provider":"apify","endpoint":"/apidojo/tweet-scraper"}`)); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("expected ErrNotLoggedIn when logged out, got %v", err)
	}

	// Run with a valid token succeeds.
	authSrc.token = "valid_token"
	runRes, err := svc.Run(context.Background(), []byte(`{"provider":"apify","endpoint":"/apidojo/tweet-scraper"}`))
	if err != nil || runRes["code"] != float64(0) {
		t.Fatalf("Run failed: %v, %#v", err, runRes)
	}

	h := NewHTTPHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/tools/discover", strings.NewReader(`{"query":"twitter"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/hoxi/tools/discover, got %d (%s)", rec.Code, rec.Body.String())
	}
}
