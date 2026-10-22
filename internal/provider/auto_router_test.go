package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// 5. A customized pool with every candidate removed must not fall back to the
	// full provider catalog, otherwise deleted models keep being selected.
	emptyPayload := map[string]interface{}{
		"poolID": "free",
		"pool": map[string]interface{}{
			"id":         "free",
			"preset":     true,
			"strategy":   "round-robin",
			"customized": true,
			"candidates": []map[string]string{},
		},
	}
	rawEmpty, _ := json.Marshal(emptyPayload)
	emptyReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/auto-pools", bytes.NewReader(rawEmpty))
	emptyRec := httptest.NewRecorder()
	mux.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("POST empty pool status = %d: %s", emptyRec.Code, emptyRec.Body.String())
	}
	p3, m3 := svc.resolveBackendAutoCandidate("hoxi-auto", "free")
	if p3 != "hoxi-auto" || m3 != "free" {
		t.Fatalf("empty customized pool resolved to %s/%s, want unresolved hoxi-auto/free", p3, m3)
	}
}

func TestFreePoolNeverSelectsPaidFallback(t *testing.T) {
	home, root := setupIsolatedEnv(t)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	svc := NewService(0, "test", root, nil)
	err := writeJSONMapFile(svc.workspaceHoxiModelsPath(), map[string]interface{}{
		"provider": map[string]interface{}{
			"paid": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"baseURL": "https://audit.invalid/v1",
					"apiKey":  "synthetic",
				},
				"models": map[string]interface{}{
					"paid-model": map[string]interface{}{
						"cost": map[string]interface{}{"input": 10.0, "output": 30.0},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pid, mid := svc.selectAutoCandidate("free", nil)
	if pid == "paid" {
		t.Fatalf("default free pool chose paid candidate %s/%s", pid, mid)
	}
}

type barrierReader struct {
	reader  io.Reader
	entered chan<- struct{}
	release <-chan struct{}
	first   bool
}

func (r *barrierReader) Read(b []byte) (int, error) {
	if !r.first {
		r.first = true
		r.entered <- struct{}{}
		<-r.release
	}
	return r.reader.Read(b)
}

func TestConcurrentProviderSavesPreserveBothCredentials(t *testing.T) {
	home, root := setupIsolatedEnv(t)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	svc := NewService(0, "test", root, nil)
	entered, release, done := make(chan struct{}, 2), make(chan struct{}), make(chan int, 2)
	for _, pid := range []string{"provA", "provB"} {
		body := `{"providerID":"` + pid + `","config":{"options":{"apiKey":"synthetic-key"},"models":{"m":{}}}}`
		go func(body string) {
			rd := &barrierReader{reader: strings.NewReader(body), entered: entered, release: release}
			req := httptest.NewRequest(http.MethodPost, "/api/hoxi/models-config", rd)
			w := httptest.NewRecorder()
			svc.handleModelsConfig(w, req)
			done <- w.Code
		}(body)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("request did not reach barrier")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if status := <-done; status != http.StatusOK {
			t.Fatalf("save failed: %d", status)
		}
	}
	doc := readJSONMapFile(userHoxiModelsPath())
	providers, _ := doc["provider"].(map[string]interface{})
	if len(providers) != 2 {
		t.Fatalf("both saves returned 200, but only %d of 2 providers remain in credential store", len(providers))
	}
}
