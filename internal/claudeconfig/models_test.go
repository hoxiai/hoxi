package claudeconfig

import (
	"os"
	"path/filepath"
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
		{`{"model":"opus","env":{"ANTHROPIC_MODEL":"other-model"}}`, "opus"},
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
