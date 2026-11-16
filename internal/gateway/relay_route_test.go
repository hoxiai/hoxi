package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayBypassesBrowserOriginGuardButNotHostGuard(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)

	// A desktop AI client sends an Origin the browser allow-list rejects and
	// no gateway token; the relay must still answer with its own 401 because
	// it authenticates hk-* keys itself.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Host = "127.0.0.1:5410"
	req.Header.Set("Origin", "vscode-webview://abc")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("relay should reach its own auth layer, got %d %s", rec.Code, rec.Body.String())
	}

	// DNS rebinding still fails the Host check before any relay work happens.
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Host = "evil.example.com"
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("untrusted Host must be rejected, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "untrusted Host") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestRelayEndToEndThroughGateway(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	_, token, err := g.providers.CreateRelayKey("integration")
	if err != nil {
		t.Fatalf("CreateRelayKey: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Host = "127.0.0.1:5410"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("models through gateway = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"hoxi-auto"`) {
		t.Fatalf("expected the auto model in the list: %s", rec.Body.String())
	}

	// A tunnel-marked request is refused even with a valid key.
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Host = "127.0.0.1:5410"
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	g.serveTunnelRequest(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tunnel relay request must be 403, got %d", rec.Code)
	}
}
