package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTunnelRouteAllowlist(t *testing.T) {
	allowed := []string{
		"/healthz", "/api/hoxi/bootstrap", "/api/hoxi/sessions", "/api/hoxi/sessions/ses_1/message",
		"/api/hoxi/event", "/api/hoxi/permission/per_1", "/api/hoxi/worktree/diff", "/file", "/file/content",
	}
	for _, p := range allowed {
		if !tunnelRouteAllowed(p) {
			t.Errorf("%s should be reachable over the tunnel", p)
		}
	}
	denied := []string{
		"/", "/api/hoxi/tunnel/status", "/api/hoxi/tunnel/pair", "/api/hoxi/auth", "/api/hoxi/models-config",
		"/api/hoxi/provider/keys", "/api/hoxi/browser", "/api/hoxi/clipboard", "/api/hoxi/sessionsx",
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
