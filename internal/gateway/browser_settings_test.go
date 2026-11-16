package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/browser"
)

func TestBrowserSettingsRoutePersistsAndSyncsSystemInfo(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")

	g := New(0, "test", t.TempDir(), nil, nil)
	defer g.CloseBrowser()

	getSettings := func(gw *Gateway) browser.Settings {
		t.Helper()
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/browser/settings", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/hoxi/browser/settings = %d: %s", rec.Code, rec.Body.String())
		}
		var cfg browser.Settings
		if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	putSettings := func(gw *Gateway, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, localRequest(http.MethodPut, "/api/hoxi/browser/settings", strings.NewReader(body)))
		return rec
	}

	// Default must be 30 minutes.
	if cfg := getSettings(g); cfg.IdleMinutes != 30 {
		t.Fatalf("default browser settings = %+v, want 30", cfg)
	}

	// Update to 15 minutes and verify system-info & bootstrap reflect it.
	if rec := putSettings(g, `{"idleMinutes":15}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT 15 = %d: %s", rec.Code, rec.Body.String())
	}
	if got := g.browser.IdleMinutes(); got != 15 {
		t.Fatalf("g.browser.IdleMinutes() = %d, want 15", got)
	}

	sysRec := httptest.NewRecorder()
	g.ServeHTTP(sysRec, localRequest(http.MethodGet, "/api/hoxi/system-info", nil))
	if sysRec.Code != http.StatusOK {
		t.Fatalf("GET system-info = %d: %s", sysRec.Code, sysRec.Body.String())
	}
	var sysPayload map[string]any
	if err := json.Unmarshal(sysRec.Body.Bytes(), &sysPayload); err != nil {
		t.Fatal(err)
	}
	if sysPayload["browserIdleMinutes"] != float64(15) {
		t.Fatalf("system-info browserIdleMinutes = %v, want 15", sysPayload["browserIdleMinutes"])
	}

	// Restart a fresh Gateway over the same HOXI_HOME and confirm persistence.
	g2 := New(0, "test", t.TempDir(), nil, nil)
	defer g2.CloseBrowser()
	if cfg := getSettings(g2); cfg.IdleMinutes != 15 {
		t.Fatalf("reloaded browser settings = %+v, want 15", cfg)
	}

	// Support 0 (never auto-close).
	if rec := putSettings(g2, `{"idleMinutes":0}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT 0 = %d: %s", rec.Code, rec.Body.String())
	}
	if cfg := getSettings(g2); cfg.IdleMinutes != 0 {
		t.Fatalf("expected 0, got %+v", cfg)
	}

	// Reject malformed / out-of-range payloads.
	for _, bad := range []string{`{}`, `{"idleMinutes":-5}`, `{"idleMinutes":1441}`, `{"idleMinutes":"30"}`, `{"idleMinutes":30,"extra":1}`} {
		if rec := putSettings(g2, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d, want 400: %s", bad, rec.Code, rec.Body.String())
		}
	}

	// Remote tunnel callers must be rejected with 403.
	remoteRec := httptest.NewRecorder()
	g2.serveTunnelRequest(remoteRec, localRequest(http.MethodPut, "/api/hoxi/browser/settings", strings.NewReader(`{"idleMinutes":30}`)))
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("tunnel PUT /api/hoxi/browser/settings = %d, want 403", remoteRec.Code)
	}
}
