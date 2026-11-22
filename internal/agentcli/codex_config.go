package agentcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CodexProviderConfig holds custom provider routing settings parsed from ~/.codex/config.toml.
type CodexProviderConfig struct {
	ProviderID string
	BaseURL    string
	EnvKey     string
	WireAPI    string
}

// ReadCodexProviderConfig reads custom model_provider / base_url settings from ~/.codex/config.toml.
func ReadCodexProviderConfig() CodexProviderConfig {
	cDir := CodexHomeDir()
	if cDir == "" {
		return CodexProviderConfig{}
	}
	raw, err := os.ReadFile(filepath.Join(cDir, "config.toml"))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return CodexProviderConfig{}
	}
	return ParseCodexProviderConfigTOML(raw)
}

// ParseCodexProviderConfigTOML extracts the active model_provider and its [model_providers.<id>]
// settings (or top-level openai_base_url) from a Codex config.toml payload.
func ParseCodexProviderConfigTOML(raw []byte) CodexProviderConfig {
	type providerSection struct {
		baseURL string
		envKey  string
		wireAPI string
	}
	sections := map[string]providerSection{}
	var activeProvider, topOpenAIBaseURL, currentProviderSection string

	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(stripTOMLInlineComment(line))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sec := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			currentProviderSection = parseModelProvidersSectionID(sec)
			continue
		}
		k, v, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		if currentProviderSection == "" {
			switch key {
			case "model_provider":
				activeProvider = val
			case "openai_base_url":
				if isValidHTTPBaseURL(val) {
					topOpenAIBaseURL = val
				}
			}
			continue
		}
		entry := sections[currentProviderSection]
		switch key {
		case "base_url":
			if isValidHTTPBaseURL(val) {
				entry.baseURL = val
			}
		case "env_key":
			entry.envKey = val
		case "wire_api":
			entry.wireAPI = val
		}
		sections[currentProviderSection] = entry
	}

	out := CodexProviderConfig{
		ProviderID: activeProvider,
		BaseURL:    topOpenAIBaseURL,
	}
	if activeProvider != "" {
		if sec, exists := sections[activeProvider]; exists {
			if sec.baseURL != "" {
				out.BaseURL = sec.baseURL
			}
			out.EnvKey = sec.envKey
			out.WireAPI = sec.wireAPI
		}
	}
	return out
}

func parseModelProvidersSectionID(section string) string {
	const prefix = "model_providers."
	if !strings.HasPrefix(section, prefix) {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(section, prefix))
	rest = strings.Trim(rest, `"'`)
	if rest == "" || strings.Contains(rest, ".") {
		return ""
	}
	return rest
}

func isValidHTTPBaseURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// ResolveCodexClientVersion returns the highest locally observed Codex CLI version
// from ~/.codex/models_cache.json and any in-memory cached bundled probe without
// spawning a subprocess on the request hot path.
func ResolveCodexClientVersion() string {
	var cacheVer string
	if cDir := CodexHomeDir(); cDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cDir, "models_cache.json")); err == nil && len(bytes.TrimSpace(raw)) > 0 {
			var header struct {
				ClientVersion string `json:"client_version"`
			}
			if json.Unmarshal(raw, &header) == nil {
				cacheVer = strings.TrimPrefix(strings.TrimSpace(header.ClientVersion), "v")
			}
		}
	}
	codexBundledMu.Lock()
	binVer := strings.TrimPrefix(strings.TrimSpace(codexBundledVer), "v")
	codexBundledMu.Unlock()

	if compareCodexVersions(binVer, cacheVer) > 0 {
		return binVer
	}
	return firstNonEmpty(cacheVer, binVer)
}
