package handoff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func TestDiscoveryPreservesStatsOrderAndFreshness(t *testing.T) {
	dir := t.TempDir()
	setupSyntheticCLIHome(t, dir)
	g := NewService(0, "test", dir, nil)
	request := func(path string, result any) {
		t.Helper()
		w := httptest.NewRecorder()
		gMux := http.NewServeMux()
		g.RegisterRoutes(gMux)
		gMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request failed: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), result); err != nil {
			t.Fatal(err)
		}
	}
	var all, filtered HandoffSessionsResponse
	request("/api/hoxi/handoff/sessions", &all)
	request("/api/hoxi/handoff/sessions?source=claude-code", &filtered)
	if len(all.Sources) != 8 || !reflect.DeepEqual(all.Sources, filtered.Sources) {
		t.Fatal("source filter changed full source stats")
	}
	if len(filtered.Sessions) != 1 || filtered.Sessions[0].Source != ExternalToolClaude {
		t.Fatalf("unexpected filtered sessions: %+v", filtered.Sessions)
	}
	var status LocalCLIStatusResponse
	request("/api/hoxi/local-cli/status", &status)
	if len(status.Tools) != 8 {
		t.Fatalf("unexpected tools: %+v", status.Tools)
	}
	for i, id := range []ExternalToolID{
		ExternalToolGrok,
		ExternalToolClaude,
		ExternalToolCodex,
		ExternalToolGemini,
		ExternalToolCodeBuddy,
		ExternalToolTrae,
		ExternalToolQoder,
		ExternalToolOpenCode,
	} {
		if status.Tools[i].ID != id || status.Tools[i].SessionCount != all.Sources[id].SessionCount {
			t.Fatalf("tool order or stats changed: %+v", status.Tools[i])
		}
	}
	// External changes must be visible on the very next request.
	projects := filepath.Join(claudeHomeDir(), "projects")
	entries, err := os.ReadDir(projects)
	if err != nil || len(entries) == 0 {
		t.Fatalf("missing synthetic projects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projects, entries[0].Name(), "new-session.jsonl"), []byte("{\"type\":\"user\",\"message\":{\"content\":\"new external session\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fresh HandoffSessionsResponse
	request("/api/hoxi/handoff/sessions?source=codex-cli", &fresh)
	if fresh.Sources[ExternalToolClaude].SessionCount != all.Sources[ExternalToolClaude].SessionCount+1 {
		t.Fatal("filtered request did not freshly scan every source")
	}
	request("/api/hoxi/local-cli/status", &status)
	if status.Tools[1].SessionCount != fresh.Sources[ExternalToolClaude].SessionCount {
		t.Fatal("CLI status did not freshly scan external sessions")
	}
}

func TestLocalCLIOAuthOnlyImportAndConnectedState(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)
	port, _, closeSrv := setupMockOpenCodeServerAndDB(t)
	defer closeSrv()

	// Replace settings/auth with OAuth-only formats (no API keys in settings.json)
	_ = os.WriteFile(filepath.Join(homeDir, ".claude", "settings.json"), []byte(`{"model":"opus[1m]","env":{"HTTP_PROXY":"http://127.0.0.1:7890"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(homeDir, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-test"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(homeDir, ".claude.json"), []byte(`{"additionalModelOptionsCache":[{"value":"claude-fable-5-1[1m]"}]}`), 0o644)

	_ = os.WriteFile(filepath.Join(homeDir, ".gemini", "settings.json"), []byte(`{"hooks":{}}`), 0o644)
	_ = os.WriteFile(filepath.Join(homeDir, ".gemini", "jetski-standalone-oauth-token"), []byte(`{"token":{"access_token":"ya29.test-antigravity"}}`), 0o644)

	_ = os.WriteFile(filepath.Join(homeDir, ".codex", "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"acc-tok","refresh_token":"ref-tok","account_id":"acct-1"}}`), 0o644)

	provSvc := provider.NewService(port, "testpass", workspaceDir, nil)
	svc := NewService(port, "testpass", workspaceDir, provSvc)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	for _, toolID := range []ExternalToolID{ExternalToolClaude, ExternalToolGemini} {
		b, _ := json.Marshal(LocalCLIImportRequest{ToolID: toolID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(b)))
		var resp LocalCLIImportResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal %s import: %v", toolID, err)
		}
		if resp.OK || !strings.Contains(resp.Error, "运行时新建会话") {
			t.Fatalf("expected OAuth-only import for %s to require Runner runtime, got %+v", toolID, resp)
		}
	}

	bCodex, _ := json.Marshal(LocalCLIImportRequest{ToolID: ExternalToolCodex})
	recCodex := httptest.NewRecorder()
	mux.ServeHTTP(recCodex, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(bCodex)))
	var codexResp LocalCLIImportResponse
	if err := json.Unmarshal(recCodex.Body.Bytes(), &codexResp); err != nil {
		t.Fatalf("unmarshal codex import: %v", err)
	}
	if !codexResp.OK || len(codexResp.Models) == 0 {
		t.Fatalf("expected OAuth-only import for codex to succeed, got %+v", codexResp)
	}
}

