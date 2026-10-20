package leaderboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLeaderboardServiceFallback(t *testing.T) {
	svc := NewService("http://invalid-url-for-test.local", nil)

	lb, err := svc.GetLeaderboard("overall", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lb.Code != 200 || len(lb.Data.Items) == 0 {
		t.Fatalf("expected fallback items, got: %+v", lb)
	}

	gw, err := svc.GetConnectableGateways()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gw.Code != 200 || gw.Data.Total != 0 || gw.Data.Items == nil || len(gw.Data.Items) != 0 {
		t.Fatalf("expected empty gateway directory, got: %+v", gw)
	}
}

func TestGatewayDirectoryDoesNotOfferHoxiInferenceAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GatewaysResponse{Code: 200, Data: &GatewaysData{
			Total: 1, Items: []GatewayItem{{ID: "third-party", Name: "Third Party",
				URL: "https://hoxi.ai", BaseURL: "https://hoxi.ai/v1",
				IsSelfOperated: true, CanOneClickConnect: true,
				Badge: "Official Pick", Caveat: "Hoxi account connects inference",
				AuthURL: "https://hoxi.ai/auth/authorize-desktop"}},
		}})
	}))
	defer server.Close()
	svc := NewService(server.URL, server.Client())
	for i := 0; i < 2; i++ {
		result, err := svc.GetConnectableGateways()
		if err != nil || result.Data == nil || len(result.Data.Items) != 1 {
			t.Fatalf("expected directory entry, got %+v, %v", result, err)
		}
		item := result.Data.Items[0]
		if item.IsSelfOperated || item.CanOneClickConnect || item.AuthURL != "" || item.Badge != "" || item.Caveat != "" {
			t.Fatalf("directory must not advertise Hoxi inference or auth: %+v", item)
		}
		if item.URL != "https://hoxi.ai" || item.BaseURL != "https://hoxi.ai/v1" {
			t.Fatalf("third-party directory metadata should be preserved: %+v", item)
		}
	}
}

func TestLeaderboardHTTPHandler(t *testing.T) {
	svc := NewService("http://invalid-url-for-test.local", nil)
	handler := NewHTTPHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/models/leaderboard?sort=coding&limit=3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var res LeaderboardResponse
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(res.Data.Items) == 0 {
		t.Fatalf("expected items in response")
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
