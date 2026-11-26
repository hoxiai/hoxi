package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayCORSExposesPreviewFrameHeaders(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "127.0.0.1:5410"
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Origin", "wails://wails")
	if tok := gw.GatewayToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	exposed := rec.Header().Get("Access-Control-Expose-Headers")
	for _, want := range []string{
		"X-Preview-Path",
		"X-Preview-Title",
		"X-Preview-Width",
		"X-Preview-Height",
		"X-Preview-Blocked-Nav",
	} {
		if !strings.Contains(exposed, want) {
			t.Errorf("Access-Control-Expose-Headers %q missing %q", exposed, want)
		}
	}
}
