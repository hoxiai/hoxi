package claudeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModelsRefreshesConfiguredSelection(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("ANTHROPIC_MODEL", "process-model")
	if got := Models()[0]; got != "process-model" {
		t.Fatalf("got %q", got)
	}
	for _, test := range []struct{ settings, want string }{
		{`{"env":{"ANTHROPIC_MODEL":"configured-model"}}`, "configured-model"},
		{`{"model":"opus","env":{"ANTHROPIC_MODEL":"other-model"}}`, "claude-opus-5-5"},
		{`{"model":"updated-model"}`, "updated-model"},
	} {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.settings), 0600); err != nil {
			t.Fatal(err)
		}
		if got := Models()[0]; got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}

func TestModelsResolveAliases(t *testing.T) {
	for _, test := range []struct {
		name, settings, processOpus string
		want                        []string
	}{
		{"default", `{}`, "", []string{"claude-opus-5-5", "sonnet", "haiku"}},
		{"opus", `{"model":"opus"}`, "", []string{"claude-opus-5-5", "sonnet", "haiku"}},
		{"settings mapping wins", `{"model":"opus","env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"custom-opus"}}`, "process-opus", []string{"custom-opus", "sonnet", "haiku"}},
		{"process mapping", `{"model":"opus"}`, "process-opus", []string{"process-opus", "sonnet", "haiku"}},
		{"concrete ID", `{"model":"claude-opus-4-6"}`, "custom-opus", []string{"claude-opus-4-6", "custom-opus", "sonnet", "haiku"}},
		{"other aliases", `{"model":"sonnet"}`, "", []string{"sonnet", "claude-opus-5-5", "haiku"}},
		{"other mappings", `{"model":"haiku","env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"custom-sonnet","ANTHROPIC_DEFAULT_HAIKU_MODEL":"custom-haiku"}}`, "", []string{"custom-haiku", "custom-sonnet", "claude-opus-5-5"}},
		{"environment alias", `{"env":{"ANTHROPIC_MODEL":"opus"}}`, "", []string{"claude-opus-5-5", "sonnet", "haiku"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".claude")
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
				t.Setenv(key, "")
			}
			t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", test.processOpus)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.settings), 0600); err != nil {
				t.Fatal(err)
			}
			if got := Models(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}
