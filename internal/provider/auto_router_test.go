package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAutoPoolsCRUDAndMultiKeyRotation(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	svc := NewService(0, "password", workspace, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Default GET /api/hoxi/provider/auto-pools returns preset pools (free, daily, frontier)
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/auto-pools", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET auto-pools status = %d", rec.Code)
	}
	var getResp struct {
		OK        bool                   `json:"ok"`
		AutoPools map[string]interface{} `json:"autoPools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil || !getResp.OK {
		t.Fatalf("invalid GET auto-pools response: %v", err)
	}
	for _, preset := range []string{"free", "daily", "frontier"} {
		if _, ok := getResp.AutoPools[preset]; !ok {
			t.Fatalf("missing preset pool %q", preset)
		}
	}

	// 2. Save a customized preset pool + a custom user pool
	savePayload := map[string]interface{}{
		"poolID": "free",
		"pool": map[string]interface{}{
			"id":         "free",
			"preset":     true,
			"strategy":   "round-robin",
			"customized": true,
			"candidates": []map[string]string{
				{"providerID": "openrouter", "modelID": "qwen/qwen3-coder:free"},
				{"providerID": "google", "modelID": "gemini-2.5-flash"},
			},
		},
	}
	rawSave, _ := json.Marshal(savePayload)
	postReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/auto-pools", bytes.NewReader(rawSave))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST auto-pools status = %d: %s", postRec.Code, postRec.Body.String())
	}

	// 3. Backend round-robin resolution for hoxi-auto/free
	p1, m1 := svc.resolveBackendAutoCandidate("hoxi-auto", "free")
	p2, m2 := svc.resolveBackendAutoCandidate("hoxi-auto", "free")
	if p1 != "openrouter" || m1 != "qwen/qwen3-coder:free" {
		t.Fatalf("first auto resolution = %s/%s, want openrouter/qwen/qwen3-coder:free", p1, m1)
	}
	if p2 != "google" || m2 != "gemini-2.5-flash" {
		t.Fatalf("second auto resolution = %s/%s, want google/gemini-2.5-flash", p2, m2)
	}

	// 4. Multi-key normalization & rotation
	cfgMap := map[string]interface{}{
		"name": "OpenRouter Free",
		"options": map[string]interface{}{
			"baseURL": "https://openrouter.ai/api/v1",
			"apiKey":  "sk-free-1, sk-free-2\nsk-free-3",
		},
	}
	svc.normalizeAndStoreMultiKeys("openrouter", cfgMap)
	opts := cfgMap["options"].(map[string]interface{})
	if opts["apiKey"] != "sk-free-1" {
		t.Fatalf("primary apiKey = %v, want sk-free-1", opts["apiKey"])
	}
	k1 := svc.selectRotatedProviderKey("openrouter", "sk-free-1")
	k2 := svc.selectRotatedProviderKey("openrouter", "sk-free-1")
	k3 := svc.selectRotatedProviderKey("openrouter", "sk-free-1")
	if k1 == k2 || k2 == k3 {
		t.Fatalf("expected rotated keys across calls, got %q, %q, %q", k1, k2, k3)
	}
}
