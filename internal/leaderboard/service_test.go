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
	if gw.Code != 200 || len(gw.Data.Items) == 0 {
		t.Fatalf("expected fallback gateways, got: %+v", gw)
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
}
