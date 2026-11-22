package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopWindowCloseRoute(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)

	// Unattached desktop window returns 503.
	res := serve(gw, http.MethodPost, "/api/hoxi/desktop/window/close", "")
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unattached close status = %d, want 503", res.Code)
	}

	// Non-POST is rejected.
	res = serve(gw, http.MethodGet, "/api/hoxi/desktop/window/close", "")
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET close status = %d, want 405", res.Code)
	}

	// Attached local desktop window invokes close callback.
	closed := 0
	gw.SetDesktopWindowCloseHandler(func() { closed++ })
	res = serve(gw, http.MethodPost, "/api/hoxi/desktop/window/close", "")
	if res.Code != http.StatusOK {
		t.Fatalf("POST close status = %d, want 200 (body=%s)", res.Code, res.Body.String())
	}
	if closed != 1 {
		t.Fatalf("close callback count = %d, want 1", closed)
	}

	// Remote requests and remote tunnel allowlist must reject the route.
	if tunnelRouteAllowed(http.MethodPost, "/api/hoxi/desktop/window/close") {
		t.Fatal("POST /api/hoxi/desktop/window/close must never be allowed over remote tunnel")
	}
	remoteRec := httptest.NewRecorder()
	gw.serveTunnelRequest(remoteRec, localRequest(http.MethodPost, "/api/hoxi/desktop/window/close", nil))
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote tunnel close status = %d, want 403", remoteRec.Code)
	}
	if closed != 1 {
		t.Fatalf("remote tunnel request must not invoke close callback, count = %d", closed)
	}
}