func TestGeminiCLIImportRefreshesStaleAgyModelsCache(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)
	port, _, closeSrv := setupMockOpenCodeServerAndDB(t)
	defer closeSrv()

	agyCliBinDir := filepath.Join(homeDir, ".gemini", "antigravity-cli", "bin")
	if err := os.MkdirAll(agyCliBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsOutFile := filepath.Join(t.TempDir(), "agy_models.txt")
	if err := os.WriteFile(modelsOutFile, []byte("gemini-4-argon-high   Gemini 4 Argon (High)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_AGY_MODELS_FILE", modelsOutFile)
	execstub.Write(t, agyCliBinDir, "agy", "cat \"$TEST_AGY_MODELS_FILE\"\n")

	provSvc := provider.NewService(port, "testpass", workspaceDir, nil)
	svc := NewService(port, "testpass", workspaceDir, provSvc)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Warm the 10-minute cache with the older model list via status check.
	recStatus := httptest.NewRecorder()
	mux.ServeHTTP(recStatus, httptest.NewRequest(http.MethodGet, "/api/hoxi/local-cli/status", nil))
	if recStatus.Code != http.StatusOK {
		t.Fatalf("status HTTP %d", recStatus.Code)
	}

	// Simulate `agy update` adding gemini-4-carbon while Hoxi is still running.
	if err := os.WriteFile(modelsOutFile, []byte("gemini-4-carbon-high  Gemini 4 Carbon (High)\ngemini-4-argon-high   Gemini 4 Argon (High)\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bGem, _ := json.Marshal(LocalCLIImportRequest{ToolID: ExternalToolGemini})
	recImp := httptest.NewRecorder()
	mux.ServeHTTP(recImp, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(bGem)))
	var impResp LocalCLIImportResponse
	if err := json.Unmarshal(recImp.Body.Bytes(), &impResp); err != nil {
		t.Fatalf("unmarshal gemini import: %v", err)
	}
	if !impResp.OK {
		t.Fatalf("expected gemini import OK, got %+v", impResp)
	}
	var sawCarbon bool
	for _, m := range impResp.Models {
		if m == "gemini-4-carbon-high" {
			sawCarbon = true
		}
	}
	if !sawCarbon {
		t.Fatalf("expected import to force-refresh agy models and include gemini-4-carbon-high, got %v", impResp.Models)
	}
}

func TestCodexCLIImportAndStartupSyncLatestCatalog(t *testing.T) {
	workspaceDir := t.TempDir()
	_ = setupSyntheticCLIHome(t, workspaceDir)
	port, _, closeSrv := setupMockOpenCodeServerAndDB(t)
	defer closeSrv()

	bundledFile := filepath.Join(t.TempDir(), "codex_bundled.json")
	if err := os.WriteFile(bundledFile, []byte(`{
		"models": [
			{
				"slug": "gpt-6-astra",
				"display_name": "GPT-6-Astra",
				"supported_reasoning_levels": [{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}],
				"visibility": "list",
				"priority": 2
			},
			{
				"slug": "gpt-6.1-sol",
				"display_name": "GPT-6.1-Sol",
				"supported_reasoning_levels": [{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}],
				"visibility": "list",
				"priority": 1
			},
			{
				"slug": "codex-auto-review",
				"display_name": "Codex Auto Review",
				"visibility": "hide",
				"priority": 43
			}
		]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	verFile := filepath.Join(t.TempDir(), "codex_ver.txt")
	if err := os.WriteFile(verFile, []byte("codex-cli 0.169.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_CODEX_MODELS_FILE", bundledFile)
	t.Setenv("TEST_CODEX_VER_FILE", verFile)

	codexBin := execstub.Write(t, t.TempDir(), "codex", `
if [ "$1" = "--version" ]; then
  cat "$TEST_CODEX_VER_FILE"
  exit 0
fi
if [ "$1" = "debug" ] && [ "$2" = "models" ]; then
  cat "$TEST_CODEX_MODELS_FILE"
  exit 0
fi
exit 1
`)
	t.Setenv("HOXI_CODEX_BIN", codexBin)

	provSvc := provider.NewService(port, "testpass", workspaceDir, nil)
	svc := NewService(port, "testpass", workspaceDir, provSvc)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Seed a legacy imported Codex provider in .hoxi/models.json and verify startup sync upgrades it.
	if err := provSvc.UpdateHoxiConfigDoc(func(doc map[string]interface{}) error {
		doc["provider"] = map[string]interface{}{
			"openai": map[string]interface{}{
				"name": "OpenAI Codex (Local)",
				"npm":  "@ai-sdk/openai",
				"models": map[string]interface{}{
					"gpt-5-codex": map[string]interface{}{"name": "gpt-5-codex (Codex)"},
				},
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	svc.SetWSBaseURL("wss://127.0.0.1:7099")
	t.Cleanup(svc.StopCredentialSync)

	openaiProv, _ := svc.loadHoxiProvidersMap()["openai"].(map[string]interface{})
	syncedModels, _ := openaiProv["models"].(map[string]interface{})
	if syncedModels["gpt-6.1-sol"] == nil || syncedModels["gpt-6-astra"] == nil {
		t.Fatalf("expected startup sync to populate gpt-6.1-sol and gpt-6-astra, got %+v", syncedModels)
	}
	if syncedModels["gpt-5-codex"] != nil || syncedModels["codex-auto-review"] != nil {
		t.Fatalf("expected stale/hidden models to be pruned on sync, got %+v", syncedModels)
	}
	opts, _ := openaiProv["options"].(map[string]interface{})
	if gotDef := asString(opts["defaultModel"]); gotDef != "gpt-6.1-sol" {
		t.Fatalf("expected defaultModel=gpt-6.1-sol, got %q", gotDef)
	}

	// Simulate `codex update` adding gpt-6.2-sol while Hoxi is running, then trigger manual sync/import.
	if err := os.WriteFile(verFile, []byte("codex-cli 0.171.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundledFile, []byte(`{
		"models": [
			{"slug": "gpt-6.2-sol", "display_name": "GPT-6.2-Sol", "visibility": "list", "priority": 1},
			{"slug": "gpt-6.1-sol", "display_name": "GPT-6.1-Sol", "visibility": "list", "priority": 2}
		]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	bCodex, _ := json.Marshal(LocalCLIImportRequest{ToolID: ExternalToolCodex})
	recImp := httptest.NewRecorder()
	mux.ServeHTTP(recImp, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(bCodex)))
	var impResp LocalCLIImportResponse
	if err := json.Unmarshal(recImp.Body.Bytes(), &impResp); err != nil {
		t.Fatalf("unmarshal codex import: %v", err)
	}
	if !impResp.OK || len(impResp.Models) != 2 || impResp.Models[0] != "gpt-6.2-sol" || impResp.Models[1] != "gpt-6.1-sol" {
		t.Fatalf("expected force-refreshed codex models [gpt-6.2-sol, gpt-6.1-sol], got %+v", impResp)
	}
	afterProv, _ := svc.loadHoxiProvidersMap()["openai"].(map[string]interface{})
	afterModels, _ := afterProv["models"].(map[string]interface{})
	solEntry, _ := afterModels["gpt-6.2-sol"].(map[string]interface{})
	if gotName := asString(solEntry["name"]); gotName != "GPT-6.2-Sol (Codex)" {
		t.Fatalf("expected display_name preserved as 'GPT-6.2-Sol (Codex)', got %q", gotName)
	}
}
