package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestNativeProjectConfigRoutesReportErrorsAndStayLocal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_HOME", filepath.Join(t.TempDir(), "state"))
	g := &Gateway{rootDir: root, providers: provider.NewService(0, "", root, nil)}
	if err := os.WriteFile(filepath.Join(root, "hoxi.json"), []byte(`{"permission":{"bash":"typo"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{g.handleNativeAgentList, g.handleNativeCommandList} {
		r := httptest.NewRecorder()
		handler(r, httptest.NewRequest(http.MethodGet, "/api/hoxi/agents?directory="+url.QueryEscape(root), nil))
		if r.Code != http.StatusBadRequest {
			t.Fatalf("invalid project config: %d %s", r.Code, r.Body.String())
		}
	}
	if tunnelRouteAllowed(http.MethodGet, "/api/hoxi/provider/config") {
		t.Fatal("project config leaked into tunnel")
	}
	if !isTokenProtectedHoxiHostPath("/api/hoxi/provider/config") {
		t.Fatal("project config missing local token protection")
	}
}
