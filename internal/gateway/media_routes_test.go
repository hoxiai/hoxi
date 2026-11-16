package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestMediaRoutesRejectRemoteTunnelAndProtectedPaths(t *testing.T) {
	wsDir := t.TempDir()
	t.Setenv("HOME", wsDir)
	t.Setenv("HOXI_HOME", filepath.Join(wsDir, ".hoxi-home"))

	g := &Gateway{
		rootDir:   wsDir,
		mux:       http.NewServeMux(),
		providers: provider.NewService(0, "", wsDir, nil),
	}
	g.registerMediaRoutes()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/hoxi/media/capabilities"},
		{http.MethodPost, "/api/hoxi/media/image"},
		{http.MethodPost, "/api/hoxi/media/transcribe"},
	} {
		// Blocked at tunnel allow-list layer
		recTunnel := httptest.NewRecorder()
		g.serveRemoteRequest(recTunnel, httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`))), remoteSourceRelay)
		if recTunnel.Code != http.StatusForbidden {
			t.Fatalf("expected tunnel allow-list HTTP 403 for %s, got %d", tc.path, recTunnel.Code)
		}

		// Defense-in-depth inside handler via isRemoteRequest(r)
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`)))
		req = req.WithContext(context.WithValue(req.Context(), remoteSourceKey{}, remoteSourceRelay))
		recHandler := httptest.NewRecorder()
		g.mux.ServeHTTP(recHandler, req)
		if recHandler.Code != http.StatusForbidden {
			t.Fatalf("expected handler HTTP 403 for remote context on %s, got %d", tc.path, recHandler.Code)
		}
	}

	capRec := httptest.NewRecorder()
	g.mux.ServeHTTP(capRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/media/capabilities", nil))
	if capRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 on local capabilities, got %d", capRec.Code)
	}
	var capBody map[string]any
	_ = json.Unmarshal(capRec.Body.Bytes(), &capBody)
	if capBody["ok"] != true {
		t.Fatalf("expected ok=true in capabilities response, got %+v", capBody)
	}

	for _, badOut := range []string{"../escape.png", ".env", ".git/hooks/pre-commit", ".opencode/plugin.js"} {
		raw, _ := json.Marshal(map[string]any{
			"prompt":     "icon",
			"directory":  wsDir,
			"outputPath": badOut,
		})
		rec := httptest.NewRecorder()
		g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/media/image", bytes.NewReader(raw)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected HTTP 403 for unsafe outputPath %q, got %d (%s)", badOut, rec.Code, rec.Body.String())
		}
	}
}
