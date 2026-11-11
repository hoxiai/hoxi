package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/ecom"
	"github.com/hoxiai/hoxi/internal/workapps"
)

func TestWorkRoutesDesktopOnlyAndStoreDefaultsWiring(t *testing.T) {
	wsDir := t.TempDir()
	storePath := filepath.Join(wsDir, "work-apps.json")
	workSvc := workapps.NewService(nil, workapps.NewStore(storePath))

	if err := workSvc.SaveState(map[string]any{
		"storesByApp": map[string]any{
			"amazon": []any{
				map[string]any{
					"id":                 "amz-us-1",
					"name":               "AnkerHome US",
					"platform":           "amazon",
					"marketplace":        "US",
					"currency":           "USD",
					"proxyURL":           "socks5://seller:SuperSecret123@198.51.100.77:1080",
					"proxyRequired":      true,
					"referralFeePct":     15.0,
					"targetNetMarginPct": 20.0,
					"fxRate":             7.2,
				},
			},
		},
	}); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	g := &Gateway{
		rootDir:  wsDir,
		mux:      http.NewServeMux(),
		workApps: workSvc,
	}
	g.registerWorkRoutes()

	// 1. All /api/hoxi/work/* endpoints must be blocked (403) on both remote planes.
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/hoxi/work/apps"},
		{http.MethodPut, "/api/hoxi/work/apps"},
		{http.MethodGet, "/api/hoxi/work/stores"},
		{http.MethodPost, "/api/hoxi/work/proxy-check"},
		{http.MethodPost, "/api/hoxi/work/ecom/profit"},
		{http.MethodPost, "/api/hoxi/work/ecom/ads-audit"},
		{http.MethodPost, "/api/hoxi/work/ecom/listing-check"},
		{http.MethodPost, "/api/hoxi/work/ecom/shopify-csv"},
	} {
		recTunnel := httptest.NewRecorder()
		g.serveRemoteRequest(recTunnel, httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`))), remoteSourceRelay)
		if recTunnel.Code != http.StatusForbidden {
			t.Fatalf("expected tunnel allow-list HTTP 403 for %s %s, got %d", tc.method, tc.path, recTunnel.Code)
		}

		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`)))
		req = req.WithContext(context.WithValue(req.Context(), remoteSourceKey{}, remoteSourceRelay))
		recHandler := httptest.NewRecorder()
		g.mux.ServeHTTP(recHandler, req)
		if recHandler.Code != http.StatusForbidden {
			t.Fatalf("expected handler HTTP 403 for remote context on %s %s, got %d", tc.method, tc.path, recHandler.Code)
		}
	}

	// 2. Local GET /api/hoxi/work/stores returns scrubbed proxy URL without raw credentials.
	storesRec := httptest.NewRecorder()
	g.mux.ServeHTTP(storesRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/work/stores", nil))
	if storesRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 on local /api/hoxi/work/stores, got %d (%s)", storesRec.Code, storesRec.Body.String())
	}
	if strings.Contains(storesRec.Body.String(), "SuperSecret123") || !strings.Contains(storesRec.Body.String(), "socks5://***@198.51.100.77:1080") {
		t.Fatalf("unexpected store redaction in /api/hoxi/work/stores: %s", storesRec.Body.String())
	}

	// 3. Local POST /api/hoxi/work/ecom/profit inherits store defaults via workStoreLookup.
	profitReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/work/ecom/profit", strings.NewReader(`{"storeId":"amz-us-1","price":40,"productCostCNY":36,"fbaFee":5.5}`))
	profitReq.Header.Set("Content-Type", "application/json")
	profitRec := httptest.NewRecorder()
	g.mux.ServeHTTP(profitRec, profitReq)
	if profitRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 on local /api/hoxi/work/ecom/profit, got %d (%s)", profitRec.Code, profitRec.Body.String())
	}
	var profitRes ecom.ProfitBreakdown
	if err := json.Unmarshal(profitRec.Body.Bytes(), &profitRes); err != nil {
		t.Fatalf("decode profit result: %v", err)
	}
	if profitRes.Platform != "amazon" || profitRes.Currency != "USD" || profitRes.SourcingUnitCost != 5.0 {
		t.Fatalf("expected store defaults (amazon/USD/FX 7.2 -> cost 5.0) inherited, got %#v", profitRes)
	}
}
