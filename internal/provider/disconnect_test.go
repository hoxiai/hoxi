package provider

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func TestDisconnectProviderPurgesKeyPoolsDismissesExternalAndAllowsCleanReimport(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)

	// Seed external OpenCode config and legacy external auth.json (must never be modified).
	extCfgDir := filepath.Join(homeDir, ".config", "opencode")
	if err := os.MkdirAll(extCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	extCfgPath := filepath.Join(extCfgDir, "opencode.json")
	extCfgRaw := `{"provider":{"ext-relay":{"name":"Ext Relay","npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://ext.example/v1","apiKey":"sk-ext-relay"},"models":{"m-old":{"name":"Old"}}}}}`
	if err := os.WriteFile(extCfgPath, []byte(extCfgRaw), 0o600); err != nil {
		t.Fatal(err)
	}

	extAuthDir := filepath.Join(homeDir, ".local", "share", "opencode")
	if err := os.MkdirAll(extAuthDir, 0o700); err != nil {
		t.Fatal(err)
	}
	extAuthPath := filepath.Join(extAuthDir, "auth.json")
	extAuthRaw := `{"deepseek":{"type":"api","key":"sk-legacy-deepseek"}}`
	if err := os.WriteFile(extAuthPath, []byte(extAuthRaw), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", workspaceDir, nil)
	var notified []string
	svc.SetOnProviderDisconnected(func(pid string) {
		notified = append(notified, pid)
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Refusal paths
	if rec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/disconnect", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET disconnect, got %d", rec.Code)
	}
	if rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/disconnect", map[string]any{"providerID": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty providerID, got %d", rec.Code)
	}
	if rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/disconnect", map[string]any{"providerID": AutoProviderID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for %s disconnect, got %d", AutoProviderID, rec.Code)
	}

	// Save a multi-key provider in Hoxi and verify rotation pool exists.
	rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/models-config", map[string]any{
		"providerID": "openrouter",
		"config": map[string]any{
			"name": "OpenRouter",
			"options": map[string]any{
				"baseURL": "https://openrouter.ai/api/v1",
				"apiKey":  "sk-or-1, sk-or-2",
			},
			"models": map[string]any{
				"qwen/qwen3-coder": map[string]any{"name": "Qwen3"},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save openrouter failed: %d %s", rec.Code, rec.Body.String())
	}
	_ = doMuxReq(t, mux, http.MethodPut, "/api/hoxi/provider/credentials/openrouter", map[string]any{
		"type": "api",
		"key":  "sk-or-1",
	})

	// Disconnect openrouter -> removes models.json entry, engine auth.json entry, and keyPools entry.
	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/disconnect", map[string]any{"providerID": "openrouter"})
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect openrouter failed: %d %s", rec.Code, rec.Body.String())
	}
	if len(notified) != 1 || notified[0] != "openrouter" {
		t.Fatalf("expected disconnect hook for openrouter, got %v", notified)
	}
	if got := svc.selectRotatedProviderKey("openrouter", "sk-fresh-single"); got != "sk-fresh-single" {
		t.Fatalf("stale multi-key pool leaked after disconnect: got %q, want sk-fresh-single", got)
	}
	if got := svc.ResolveStoredProviderKey("openrouter", "https://openrouter.ai/api/v1"); got != "" {
		t.Fatalf("expected openrouter key cleared, got %q", got)
	}

	// Disconnect deepseek (backed by external ~/.local/share/opencode/auth.json) and ext-relay (backed by ~/.config/opencode/opencode.json).
	if got := svc.ResolveStoredProviderKey("deepseek", DefaultBaseURL("deepseek")); got != "sk-legacy-deepseek" {
		t.Fatalf("precondition: expected fallback deepseek key before disconnect, got %q", got)
	}
	for _, pid := range []string{"deepseek", "ext-relay"} {
		if r := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/disconnect", map[string]any{"providerID": pid}); r.Code != http.StatusOK {
			t.Fatalf("disconnect %s failed: %d %s", pid, r.Code, r.Body.String())
		}
	}

	// External files must stay 100% untouched.
	if afterCfg, _ := os.ReadFile(extCfgPath); string(afterCfg) != extCfgRaw {
		t.Fatalf("external opencode.json was modified: %s", string(afterCfg))
	}
	if afterAuth, _ := os.ReadFile(extAuthPath); string(afterAuth) != extAuthRaw {
		t.Fatalf("external auth.json was modified: %s", string(afterAuth))
	}
	if got := svc.ResolveStoredProviderKey("deepseek", DefaultBaseURL("deepseek")); got != "" {
		t.Fatalf("expected dismissed external deepseek key to stay disconnected, got %q", got)
	}

	// Offline catalog must NOT list deepseek or ext-relay as connected, and ext-relay must be absent from all,
	// while built-in deepseek stays available (unconnected) for future connection.
	catRec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/catalog", nil)
	var catPayload struct {
		All       []map[string]any `json:"all"`
		Connected []string         `json:"connected"`
	}
	if err := json.Unmarshal(catRec.Body.Bytes(), &catPayload); err != nil {
		t.Fatal(err)
	}
	for _, cid := range catPayload.Connected {
		if cid == "deepseek" || cid == "ext-relay" {
			t.Fatalf("disconnected provider %q resurrected in connected list: %v", cid, catPayload.Connected)
		}
	}
	hasDeepSeekInAll := false
	for _, item := range catPayload.All {
		id, _ := item["id"].(string)
		if id == "ext-relay" {
			t.Fatalf("dismissed external custom provider ext-relay still in all list")
		}
		if id == "deepseek" {
			hasDeepSeekInAll = true
		}
	}
	if !hasDeepSeekInAll {
		t.Fatalf("built-in deepseek should remain available as unconnected catalog entry")
	}

	// Even if deepseek was also in disabled_providers, re-connecting with a new key clears both disabled_providers and dismissal.
	_ = doMuxReq(t, mux, http.MethodPatch, "/api/hoxi/provider/config", map[string]any{
		"disabled_providers": []string{"deepseek"},
	})
	if r := doMuxReq(t, mux, http.MethodPut, "/api/hoxi/provider/credentials/deepseek", map[string]any{
		"type": "api",
		"key":  "sk-brand-new-deepseek",
	}); r.Code != http.StatusOK {
		t.Fatalf("reconnect deepseek failed: %d %s", r.Code, r.Body.String())
	}
	if got := svc.ResolveStoredProviderKey("deepseek", DefaultBaseURL("deepseek")); got != "sk-brand-new-deepseek" {
		t.Fatalf("expected brand new deepseek key after reconnect, got %q", got)
	}
	if svc.disabledProvidersSet()["deepseek"] {
		t.Fatalf("expected deepseek removed from disabled_providers on explicit credential save")
	}
	if _, err := os.Stat(hoxipaths.EngineAuthPath()); err != nil {
		t.Fatalf("expected engine auth file persisted: %v", err)
	}

	// Re-saving ext-relay into Hoxi clears dismissal and restores it cleanly.
	if r := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/models-config", map[string]any{
		"providerID": "ext-relay",
		"config": map[string]any{
			"name":    "Ext Relay Fresh",
			"options": map[string]any{"baseURL": "https://ext2.example/v1", "apiKey": "sk-fresh-relay"},
			"models":  map[string]any{"m-new": map[string]any{"name": "New"}},
		},
	}); r.Code != http.StatusOK {
		t.Fatalf("re-save ext-relay failed: %d %s", r.Code, r.Body.String())
	}
	if svc.IsExternalProviderDismissed("ext-relay") || strings.Contains(doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/models", nil).Body.String(), "m-old") {
		t.Fatalf("expected ext-relay restored cleanly with only m-new")
	}
}
