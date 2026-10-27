package opencode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinModesOverlayEnforcesReadOnlyAsk(t *testing.T) {
	raw := `{"agent":{"ask":{"model":"p/m","permission":{"edit":"allow","bash":"allow","webfetch":"allow"}},"custom":{"prompt":"mine"}}}`
	t.Setenv("OPENCODE_CONFIG_CONTENT", raw)
	env, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:9000", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	var config struct {
		Agent map[string]struct {
			Mode        string            `json:"mode"`
			Description string            `json:"description"`
			Prompt      string            `json:"prompt"`
			Model       string            `json:"model"`
			Permission  map[string]string `json:"permission"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], "OPENCODE_CONFIG_CONTENT=")), &config); err != nil {
		t.Fatal(err)
	}

	ask := config.Agent["ask"]
	if ask.Model != "p/m" {
		t.Fatalf("user model for ask was dropped: %+v", ask)
	}
	if ask.Mode != "primary" || ask.Description == "" || !strings.Contains(ask.Prompt, "read-only") {
		t.Fatalf("ask built-in definition incomplete: %+v", ask)
	}
	if len(ask.Permission) != 2 || ask.Permission["edit"] != "deny" || ask.Permission["bash"] != "deny" {
		t.Fatalf("ask must deny edit and bash regardless of user config, got %v", ask.Permission)
	}

	debug := config.Agent["debug"]
	if debug.Mode != "primary" || debug.Description == "" || debug.Prompt == "" {
		t.Fatalf("debug built-in definition incomplete: %+v", debug)
	}
	if debug.Permission != nil {
		t.Fatalf("debug should inherit global permissions, got %v", debug.Permission)
	}

	if custom := config.Agent["custom"]; custom.Prompt != "mine" {
		t.Fatalf("user-defined agent was altered: %+v", custom)
	}
}

func TestBuiltinModesRejectMalformedAgentWithoutLeakingOverlay(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"agent":"not-an-object"}`)
	env, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:9000", "secret", nil)
	if err == nil {
		cleanup()
		t.Fatalf("expected malformed agent config to fail, got %v", env)
	}
	if cleanup == nil {
		t.Fatal("error path must return a cleanup function for the overlay directory")
	}
	cleanup()
}
