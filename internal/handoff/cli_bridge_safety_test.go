package handoff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestClaudeAndAntigravityBridgesFailClosedWith410Gone(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("GEMINI_HOME", binDir)
	t.Setenv("PATH", binDir)
	t.Setenv("BRIDGE_MARKER", marker)
	for _, bin := range []string{"agy", "claude"} {
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte("#!/bin/sh\nprintf executed > \"$BRIDGE_MARKER\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(0, "", t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	for _, ep := range []string{
		"/api/hoxi/bridge/gemini-antigravity/v1/chat/completions",
		"/api/hoxi/bridge/claude-code/v1/chat/completions",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ep, strings.NewReader(`{"messages":[{"role":"user","content":"edit files"}]}`)))
		if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "运行时新建会话") {
			t.Fatalf("%s: expected 410 Gone with runner guidance, got HTTP %d: %s", ep, rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("%s must not execute CLI binary: %v", ep, err)
		}
	}
}

func TestSyncLocalCLIBridgeURLsRemovesDeprecatedBridgesAndSpacedModels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := NewService(0, "", root, provider.NewService(0, "", root, nil))
	providers := map[string]interface{}{
		"claude-code": map[string]interface{}{
			"name":    "Claude Bridge",
			"options": map[string]interface{}{"baseURL": "http://127.0.0.1:1/api/hoxi/bridge/claude-code/v1"},
			"models":  map[string]interface{}{"claude-sonnet-4-6": map[string]interface{}{"tool_call": false}},
		},
		"google": map[string]interface{}{
			"name":    "Antigravity Bridge",
			"options": map[string]interface{}{"baseURL": "http://127.0.0.1:1/api/hoxi/bridge/gemini-antigravity/v1"},
			"models":  map[string]interface{}{"Gemini 4 Argon (High)": map[string]interface{}{"tool_call": false}},
		},
		"grok-cli": map[string]interface{}{
			"name":    "Grok Bridge",
			"options": map[string]interface{}{"baseURL": "http://127.0.0.1:1/api/hoxi/bridge/grok-cli/v1"},
			"models":  map[string]interface{}{"grok-4": map[string]interface{}{"tool_call": true}},
		},
		"direct": map[string]interface{}{
			"name":    "Direct",
			"options": map[string]interface{}{"baseURL": "https://api.example/v1"},
			"models":  map[string]interface{}{"existing": map[string]interface{}{"tool_call": true}},
		},
	}
	if err := svc.persistHoxiConfigDoc(map[string]interface{}{"provider": providers}); err != nil {
		t.Fatal(err)
	}
	svc.SetWSBaseURL("ws://127.0.0.1:2")
	doc := svc.loadHoxiConfigDoc()
	provMap := doc["provider"].(map[string]interface{})
	if _, hasGoogle := provMap["google"]; hasGoogle {
		t.Fatalf("expected deprecated google bridge provider to be removed, got %+v", provMap["google"])
	}
	if _, hasClaude := provMap["claude-code"]; hasClaude {
		t.Fatalf("expected deprecated claude-code bridge provider to be removed, got %+v", provMap["claude-code"])
	}
	grokOpts := provMap["grok-cli"].(map[string]interface{})["options"].(map[string]interface{})
	if got := grokOpts["baseURL"]; got != grokCLIStoredBridgeURL {
		t.Fatalf("expected grok-cli baseURL migrated to the neutral value, got %v", got)
	}
}

