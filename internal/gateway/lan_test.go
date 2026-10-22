package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/pairing"
)

func lanGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	g := New(0, "test", t.TempDir(), nil, nil)
	svc := pairing.NewService(filepath.Join(t.TempDir(), "pairing.json"), 5417)
	g.pairing = svc
	token, err := svc.Token()
	if err != nil {
		t.Fatalf("pairing token: %v", err)
	}
	return g, token
}

func lanRequest(t *testing.T, g *Gateway, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "192.168.1.5:5417"
	// A same-LAN source address; httptest defaults to a public TEST-NET IP,
	// which the listener correctly refuses.
	req.RemoteAddr = "192.168.1.30:40000"
	if token != "" {
		req.Header.Set("X-Auth", token)
	}
	rec := httptest.NewRecorder()
	g.ServeLANRequest(rec, req)
	return rec.Code
}

func TestLANRequestRejectsPublicSource(t *testing.T) {
	g, token := lanGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "192.168.1.5:5417"
	req.RemoteAddr = "8.8.8.8:33333"
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()
	g.ServeLANRequest(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /healthz from a public source with a valid token = %d, want 403", rec.Code)
	}
}

func TestIsLANClient(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:5000":      true,
		"[::1]:5000":          true,
		"[fd00::1]:5000":      true,
		"[fe80::1%en0]:54321": true,  // IPv6 link-local with interface zone ID
		"192.168.1.30:40000":  true,
		"10.0.0.9:1234":       true,
		"172.16.4.4:80":       true,
		"100.101.102.103:80":  true,  // CGNAT / Tailscale-style
		"169.254.1.1:80":      true,  // link-local
		"8.8.8.8:53":          false, // public
		"1.1.1.1:80":          false,
		"[2001:4860:4860::8888]:443": false,
		"":                    false,
		"not-an-address":      false,
	}
	for addr, want := range cases {
		if got := isLANClient(addr); got != want {
			t.Errorf("isLANClient(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestLANRequestRequiresPairingToken(t *testing.T) {
	g, token := lanGateway(t)

	if code := lanRequest(t, g, http.MethodGet, "/healthz", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz without a token = %d, want 401", code)
	}
	if code := lanRequest(t, g, http.MethodGet, "/healthz", "not-the-token"); code != http.StatusUnauthorized {
		t.Fatalf("GET /healthz with a wrong token = %d, want 401", code)
	}
	if code := lanRequest(t, g, http.MethodGet, "/healthz", token); code != http.StatusOK {
		t.Fatalf("GET /healthz with the pairing token = %d, want 200", code)
	}
}

func TestLANRequestEnforcesRemoteAllowList(t *testing.T) {
	g, token := lanGateway(t)

	denied := []struct{ method, path string }{
		{http.MethodGet, "/api/hoxi/tunnel/status"},
		{http.MethodPost, "/api/hoxi/tunnel/pair"},
		{http.MethodPost, "/api/hoxi/tunnel/pair/request"},
		// The LAN pairing token itself must never be readable from another LAN device.
		{http.MethodGet, "/api/hoxi/pairing/qr"},
		{http.MethodPost, "/api/hoxi/pairing/rotate"},
		{http.MethodGet, "/api/hoxi/cloudflared/status"},
		{http.MethodPost, "/api/hoxi/cloudflared/enable"},
		{http.MethodPost, "/api/hoxi/cloudflared/disable"},
		{http.MethodGet, "/api/hoxi/auth"},
		{http.MethodGet, "/api/hoxi/models-config"},
		{http.MethodGet, "/api/hoxi/provider/keys"},
		{http.MethodPost, "/api/hoxi/desktop/notify"},
		{http.MethodGet, "/api/hoxi/request-log"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodGet, "/session"},
		{http.MethodGet, "/pty/1/connect"},
		{http.MethodGet, "/config"},
	}
	for _, r := range denied {
		if code := lanRequest(t, g, r.method, r.path, token); code != http.StatusForbidden {
			t.Errorf("%s %s over LAN = %d, want 403", r.method, r.path, code)
		}
	}
}

func TestLANRequestSanitizesHeadersAndAcceptsExternalOrigin(t *testing.T) {
	g, token := lanGateway(t)

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

	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/workspaces?hoxiToken=leaked&foo=bar", nil)
	req.Host = "192.168.1.5:5417"
	req.RemoteAddr = "[fe80::1%en0]:54321"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Auth", token)
	req.Header.Set("Origin", "wails://wails")
	req.Header.Set("Referer", "https://evil.example/page")
	req.Header.Set("Cookie", "hoxi_gateway_token=secret")
	req.Header.Set("X-Hoxi-Token", "secret")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Forwarded-For", "8.8.8.8")
	req.Header.Set("X-GoPanel-Tunnel-Token", "gp-secret")

	rec := httptest.NewRecorder()
	g.ServeLANRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/workspaces over LAN = %d, want 200", rec.Code)
	}
	if seenWails {
		t.Fatal("spoofed Origin: wails://wails on LAN must not be treated as native Wails request")
	}
	for _, h := range []string{
		"Origin", "Referer", "Cookie", "Authorization", "X-Auth", "X-Hoxi-Token",
		"Sec-Fetch-Site", "X-Forwarded-For", "X-GoPanel-Tunnel-Token",
	} {
		if got := seenHeader.Get(h); got != "" {
			t.Errorf("header %s leaked to downstream handler: %q", h, got)
		}
	}
	if seenQuery != "foo=bar" {
		t.Errorf("RawQuery = %q, want foo=bar (hoxiToken stripped)", seenQuery)
	}
}
