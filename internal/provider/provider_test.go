package provider

import (
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

func TestCleanAndFallbackSummarizeTitle(t *testing.T) {
	rawZh := "<think>思考中...</think>\n标题：“修复网关 CORS 与单元测试”."
	if got := cleanSummarizedTitle(rawZh, "zh-CN"); got != "修复网关 CORS 与单元测试" {
		t.Errorf("cleanSummarizedTitle zh-CN = %q, want %q", got, "修复网关 CORS 与单元测试")
	}

	rawEn := `Title: "Optimize SSE Streaming & Worktree Isolation".`
	if got := cleanSummarizedTitle(rawEn, "en-US"); got != "Optimize SSE Streaming & Worktree Isolation" {
		t.Errorf("cleanSummarizedTitle en-US = %q, want %q", got, "Optimize SSE Streaming & Worktree Isolation")
	}

	if got := cleanSummarizedTitle("   <think>only thought</think>  ", "en-US"); got != "" {
		t.Errorf("expected empty cleaned title, got %q", got)
	}

	fbZh := fallbackSummarizeTitle("请帮我排查 desktop_gateway.go 里的 CORS 配置问题。顺便加上单元测试。", "zh-CN")
	if fbZh == "" || fbZh == "新任务会话" {
		t.Fatalf("expected meaningful zh fallback title, got %q", fbZh)
	}

	fbEn := fallbackSummarizeTitle("```go\nfmt.Println()\n```\nFix race condition in worktree manager. Also update docs.", "en-US")
	if fbEn != "Fix race condition in worktree manager" {
		t.Errorf("fallbackSummarizeTitle en-US = %q, want %q", fbEn, "Fix race condition in worktree manager")
	}

	if got := fallbackSummarizeTitle("<think>thinking</think>", "zh-CN"); got != "新任务会话" {
		t.Errorf("expected default zh fallback, got %q", got)
	}
	if got := fallbackSummarizeTitle("   ", "en-US"); got != "New Task Session" {
		t.Errorf("expected default en fallback, got %q", got)
	}
}

func TestEvaluateStaticCodeRulesAndParseObserverAudit(t *testing.T) {
	bigFileLines := strings.Repeat("const x = 1;\n", 505)
	diffs := []auditDiffFile{
		{
			File:      "web/src/App.tsx",
			Before:    "const a = 1;\n",
			After:     "const a = 2;\n// TODO: implement rest of logic\n",
			Additions: 2,
			Deletions: 1,
		},
		{
			File:      "internal/foo/big.go",
			Before:    "package foo\n",
			After:     bigFileLines,
			Additions: 505,
		},
		{
			File:      "web/src/components/icons/svg.tsx",
			After:     bigFileLines,
			Additions: 505,
		},
	}

	deductionZh, issuesZh := evaluateStaticCodeRules(diffs, 2, "TS2322 type mismatch", "zh-CN")
	if deductionZh < 60 || len(issuesZh) != 3 {
		t.Fatalf("expected deduction >= 60 and 3 issues (LSP + lazy + >500 lines, excluding icons), got deduction=%d issues=%v", deductionZh, issuesZh)
	}

	deductionEn, issuesEn := evaluateStaticCodeRules(diffs, 3, "undefined symbol", "en-US")
	if deductionEn < 60 || len(issuesEn) != 3 {
		t.Fatalf("expected English static rules issues, got deduction=%d issues=%v", deductionEn, issuesEn)
	}

	score, summary, parsedIssues := parseObserverAuditJSON("```json\n{\"score\": 68, \"pass\": false, \"summary\": \"Missing error handling\", \"issues\": [\"Unchecked null pointer\"]}\n```")
	if score != 68 || summary != "Missing error handling" || len(parsedIssues) != 1 {
		t.Fatalf("unexpected parsed observer JSON: score=%d summary=%q issues=%v", score, summary, parsedIssues)
	}

	if s, _, _ := parseObserverAuditJSON("92"); s != 92 {
		t.Errorf("expected numeric score 92, got %d", s)
	}
	if s, sum, _ := parseObserverAuditJSON("Looks good overall"); s != 85 || sum != "Looks good overall" {
		t.Errorf("expected fallback score 85, got %d (%q)", s, sum)
	}

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

	// Test deepMergeMap, stripNullsMap, and PersistHoxiConfigDoc
	doc := svc.LoadHoxiConfigDoc()
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
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatalf("PersistHoxiConfigDoc failed: %v", err)
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

func TestProviderTelemetryStorePersistenceAndRecording(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	store := newProviderTelemetryStore(workspaceDir)

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
	reloadedStore := newProviderTelemetryStore(workspaceDir)
	reloadedRec := reloadedStore.records["openai"]
	if reloadedRec == nil || reloadedRec.Models["gpt-4o"] == nil {
		t.Fatalf("expected telemetry record and model stats to persist on disk, got %+v", reloadedStore.records)
	}
	if reloadedRec.Models["gpt-4o"].AuditTotalCount != 2 || reloadedRec.Models["gpt-4o"].HallucinationFlags != 1 {
		t.Errorf("unexpected persisted model telemetry stats: %+v", reloadedRec.Models["gpt-4o"])
	}
}
