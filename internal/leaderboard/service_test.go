package leaderboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
