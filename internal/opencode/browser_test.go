package opencode

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserOverlay(t *testing.T) {
	raw := `{"model":"test/model","plugin":["existing",["configured",{"enabled":true}]],"permission":{"edit":"ask"}}`
	t.Setenv("OPENCODE_CONFIG_CONTENT", raw)
	env, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:9000", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if len(env) != 3 || env[1] != "HOXI_BROWSER_URL=http://127.0.0.1:9000" || env[2] != "HOXI_BROWSER_TOKEN=secret" {
		t.Fatalf("unexpected overlay: %v", env)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], "OPENCODE_CONFIG_CONTENT=")), &config); err != nil {
		t.Fatal(err)
	}
	var original map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &original)
	for _, key := range []string{"model", "permission"} {
		if !bytes.Equal(config[key], original[key]) {
			t.Fatalf("changed %s", key)
		}
	}
	if string(config["$schema"]) != `"https://opencode.ai/config.json"` {
		t.Fatal("missing schema")
	}
	var plugins []json.RawMessage
	_ = json.Unmarshal(config["plugin"], &plugins)
	if len(plugins) != 3 || string(plugins[0]) != `"existing"` || string(plugins[1]) != `["configured",{"enabled":true}]` {
		t.Fatalf("lost existing plugins: %s", config["plugin"])
	}
	var location string
	_ = json.Unmarshal(plugins[2], &location)
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "file" || filepath.Base(u.Path) != "plugin.mjs" {
		t.Fatalf("invalid plugin location: %s", location)
	}
	data, err := os.ReadFile(u.Path)
	if err != nil || !bytes.Equal(data, browserPlugin) {
		t.Fatalf("embedded asset mismatch: %v", err)
	}
	for path, mode := range map[string]os.FileMode{u.Path: 0600, filepath.Dir(u.Path): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("invalid private permissions: %s %v", path, err)
		}
	}
	if os.Getenv("OPENCODE_CONFIG_CONTENT") != raw {
		t.Fatal("mutated process environment")
	}
	cleanup()
	cleanup()
	if _, err := os.Stat(filepath.Dir(u.Path)); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove overlay: %v", err)
	}
}

func TestBrowserOverlayInvalidConfig(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", `"text"`, "true", "1", "{", `{} {}`, `{"plugin":null}`, `{"plugin":{}}`, `{"plugin":"x"}`} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_CONTENT", raw)
			if _, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:9000", "secret"); err == nil {
				cleanup()
				t.Fatal("accepted malformed config")
			}
		})
	}
}

func TestBrowserOverlayWithoutConfig(t *testing.T) {
	old, exists := os.LookupEnv("OPENCODE_CONFIG_CONTENT")
	_ = os.Unsetenv("OPENCODE_CONFIG_CONTENT")
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv("OPENCODE_CONFIG_CONTENT", old)
		} else {
			_ = os.Unsetenv("OPENCODE_CONFIG_CONTENT")
		}
	})
	for _, raw := range []string{"", "{}", `{"plugin":[]}`} {
		if raw != "" {
			_ = os.Setenv("OPENCODE_CONFIG_CONTENT", raw)
		}
		env, cleanup, err := PrepareBrowserPlugin("http://127.0.0.1:9000", "secret")
		if err != nil || len(env) != 3 {
			t.Fatalf("valid empty config rejected: %v", err)
		}
		cleanup()
	}
}

func TestBrowserOverlayInvalidRuntime(t *testing.T) {
	for _, input := range [][2]string{{"", "secret"}, {"file:///tmp", "secret"}, {"http://localhost?x=1", "secret"}, {"http://localhost", ""}} {
		if _, cleanup, err := PrepareBrowserPlugin(input[0], input[1]); err == nil {
			cleanup()
			t.Fatal("accepted invalid runtime settings")
		}
	}
}
