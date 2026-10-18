package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Models puts the configured selection first, without inferring it from old sessions.
func Models() []string {
	var models []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, " ()\t\r\n") || strings.HasPrefix(value, "<") {
			return
		}
		for _, existing := range models {
			if existing == value {
				return
			}
		}
		models = append(models, value)
	}
	home, _ := os.UserHomeDir()
	dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if dir == "" && home != "" {
		dir = filepath.Join(home, ".claude")
	}
	var settings struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
			_ = json.Unmarshal(raw, &settings)
		}
	}
	add(settings.Model)
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		value, ok := settings.Env[key]
		if !ok {
			value = os.Getenv(key)
		}
		add(value)
	}
	if dir != "" {
		var cache struct {
			Options []struct {
				Value string `json:"value"`
			} `json:"additionalModelOptionsCache"`
		}
		if raw, err := os.ReadFile(filepath.Join(filepath.Dir(dir), ".claude.json")); err == nil && json.Unmarshal(raw, &cache) == nil {
			for _, option := range cache.Options {
				add(option.Value)
			}
		}
	}
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001"} {
		add(model)
	}
	return models
}