// Release and dev Hoxi share ~/.hoxi/models.json. Whichever starts last must not
// rewrite the grok-cli bridge to its own port, or the other process's OpenCode
// reads a gateway that may already have exited.
func TestSyncLocalCLIBridgeURLsDoesNotStoreProcessPort(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	providers := provider.NewService(0, "", root, nil)
	seed := NewService(0, "", root, providers)
	if err := seed.persistHoxiConfigDoc(map[string]interface{}{"provider": map[string]interface{}{
		"grok-cli": map[string]interface{}{
			"options": map[string]interface{}{"baseURL": "http://127.0.0.1:5420/api/hoxi/bridge/grok-cli/v1"},
			"models":  map[string]interface{}{"grok-4": map[string]interface{}{"tool_call": true}},
		},
		"custom": map[string]interface{}{
			"options": map[string]interface{}{"baseURL": "https://grok.example/api/hoxi/bridge/grok-cli/v1"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"ws://127.0.0.1:5410", "ws://127.0.0.1:5420", "ws://127.0.0.1:61234"} {
		peer := NewService(0, "", root, providers)
		peer.SetWSBaseURL(ws)
		peer.StopCredentialSync()
		provMap := seed.loadHoxiConfigDoc()["provider"].(map[string]interface{})
		grokOpts := provMap["grok-cli"].(map[string]interface{})["options"].(map[string]interface{})
		if got := grokOpts["baseURL"]; got != grokCLIStoredBridgeURL {
			t.Fatalf("after %s started, models.json grok-cli baseURL = %v, want %s", ws, got, grokCLIStoredBridgeURL)
		}
		customOpts := provMap["custom"].(map[string]interface{})["options"].(map[string]interface{})
		if got := customOpts["baseURL"]; got != "https://grok.example/api/hoxi/bridge/grok-cli/v1" {
			t.Fatalf("non-loopback baseURL must never be rewritten, got %v", got)
		}
	}
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"default":{"key":"xai-test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	importer := NewService(0, "", root, providers)
	importer.SetWSBaseURL("ws://127.0.0.1:5420")
	importer.StopCredentialSync()
	if resp := importer.importGrokCLIProvider(); !resp.OK {
		t.Fatalf("grok import failed: %+v", resp)
	}
	grokOpts := seed.loadHoxiConfigDoc()["provider"].(map[string]interface{})["grok-cli"].(map[string]interface{})["options"].(map[string]interface{})
	if got := grokOpts["baseURL"]; got != grokCLIStoredBridgeURL {
		t.Fatalf("import stored a process-specific bridge URL: %v", got)
	}
}

func TestImportNeverTouchesOAuthTokensAndRequiresRunnerWhenNoAPIKey(t *testing.T) {
	root := t.TempDir()
	home := setupSyntheticCLIHome(t, root)
	t.Setenv("PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_GENERATIVE_AI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	// Write OAuth token files with poison values and Remove settings API keys
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"claude-sonnet-4-6"}`), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat-poison"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".gemini", "settings.json"), []byte(`{"model":"gemini-3.1-pro-high"}`), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".gemini", "jetski-standalone-oauth-token"), []byte(`{"token":{"access_token":"ya29.poison"}}`), 0o644)

	svc := NewService(0, "", root, provider.NewService(0, "", root, nil))
	for _, resp := range []LocalCLIImportResponse{svc.importClaudeCLIProvider(), svc.importGeminiCLIProvider()} {
		if resp.OK || !strings.Contains(resp.Error, "运行时新建会话") {
			t.Fatalf("expected OAuth-only import to fail with Runner instruction, got %+v", resp)
		}
	}
	rawDoc, _ := json.Marshal(svc.loadHoxiConfigDoc())
	if strings.Contains(string(rawDoc), "poison") {
		t.Fatalf("OAuth token leaked into config doc: %s", string(rawDoc))
	}

	// Real API keys still import direct @ai-sdk/google and @ai-sdk/anthropic providers
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-real")
	t.Setenv("GEMINI_API_KEY", "AIzaSyRealKey")
	for _, resp := range []LocalCLIImportResponse{svc.importClaudeCLIProvider(), svc.importGeminiCLIProvider()} {
		if !resp.OK || len(resp.Models) == 0 {
			t.Fatalf("real API key import failed: %+v", resp)
		}
		for _, m := range resp.Models {
			if strings.ContainsAny(m, " ()") {
				t.Fatalf("model ID must not contain spaces: %q", m)
			}
		}
	}
}
