package leaderboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModelPricingServiceFallback(t *testing.T) {
	svc := NewService("http://invalid-url-for-test.local", nil)

	pricing, err := svc.GetModelPricing(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pricing.Code != 200 || pricing.Data == nil || len(pricing.Data.Items) == 0 {
		t.Fatalf("expected fallback pricing items, got: %+v", pricing)
	}

	if _, err := svc.GetConnectableGateways(); err == nil {
		t.Fatal("expected gateway directory to fail without a live server instead of serving a local fallback")
	}
}

func TestConnectableGatewaysAlwaysHitsUpstream(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		json.NewEncoder(w).Encode(GatewaysResponse{Code: 200, Data: &GatewaysData{
			Total: 1,
			Items: []GatewayItem{{ID: "hoxi-gateway", Name: "Hoxi Gateway", BaseURL: "https://api.hoxi.ai/v1"}},
		}})
	}))
	defer server.Close()

	svc := NewService(server.URL, server.Client())
	for i := 1; i <= 3; i++ {
		if _, err := svc.GetConnectableGateways(); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if hits != i {
			t.Fatalf("call %d: expected %d upstream hits (no caching), got %d", i, i, hits)
		}
	}
}

func TestGatewayDirectoryDoesNotOfferHoxiInferenceAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GatewaysResponse{Code: 200, Data: &GatewaysData{
			Total: 2, Items: []GatewayItem{
				{ID: "third-party", Name: "Third Party",
					URL: "https://hoxi.ai", BaseURL: "https://hoxi.ai/v1",
					IsSelfOperated: true, CanOneClickConnect: true,
					Badge: "Official Pick", Caveat: "Hoxi account connects inference",
					AuthURL: "https://hoxi.ai/auth/authorize-desktop"},
				{ID: "hoxi-gateway", Name: "Hoxi Gateway",
					Domain: "hoxi.ai", URL: "https://api.hoxi.ai", BaseURL: "https://api.hoxi.ai/v1",
					IsSelfOperated: true, CanOneClickConnect: true,
					Badge: "Official Pick", Caveat: "Enterprise dedicated line",
					AuthURL: "https://hoxi.ai/auth/authorize-desktop?gateway=hoxi-gateway"},
			},
		}})
	}))
	defer server.Close()
	svc := NewService(server.URL, server.Client())
	for i := 0; i < 2; i++ {
		result, err := svc.GetConnectableGateways()
		if err != nil || result.Data == nil || len(result.Data.Items) != 2 {
			t.Fatalf("expected directory entries, got %+v, %v", result, err)
		}
		item := result.Data.Items[0]
		if item.IsSelfOperated || item.CanOneClickConnect || item.AuthURL != "" || item.Badge != "" || item.Caveat != "" {
			t.Fatalf("third-party directory must not advertise Hoxi inference or auth: %+v", item)
		}
		if item.URL != "https://hoxi.ai" || item.BaseURL != "https://hoxi.ai/v1" {
			t.Fatalf("third-party directory metadata should be preserved: %+v", item)
		}
		official := result.Data.Items[1]
		if !official.IsSelfOperated || !official.CanOneClickConnect || official.Badge != "Official Pick" {
			t.Fatalf("expected self-operated gateway to preserve one-click connect metadata: %+v", official)
		}
	}
}

func TestHTTPHandlerRoutes(t *testing.T) {
	svc := NewService("http://invalid-url-for-test.local", nil)
	handler := NewHTTPHandler(svc)

	reqRemoved := httptest.NewRequest(http.MethodGet, "/api/hoxi/models/leaderboard?sort=coding&limit=3", nil)
	recRemoved := httptest.NewRecorder()
	handler.ServeHTTP(recRemoved, reqRemoved)
	if recRemoved.Code != http.StatusNotFound {
		t.Fatalf("expected removed leaderboard route to return 404, got %d", recRemoved.Code)
	}

	reqPricing := httptest.NewRequest(http.MethodGet, "/api/hoxi/models/pricing", nil)
	recPricing := httptest.NewRecorder()
	handler.ServeHTTP(recPricing, reqPricing)
	if recPricing.Code != http.StatusOK {
		t.Fatalf("expected status 200 for pricing, got %d", recPricing.Code)
	}
	var pricingRes ModelPricingResponse
	if err := json.NewDecoder(recPricing.Body).Decode(&pricingRes); err != nil {
		t.Fatalf("failed to decode pricing response: %v", err)
	}
	if pricingRes.Data == nil || len(pricingRes.Data.Items) == 0 {
		t.Fatalf("expected pricing items in response, got %+v", pricingRes)
	}

	reqFree := httptest.NewRequest(http.MethodGet, "/api/hoxi/models/free-tiers?lang=zh", nil)
	recFree := httptest.NewRecorder()
	handler.ServeHTTP(recFree, reqFree)
	if recFree.Code != http.StatusOK {
		t.Fatalf("expected status 200 for free-tiers, got %d", recFree.Code)
	}
	var freeRes FreeTiersResponse
	if err := json.NewDecoder(recFree.Body).Decode(&freeRes); err != nil {
		t.Fatalf("failed to decode free-tiers response: %v", err)
	}
	if freeRes.Data == nil || freeRes.Data.Items == nil {
		t.Fatalf("expected valid free-tiers response envelope, got %+v", freeRes)
	}
}

func TestGetModelPricingServesDiskCacheWithoutBlockingOnUpstream(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	cached := ModelPricingResponse{
		Code:    200,
		Message: "ok",
		Data: &ModelPricingData{
			UpdatedAt: "2026-11-24",
			Total:     1,
			Items:     []ModelPricingItem{{Slug: "cached-model", Name: "Cached Model"}},
		},
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "model-pricing-cache.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	svc := NewService(server.URL, &http.Client{Timeout: 5 * time.Second})
	start := time.Now()
	got, err := svc.GetModelPricing(false)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetModelPricing(false): %v", err)
	}
	if got.Data == nil || len(got.Data.Items) != 1 || got.Data.Items[0].Slug != "cached-model" {
		t.Fatalf("expected disk cached model, got %+v", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("GetModelPricing(false) blocked %v instead of returning disk cache immediately", elapsed)
	}

	// Future mtime (clock skew) must be treated as stale and trigger background refresh.
	future := time.Now().Add(2 * time.Hour)
	cachePath := filepath.Join(home, "model-pricing-cache.json")
	if err := os.Chtimes(cachePath, future, future); err != nil {
		t.Fatal(err)
	}
	refreshed := make(chan struct{}, 1)
	refreshSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case refreshed <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode([]ModelPricingItem{{Slug: "fresh-model", Name: "Fresh Model"}})
	}))
	defer refreshSrv.Close()

	svc2 := NewService(refreshSrv.URL, refreshSrv.Client())
	if _, err := svc2.GetModelPricing(false); err != nil {
		t.Fatalf("GetModelPricing(false) with future mtime: %v", err)
	}
	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("expected future-mtime pricing disk cache to trigger background refresh")
	}
}
