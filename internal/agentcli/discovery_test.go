package agentcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func TestCodeBuddyAndQoderModelDiscovery(t *testing.T) {
	if mult, free := parseCodeBuddyCredits("x0.00 credits"); !free || mult != "0x" {
		t.Fatalf("expected x0.00 credits to be free 0x, got %q / %v", mult, free)
	}
	if mult, free := parseCodeBuddyCredits("x0.29 credits"); free || mult != "0.29x" {
		t.Fatalf("expected x0.29 credits to be 0.29x, got %q / %v", mult, free)
	}
	cbBuilt := buildCodeBuddyModelItems("", defaultCodeBuddyModelSlugs())
	var sawCBFreeHy3, sawCBFreeDS, sawCBHy4 bool
	for _, it := range cbBuilt {
		if it.ID == "hy3" && it.Free && it.Multiplier == "0x" && it.ContextLimit == 192000 {
			sawCBFreeHy3 = true
		}
		if it.ID == "deepseek-v4.1-flash" && it.Free && it.Multiplier == "0x" && it.ContextLimit == 1000000 {
			sawCBFreeDS = true
		}
		if it.ID == "hy4-preview" && !it.Free && it.Multiplier == "0.29x" && it.InputCost > 0 {
			sawCBHy4 = true
		}
	}
	if !sawCBFreeHy3 || !sawCBFreeDS || !sawCBHy4 {
		t.Fatalf("expected CodeBuddy free (0x) and multiplier metadata, got: %+v", cbBuilt)
	}

	cbModels := ParseCodeBuddyModelsOutput("  --model <model>  Currently supported: (default-model, hy4-preview, glm-5.3, gpt-6-astra)\n")
	if len(cbModels) != 4 || cbModels[1] != "hy4-preview" || cbModels[3] != "gpt-6-astra" {
		t.Fatalf("unexpected CodeBuddy parsed models: %v", cbModels)
	}
	tmpDir := t.TempDir()
	intlBin := execstub.Write(t, tmpDir, "qodercli", "printf 'MODEL\\nAuto\\nclaude-sonnet-4-6\\n'\n")
	cnBin := execstub.Write(t, tmpDir, "qoderclicn", "printf 'MODEL\\nAuto\\nQwen3.8-Max\\nQwen3.8-Flash\\n'\n")
	t.Setenv("QODER_BIN", intlBin)
	t.Setenv("QODER_CN_BIN", cnBin)
	// execstub avoids the macOS first-exec scan of fresh scripts; keep headroom
	// over the production probe timeout for a loaded full-suite run anyway.
	prevTimeout := qoderProbeTimeout
	qoderProbeTimeout = 30 * time.Second
	t.Cleanup(func() { qoderProbeTimeout = prevTimeout })

	items := DiscoverQoderModelsWithRefresh(true)
	var sawCN, sawCNFree, sawIntl, sawHeader bool
	for _, it := range items {
		if strings.EqualFold(it.ID, "model") || strings.HasSuffix(strings.ToLower(it.ID), ":model") {
			sawHeader = true
		}
		if it.ID == "cn:Qwen3.8-Max" && it.Multiplier == "0.5x" && it.InputCost > 0 {
			sawCN = true
		}
		if it.ID == "cn:Qwen3.8-Flash" && it.Free && it.Multiplier == "0x" {
			sawCNFree = true
		}
		if it.ID == "intl:claude-sonnet-4-6" && it.Multiplier == "1.1x" {
			sawIntl = true
		}
	}
	if sawHeader || !sawCN || !sawCNFree || !sawIntl {
		t.Fatalf("expected both CN (with free 0x & 0.5x) and Intl Qoder models without MODEL header, got: %+v", items)
	}

	resolvedBin, cleanModel := ResolveQoderBinaryForModel("cn:Qwen3.8-Max")
	if resolvedBin != cnBin || cleanModel != "Qwen3.8-Max" {
		t.Fatalf("expected CN bin %q and clean model Qwen3.8-Max, got %q / %q", cnBin, resolvedBin, cleanModel)
	}
	resolvedIntl, cleanIntl := ResolveQoderBinaryForModel("intl:claude-sonnet-4-6")
	if resolvedIntl != intlBin || cleanIntl != "claude-sonnet-4-6" {
		t.Fatalf("expected Intl bin %q and clean model claude-sonnet-4-6, got %q / %q", intlBin, resolvedIntl, cleanIntl)
	}

	if got := FormatClaudeModelName("claude-sonnet-4-6-20251001-thinking"); got != "Claude Sonnet 4.6" {
		t.Fatalf("FormatClaudeModelName with date before suffix = %q, want Claude Sonnet 4.6", got)
	}
	if got := FormatClaudeModelName("claude-fable-6-1"); got != "Claude Fable 6.1" {
		t.Fatalf("FormatClaudeModelName(claude-fable-6-1) = %q, want Claude Fable 6.1", got)
	}
}

func TestGeminiCLIModelDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GEMINI_HOME", tmpDir)
	t.Setenv("GEMINI_BIN", "")
	if got := ResolveGeminiBinary(); got != "" {
		t.Fatalf("expected empty gemini binary when GEMINI_HOME has no bin/gemini, got %q", got)
	}

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	gemBin := execstub.Write(t, binDir, "gemini", "exit 0\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(`{"model":{"name":"gemini-2.5-pro-exp"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveGeminiBinary(); got != gemBin {
		t.Fatalf("expected ResolveGeminiBinary=%q, got %q", gemBin, got)
	}
	models := DiscoverGeminiModelsWithRefresh(true)
	if len(models) < 6 {
		t.Fatalf("expected at least 6 Gemini CLI models, got %d", len(models))
	}
	if models[0].ID != "auto" || models[0].Runtime != RuntimeGeminiCLI {
		t.Fatalf("expected first model auto for gemini-cli, got %+v", models[0])
	}
	if models[1].ID != "gemini-2.5-pro-exp" || models[1].Name != "Gemini 2.5 Pro Exp" {
		t.Fatalf("expected custom model from settings.json, got %+v", models[1])
	}

	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_GENERATIVE_AI_API_KEY", "")
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(`{"security":{"auth":{"selectedType":"oauth-personal"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if HasGeminiCLIDirectAuth() {
		t.Fatal("expected HasGeminiCLIDirectAuth() == false for oauth-personal without API key")
	}
	if !IsAgyOnlyGeminiModel("gemini-4-carbon") || !IsAgyOnlyGeminiModel("gemini-4-argon") || !IsAgyOnlyGeminiModel("gemini-3.8-flash") || IsAgyOnlyGeminiModel("gemini-2.5-pro") {
		t.Fatal("unexpected IsAgyOnlyGeminiModel classification")
	}
	var sawCarbon bool
	for _, it := range models {
		if it.ID == "gemini-4-carbon" && it.Name == "Gemini 4 Carbon" && it.OutputLimit == 131072 {
			sawCarbon = true
		}
	}
	if !sawCarbon {
		t.Fatalf("expected gemini-4-carbon with 131072 output limit in Gemini CLI catalog, got %+v", models)
	}

	// Verify standalone ~/.gemini/antigravity-cli/bin/agy resolution and dynamic Gemini model refresh.
	agyCliBinDir := filepath.Join(tmpDir, "antigravity-cli", "bin")
	if err := os.MkdirAll(agyCliBinDir, 0755); err != nil {
		t.Fatal(err)
	}
	agyCliBin := execstub.Write(t, agyCliBinDir, "agy", "printf 'gemini-4-carbon-low   Gemini 4 Carbon (Low)\\ngemini-4-carbon-high  Gemini 4 Carbon (High)\\ngemini-4-diamond-high Gemini 4 Diamond (High)\\nclaude-opus-5-5-high  Claude Opus 5.5 (High)\\n'\n")
	execstub.Write(t, binDir, "agy", "exit 1\n")
	if gotAgy := ResolveAgyBinary(); gotAgy != agyCliBin {
		t.Fatalf("expected ResolveAgyBinary to prefer antigravity-cli/bin/agy (%q), got %q", agyCliBin, gotAgy)
	}
	refreshed := DiscoverGeminiModelsWithRefresh(true)
	var sawDiamond, sawClaudeInGem, sawRawSuffix bool
	for _, it := range refreshed {
		if it.ID == "gemini-4-diamond" && it.OutputLimit == 131072 {
			sawDiamond = true
		}
		if strings.HasPrefix(it.ID, "claude-") {
			sawClaudeInGem = true
		}
		if it.ID == "gemini-4-diamond-high" || it.ID == "gemini-4-carbon-high" {
			sawRawSuffix = true
		}
	}
	if !sawDiamond || sawClaudeInGem || sawRawSuffix {
		t.Fatalf("expected dynamic agy gemini base slug without non-gemini or raw effort suffixes, got %+v", refreshed)
	}

	t.Setenv("GEMINI_API_KEY", "AIzaSyTestKey123456")
	if !HasGeminiCLIDirectAuth() {
		t.Fatal("expected HasGeminiCLIDirectAuth() == true when GEMINI_API_KEY is set")
	}
}

func TestWithUserBinPathPrependsMissingDirs(t *testing.T) {
	env := []string{"FOO=1", "PATH=/usr/bin:/opt/homebrew/bin"}
	got := WithUserBinPath(env)
	if env[1] != "PATH=/usr/bin:/opt/homebrew/bin" {
		t.Fatalf("input env was mutated: %#v", env)
	}
	path := ""
	for _, kv := range got {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if !strings.HasSuffix(path, "/usr/bin:/opt/homebrew/bin") {
		t.Fatalf("original PATH order lost: %q", path)
	}
	if strings.Count(path, "/opt/homebrew/bin") != 1 {
		t.Fatalf("/opt/homebrew/bin duplicated: %q", path)
	}
	if first := filepath.SplitList(path)[0]; first == "/usr/bin" {
		t.Fatalf("user bin dirs were not prepended: %q", path)
	}
	if !strings.Contains(path, filepath.Join(".hoxi", "bin")) {
		t.Fatalf("expected ~/.hoxi/bin in prepended PATH so native CLIs can invoke hoxi code-index, got %q", path)
	}
	if !strings.Contains(path, filepath.Join(".gemini", "antigravity-cli", "bin")) {
		t.Fatalf("expected ~/.gemini/antigravity-cli/bin in prepended PATH, got %q", path)
	}
}

func TestWithUserBinPathAddsPATHWhenMissing(t *testing.T) {
	got := WithUserBinPath([]string{"FOO=1"})
	found := false
	for _, kv := range got {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("PATH was not added: %#v", got)
	}

	// Windows environment variables often use mixed-case "Path=".
	winEnv := []string{"SystemRoot=C:\\Windows", "Path=C:\\Windows\\system32"}
	winGot := WithUserBinPath(winEnv)
	if len(winGot) != 2 || !strings.HasPrefix(winGot[1], "Path=") || !strings.HasSuffix(winGot[1], "C:\\Windows\\system32") {
		t.Fatalf("expected mixed-case Path= to be updated in place, got %#v", winGot)
	}
}

func TestCodexCatalogDiscoveryPriorityVisibilityAndUpgrade(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	// Stale on-disk cache (0.42.0) + retired model in config.toml + section model noise.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(
		"# user comment\nmodel = \"gpt-5-codex\" # legacy selection\n\n[profiles.legacy]\nmodel = \"o4-mini\"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "models_cache.json"), []byte(
		`{"client_version":"0.42.0","models":[{"slug":"gpt-5-codex"},{"slug":"o3"}]}`,
	), 0o600); err != nil {
		t.Fatal(err)
	}

	bundledFile := filepath.Join(t.TempDir(), "bundled.json")
	if err := os.WriteFile(bundledFile, []byte(`{
		"models": [
			{
				"slug": "gpt-6-astra",
				"display_name": "GPT-6-Astra",
				"default_reasoning_level": "low",
				"supported_reasoning_levels": [{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}],
				"visibility": "list",
				"priority": 2,
				"context_window": 272000,
				"max_context_window": 872000,
				"input_modalities": ["text", "image"]
			},
			{
				"slug": "gpt-6.1-sol",
				"display_name": "GPT-6.1-Sol",
				"default_reasoning_level": "low",
				"supported_reasoning_levels": [{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}],
				"visibility": "list",
				"priority": 1,
				"context_window": 272000,
				"max_context_window": 872000,
				"input_modalities": ["text", "image"]
			},
			{
				"slug": "gpt-5.5",
				"display_name": "GPT-5.5",
				"default_reasoning_level": "medium",
				"supported_reasoning_levels": [{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}],
				"visibility": "list",
				"priority": 13,
				"upgrade": {"model": "gpt-6-sol"}
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
	t.Setenv("TEST_CODEX_BUNDLED_JSON", bundledFile)
	binDir := t.TempDir()
	codexStub := execstub.Write(t, binDir, "codex", `
if [ "$1" = "--version" ]; then
  printf 'codex-cli 0.169.0\n'
  exit 0
fi
if [ "$1" = "debug" ] && [ "$2" = "models" ] && [ "$3" = "--bundled" ]; then
  mkdir -p "$CODEX_HOME/tmp"
  cat "$TEST_CODEX_BUNDLED_JSON"
  exit 0
fi
exit 1
`)
	t.Setenv("HOXI_CODEX_BIN", codexStub)

	snap := DiscoverCodexCatalog(true)
	if snap.Version != "0.169.0" {
		t.Fatalf("expected version 0.169.0, got %q", snap.Version)
	}
	// Retired config.toml model ("gpt-5-codex") must promote to priority-1 "gpt-6.1-sol",
	// stale 0.42.0 cache models and hidden models must be excluded.
	if snap.DefaultModel != "gpt-6.1-sol" {
		t.Fatalf("expected DefaultModel=gpt-6.1-sol, got %q", snap.DefaultModel)
	}
	if len(snap.Models) != 3 || snap.Models[0].Slug != "gpt-6.1-sol" || snap.Models[1].Slug != "gpt-6-astra" || snap.Models[2].Slug != "gpt-5.5" {
		t.Fatalf("expected priority-ordered visible models [gpt-6.1-sol, gpt-6-astra, gpt-5.5], got %+v", snap.Models)
	}
	if len(snap.Models[0].Efforts) != 5 || snap.Models[0].Efforts[4] != "max" {
		t.Fatalf("expected ultra mapped into low..max efforts, got %v", snap.Models[0].Efforts)
	}
	// Verify real $CODEX_HOME was never polluted with $CODEX_HOME/tmp.
	if _, err := os.Stat(filepath.Join(codexHome, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("expected probeCodexBundledCatalog to isolate CODEX_HOME and never write to user codexHome")
	}

	// Deprecated model with explicit `upgrade` in config.toml also promotes to priority-1 model.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-5.5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverCodexCatalog(false).DefaultModel; got != "gpt-6.1-sol" {
		t.Fatalf("expected deprecated gpt-5.5 config to promote to gpt-6.1-sol, got %q", got)
	}

	// Active non-deprecated model in config.toml is preserved.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-6-astra\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverCodexCatalog(false).DefaultModel; got != "gpt-6-astra" {
		t.Fatalf("expected active gpt-6-astra config to stay DefaultModel, got %q", got)
	}

	// Server-refreshed models_cache.json with >= client_version takes precedence and surfaces new server models.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "models_cache.json"), []byte(`{
		"client_version": "0.170.0",
		"models": [
			{"slug": "gpt-6.2-sol", "display_name": "GPT-6.2-Sol", "visibility": "list", "priority": 1},
			{"slug": "gpt-6-astra", "display_name": "GPT-6-Astra", "visibility": "list", "priority": 2}
		]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	serverSnap := DiscoverCodexCatalog(false)
	if serverSnap.Version != "0.170.0" || serverSnap.DefaultModel != "gpt-6.2-sol" || serverSnap.Models[0].Slug != "gpt-6.2-sol" {
		t.Fatalf("expected newer server models_cache.json to lead with gpt-6.2-sol, got %+v", serverSnap)
	}
}

func TestParseOpenCodeModelsVerboseAndPlainOutput(t *testing.T) {
	verboseRaw := `anthropic/claude-sonnet-4-5
{
  "id": "claude-sonnet-4-5",
  "providerID": "anthropic",
  "name": "Claude Sonnet 4.5",
  "status": "active",
  "cost": { "input": 3, "output": 15 },
  "limit": { "context": 200000, "output": 64000 },
  "capabilities": { "reasoning": true, "attachment": true, "input": { "image": true } },
  "variants": { "low": {}, "high": {} }
}
openai/gpt-5
{
  "id": "gpt-5",
  "providerID": "openai",
  "name": "GPT-5",
  "status": "active",
  "cost": { "input": 0, "output": 0 },
  "limit": { "context": 400000, "output": 128000 },
  "capabilities": { "reasoning": true }
}
opencode/deprecated-free
{
  "id": "deprecated-free",
  "providerID": "opencode",
  "name": "Old Free",
  "status": "deprecated",
  "cost": { "input": 0, "output": 0 }
}
opencode/beam-free
{
  "id": "beam-free",
  "providerID": "opencode",
  "name": "Beam Free",
  "status": "active",
  "cost": { "input": 0, "output": 0, "cache": { "read": 0, "write": 0 } },
  "limit": { "context": 1048576, "output": 131072 },
  "capabilities": { "reasoning": true, "attachment": false, "input": { "image": false } },
  "variants": { "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {} }
}
opencode/big-pickle
{
  "id": "big-pickle",
  "providerID": "opencode",
  "name": "Big Pickle",
  "status": "active",
  "cost": { "input": 0, "output": 0 },
  "limit": { "context": 200000, "output": 32000 },
  "capabilities": { "reasoning": true },
  "variants": {}
}
`
	items := ParseOpenCodeModelsOutput(verboseRaw)
	if len(items) != 4 {
		t.Fatalf("expected 4 active models (deprecated excluded), got %d: %+v", len(items), items)
	}
	if items[0].ID != "opencode/beam-free" || !items[0].Free || items[0].Multiplier != "0x" || items[0].Name != "Beam Free" {
		t.Fatalf("expected opencode/beam-free ranked first as free 0x, got %+v", items[0])
	}
	if items[0].ContextLimit != 1048576 || items[0].OutputLimit != 131072 || len(items[0].Efforts) != 5 || items[0].DefaultEffort != "medium" {
		t.Fatalf("unexpected beam-free metadata: %+v", items[0])
	}
	if items[1].ID != "opencode/big-pickle" || !items[1].Free || items[1].Multiplier != "0x" {
		t.Fatalf("expected opencode/big-pickle ranked second as free 0x, got %+v", items[1])
	}
	if items[2].ID != "anthropic/claude-sonnet-4-5" || items[2].Free || !items[2].Image || items[2].InputCost != 3 {
		t.Fatalf("expected paid anthropic model after free models, got %+v", items[2])
	}
	if items[3].ID != "openai/gpt-5" || items[3].Free {
		t.Fatalf("zero-cost third-party placeholder must not be marked free, got %+v", items[3])
	}

	plainItems := ParseOpenCodeModelsOutput("anthropic/claude-sonnet-4-5\nopencode/big-pickle\nopencode/deepseek-v4.1-flash-free\n")
	if len(plainItems) != 3 {
		t.Fatalf("expected 3 plain models, got %d", len(plainItems))
	}
	if !plainItems[0].Free || !plainItems[1].Free || plainItems[2].Free {
		t.Fatalf("expected built-in opencode free models ranked first in plain mode, got %+v", plainItems)
	}
}

func TestDiscoverOpenCodeCLIModelsFallbackAndWorkBuddyPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Stub rejects --verbose and succeeds on plain `models` to verify fallback.
	ocStub := execstub.Write(t, tmpDir, "opencode", `
if [ "$1" = "models" ] && [ "$2" = "--verbose" ]; then
  exit 1
fi
if [ "$1" = "models" ]; then
  printf 'opencode/beam-free\nanthropic/claude-sonnet-4-5\n'
  exit 0
fi
exit 1
`)
	t.Setenv("HOXI_OPENCODE_BIN", ocStub)
	prevTimeout := ocCLIProbeTimeout
	ocCLIProbeTimeout = 15 * time.Second
	t.Cleanup(func() { ocCLIProbeTimeout = prevTimeout })

	items := DiscoverOpenCodeCLIModelsWithRefresh(true)
	if len(items) != 2 || items[0].ID != "opencode/beam-free" || !items[0].Free || items[1].Free {
		t.Fatalf("expected fallback plain OpenCode discovery with free model first, got %+v", items)
	}

	fakeHome := t.TempDir()
	wbBinDir := filepath.Join(fakeHome, ".workbuddy", "bin")
	if err := os.MkdirAll(wbBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wbBin := execstub.Write(t, wbBinDir, "codebuddy", "exit 0\n")
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PATH", "")
	t.Setenv("CODEBUDDY_BIN", "")
	t.Setenv("CODEBUDDY_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	if got := ResolveCodeBuddyBinary(); got != wbBin {
		t.Fatalf("ResolveCodeBuddyBinary() = %q, want %q from ~/.workbuddy/bin", got, wbBin)
	}

	// HOXI_OPENCODE="opencode.exe" must be treated as the default name, not a literal path.
	hoxiHome := t.TempDir()
	hoxiBinDir := filepath.Join(hoxiHome, "bin")
	if err := os.MkdirAll(hoxiBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	managedOC := execstub.Write(t, hoxiBinDir, "opencode", "exit 0\n")
	t.Setenv("HOXI_HOME", hoxiHome)
	t.Setenv("HOXI_OPENCODE_BIN", "")
	t.Setenv("HOXI_OPENCODE", "opencode.exe")
	if got := ResolveOpenCodeBinary(); got != managedOC {
		t.Fatalf("ResolveOpenCodeBinary() with HOXI_OPENCODE=opencode.exe = %q, want %q", got, managedOC)
	}

	// Qoder login status distinguishes authenticated vs unauthenticated binaries.
	unauthQoder := execstub.Write(t, tmpDir, "qoderclicn-unauth", "printf 'Not logged in. Please run qoderclicn login.\\n'\n")
	t.Setenv("QODER_BIN", "")
	t.Setenv("QODER_CN_BIN", unauthQoder)
	prevQTimeout := qoderProbeTimeout
	qoderProbeTimeout = 15 * time.Second
	t.Cleanup(func() { qoderProbeTimeout = prevQTimeout })
	unauthModels, authed := DiscoverQoderStatusWithRefresh(true)
	if authed || len(unauthModels) == 0 {
		t.Fatalf("expected unauthenticated Qoder to report authed=false with fallback defaults, got authed=%v len=%d", authed, len(unauthModels))
	}
}
