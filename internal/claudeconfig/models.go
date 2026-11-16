package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Models puts the configured selection first, without inferring it from old sessions.
func Models() []string {
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
	env := func(key string) string {
		if value, ok := settings.Env[key]; ok {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(os.Getenv(key))
	}
	var models []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		switch value {
		case "opus", "sonnet", "haiku":
			if mapped := env("ANTHROPIC_DEFAULT_" + strings.ToUpper(value) + "_MODEL"); mapped != "" && mapped != value {
				value = mapped
			} else if value == "opus" {
				value = "claude-opus-5-5"
			}
		}
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
	add(settings.Model)
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		add(env(key))
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
	for _, model := range []string{"opus", "sonnet", "haiku"} {
		add(model)
	}
	return models
}
