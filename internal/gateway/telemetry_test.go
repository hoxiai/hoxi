package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	if w := telemetryRequest(t, http.MethodGet, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("default status: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":false} true`} {
		if w := telemetryRequest(t, http.MethodPut, body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", body, w.Code)
		}
	}
	if w := telemetryRequest(t, http.MethodPut, `{"enabled":false}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodGet, ""); !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("preference not persisted: %s", w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodPost, `{}`); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", w.Code)
	}
}

func TestTelemetryEndpointRejectsEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DO_NOT_TRACK", "1")
	w := telemetryRequest(t, http.MethodGet, "")
	if !strings.Contains(w.Body.String(), `"enabled":false`) || !strings.Contains(w.Body.String(), `"envOverride":true`) {
		t.Fatalf("unexpected status: %s", w.Body.String())
	}
	if w := telemetryRequest(t, http.MethodPut, `{"enabled":true}`); w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", w.Code)
	}
}
