package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

func TestEngineInstallRoute(t *testing.T) {
	gw := New(0, "test-pass", t.TempDir(), nil, nil)

	recMethod := httptest.NewRecorder()
	gw.ServeHTTP(recMethod, localRequest(http.MethodGet, "/api/hoxi/engine/install", nil))
	if recMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", recMethod.Code)
	}

	recUnavailable := httptest.NewRecorder()
	gw.ServeHTTP(recUnavailable, localRequest(http.MethodPost, "/api/hoxi/engine/install", nil))
	if recUnavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without an installer, got %d", recUnavailable.Code)
	}

	calls := 0
	gw.SetEngineInstaller(func(context.Context, bool) (string, error) {
		calls++
		return "/opt/custom/opencode", nil
	})
	recOK := httptest.NewRecorder()
	gw.ServeHTTP(recOK, localRequest(http.MethodPost, "/api/hoxi/engine/install", nil))
	if recOK.Code != http.StatusOK || calls != 1 {
		t.Fatalf("expected installer to run once and return 200, got %d calls=%d", recOK.Code, calls)
	}
	if !strings.Contains(recOK.Body.String(), `"ok":true`) || !strings.Contains(recOK.Body.String(), `"openCodeBinPath":"/opt/custom/opencode"`) {
		t.Fatalf("unexpected response body: %s", recOK.Body.String())
	}

	gw.SetEngineInstaller(func(context.Context, bool) (string, error) { return "", errors.New("download failed") })
	recErr := httptest.NewRecorder()
	gw.ServeHTTP(recErr, localRequest(http.MethodPost, "/api/hoxi/engine/install", nil))
	if recErr.Code != http.StatusInternalServerError || !strings.Contains(recErr.Body.String(), "download failed") {
		t.Fatalf("expected installer error to surface, got %d: %s", recErr.Code, recErr.Body.String())
	}
}

func TestEngineInstallRestartFlag(t *testing.T) {
	gw := New(0, "test-pass", t.TempDir(), nil, nil)
	var gotRestart bool
	gw.SetEngineInstaller(func(_ context.Context, restart bool) (string, error) {
		gotRestart = restart
		return "/opt/custom/opencode", nil
	})

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/engine/install", strings.NewReader(`{"restart":true}`)))
	if rec.Code != http.StatusOK || !gotRestart {
		t.Fatalf("expected restart install, got %d restart=%v: %s", rec.Code, gotRestart, rec.Body.String())
	}

	recQuery := httptest.NewRecorder()
	gw.ServeHTTP(recQuery, localRequest(http.MethodPost, "/api/hoxi/engine/install?restart=1", nil))
	if recQuery.Code != http.StatusOK || !gotRestart {
		t.Fatalf("expected ?restart=1 to force a restart, got %d", recQuery.Code)
	}

	recPlain := httptest.NewRecorder()
	gw.ServeHTTP(recPlain, localRequest(http.MethodPost, "/api/hoxi/engine/install", nil))
	if recPlain.Code != http.StatusOK || gotRestart {
		t.Fatalf("plain install must not restart, got %d restart=%v", recPlain.Code, gotRestart)
	}

	recBad := httptest.NewRecorder()
	gw.ServeHTTP(recBad, localRequest(http.MethodPost, "/api/hoxi/engine/install", strings.NewReader(`{`)))
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed body, got %d", recBad.Code)
	}
}

// A dead engine must not cost a 1.5s probe on every settings refresh.
func TestEngineReadyCachesProbeResult(t *testing.T) {
	gw := New(0, "test-pass", t.TempDir(), nil, nil)
	gw.openCode = ocapi.New("http://127.0.0.1:1", "unreachable")

	gw.engineReadyMu.Lock()
	gw.engineReadyOK = true
	gw.engineReadyAt = time.Now()
	gw.engineReadyMu.Unlock()
	if !gw.engineReady() {
		t.Fatal("expected the cached ready value")
	}

	gw.invalidateEngineReadyCache()
	if gw.engineReady() {
		t.Fatal("expected the uncached probe of an unreachable engine to fail")
	}
	gw.engineReadyMu.Lock()
	cached := !gw.engineReadyAt.IsZero()
	gw.engineReadyMu.Unlock()
	if !cached {
		t.Fatal("expected the failed probe to be cached too")
	}
}

func TestOpenCodeBinarySource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	if got := openCodeBinarySource(filepath.Join(home, "bin", "opencode")); got != "downloaded" {
		t.Fatalf("managed binary source = %q, want downloaded", got)
	}
	if got := openCodeBinarySource("/opt/homebrew/bin/opencode"); got != "external" {
		t.Fatalf("PATH binary source = %q, want external", got)
	}
	if got := openCodeBinarySource(""); got != "" {
		t.Fatalf("empty path source = %q, want empty", got)
	}
}
