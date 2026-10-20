// Package agentcli discovers locally installed agent CLIs (Qoder, CodeBuddy)
// and the models they expose, plus the shared reasoning-effort vocabulary.
//
// It is a foundation package: it shells out to the CLIs with a bounded
// context but never imports a business domain package, so both the runner
// (which drives the CLIs) and handoff (which reports their status) can use it.
package agentcli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Runtime identifiers shared with the runner's RuntimeType values.
const (
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
