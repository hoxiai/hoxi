package handoff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestImportCodexCLIProviderHonorsCustomConfigTOMLBaseURLAndWireAPI(t *testing.T) {
	homeDir := t.TempDir()
	hoxiHome := t.TempDir()
	wsDir := t.TempDir()
	codexDir := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", hoxiHome)
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv("HOXI_CODEX_BIN", filepath.Join(homeDir, "nonexistent-codex"))
	t.Setenv("CUSTOM_CODEX_GW_KEY", "sk-custom-codex-gateway")

	configTOML := `
model = "gpt-6.2-astra"
model_provider = "corp-gw"

[model_providers.corp-gw]
base_url = "https://gw.corp.example/v1"
env_key = "CUSTOM_CODEX_GW_KEY"
wire_api = "chat"
`
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(configTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "models_cache.json"), []byte(`{"client_version":"0.165.0","models":[{"slug":"gpt-6.2-astra","display_name":"GPT-6.2 Astra"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	provSvc := provider.NewService(0, "", wsDir, nil)
	svc := NewService(0, "", wsDir, provSvc)
	resp := svc.importCodexCLIProvider()
	if !resp.OK {
		t.Fatalf("expected custom config.toml Codex import to succeed, got %+v", resp)
	}

	oaiProv, ok := svc.loadHoxiProvidersMap()["openai"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected openai provider in models.json")
	}
	if gotNPM := asString(oaiProv["npm"]); gotNPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %q, want @ai-sdk/openai-compatible for wire_api=chat", gotNPM)
	}
	opts, _ := oaiProv["options"].(map[string]interface{})
	if gotURL := asString(opts["baseURL"]); gotURL != "https://gw.corp.example/v1" {
		t.Fatalf("baseURL = %q, want https://gw.corp.example/v1", gotURL)
	}
	if gotKey := asString(opts["apiKey"]); gotKey != "sk-custom-codex-gateway" {
		t.Fatalf("apiKey = %q, want sk-custom-codex-gateway", gotKey)
	}
	if gotVer := asString(opts["codexVersion"]); gotVer != "0.165.0" {
		t.Fatalf("codexVersion = %q, want 0.165.0", gotVer)
	}
}
