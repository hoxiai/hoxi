package opencode

import (
	"encoding/json"
	"strings"
	"testing"
)

func overlayGrokOptions(t *testing.T, env []string) map[string]any {
	t.Helper()
	var config struct {
		Provider map[string]struct {
			NPM     string         `json:"npm"`
			Options map[string]any `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], "OPENCODE_CONFIG_CONTENT=")), &config); err != nil {
		t.Fatal(err)
	}
	grok, ok := config.Provider["grok-cli"]
	if !ok {
		t.Fatalf("overlay has no grok-cli provider: %s", env[0])
	}
	return grok.Options
}

// Two Hoxi processes share ~/.hoxi/models.json; each OpenCode must still be
// pointed at its own gateway through the per-process overlay.
func TestBrowserOverlayPointsGrokBridgeAtOwnGateway(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "{}")
	for _, base := range []string{"http://127.0.0.1:5410", "http://127.0.0.1:5420/"} {
		env, cleanup, err := PrepareBrowserPlugin(base, "secret", nil)
		if err != nil {
			t.Fatal(err)
		}
		cleanup()
		want := strings.TrimRight(base, "/") + "/api/hoxi/bridge/grok-cli/v1"
		if got := overlayGrokOptions(t, env)["baseURL"]; got != want {
			t.Fatalf("base %s: grok-cli baseURL = %v, want %s", base, got, want)
		}
	}
}

func TestBrowserOverlayKeepsExistingProviderSettings(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"other":{"options":{"baseURL":"https://x.example"}},"grok-cli":{"npm":"custom","options":{"timeout":5,"baseURL":"http://127.0.0.1:1/api/hoxi/bridge/grok-cli/v1"}}}}`)
	env, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:5420", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	opts := overlayGrokOptions(t, env)
	if opts["baseURL"] != "http://127.0.0.1:5420/api/hoxi/bridge/grok-cli/v1" || opts["timeout"] != float64(5) {
		t.Fatalf("unexpected grok-cli options: %v", opts)
	}
	if !strings.Contains(env[0], `"other":{"options":{"baseURL":"https://x.example"}}`) || !strings.Contains(env[0], `"npm":"custom"`) {
		t.Fatalf("lost existing provider settings: %s", env[0])
	}
}

func TestBrowserOverlayRejectsMalformedProvider(t *testing.T) {
	for _, raw := range []string{`{"provider":null}`, `{"provider":[]}`, `{"provider":{"grok-cli":"x"}}`, `{"provider":{"grok-cli":{"options":[]}}}`} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_CONTENT", raw)
			if _, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:9000", "secret", nil); err == nil {
				cleanup()
				t.Fatal("accepted malformed provider config")
			}
		})
	}
}
