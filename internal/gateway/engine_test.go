package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	gw.SetEngineInstaller(func(context.Context) error {
		calls++
		return nil
	})
	recOK := httptest.NewRecorder()
	gw.ServeHTTP(recOK, localRequest(http.MethodPost, "/api/hoxi/engine/install", nil))
	if recOK.Code != http.StatusOK || calls != 1 {
		t.Fatalf("expected installer to run once and return 200, got %d calls=%d", recOK.Code, calls)
	}
	if !strings.Contains(recOK.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected response body: %s", recOK.Body.String())
	}

	gw.SetEngineInstaller(func(context.Context) error { return errors.New("download failed") })
	recErr := httptest.NewRecorder()
	gw.ServeHTTP(recErr, localRequest(http.MethodPost, "/api/hoxi/engine/install", nil))
	if recErr.Code != http.StatusInternalServerError || !strings.Contains(recErr.Body.String(), "download failed") {
		t.Fatalf("expected installer error to surface, got %d: %s", recErr.Code, recErr.Body.String())
	}
}
