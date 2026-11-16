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

func ingressGateway(t *testing.T) (*Gateway, string, string) {
	t.Helper()
	g, lanToken := lanGateway(t)
	relayToken, err := g.pairing.RelayToken()
	if err != nil {
		t.Fatalf("relay pairing token: %v", err)
	}
	return g, lanToken, relayToken
}

func TestIngressRequestRequiresPairingToken(t *testing.T) {
	g, lanToken, relayToken := ingressGateway(t)

	if code := ingressRequest(t, g, http.MethodGet, "/healthz", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz without a token = %d, want 401", code)
	}
	if code := ingressRequest(t, g, http.MethodGet, "/healthz", "not-the-token"); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz with a wrong token = %d, want 401", code)
	}
	// A leaked LAN token must NOT authorize requests on the public relay ingress.
	if code := ingressRequest(t, g, http.MethodGet, "/healthz", lanToken); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz over ingress with the LAN token = %d, want 401", code)
	}
	// Conversely, the relay token must NOT authorize requests on the LAN listener.
	if code := lanRequest(t, g, http.MethodGet, "/healthz", relayToken); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz over LAN with the relay token = %d, want 401", code)
	}
	// A valid relay token from a public source address works on the ingress.
	if code := ingressRequest(t, g, http.MethodGet, "/healthz", relayToken); code != http.StatusOK {
		t.Fatalf("GET /healthz with the relay token = %d, want 200", code)
	}
}

func TestIngressRequestEnforcesRemoteAllowList(t *testing.T) {
	g, _, relayToken := ingressGateway(t)

	denied := []struct{ method, path string }{
		{http.MethodGet, "/api/hoxi/tunnel/status"},
		{http.MethodPost, "/api/hoxi/tunnel/pair"},
		{http.MethodPost, "/api/hoxi/tunnel/pair/request"},
		{http.MethodGet, "/api/hoxi/pairing/qr"},
		{http.MethodPost, "/api/hoxi/pairing/rotate"},
		{http.MethodGet, "/api/hoxi/cloudflared/status"},
		{http.MethodPost, "/api/hoxi/cloudflared/enable"},
		{http.MethodPost, "/api/hoxi/cloudflared/disable"},
		{http.MethodGet, "/api/hoxi/auth"},
		{http.MethodGet, "/api/hoxi/models-config"},
		{http.MethodGet, "/api/hoxi/provider/keys"},
		{http.MethodPost, "/api/hoxi/desktop/notify"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodGet, "/session"},
	}
	for _, r := range denied {
		if code := ingressRequest(t, g, r.method, r.path, relayToken); code != http.StatusForbidden {
			t.Errorf("%s %s over ingress = %d, want 403", r.method, r.path, code)
		}
	}
}

func TestIngressRequestSanitizesHeadersAndAcceptsExternalOrigin(t *testing.T) {
	g, _, relayToken := ingressGateway(t)

	var seenHeader http.Header
	var seenQuery string
	var seenWails bool
	g.mux = http.NewServeMux()
	g.mux.HandleFunc("/api/hoxi/workspaces", func(w http.ResponseWriter, r *http.Request) {
		seenHeader = r.Header.Clone()
		seenQuery = r.URL.RawQuery
		seenWails = nativeWailsRequest(r)
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/workspaces?hoxiToken=leaked&dir=x", nil)
	req.Host = "abc123.node.hoxi.work"
	req.RemoteAddr = "127.0.0.1:51000"
	req.Header.Set("X-Auth", relayToken)
	req.Header.Set("Authorization", "Bearer "+relayToken)
	req.Header.Set("Origin", "https://abc123.node.hoxi.work")
	req.Header.Set("Referer", "https://abc123.node.hoxi.work/app")
	req.Header.Set("Cookie", "CF_Authorization=jwt; hoxi_gateway_token=secret")
	req.Header.Set("X-Hoxi-Token", "secret")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-GoPanel-Tunnel-Client", "cid")

	rec := httptest.NewRecorder()
	g.ServeIngressRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/workspaces over ingress with external Origin = %d, want 200", rec.Code)
	}
	if seenWails {
		t.Fatal("ingress request must not be treated as native Wails request")
	}
	for _, h := range []string{
		"Origin", "Referer", "Cookie", "Authorization", "X-Auth", "X-Hoxi-Token",
		"Sec-Fetch-Site", "X-Forwarded-Proto", "X-GoPanel-Tunnel-Client",
	} {
		if got := seenHeader.Get(h); got != "" {
			t.Errorf("header %s leaked to downstream handler: %q", h, got)
		}
	}
	if seenQuery != "dir=x" {
		t.Errorf("RawQuery = %q, want dir=x (hoxiToken stripped)", seenQuery)
	}
}
