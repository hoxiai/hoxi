package handoff

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
)

func TestClaudeStatusAndRunnerModelsAgree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("ANTHROPIC_MODEL", "environment-model")
	for _, settings := range []string{`{}`, `{"model":"custom-model"}`, `{"model":"opus"}`} {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, selected, models := readClaudeSettingsEnv()
		runnerModels := runner.DiscoverClaudeModels()
		var ids []string
		for _, model := range runnerModels {
			ids = append(ids, model.ID)
		}
		if selected != ids[0] || !reflect.DeepEqual(models, ids) {
			t.Fatalf("status %q %v differs from runner %v", selected, models, ids)
		}
	}
}
