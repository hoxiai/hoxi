package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/workapps"
	"github.com/hoxiai/hoxi/internal/workcatalog"
)

func TestWorkMCPScopeAuthenticationStoreBindingAndRemoteDenial(t *testing.T) {
	dir := t.TempDir()
	svc := runner.NewService(dir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.Store().Put(runner.SessionRecord{ID: "ses_work", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir}); err != nil {
		t.Fatal(err)
	}
	work := workapps.NewService(nil, workapps.NewStore(filepath.Join(t.TempDir(), "work-apps.json")))
	if err := work.SaveState(map[string]any{
		"storesByApp":          map[string]any{"1688": []any{map[string]any{"id": "store-a", "platform": "1688", "alibaba1688SellerLoginId": "factory-a", "alibaba1688CredentialEnv": "FACTORY_A"}}},
		"sessionStoreBindings": map[string]any{"ses_work": map[string]any{"appId": "1688", "storeId": "store-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{runner: svc, workApps: work, workCatalog: workcatalog.NewService(workcatalog.NewStore(t.TempDir())), mux: http.NewServeMux()}
	g.SetGatewayToken("local-secret")
	g.registerWorkMCPRoutes()
	for _, tc := range []struct {
		token, session, store, directory string
		allowed                          bool
	}{
		{"local-secret", "ses_work", "store-a", dir, true},
		{"", "ses_work", "store-a", dir, false},
		{"wrong", "ses_work", "store-a", dir, false},
		{"local-secret", "ses_work", "store-b", dir, false},
		{"local-secret", "ses_work", "store-a", t.TempDir(), false},
		{"local-secret", "ses_unknown", "store-a", dir, false},
	} {
		query := url.Values{"sessionID": {tc.session}, "directory": {tc.directory}, "storeId": {tc.store}}
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/hoxi/work/mcp?"+query.Encode(), nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		conn, err := g.resolveWorkMCP(r)
		if (err == nil) != tc.allowed || err == nil && conn.Scope.StoreID != "store-a" {
			t.Fatal(tc, conn.Scope, err)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/hoxi/work/mcp", nil)
		r = r.WithContext(context.WithValue(r.Context(), remoteSourceKey{}, remoteSourceRelay))
		w := httptest.NewRecorder()
		g.mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatal("remote request reached MCP", method, w.Code)
		}
		w = httptest.NewRecorder()
		g.serveRemoteRequest(w, httptest.NewRequest(method, "/api/hoxi/work/mcp", nil), remoteSourceRelay)
		if w.Code != http.StatusForbidden {
			t.Fatal("remote allowlist admitted MCP", method, w.Code)
		}
	}
}
