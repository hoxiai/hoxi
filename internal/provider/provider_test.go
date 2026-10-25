package provider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupIsolatedEnv(t *testing.T) (homeDir, workspaceDir string) {
	t.Helper()
	homeDir = t.TempDir()
	workspaceDir = t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("APPDATA", "")
	return homeDir, workspaceDir
}

func TestParseOpenAIModelsPayloadAndExtractAPIError(t *testing.T) {
	// 1. Standard OpenAI {"data": [...]}
	raw := []byte(`{"data":[{"id":"gpt-4o","name":"GPT-4o"},{"id":"claude-3-7-sonnet"},{"id":"gpt-4o"},{"id":"  "}]}`)
	models := parseOpenAIModelsPayload(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 deduplicated models, got %d", len(models))
	}
	if models[0].ID != "claude-3-7-sonnet" || models[0].Name != "claude-3-7-sonnet" || models[1].ID != "gpt-4o" {
		t.Errorf("unexpected sorted models: %+v", models)
	}

	// 2. {"models": [...]} and raw array fallback
	modelsAlt := parseOpenAIModelsPayload([]byte(`{"models":[{"id":"deepseek-chat","name":"DeepSeek"}]}`))
	if len(modelsAlt) != 1 || modelsAlt[0].ID != "deepseek-chat" {
		t.Errorf("unexpected models from wrapper.Models: %+v", modelsAlt)
	}
	modelsArr := parseOpenAIModelsPayload([]byte(`[{"id":"qwen-max"}]`))
	if len(modelsArr) != 1 || modelsArr[0].ID != "qwen-max" {
		t.Errorf("unexpected models from raw array: %+v", modelsArr)
	}

	// 3. extractAPIErrorMessage variations
	if got := extractAPIErrorMessage(401, []byte(`{"error":{"message":"Invalid API key provided"}}`)); got != "HTTP 401: Invalid API key provided" {
		t.Errorf("unexpected error message: %q", got)
	}
	if got := extractAPIErrorMessage(403, []byte(`{"error":"Forbidden access"}`)); got != "HTTP 403: Forbidden access" {
		t.Errorf("unexpected string error message: %q", got)
	}
	if got := extractAPIErrorMessage(400, []byte(`{"message":"Bad request param"}`)); got != "HTTP 400: Bad request param" {
		t.Errorf("unexpected top-level message: %q", got)
	}
	if got := extractAPIErrorMessage(502, []byte("upstream gateway failure")); got != "HTTP 502: upstream gateway failure" {
		t.Errorf("unexpected plain text error: %q", got)
	}
	if got := extractAPIErrorMessage(500, nil); got != "HTTP 500" {
		t.Errorf("unexpected empty error: %q", got)
	}
}

func TestExtractLLMTextContent(t *testing.T) {
	// Test extractLLMTextContent for OpenAI, Anthropic, and raw fallback
	if got := extractLLMTextContent([]byte(`{"choices":[{"message":{"content":"hello openai"}}]}`)); got != "hello openai" {
		t.Errorf("extractLLMTextContent OpenAI = %q", got)
	}
	if got := extractLLMTextContent([]byte(`{"content":[{"text":"hello anthropic"}]}`)); got != "hello anthropic" {
		t.Errorf("extractLLMTextContent Anthropic = %q", got)
	}
	if got := extractLLMTextContent([]byte(`plain text`)); got != "plain text" {
		t.Errorf("extractLLMTextContent fallback = %q", got)
	}
}

func TestExtractLLMTextContentNeverLeaksRawJSON(t *testing.T) {
	truncatedReasoningJSON := []byte(`{"choices":[{"finish_reason":"length","index":0,"logprobs":null,"message":{"content":"","reasoning_content":"The user wants a git commit message..."}}]}`)
	if extracted := extractLLMTextContent(truncatedReasoningJSON); extracted != "" {
		t.Fatalf("expected empty string when choices[0].message.content is empty, got %q", extracted)
	}
	validResp := []byte(`{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"feat(git): 新增底部一键提交并推送功能"}}]}`)
	if got := extractLLMTextContent(validResp); got != "feat(git): 新增底部一键提交并推送功能" {
		t.Fatalf("unexpected extracted content: %q", got)
	}
}

