package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/telemetry"
)

func telemetryRequest(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	g := &Gateway{}
	r := httptest.NewRequest(method, "/api/hoxi/telemetry", strings.NewReader(body))
	w := httptest.NewRecorder()
	g.handleTelemetry(w, r)
	return w
}

func TestTelemetryEndpointPersistsPreference(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_DISABLE_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("HOXI_OFFLINE", "")
	telemetry.SetDefaultEnabled(false)

	// Source/dev builds default to enabled=false and offline=false.
	if w := telemetryRequest(t, http.MethodGet, ""); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"enabled":false`) ||
		!strings.Contains(w.Body.String(), `"offline":false`) {
		t.Fatalf("default status: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `null`, `{"enabled":null}`, `{"offline":null}`, `{"enabled":"false"}`, `{"enabled":false} true`} {
		if w := telemetryRequest(t, http.MethodPut, body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", body, w.Code)
		}
	}
	if w := telemetryRequest(t, http.MethodPut, `{"enabled":true}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodPut, `{"offline":true}`); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"offline":true`) ||
		!strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("enable offline: %d %s", w.Code, w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodPut, `{"enabled":false,"offline":false}`); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"enabled":false`) ||
		!strings.Contains(w.Body.String(), `"offline":false`) {
		t.Fatalf("disable both: %d %s", w.Code, w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodPost, `{}`); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", w.Code)
	}
}

func TestTelemetryEndpointRejectsEnvOverrides(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DO_NOT_TRACK", "1")
	t.Setenv("HOXI_OFFLINE", "1")
	w := telemetryRequest(t, http.MethodGet, "")
	if !strings.Contains(w.Body.String(), `"enabled":false`) ||
		!strings.Contains(w.Body.String(), `"envOverride":true`) ||
		!strings.Contains(w.Body.String(), `"offline":true`) ||
		!strings.Contains(w.Body.String(), `"offlineEnvOverride":true`) {
		t.Fatalf("unexpected status: %s", w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodPut, `{"enabled":true}`); w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", w.Code)
	}
	if w := telemetryRequest(t, http.MethodPut, `{"offline":false}`); w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", w.Code)
	}
}
