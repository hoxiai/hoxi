package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTunnelRouteAllowlist(t *testing.T) {
	allowed := []string{
		"/healthz", "/api/hoxi/bootstrap", "/api/hoxi/sessions", "/api/hoxi/sessions/ses_1/message",
		"/api/hoxi/event", "/api/hoxi/permission/per_1", "/api/hoxi/worktree/diff", "/file", "/file/content",
		"/api/hoxi/provider/models",
	}
	for _, p := range allowed {
		if !tunnelRouteAllowed(p) {
			t.Errorf("%s should be reachable over the tunnel", p)
		}
	}
	denied := []string{
		"/", "/api/hoxi/tunnel/status", "/api/hoxi/tunnel/pair", "/api/hoxi/auth", "/api/hoxi/models-config",
		"/api/hoxi/provider/keys", "/api/hoxi/provider/fetch-models", "/api/hoxi/provider/test-model",
		"/api/hoxi/provider/auto-pools", "/api/hoxi/provider/models/x", "/provider", "/global/config", "/api/hoxi/browser", "/api/hoxi/clipboard", "/api/hoxi/sessionsx",
		"/api/hoxi/sessions/../tunnel/pair", "/session", "/pty/1/connect", "/config", "/auth/openai",
	}
	for _, p := range denied {
		if tunnelRouteAllowed(p) {
			t.Errorf("%s must stay desktop-only", p)
		}
	}
}

func TestTunnelRequestsAreFilteredAndNeverReceiveGatewayToken(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	g.SetGatewayToken("loopback-secret")

	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/tunnel/status", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tunnel management over tunnel = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap over tunnel = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["gatewayToken"]; ok || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("bootstrap must not expose the loopback gateway token over the tunnel")
	}

	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	if !json.Valid(rec.Body.Bytes()) || rec.Header().Get("Set-Cookie") == "" {
		t.Fatal("local bootstrap should still issue the gateway token")
	}
}

func TestTunnelModelListIsSanitized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{"custom":{"name":"Custom","options":{"apiKey":"sk-tunnel-secret","baseURL":"https://secret.example/v1"},"models":{"m1":{"name":"Model One"}}}}}`
	if err := os.WriteFile(filepath.Join(root, ".hoxi", "models.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	g := New(0, "test", root, nil, nil)

	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/models-config", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("models-config over tunnel = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/provider/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("provider/models over tunnel = %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"sk-tunnel-secret", "secret.example", "apiKey", "baseURL", "options"} {
		if strings.Contains(body, secret) {
			t.Fatalf("tunnel model list leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"id":"m1"`) || !strings.Contains(body, `"name":"Model One"`) {
		t.Fatalf("tunnel model list missing configured model: %s", body)
	}
}