func TestConfigHelpersAndEnsureHoxiModelsConfig(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)

	// Prepare legacy opencode.jsonc and imported config.json in ~/.config/opencode
	ocDir := filepath.Join(homeDir, ".config", "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatalf("mkdir ocDir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(ocDir, "config.json"), []byte(`{
		"provider": {
			"imported-prov": {"options": {"apiKey": "sk-imported"}}
		}
	}`), 0o644)
	legacyPath := filepath.Join(ocDir, "opencode.jsonc")
	_ = os.WriteFile(legacyPath, []byte(`{
		"theme": "tokyo-night",
		"mcp": {
			"custom-mcp": {"type": "local", "command": ["npx", "custom-mcp"]}
		},
		"provider": {
			"imported-prov": {"options": {"apiKey": "sk-imported"}},
			"legacy-custom": {"options": {"baseURL": "https://legacy.example/v1"}}
		}
	}`), 0o644)

	cfgPath := EnsureHoxiModelsConfig(workspaceDir)
	if cfgPath != UserHoxiModelsPath() {
		t.Fatalf("EnsureHoxiModelsConfig = %q, want %q", cfgPath, UserHoxiModelsPath())
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy opencode.jsonc to be removed after migration")
	}

	svc := NewService(0, "pass", workspaceDir, nil)
	if want := filepath.Join(workspaceDir, ".hoxi", "models.json"); svc.WorkspaceHoxiModelsPath() != want {
		t.Fatalf("WorkspaceHoxiModelsPath = %q, want %q", svc.WorkspaceHoxiModelsPath(), want)
	}

	provs := svc.LoadHoxiProvidersMap()
	if _, ok := provs["legacy-custom"]; !ok {
		t.Fatalf("expected legacy-custom provider to be migrated into Hoxi config, got %+v", provs)
	}
	if _, ok := provs["imported-prov"]; ok {
		t.Fatalf("did not expect imported-prov to be duplicated into Hoxi config")
	}
	fullDoc := svc.loadHoxiConfigDoc()
	if gotTheme, _ := fullDoc["theme"].(string); gotTheme != "tokyo-night" {
		t.Fatalf("expected top-level theme from opencode.jsonc to be preserved, got %v", fullDoc["theme"])
	}
	if mcpMap, _ := fullDoc["mcp"].(map[string]interface{}); mcpMap == nil || mcpMap["custom-mcp"] == nil {
		t.Fatalf("expected top-level mcp config from opencode.jsonc to be preserved, got %v", fullDoc["mcp"])
	}

	// Test deepMergeMap, stripNullsMap, and the config write transaction.
	if err := svc.UpdateHoxiConfigDoc(func(doc map[string]interface{}) error {
		deepMergeMap(doc, map[string]interface{}{
			"provider": map[string]interface{}{
				"legacy-custom": nil, // delete key
				"new-prov": map[string]interface{}{
					"models": map[string]interface{}{
						"m1": map[string]interface{}{"name": "Model 1"},
					},
				},
			},
		})
		return nil
	}); err != nil {
		t.Fatalf("UpdateHoxiConfigDoc failed: %v", err)
	}

	reloaded := svc.LoadHoxiProvidersMap()
	if _, ok := reloaded["legacy-custom"]; ok {
		t.Fatalf("expected legacy-custom to be deleted after nil merge")
	}
	if _, ok := reloaded["new-prov"]; !ok {
		t.Fatalf("expected new-prov in reloaded config")
	}

	cleaned := stripNullsMap(map[string]interface{}{
		"keep": "yes",
		"drop": nil,
		"nested": map[string]interface{}{
			"a": 1,
			"b": nil,
		},
	})
	if _, hasDrop := cleaned["drop"]; hasDrop {
		t.Errorf("stripNullsMap did not remove top-level nil")
	}
	if nested, ok := cleaned["nested"].(map[string]interface{}); !ok || nested["b"] != nil || len(nested) != 1 {
		t.Errorf("stripNullsMap did not remove nested nil: %+v", cleaned)
	}

	// Test filterProviderModelsByHoxi
	upstreamProv := map[string]interface{}{
		"new-prov": map[string]interface{}{
			"models": map[string]interface{}{
				"m1":    map[string]interface{}{"name": "Model 1"},
				"extra": map[string]interface{}{"name": "Extra Model"},
			},
		},
	}
	filterProviderModelsByHoxi(upstreamProv, reloaded)
	modelsMap := upstreamProv["new-prov"].(map[string]interface{})["models"].(map[string]interface{})
	if _, hasM1 := modelsMap["m1"]; !hasM1 || len(modelsMap) != 1 {
		t.Fatalf("expected only m1 to remain after filterProviderModelsByHoxi, got %+v", modelsMap)
	}
}

