package provider

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

func assertPrivateConfig(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
	}
}

func TestConfigCredentialMigrationAndUpstreamReload(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	userPath := userHoxiModelsPath()
	workspacePath := filepath.Join(workspace, ".hoxi", "models.json")
	// Real persisted compatibility: workspace values override user values, and
	// older versions duplicated literal keys and custom authentication headers.
	for _, path := range []string{userPath, workspacePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"provider":{"custom":{"options":{"apiKey":"old-key","baseURL":"https://example.test/v1","headers":{"Authorization":"Bearer header-key"}},"models":{"m1":{"name":"Model"}}}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(workspacePath, []byte(`{"provider":{"custom":{"options":{"apiKey":"workspace-key"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := EnsureHoxiModelsConfig(workspace); got != userPath {
		t.Fatalf("upstream config = %q, want %q", got, userPath)
	}
	svc := NewService(0, "password", workspace, nil)
	check := func(wantKey string) {
		t.Helper()
		for _, path := range []string{userPath, workspacePath} {
			assertPrivateConfig(t, path)
		}
		user := readJSONMapFile(userPath)
		mirror := readJSONMapFile(workspacePath)
		opts := user["provider"].(map[string]interface{})["custom"].(map[string]interface{})["options"].(map[string]interface{})
		if opts["apiKey"] != wantKey || opts["headers"] == nil {
			t.Fatalf("upstream credentials not preserved: %+v", opts)
		}
		mirrorOpts := mirror["provider"].(map[string]interface{})["custom"].(map[string]interface{})["options"].(map[string]interface{})
		if _, ok := mirrorOpts["apiKey"]; ok {
			t.Fatal("workspace contains apiKey")
		}
		if _, ok := mirrorOpts["headers"]; ok {
			t.Fatal("workspace contains headers")
		}
		if mirrorOpts["baseURL"] != "https://example.test/v1" {
			t.Fatal("workspace lost non-secret options")
		}
		if got := svc.ResolveStoredProviderKey("custom", ""); got != wantKey {
			t.Fatalf("resolved key = %q, want %q", got, wantKey)
		}
	}
	check("workspace-key")
	// Repeated initialization must not erase credentials omitted from the mirror.
	EnsureHoxiModelsConfig(workspace)
	check("workspace-key")
	doc := svc.LoadHoxiConfigDoc()
	doc["provider"].(map[string]interface{})["custom"].(map[string]interface{})["options"].(map[string]interface{})["apiKey"] = "updated-key"
	reloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/dispose" || r.Method != http.MethodPost {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		username, password, _ := r.BasicAuth()
		if username != "opencode" || password != "password" {
			t.Error("missing upstream authentication")
		}
		upstream := readJSONMapFile(userPath)
		if !reflect.DeepEqual(upstream, doc) {
			t.Error("upstream config not fully persisted before reload")
		}
		reloads++
	}))
	defer server.Close()
	svc.openCode = ocapi.NewForPort(server.Listener.Addr().(*net.TCPAddr).Port, "password")
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatal(err)
	}
	svc.openCode = ocapi.NewForPort(0, "")
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	check("updated-key")
}

func TestConfigSaveFailureDoesNotStripOnlyCredentialCopy(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	svc := NewService(0, "", workspace, nil)
	if err := writeJSONMapFile(svc.workspaceHoxiModelsPath(), map[string]interface{}{"provider": map[string]interface{}{"custom": map[string]interface{}{"options": map[string]interface{}{"apiKey": "only-copy"}}}}); err != nil {
		t.Fatal(err)
	}
	// A directory at the destination makes replacement fail even as root.
	if err := os.MkdirAll(userHoxiModelsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	before := readJSONMapFile(svc.workspaceHoxiModelsPath())
	if err := svc.PersistHoxiConfigDoc(svc.LoadHoxiConfigDoc()); err == nil {
		t.Fatal("expected user config write failure")
	}
	EnsureHoxiModelsConfig(workspace)
	if after := readJSONMapFile(svc.workspaceHoxiModelsPath()); !reflect.DeepEqual(before, after) {
		t.Fatal("failed user save changed workspace credential copy")
	}
}

func TestConfigSamePathAndNoHomeFallback(t *testing.T) {
	home, _ := setupIsolatedEnv(t)
	svc := NewService(0, "", home, nil)
	doc := map[string]interface{}{"provider": map[string]interface{}{"custom": map[string]interface{}{"options": map[string]interface{}{"apiKey": "retained-key"}}}}
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatal(err)
	}
	if got := svc.ResolveStoredProviderKey("custom", ""); got != "retained-key" {
		t.Fatalf("same-path save lost key: %q", got)
	}
	path := filepath.Join(t.TempDir(), "models.json")
	if err := persistHoxiModelsFiles("", path, doc); err != nil {
		t.Fatal(err)
	}
	assertPrivateConfig(t, path)
	if !reflect.DeepEqual(readJSONMapFile(path), doc) {
		t.Fatal("no-home fallback lost upstream credentials")
	}
}

func TestDeferredReloadWhileBusyDoesNotDisposeUntilIdle(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	var disposeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/dispose" && r.Method == http.MethodPost {
			disposeCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`true`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	svc := NewService(port, "password", workspace, nil)
	var busy atomic.Bool
	busy.Store(true)
	svc.SetBusyCheck(func() bool { return busy.Load() })

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	doc := map[string]interface{}{
		"provider": map[string]interface{}{
			"custom": map[string]interface{}{
				"options": map[string]interface{}{"apiKey": "busy-safe-key"},
			},
		},
	}
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatal(err)
	}
	// Direct POST /global/dispose through the provider route must also defer while busy.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/global/dispose", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /global/dispose status = %d, want 200", rec.Code)
	}
	if !svc.HasPendingReload() {
		t.Fatal("expected pending reload while session is busy")
	}
	if got := disposeCalls.Load(); got != 0 {
		t.Fatalf("dispose calls while busy = %d, want 0", got)
	}
	if got := svc.ResolveStoredProviderKey("custom", ""); got != "busy-safe-key" {
		t.Fatalf("expected persisted key immediately while busy, got %q", got)
	}

	// Mark idle and verify the deferred reload flushes exactly once.
	busy.Store(false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && disposeCalls.Load() == 0 {
		time.Sleep(25 * time.Millisecond)
	}
	svc.openCode = ocapi.NewForPort(0, "")
	if got := disposeCalls.Load(); got != 1 {
		t.Fatalf("dispose calls after idle = %d, want 1", got)
	}
	if svc.HasPendingReload() {
		t.Fatal("expected pending reload flag to clear after flush")
	}
}

func TestNormalizeDocModelVariantsImageAndReasoning(t *testing.T) {
	doc := map[string]interface{}{
		"provider": map[string]interface{}{
			"hoxi": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"models": map[string]interface{}{
					"claude-opus-4-6": map[string]interface{}{
						"name":      "claude-opus-4-6",
						"reasoning": false,
						"modalities": map[string]interface{}{
							"input":  []interface{}{"text"},
							"output": []interface{}{"text"},
						},
					},
					"MiniMax-M2.7": map[string]interface{}{
						"name": "MiniMax-M2.7",
					},
					"explicit-no-image": map[string]interface{}{
						"name":       "claude-sonnet-4-6",
						"attachment": false,
						"reasoning":  false,
						"modalities": map[string]interface{}{
							"input":  []interface{}{"text"},
							"output": []interface{}{"text"},
						},
					},
				},
			},
			"openai": map[string]interface{}{
				"npm": "@ai-sdk/openai",
				"models": map[string]interface{}{
					"gpt-5-codex": map[string]interface{}{
						"name":       "gpt-5-codex (Codex)",
						"attachment": true,
						"reasoning":  true,
					},
				},
			},
		},
	}

	normalizeDocModelVariants(doc)

	hoxiModels := doc["provider"].(map[string]interface{})["hoxi"].(map[string]interface{})["models"].(map[string]interface{})
	opus := hoxiModels["claude-opus-4-6"].(map[string]interface{})
	if opus["attachment"] != true || !hasImageInputModality(opus) {
		t.Fatalf("expected legacy claude-opus-4-6 to gain image input modality, got: %+v", opus)
	}
	if opus["reasoning"] != true || opus["variants"] == nil {
		t.Fatalf("expected legacy claude-opus-4-6 to gain reasoning variants, got: %+v", opus)
	}

	mm := hoxiModels["MiniMax-M2.7"].(map[string]interface{})
	if mm["attachment"] != true || !hasImageInputModality(mm) || mm["reasoning"] != true {
		t.Fatalf("expected MiniMax-M2.7 to gain image & reasoning capabilities, got: %+v", mm)
	}

	noImg := hoxiModels["explicit-no-image"].(map[string]interface{})
	if noImg["attachment"] != false || hasImageInputModality(noImg) || noImg["reasoning"] != false {
		t.Fatalf("expected explicit attachment=false & reasoning=false to be preserved, got: %+v", noImg)
	}

	codex := doc["provider"].(map[string]interface{})["openai"].(map[string]interface{})["models"].(map[string]interface{})["gpt-5-codex"].(map[string]interface{})
	if !hasImageInputModality(codex) {
		t.Fatalf("expected attachment=true model to receive modalities.input=['text','image'], got: %+v", codex)
	}
}

func TestDefaultModelVariantsVersionParsing(t *testing.T) {
	legacyClaude := []string{
		"claude-3-7-sonnet-20250219",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4.5",
	}
	for _, mid := range legacyClaude {
		v := defaultModelVariants("anthropic", "@ai-sdk/anthropic", mid)
		high, _ := v["high"].(map[string]interface{})
		thinking, _ := high["thinking"].(map[string]interface{})
		if thinking["type"] != "enabled" || thinking["budgetTokens"] != 16000 {
			t.Fatalf("expected legacy budgetTokens for %q, got %+v", mid, high)
		}
	}

	modernClaude := []string{
		"claude-opus-4-6",
		"claude-sonnet-4.6",
		"claude-opus-4-7",
		"claude-sonnet-5",
		"claude-fable-6",
		"opus",
		"sonnet",
	}
	for _, mid := range modernClaude {
		v := defaultModelVariants("anthropic", "@ai-sdk/anthropic", mid)
		high, _ := v["high"].(map[string]interface{})
		thinking, _ := high["thinking"].(map[string]interface{})
		if thinking["type"] != "adaptive" || high["effort"] != "high" {
			t.Fatalf("expected modern adaptive+effort for %q, got %+v", mid, high)
		}
	}

	if !isLegacyGeminiBudgetModel("gemini-2.5-pro") || !isLegacyGeminiBudgetModel("gemini-1.5-flash") {
		t.Fatal("expected gemini 1.x/2.x to use legacy thinkingBudget")
	}
	if isLegacyGeminiBudgetModel("gemini-3-pro") || isLegacyGeminiBudgetModel("gemini-4-ultra") || isLegacyGeminiBudgetModel("gemini-5-pro") || isLegacyGeminiBudgetModel("route-1/gemini-3-pro") {
		t.Fatal("expected gemini 3.x+ to use modern thinkingLevel")
	}
	if isLegacyClaudeBudgetModel("route-1/claude-opus-4-6") {
		t.Fatal("expected route-1/claude-opus-4-6 to ignore route prefix version")
	}
}

func TestDeepMergeMapStripsNestedNullsOnMissingParent(t *testing.T) {
	dst := map[string]interface{}{}
	deepMergeMap(dst, map[string]interface{}{
		"mcp": map[string]interface{}{
			"stale": nil,
		},
		"agent": map[string]interface{}{
			"build": map[string]interface{}{
				"model": nil,
			},
			"custom": map[string]interface{}{
				"description": "Custom mode",
				"model":       nil,
			},
		},
	})
	if _, hasMCP := dst["mcp"]; hasMCP {
		t.Fatalf("expected all-null nested mcp patch not to create empty/null map, got %+v", dst["mcp"])
	}
	agentMap, _ := dst["agent"].(map[string]interface{})
	if _, hasBuild := agentMap["build"]; hasBuild {
		t.Fatalf("expected all-null agent.build patch not to persist null model, got %+v", agentMap["build"])
	}
	customMap, ok := agentMap["custom"].(map[string]interface{})
	if !ok || customMap["description"] != "Custom mode" {
		t.Fatalf("expected agent.custom to be kept, got %+v", agentMap)
	}
	if _, hasModel := customMap["model"]; hasModel {
		t.Fatalf("expected null model key to be omitted from agent.custom, got %+v", customMap)
	}
}
