// Package agentcli discovers locally installed agent CLIs (Qoder, CodeBuddy)
// and the models they expose, plus the shared reasoning-effort vocabulary.
//
// It is a foundation package: it shells out to the CLIs with a bounded
// context but never imports a business domain package, so both the runner
// (which drives the CLIs) and handoff (which reports their status) can use it.
package agentcli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Runtime identifiers shared with the runner's RuntimeType values.
const (
	RuntimeOpenCode    = "opencode"
	RuntimeAntigravity = "antigravity"
	RuntimeClaudeCode  = "claude-code"
	RuntimeCodeBuddy   = "codebuddy"
	RuntimeQoder       = "qoder"
)

// ModelItem is one model a local agent CLI can run.
type ModelItem struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ProviderID    string   `json:"providerID"`
	Runtime       string   `json:"runtime"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	Image         bool     `json:"image,omitempty"`
	Free          bool     `json:"free,omitempty"`
	Multiplier    string   `json:"multiplier,omitempty"`
	ContextLimit  int      `json:"contextLimit,omitempty"`
	OutputLimit   int      `json:"outputLimit,omitempty"`
	InputCost     float64  `json:"inputCost,omitempty"`
	OutputCost    float64  `json:"outputCost,omitempty"`
}

// CommonUserBinDirs lists user-level bin directories that GUI apps miss from PATH.
func CommonUserBinDirs() []string {
	var dirs []string
	if home, _ := os.UserHomeDir(); home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "sdk", "node", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".fnm", "current", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
		)
		if nvmBins, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); err == nil && len(nvmBins) > 0 {
			sort.Strings(nvmBins)
			for i := len(nvmBins) - 1; i >= 0; i-- {
				dirs = append(dirs, nvmBins[i])
			}
		}
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	return dirs
}

// FirstExecutableFile returns the first candidate that is an existing file.
func FirstExecutableFile(candidates ...string) string {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// GeminiHomeDir returns $GEMINI_HOME or ~/.gemini.
func GeminiHomeDir() string {
	if env := strings.TrimSpace(os.Getenv("GEMINI_HOME")); env != "" {
		return filepath.Clean(env)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini")
}

// ResolveAgyBinary locates the Antigravity CLI (`agy`) binary.
func ResolveAgyBinary() string {
	if gRoot := GeminiHomeDir(); gRoot != "" {
		candidate := filepath.Join(gRoot, "bin", "agy")
		if p := FirstExecutableFile(candidate); p != "" {
			return p
		}
		if os.Getenv("GEMINI_HOME") != "" || os.Getenv("ANTIGRAVITY_HOME") != "" {
			return ""
		}
	}
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	var candidates []string
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "agy"),
			filepath.Join(home, ".gemini", "bin", "agy"),
		)
	}
	for _, dir := range CommonUserBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "agy"))
	}
	return FirstExecutableFile(candidates...)
}

// ResolveClaudeBinary locates the Claude Code CLI (`claude`) binary.
func ResolveClaudeBinary() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	var candidates []string
	for _, dir := range CommonUserBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "claude"))
	}
	return FirstExecutableFile(candidates...)
}

var (
	agyModelsMu          sync.Mutex
	agyCachedBin         string
	agyCachedTime        time.Time
	agyCachedSlugs       []string
	agyCachedDisplayMap  map[string]string
	agyCachedReverseSlug map[string]string
)

// ParseAgyModelsOutput parses `agy models` stdout into slugs and display-name maps.
func ParseAgyModelsOutput(raw string) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	displayBySlug = make(map[string]string)
	slugByDisplayLower = make(map[string]string)
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(raw, "\n") {
		segments := strings.Split(rawLine, "\r")
		line := strings.TrimSpace(segments[len(segments)-1])
		if line == "" || strings.Contains(line, "Fetching available models") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		slug := strings.TrimSpace(fields[0])
		if slug == "" || strings.ContainsAny(slug, " ()") || !strings.Contains(slug, "-") {
			continue
		}
		display := strings.TrimSpace(strings.TrimPrefix(line, slug))
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
		if display != "" {
			displayBySlug[slug] = display
			slugByDisplayLower[strings.ToLower(display)] = slug
		}
	}
	return slugs, displayBySlug, slugByDisplayLower
}

// DiscoverAgyCLIModels executes `agy models` with a 10-minute cache.
func DiscoverAgyCLIModels(forceRefresh bool) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	agyBin := ResolveAgyBinary()
	if agyBin == "" {
		return nil, nil, nil
	}
	agyModelsMu.Lock()
	defer agyModelsMu.Unlock()
	if !forceRefresh && agyCachedBin == agyBin && len(agyCachedSlugs) > 0 && time.Since(agyCachedTime) < 10*time.Minute {
		return agyCachedSlugs, agyCachedDisplayMap, agyCachedReverseSlug
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, agyBin, "models")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return agyCachedSlugs, agyCachedDisplayMap, agyCachedReverseSlug
	}
	s, dMap, rMap := ParseAgyModelsOutput(string(out))
	if len(s) > 0 {
		agyCachedBin = agyBin
		agyCachedTime = time.Now()
		agyCachedSlugs = s
		agyCachedDisplayMap = dMap
		agyCachedReverseSlug = rMap
	}
	return agyCachedSlugs, agyCachedDisplayMap, agyCachedReverseSlug
}

// CachedAgyCLISlugs returns a copy of the most recently discovered Antigravity model slugs without spawning a process.
func CachedAgyCLISlugs() []string {
	agyModelsMu.Lock()
	defer agyModelsMu.Unlock()
	return append([]string(nil), agyCachedSlugs...)
}

// FormatClaudeModelName derives a human-readable label for any Claude model slug or alias
// (e.g., "claude-sonnet-4-6" -> "Claude Sonnet 4.6", "claude-haiku-4-5-20251001" -> "Claude Haiku 4.5")
// without requiring a hardcoded map per point version or tier.
func FormatClaudeModelName(slug string) string {
	trimmed := strings.TrimSpace(slug)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "opus", "sonnet", "haiku", "fable":
		return "Claude " + strings.ToUpper(lower[:1]) + lower[1:]
	}
	if !strings.HasPrefix(lower, "claude-") {
		return trimmed
	}
	parts := strings.Split(strings.TrimPrefix(lower, "claude-"), "-")
	if len(parts) > 1 && len(parts[len(parts)-1]) == 8 && isAllDigits(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	tierIdx := -1
	var tierTitle string
	for i, p := range parts {
		if p == "latest" || p == "thinking" {
			continue
		}
		if isAllASCIIAlpha(p) {
			tierIdx = i
			tierTitle = strings.ToUpper(p[:1]) + p[1:]
			break
		}
	}
	if tierIdx < 0 {
		return trimmed
	}
	var verParts []string
	for _, p := range parts {
		if p != "" && isAllDigits(strings.ReplaceAll(p, ".", "")) {
			verParts = append(verParts, p)
		}
	}
	if len(verParts) == 0 {
		return "Claude " + tierTitle
	}
	return "Claude " + tierTitle + " " + strings.Join(verParts, ".")
}

func isAllASCIIAlpha(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
