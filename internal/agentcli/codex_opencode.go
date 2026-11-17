package agentcli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/subproc"
)

// ResolveCodexBinary locates the OpenAI Codex CLI (`codex`) binary.
func ResolveCodexBinary() string {
	if env := strings.TrimSpace(os.Getenv("HOXI_CODEX_BIN")); env != "" {
		return FirstExecutableFile(env)
	}
	if p, err := exec.LookPath("codex"); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	var candidates []string
	for _, dir := range CommonUserBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "codex"))
	}
	return FirstExecutableFile(candidates...)
}

// ResolveOpenCodeBinary locates the standalone `opencode` CLI binary.
func ResolveOpenCodeBinary() string {
	for _, key := range []string{"HOXI_OPENCODE_BIN", "HOXI_OPENCODE"} {
		if env := strings.TrimSpace(os.Getenv(key)); env != "" && env != "opencode" {
			return FirstExecutableFile(env)
		}
	}
	if p, err := exec.LookPath("opencode"); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	var candidates []string
	if binDir := hoxipaths.BinDir(); binDir != "" {
		candidates = append(candidates, filepath.Join(binDir, "opencode"))
	}
	for _, dir := range CommonUserBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "opencode"))
	}
	return FirstExecutableFile(candidates...)
}

func readCodexConfiguredModel() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			if strings.HasPrefix(trimmed, "[") {
				break
			}
			continue
		}
		k, v, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(k) != "model" {
			continue
		}
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		if val != "" {
			return val
		}
	}
	return ""
}

// DiscoverCodexModelsWithRefresh returns available models for the Codex CLI runner.
func DiscoverCodexModelsWithRefresh(_ bool) []ModelItem {
	defaultEfforts := []string{"low", "medium", "high"}
	seen := make(map[string]bool)
	var out []ModelItem
	add := func(id, name string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if name == "" {
			name = id
		}
		out = append(out, ModelItem{
			ID:            id,
			Name:          name,
			ProviderID:    RuntimeCodex,
			Runtime:       RuntimeCodex,
			Reasoning:     true,
			Efforts:       defaultEfforts,
			DefaultEffort: "medium",
			Image:         true,
			ContextLimit:  400000,
			OutputLimit:   128000,
		})
	}
	if cfgModel := readCodexConfiguredModel(); cfgModel != "" {
		add(cfgModel, cfgModel+" (Codex Config)")
	}
	add("gpt-5.4", "GPT-5.4 (Codex)")
	add("gpt-5.3-codex", "GPT-5.3 Codex")
	add("gpt-5.2-codex", "GPT-5.2 Codex")
	add("o3", "o3")
	add("o4-mini", "o4-mini")
	return out
}

var (
	ocCLIModelsMu   sync.Mutex
	ocCLICachedBin  string
	ocCLICachedTime time.Time
	ocCLICachedList []ModelItem
)

// DiscoverOpenCodeCLIModelsWithRefresh queries `opencode models` (cached 2m) for single-turn CLI mode.
func DiscoverOpenCodeCLIModelsWithRefresh(forceRefresh bool) []ModelItem {
	bin := ResolveOpenCodeBinary()
	if bin == "" {
		return nil
	}
	ocCLIModelsMu.Lock()
	defer ocCLIModelsMu.Unlock()
	if !forceRefresh && ocCLICachedBin == bin && len(ocCLICachedList) > 0 && time.Since(ocCLICachedTime) < 2*time.Minute {
		return append([]ModelItem(nil), ocCLICachedList...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, bin, "models")
	cmd.Env = WithUserBinPath(os.Environ())
	cmd.WaitDelay = time.Second
	raw, err := cmd.Output()
	var items []ModelItem
	if err == nil && len(bytes.TrimSpace(raw)) > 0 {
		seen := make(map[string]bool)
		for _, line := range strings.Split(string(raw), "\n") {
			slug := strings.TrimSpace(line)
			if slug == "" || strings.ContainsAny(slug, " \t") || !strings.Contains(slug, "/") || seen[slug] {
				continue
			}
			seen[slug] = true
			items = append(items, ModelItem{
				ID:           slug,
				Name:         slug,
				ProviderID:   RuntimeOpenCode,
				Runtime:      RuntimeOpenCode,
				Reasoning:    true,
				Efforts:      []string{"low", "medium", "high"},
				ContextLimit: 200000,
				OutputLimit:  64000,
			})
			if len(items) >= 120 {
				break
			}
		}
	}
	if len(items) > 0 {
		ocCLICachedBin = bin
		ocCLICachedTime = time.Now()
		ocCLICachedList = items
	}
	return append([]ModelItem(nil), ocCLICachedList...)
}
