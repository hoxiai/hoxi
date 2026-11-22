package agentcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCodexProviderConfigTOMLAndClientVersion(t *testing.T) {
	tomlData := []byte(`
model = "gpt-6.2-astra"
model_provider = "my-relay" # custom gateway

[model_providers.unused]
base_url = "https://unused.example.com/v1"

[model_providers."my-relay"]
base_url = "https://relay.example.com/v1"
env_key = "RELAY_API_KEY"
wire_api = "chat"
`)
	cfg := ParseCodexProviderConfigTOML(tomlData)
	if cfg.ProviderID != "my-relay" {
		t.Fatalf("ProviderID = %q, want my-relay", cfg.ProviderID)
	}
	if cfg.BaseURL != "https://relay.example.com/v1" {
		t.Fatalf("BaseURL = %q, want https://relay.example.com/v1", cfg.BaseURL)
	}
	if cfg.EnvKey != "RELAY_API_KEY" {
		t.Fatalf("EnvKey = %q, want RELAY_API_KEY", cfg.EnvKey)
	}
	if cfg.WireAPI != "chat" {
		t.Fatalf("WireAPI = %q, want chat", cfg.WireAPI)
	}

	// Non-HTTP schemes must be rejected
	badCfg := ParseCodexProviderConfigTOML([]byte(`openai_base_url = "file:///etc/passwd"`))
	if badCfg.BaseURL != "" {
		t.Fatalf("expected non-HTTP base_url to be rejected, got %q", badCfg.BaseURL)
	}

	cDir := t.TempDir()
	t.Setenv("CODEX_HOME", cDir)
	if err := os.WriteFile(filepath.Join(cDir, "models_cache.json"), []byte(`{"client_version":"v0.168.2","models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ver := ResolveCodexClientVersion(); ver != "0.168.2" {
		t.Fatalf("ResolveCodexClientVersion = %q, want 0.168.2", ver)
	}
}
