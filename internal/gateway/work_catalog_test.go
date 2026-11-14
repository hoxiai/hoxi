package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/workapps"
	"github.com/hoxiai/hoxi/internal/workcatalog"
)

func TestWorkCatalogDesktopUIQueriesWithoutChatAndRejectsUntrustedCallers(t *testing.T) {
	work := workapps.NewService(nil, workapps.NewStore(filepath.Join(t.TempDir(), "work-apps.json")))
	if err := work.SaveState(map[string]any{"storesByApp": map[string]any{"1688": []any{
		map[string]any{"id": "store-a", "platform": "1688"}, map[string]any{"id": "store-b", "platform": "1688"},
	}}}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{workApps: work, workCatalog: workcatalog.NewService(workcatalog.NewStore(t.TempDir())), mux: http.NewServeMux()}
	t.Cleanup(g.workCatalog.Close)
	g.SetGatewayToken("catalog-local-token")
	g.uiOrigins = map[string]bool{
		"http://localhost:5421":        true,
		"wails://wails.localhost:34115": true,
		"http://wails.localhost:34115":  true,
	}
	g.registerWorkMCPRoutes()
	request := func(method, action, store, origin, token, ui, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1/api/hoxi/work/catalog/"+action+"?storeId="+store, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if ui != "" {
			r.Header.Set("X-Hoxi-Work-UI", ui)
		}
		w := httptest.NewRecorder()
		g.mux.ServeHTTP(w, r)
		return w
	}
	for _, input := range []struct {
		store, origin, token, ui string
		want                     int
	}{
		{"store-a", "http://localhost:5421", "catalog-local-token", "true", 200},
		{"store-a", "https://evil.example", "catalog-local-token", "true", 403},
		{"store-a", "", "catalog-local-token", "true", 403},
		{"store-a", "http://localhost:5421", "wrong", "true", 403},
		{"store-a", "http://localhost:5421", "catalog-local-token", "", 403},
		{"unknown-store", "http://localhost:5421", "catalog-local-token", "true", 403},
	} {
		w := request("GET", "status", input.store, input.origin, input.token, input.ui, "")
		if w.Code != input.want {
			t.Fatal(input, w.Code, w.Body.String())
		}
	}
	credentials := `{"sellerLoginId":"factory-a","appKey":"123456","appSecret":"hidden-app-secret","accessToken":"hidden-access-token"}`
	// Same-origin GETs through the Vite proxy may omit Origin and change Host.
	// Their registered UI Referer plus fetch metadata must still be recognized.
	proxied := httptest.NewRequest("GET", "http://127.0.0.1:5420/api/hoxi/work/catalog/status?storeId=store-a", nil)
	proxied.Header.Set("Authorization", "Bearer catalog-local-token")
	proxied.Header.Set("X-Hoxi-Work-UI", "true")
	proxied.Header.Set("Sec-Fetch-Site", "same-origin")
	proxied.Header.Set("Referer", "http://localhost:5421/work")
	proxiedResponse := httptest.NewRecorder()
	g.mux.ServeHTTP(proxiedResponse, proxied)
	if proxiedResponse.Code != 200 {
		t.Fatal("trusted proxied UI GET was denied", proxiedResponse.Code)
	}
	proxied.Header.Set("Referer", "https://evil.example/")
	proxiedResponse = httptest.NewRecorder()
	g.mux.ServeHTTP(proxiedResponse, proxied)
	if proxiedResponse.Code != 403 {
		t.Fatal("untrusted Referer was accepted")
	}

	// Native Wails release/dev contexts omit Origin and Sec-Fetch-Site on GETs;
	// unconfigured ports, cross-site metadata, and forged Referers must still fail closed.
	for _, tc := range []struct {
		name    string
		host    string
		site    string
		referer string
		want    int
	}{
		{"wails release host", "wails", "", "", 200},
		{"wails dev macos port", "wails.localhost:34115", "", "wails://wails.localhost:34115/", 200},
		{"wails dev proxied referer", "127.0.0.1:5420", "", "wails://wails.localhost:34115/", 200},
		{"wails dev windows http", "wails.localhost:34115", "same-origin", "http://wails.localhost:34115/", 200},
		{"unconfigured wails port", "wails.localhost:9999", "", "wails://wails.localhost:9999/", 403},
		{"wails cross-site fetch", "wails.localhost:34115", "cross-site", "wails://wails.localhost:34115/", 403},
		{"wails same-site fetch", "wails.localhost:34115", "same-site", "wails://wails.localhost:34115/", 403},
		{"forged referer userinfo", "127.0.0.1:5420", "", "wails://user@wails.localhost:34115/", 403},
		{"bare loopback without context", "127.0.0.1:5420", "", "", 403},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+"/api/hoxi/work/catalog/status?storeId=store-a", nil)
		r.Host = tc.host
		r.Header.Set("Authorization", "Bearer catalog-local-token")
		r.Header.Set("X-Hoxi-Work-UI", "true")
		if tc.site != "" {
			r.Header.Set("Sec-Fetch-Site", tc.site)
		}
		if tc.referer != "" {
			r.Header.Set("Referer", tc.referer)
		}
		rec := httptest.NewRecorder()
		g.mux.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Fatalf("%s: expected %d, got %d (%s)", tc.name, tc.want, rec.Code, rec.Body.String())
		}
	}
	w := request("PUT", "authorization", "store-a", "http://localhost:5421", "catalog-local-token", "true", credentials)
	if w.Code != 200 || strings.Contains(w.Body.String(), "hidden-") {
		t.Fatal("authorization save failed or exposed secrets", w.Code, w.Body.String())
	}
	w = request("GET", "status", "store-a", "http://localhost:5421", "catalog-local-token", "true", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":true`) || strings.Contains(w.Body.String(), "hidden-") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "status", "store-b", "http://localhost:5421", "catalog-local-token", "true", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) || strings.Contains(w.Body.String(), "factory-a") {
		t.Fatal("credentials crossed the store boundary", w.Code, w.Body.String())
	}
	w = request("POST", "publish", "store-a", "http://localhost:5421", "catalog-local-token", "true", `{"draftId":"drf_missing","revision":"v1"}`)
	if w.Code == 200 {
		t.Fatal("publication bypassed managed-session approval")
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/hoxi/work/catalog/authorization?storeId=store-a", strings.NewReader(credentials))
		r = r.WithContext(context.WithValue(r.Context(), remoteSourceKey{}, remoteSourceRelay))
		w := httptest.NewRecorder()
		g.mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatal("remote controller reached local catalog", method, w.Code)
		}
		w = httptest.NewRecorder()
		g.serveRemoteRequest(w, httptest.NewRequest(method, "/api/hoxi/work/catalog/status", nil), remoteSourceRelay)
		if w.Code != http.StatusForbidden {
			t.Fatal("remote allowlist admitted catalog", method, w.Code)
		}
	}
}
