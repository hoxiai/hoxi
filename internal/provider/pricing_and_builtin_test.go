package provider

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModelPricingAndCostUSD(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)
	_ = homeDir
	_ = os.MkdirAll(filepath.Join(workspaceDir, ".hoxi"), 0o755)
	_ = os.WriteFile(filepath.Join(workspaceDir, ".hoxi", "models.json"), []byte(`{
		"provider": {
			"custom-corp": {
				"name": "Corp Gateway",
				"options": {"baseURL": "https://llm.corp.example/v1", "apiKey": "sk-corp"},
				"models": {
					"corp-coder": {
						"name": "Corp Coder",
						"cost": {"input": 2.0, "output": 8.0, "cache_read": 0.2, "cache_write": 2.5}
					}
				}
			}
		}
	}`), 0o644)

	svc := NewService(0, "", workspaceDir, nil)

	// 1. Explicit cost from .hoxi/models.json
	corpTarget := svc.ResolveStreamTarget("custom-corp", "corp-coder")
	gotCorpUSD := corpTarget.Cost.ComputeUSD(500_000, 250_000, 0, 1_000_000, 200_000)
	// 0.5*2.0 ($1.00) + 0.25*8.0 ($2.00) + 1.0*0.2 ($0.20) + 0.2*2.5 ($0.50) = $3.70
	if math.Abs(gotCorpUSD-3.70) > 1e-6 {
		t.Fatalf("expected custom cost $3.70, got %.6f (pricing=%+v)", gotCorpUSD, corpTarget.Cost)
	}

	// 2. Built-in public pricing fallback (Anthropic Claude Sonnet 4.5)
	claudeTarget := svc.ResolveStreamTarget("anthropic", "claude-sonnet-4-5-20250929")
	gotClaudeUSD := claudeTarget.Cost.ComputeUSD(100_000, 20_000, 10_000, 50_000, 0)
	// 0.1*3.0 ($0.30) + (0.02+0.01)*15.0 ($0.45) + 0.05*0.30 ($0.015) = $0.765
	if math.Abs(gotClaudeUSD-0.765) > 1e-6 {
		t.Fatalf("expected claude fallback cost $0.765, got %.6f (pricing=%+v)", gotClaudeUSD, claudeTarget.Cost)
	}

	// 3. Local Ollama / loopback must always price at $0
	ollamaTarget := svc.ResolveStreamTarget("ollama", "qwen2.5-coder:32b")
	if usd := ollamaTarget.Cost.ComputeUSD(1_000_000, 1_000_000, 0, 0, 0); usd != 0 {
		t.Fatalf("expected $0 for local ollama model, got %f", usd)
	}
}

func TestOfflineBuiltinProviderCatalogAndAuthActivation(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	svc := NewService(0, "", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Offline GET /provider includes built-in presets with zero connected providers initially
	recProv := doMuxReq(t, mux, http.MethodGet, "/provider", nil)
	if recProv.Code != http.StatusOK {
		t.Fatalf("GET /provider = %d %s", recProv.Code, recProv.Body.String())
	}
	var payload struct {
		All       []map[string]any `json:"all"`
		Connected []string         `json:"connected"`
		Default   map[string]any   `json:"default"`
	}
	if err := json.Unmarshal(recProv.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode /provider: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, item := range payload.All {
		if id, _ := item["id"].(string); id != "" {
			byID[id] = item
		}
	}
	for _, wantID := range []string{"anthropic", "openai", "google", "deepseek", "openrouter", "siliconflow", "ollama"} {
		if byID[wantID] == nil {
			t.Fatalf("expected built-in provider %q in offline /provider list", wantID)
		}
	}
	if len(payload.Connected) != 0 {
		t.Fatalf("expected 0 connected providers in clean isolated env, got %+v", payload.Connected)
	}

	// 2. PUT /auth/anthropic activates anthropic in /provider, /config/providers, and sanitized /api/hoxi/provider/models
	secretKey := "sk-ant-offline-secret-key"
	recAuth := doMuxReq(t, mux, http.MethodPut, "/auth/anthropic", map[string]any{
		"type": "api",
		"key":  secretKey,
	})
	if recAuth.Code != http.StatusOK {
		t.Fatalf("PUT /auth/anthropic = %d %s", recAuth.Code, recAuth.Body.String())
	}

	recCat := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/models", nil)
	if recCat.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/provider/models = %d %s", recCat.Code, recCat.Body.String())
	}
	if strings.Contains(recCat.Body.String(), secretKey) {
		t.Fatalf("catalog leaked auth.json secret key: %s", recCat.Body.String())
	}
	cat := assertCatalogSanitized(t, recCat.Body.Bytes())
	if cat.Source != "config" || len(cat.Providers) != 1 || cat.Providers[0].ID != "anthropic" || len(cat.Providers[0].Models) == 0 {
		t.Fatalf("expected sanitized anthropic built-in catalog after PUT /auth/anthropic, got %+v", cat)
	}

	recCfgProvs := doMuxReq(t, mux, http.MethodGet, "/config/providers", nil)
	if recCfgProvs.Code != http.StatusOK || !strings.Contains(recCfgProvs.Body.String(), `"anthropic"`) {
		t.Fatalf("expected /config/providers to include connected anthropic, got %d %s", recCfgProvs.Code, recCfgProvs.Body.String())
	}
	if econModel, ok := svc.ResolveEconomyModel("anthropic", "claude-opus-4-5"); !ok || econModel != "claude-haiku-4-5" {
		t.Fatalf("expected offline economy subagent routing to pick claude-haiku-4-5, got ok=%v model=%q", ok, econModel)
	}

	// 3. DELETE /auth/anthropic disconnects it cleanly
	recDel := doMuxReq(t, mux, http.MethodDelete, "/auth/anthropic", nil)
	if recDel.Code != http.StatusOK {
		t.Fatalf("DELETE /auth/anthropic = %d %s", recDel.Code, recDel.Body.String())
	}
	recCatAfter := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/models", nil)
	catAfter := assertCatalogSanitized(t, recCatAfter.Body.Bytes())
	if len(catAfter.Providers) != 0 {
		t.Fatalf("expected 0 providers after DELETE /auth/anthropic, got %+v", catAfter)
	}
}