func TestModelStatsStorePersistenceAndRecording(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	store := newModelStatsStore(workspaceDir)

	// Record probe results (success, api_error, timeout)
	store.recordProbeResult("openai", true, 120, "", "gpt-4o")
	store.recordProbeResult("openai", false, 500, "HTTP 401: bad key", "gpt-4o")
	store.recordProbeResult("openai", false, 15000, "context deadline exceeded (timeout)", "gpt-4o")

	// Record audit results (pass and fail)
	store.recordAuditResult("openai", "gpt-4o", 92, true, "Clean implementation")
	store.recordAuditResult("openai", "gpt-4o", 55, false, "")

	rec := store.records["openai"]
	if rec == nil {
		t.Fatalf("expected telemetry record for openai")
	}
	if rec.AuditTotalCount != 2 || rec.AuditPassCount != 1 || rec.HallucinationFlags != 1 {
		t.Fatalf("unexpected provider audit counts: %+v", rec)
	}
	if len(rec.RecentIncidents) != 3 {
		t.Fatalf("expected 3 incidents (api_error, timeout, code_audit_fail), got %+v", rec.RecentIncidents)
	}
	if rec.RecentIncidents[0].Type != "code_audit_fail" || rec.RecentIncidents[1].Type != "timeout" || rec.RecentIncidents[2].Type != "api_error" {
		t.Errorf("unexpected incident order/types: %+v", rec.RecentIncidents)
	}

	// Reload from disk into a fresh store instance
	reloadedStore := newModelStatsStore(workspaceDir)
	reloadedRec := reloadedStore.records["openai"]
	if reloadedRec == nil || reloadedRec.Models["gpt-4o"] == nil {
		t.Fatalf("expected telemetry record and model stats to persist on disk, got %+v", reloadedStore.records)
	}
	if reloadedRec.Models["gpt-4o"].AuditTotalCount != 2 || reloadedRec.Models["gpt-4o"].HallucinationFlags != 1 {
		t.Errorf("unexpected persisted model telemetry stats: %+v", reloadedRec.Models["gpt-4o"])
	}
}

func TestResolveStoredProviderKeyRejectsMismatchedBaseURL(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)
	authDir := filepath.Join(homeDir, ".local", "share", "opencode")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatalf("mkdir authDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"openai":{"key":"sk-secret-openai-key"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	svc := NewService(0, "pass", workspaceDir, nil)
	if err := svc.PersistHoxiConfigDoc(map[string]any{
		"provider": map[string]any{
			"custom-gw": map[string]any{
				"options": map[string]any{
					"baseURL": "https://gateway.trusted.example/v1",
					"apiKey":  "sk-custom-secret",
				},
			},
		},
	}); err != nil {
		t.Fatalf("persist config: %v", err)
	}

	// Official default baseURL for openai is allowed
	if got := svc.ResolveStoredProviderKey("openai", "https://api.openai.com/v1/"); got != "sk-secret-openai-key" {
		t.Fatalf("expected stored key for official default baseURL, got %q", got)
	}
	// Mismatched untrusted baseURL must not receive stored key
	if got := svc.ResolveStoredProviderKey("openai", "https://attacker.hoxi.ai/v1"); got != "" {
		t.Fatalf("expected empty key for mismatched baseURL, got %q", got)
	}
	// Configured custom provider baseURL is allowed
	if got := svc.ResolveStoredProviderKey("custom-gw", "https://gateway.trusted.example/v1"); got != "sk-custom-secret" {
		t.Fatalf("expected stored key for matching custom baseURL, got %q", got)
	}
	if got := svc.ResolveStoredProviderKey("custom-gw", "https://evil.hoxi.ai/v1"); got != "" {
		t.Fatalf("expected empty key for mismatched custom baseURL, got %q", got)
	}
	if _, key, _, _ := svc.resolveProviderConnection("custom-gw", "https://evil.hoxi.ai/v1", ""); key != "" {
		t.Fatalf("resolveProviderConnection must not release stored key to mismatched hintBaseURL, got %q", key)
	}
}

