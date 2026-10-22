package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func ingressRequest(t *testing.T, g *Gateway, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "abc123.node.hoxi.work"
	// The forwarder connects over loopback, but a public source address is
	// irrelevant here: the ingress has no source-address filter, unlike the LAN
	// listener. This asserts that difference.
	req.RemoteAddr = "203.0.113.9:51000"
	if token != "" {
		req.Header.Set("X-Auth", token)
	}
	rec := httptest.NewRecorder()
	g.ServeIngressRequest(rec, req)
	return rec.Code
}

func TestIngressRequestRequiresPairingToken(t *testing.T) {
	g, token := lanGateway(t)

	if code := ingressRequest(t, g, http.MethodGet, "/healthz", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz without a token = %d, want 401", code)
	}
	if code := ingressRequest(t, g, http.MethodGet, "/healthz", "not-the-token"); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz with a wrong token = %d, want 401", code)
	}
	// A valid token from a public source address still works: the forwarder is
	// the one exposing this gateway, and it dials in from loopback.
	if code := ingressRequest(t, g, http.MethodGet, "/healthz", token); code != http.StatusOK {
		t.Fatalf("GET /healthz with the pairing token = %d, want 200", code)
	}
}

func TestIngressRequestEnforcesRemoteAllowList(t *testing.T) {
	g, token := lanGateway(t)

	denied := []struct{ method, path string }{
		{http.MethodGet, "/api/hoxi/tunnel/status"},
		{http.MethodPost, "/api/hoxi/tunnel/pair"},
		{http.MethodGet, "/api/hoxi/pairing/qr"},
		{http.MethodPost, "/api/hoxi/pairing/rotate"},
		{http.MethodGet, "/api/hoxi/cloudflared/status"},
		{http.MethodPost, "/api/hoxi/cloudflared/enable"},
		{http.MethodGet, "/api/hoxi/auth"},
		{http.MethodGet, "/api/hoxi/models-config"},
		{http.MethodGet, "/api/hoxi/provider/keys"},
		{http.MethodPost, "/api/hoxi/desktop/notify"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodGet, "/session"},
	}
	for _, r := range denied {
		if code := ingressRequest(t, g, r.method, r.path, token); code != http.StatusForbidden {
			t.Errorf("%s %s over ingress = %d, want 403", r.method, r.path, code)
		}
	}
}
