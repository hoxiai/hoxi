package agentcli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveGeminiBinary locates the official Google Gemini CLI (`gemini`) binary.
func ResolveGeminiBinary() string {
	if env := strings.TrimSpace(os.Getenv("GEMINI_BIN")); env != "" {
		return FirstExecutableFile(env)
	}
	if gRoot := GeminiHomeDir(); gRoot != "" {
		candidate := filepath.Join(gRoot, "bin", "gemini")
		if p := FirstExecutableFile(candidate); p != "" {
			return p
		}
		if os.Getenv("GEMINI_HOME") != "" {
			return ""
		}
	}
	if p, err := exec.LookPath("gemini"); err == nil {
		return p
	}
	var candidates []string
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".gemini", "bin", "gemini"),
			filepath.Join(home, ".local", "bin", "gemini"),
		)
	}
	for _, dir := range CommonUserBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "gemini"))
	}
	return FirstExecutableFile(candidates...)
}

func defaultGeminiModelSlugs() []string {
	return []string{
		"auto",
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-3-pro-preview",
		"gemini-3-flash-preview",
		"gemini-4-argon",
		"gemini-3.8-flash",
	}
}

func readGeminiSettingsModel() string {
	gRoot := GeminiHomeDir()
	if gRoot == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(gRoot, "settings.json"))
	if err != nil {
		return ""
	}
	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	switch v := doc["model"].(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		if name, _ := v["name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// FormatGeminiModelName derives a human-readable display name for a Gemini model slug.
func FormatGeminiModelName(slug string) string {
	trimmed := strings.TrimSpace(slug)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "", "auto":
		return "Auto (Gemini)"
	case "gemini-2.5-pro":
		return "Gemini 2.5 Pro"
	case "gemini-2.5-flash":
		return "Gemini 2.5 Flash"
	case "gemini-2.5-flash-lite":
		return "Gemini 2.5 Flash Lite"
	case "gemini-3-pro-preview":
		return "Gemini 3 Pro Preview"
	case "gemini-3-flash-preview":
		return "Gemini 3 Flash Preview"
	case "gemini-4-argon":
		return "Gemini 4 Argon"
	case "gemini-3.8-flash":
		return "Gemini 3.8 Flash"
	}
	if !strings.HasPrefix(lower, "gemini-") {
		return trimmed
	}
	parts := strings.Split(strings.TrimPrefix(lower, "gemini-"), "-")
	var words []string
	words = append(words, "Gemini")
	for _, p := range parts {
		if p == "" {
			continue
		}
		if isAllASCIIAlpha(p) {
			words = append(words, strings.ToUpper(p[:1])+p[1:])
		} else {
			words = append(words, p)
		}
	}
	return strings.Join(words, " ")
}

type geminiModelSpec struct {
	name          string
	contextLimit  int
	outputLimit   int
	inputCost     float64
	outputCost    float64
	image         bool
	reasoning     bool
	efforts       []string
	defaultEffort string
}

func inferGeminiModelSpec(slug string) geminiModelSpec {
	lower := strings.ToLower(strings.TrimSpace(slug))
	reasoning := !strings.Contains(lower, "lite")
	efforts, defEffort := InferVendorEffortSpec(RuntimeGeminiCLI, lower, reasoning)
	spec := geminiModelSpec{
		name:          FormatGeminiModelName(slug),
		contextLimit:  1000000,
		outputLimit:   65536,
		image:         true,
		reasoning:     reasoning,
		efforts:       efforts,
		defaultEffort: defEffort,
	}
	switch {
	case lower == "auto":
		spec.inputCost = 1.25
		spec.outputCost = 10.00
	case strings.Contains(lower, "flash-lite") || strings.Contains(lower, "lite"):
		spec.inputCost = 0.10
		spec.outputCost = 0.40
	case strings.Contains(lower, "4-argon") || strings.Contains(lower, "argon"):
		spec.inputCost = 2.00
		spec.outputCost = 12.00
	case strings.Contains(lower, "3-pro"):
		spec.inputCost = 2.00
		spec.outputCost = 12.00
	case strings.Contains(lower, "3.8-flash") || strings.Contains(lower, "3-flash"):
		spec.inputCost = 0.50
		spec.outputCost = 3.00
	case strings.Contains(lower, "flash"):
		spec.inputCost = 0.30
		spec.outputCost = 2.50
	default:
		spec.inputCost = 1.25
		spec.outputCost = 10.00
	}
	return spec
}

// DiscoverGeminiModels returns the models exposed by the local `gemini` CLI.
func DiscoverGeminiModels() []ModelItem {
	return DiscoverGeminiModelsWithRefresh(false)
}

// DiscoverGeminiModelsWithRefresh returns the models exposed by the local `gemini` CLI.
func DiscoverGeminiModelsWithRefresh(_ bool) []ModelItem {
	if ResolveGeminiBinary() == "" {
		return nil
	}
	seen := make(map[string]bool)
	var slugs []string
	addSlug := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		slugs = append(slugs, s)
	}
	addSlug("auto")
	if custom := readGeminiSettingsModel(); custom != "" {
		addSlug(custom)
	}
	for _, s := range defaultGeminiModelSlugs() {
		addSlug(s)
	}

	items := make([]ModelItem, 0, len(slugs))
	for _, s := range slugs {
		spec := inferGeminiModelSpec(s)
		items = append(items, ModelItem{
			ID:            s,
			Name:          spec.name,
			ProviderID:    RuntimeGeminiCLI,
			Runtime:       RuntimeGeminiCLI,
			Reasoning:     spec.reasoning,
			Efforts:       spec.efforts,
			DefaultEffort: spec.defaultEffort,
			Image:         spec.image,
			ContextLimit:  spec.contextLimit,
			OutputLimit:   spec.outputLimit,
			InputCost:     spec.inputCost,
			OutputCost:    spec.outputCost,
		})
	}
	return items
}