func TestIsLoopbackURL(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:11434/v1/models",
		"http://localhost:11434/v1/chat/completions",
		"http://[::1]:8080/v1",
	} {
		if !isLoopbackURL(u) {
			t.Errorf("expected %q to be recognized as loopback URL", u)
		}
	}
	for _, u := range []string{
		"http://localhost.attacker.com/v1",
		"https://api.openai.com/v1?redirect=http://127.0.0.1",
		"https://127.0.0.1.evil.com/v1",
	} {
		if isLoopbackURL(u) {
			t.Errorf("expected %q NOT to be recognized as loopback URL", u)
		}
	}
}

// A stale whole-document writer (as used by CLI import) must not erase a
// provider another writer saved after the stale snapshot was read.
func TestStaleConfigWriterCannotLoseSavedProvider(t *testing.T) {
	home, root := setupIsolatedEnv(t)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	svc := NewService(0, "test", root, nil)
	staleDoc := svc.LoadHoxiConfigDoc()
	r := httptest.NewRequest(http.MethodPost, "/api/hoxi/models-config", strings.NewReader(`{"providerID":"user-saved","config":{"options":{"apiKey":"synthetic-user-key"},"models":{"m":{}}}}`))
	w := httptest.NewRecorder()
	svc.handleModelsConfig(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("user save failed: %d %s", w.Code, w.Body.String())
	}
	providers, _ := staleDoc["provider"].(map[string]interface{})
	providers["cli-import"] = map[string]interface{}{"options": map[string]interface{}{"apiKey": "synthetic-cli-key"}, "models": map[string]interface{}{"m": map[string]interface{}{}}}
	staleDoc["provider"] = providers
	if err := svc.PersistHoxiConfigDoc(staleDoc); err != nil {
		t.Fatal(err)
	}
	doc := readJSONMapFile(userHoxiModelsPath())
	saved, ok := doc["provider"].(map[string]interface{})["user-saved"]
	if !ok {
		t.Fatal("stale writer overwrote a successful provider save and lost its key")
	}
	if key := saved.(map[string]interface{})["options"].(map[string]interface{})["apiKey"]; key != "synthetic-user-key" {
		t.Fatalf("saved key changed: %v", key)
	}
}

// models.json is shared by every Hoxi process; a bridge URL stored by (or for)
// another process must still reach this process's own gateway.
func TestLocalBridgeURLResolvesToOwnGateway(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	svc := NewService(0, "pass", workspaceDir, nil)
	svc.SetRelayBaseURL("http://127.0.0.1:5420")
	if err := svc.PersistHoxiConfigDoc(map[string]any{
		"provider": map[string]any{
			"grok-cli": map[string]any{
				"npm": "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": "http://127.0.0.1:5410/api/hoxi/bridge/grok-cli/v1",
					"apiKey":  "hoxi-local-grok-cli-bridge",
				},
			},
		},
	}); err != nil {
		t.Fatalf("persist config: %v", err)
	}
	const own = "http://127.0.0.1:5420/api/hoxi/bridge/grok-cli/v1"
	for _, hint := range []string{"", "http://127.0.0.1:61234/api/hoxi/bridge/grok-cli/v1"} {
		baseURL, key, _, _ := svc.resolveProviderConnection("grok-cli", hint, "")
		if baseURL != own || key != "hoxi-local-grok-cli-bridge" {
			t.Fatalf("hint %q resolved to %q (key %q), want own gateway %q", hint, baseURL, key, own)
		}
	}
	for _, raw := range []string{
		"http://127.0.0.1:11434/v1",
		"https://api.example.com/api/hoxi/bridge/grok-cli/v1",
		"http://192.168.1.2:5410/api/hoxi/bridge/grok-cli/v1",
	} {
		if got := svc.localizeBridgeURL(raw); got != raw {
			t.Fatalf("localizeBridgeURL rewrote non-local bridge %q to %q", raw, got)
		}
	}
	if got := NewService(0, "pass", workspaceDir, nil).localizeBridgeURL(own); got != own {
		t.Fatalf("without a gateway address the stored URL must be kept, got %q", got)
	}
}
