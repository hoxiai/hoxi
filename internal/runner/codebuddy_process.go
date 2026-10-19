package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ResolveCodeBuddyBinary locates the official CodeBuddy CLI (`codebuddy` / `cbc`),
// which supports both Domestic (CN / copilot.tencent.com) and International (codebuddy.ai) accounts.
func ResolveCodeBuddyBinary() string {
	if env := strings.TrimSpace(os.Getenv("CODEBUDDY_BIN")); env != "" {
		if p := firstExecutableFile(env); p != "" {
			return p
		}
		return ""
	}
	for _, name := range []string{"codebuddy", "cbc"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	var candidates []string
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".codebuddy", "bin", "codebuddy"),
			filepath.Join(home, ".codebuddy", "bin", "cbc"),
		)
	}
	for _, dir := range commonUserBinDirs() {
		candidates = append(candidates,
			filepath.Join(dir, "codebuddy"),
			filepath.Join(dir, "cbc"),
		)
	}
	return firstExecutableFile(candidates...)
}

var (
	codeBuddyModelsMu   sync.Mutex
	codeBuddyCachedBin  string
	codeBuddyCachedTime time.Time
	codeBuddyCachedList []string
)

// ParseCodeBuddyModelsOutput extracts model IDs from `codebuddy --help` ("Currently supported: (...)").
func ParseCodeBuddyModelsOutput(raw string) []string {
	idx := strings.Index(raw, "Currently supported:")
	if idx < 0 {
		return nil
	}
	rest := raw[idx+len("Currently supported:"):]
	openIdx := strings.IndexByte(rest, '(')
	closeIdx := strings.IndexByte(rest, ')')
	if openIdx < 0 || closeIdx <= openIdx {
		return nil
	}
	inside := rest[openIdx+1 : closeIdx]
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(inside, ",") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		slug := strings.TrimSpace(fields[0])
		if slug == "" || strings.ContainsAny(slug, " ()") {
			continue
		}
		if !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}

type codeBuddyModelSpec struct {
	name         string
	multiplier   string
	free         bool
	contextLimit int
	outputLimit  int
	inputCost    float64
	outputCost   float64
	image        bool
	reasoning    bool
}

// filteredModelPrefixes excludes low-utility model families by stable prefix rather than hardcoded versions.
var filteredModelPrefixes = []string{
	"minimax",
	"hunyuan-chat",
}

func isFilteredModelByPrefix(slug string) bool {
	lower := strings.ToLower(strings.TrimSpace(slug))
	if lower == "" || lower == "default" {
		return true
	}
	for _, pfx := range filteredModelPrefixes {
		if strings.HasPrefix(lower, pfx) {
			return true
		}
	}
	return false
}

// stripVendorSuffix removes redundant parenthetical vendor labels like `（稀宇科技）` or `（月之暗面）`.
func stripVendorSuffix(name string) string {
	s := strings.TrimSpace(name)
	if idx := strings.Index(s, "（"); idx > 0 && strings.HasSuffix(s, "）") {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

func defaultCodeBuddyModelSlugs() []string {
	return []string{
		"default-model",
		"hy3",
		"deepseek-v4.1-flash",
		"fast-model",
		"balanced-model",
		"primary-model",
		"deep-model",
		"hy4-preview",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"glm-5.3",
		"glm-5.3-flash",
		"kimi-k3",
		"kimi-k2.8-preview",
		"gemini-3.5-flash",
		"gpt-5.6-luna",
		"gpt-5.6-terra",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-6-astra",
	}
}

// inferCodeBuddyModelSpec infers model display name, credit multiplier, free status,
// context limits, and reference token pricing by model family prefix & tier keyword,
// so newly released model versions work without hardcoding every point version.
func inferCodeBuddyModelSpec(slug string) codeBuddyModelSpec {
	lower := strings.ToLower(strings.TrimSpace(slug))
	switch lower {
	case "default-model":
		return codeBuddyModelSpec{name: "Auto", multiplier: "1x", contextLimit: 176000, outputLimit: 24000, image: true, reasoning: true}
	case "fast-model":
		return codeBuddyModelSpec{name: "Fast", multiplier: "0.34x", contextLimit: 200000, outputLimit: 32000, inputCost: 0.35, outputCost: 1.40, image: true, reasoning: true}
	case "balanced-model":
		return codeBuddyModelSpec{name: "Balanced", multiplier: "0.59x", contextLimit: 256000, outputLimit: 32000, inputCost: 0.60, outputCost: 2.40, image: true, reasoning: true}
	case "primary-model":
		return codeBuddyModelSpec{name: "Primary", multiplier: "3.31x", contextLimit: 272000, outputLimit: 72000, inputCost: 3.00, outputCost: 12.00, image: true, reasoning: true}
	case "deep-model":
		return codeBuddyModelSpec{name: "Deep", multiplier: "3.33x", contextLimit: 176000, outputLimit: 24000, inputCost: 3.00, outputCost: 12.00, image: true, reasoning: true}
	}

	switch {
	case strings.HasPrefix(lower, "hy"):
		if lower == "hy3" {
			return codeBuddyModelSpec{name: "Hy3", multiplier: "0x", free: true, contextLimit: 192000, outputLimit: 64000, image: true, reasoning: true}
		}
		if strings.HasPrefix(lower, "hy3-") {
			return codeBuddyModelSpec{multiplier: "0.05x", contextLimit: 192000, outputLimit: 64000, inputCost: 0.08, outputCost: 0.30, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "0.29x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.30, outputCost: 1.20, image: true, reasoning: true}

	case strings.HasPrefix(lower, "deepseek"):
		if strings.Contains(lower, "v4.1-flash") && !strings.HasSuffix(lower, "-sg") {
			return codeBuddyModelSpec{multiplier: "0x", free: true, contextLimit: 1000000, outputLimit: 128000, image: true, reasoning: true}
		}
		if strings.Contains(lower, "flash") {
			return codeBuddyModelSpec{multiplier: "0.17x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.14, outputCost: 0.28, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "0.51x", contextLimit: 1000000, outputLimit: 128000, inputCost: 0.55, outputCost: 2.19, image: true, reasoning: true}

	case strings.HasPrefix(lower, "glm"):
		if strings.Contains(lower, "flash") {
			return codeBuddyModelSpec{multiplier: "0.06x", contextLimit: 1000000, outputLimit: 131072, inputCost: 0.10, outputCost: 0.40, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "0.79x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.70, outputCost: 2.80, image: true, reasoning: true}

	case strings.HasPrefix(lower, "kimi"):
		if strings.Contains(lower, "preview") {
			return codeBuddyModelSpec{multiplier: "0.77x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.80, outputCost: 3.20, image: true, reasoning: true}
		}
		if strings.HasPrefix(lower, "kimi-k2") {
			return codeBuddyModelSpec{multiplier: "0.57x", contextLimit: 256000, outputLimit: 32000, inputCost: 0.60, outputCost: 2.50, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "1.62x", contextLimit: 1000000, outputLimit: 32000, inputCost: 1.50, outputCost: 6.00, image: true, reasoning: true}

	case strings.HasPrefix(lower, "qwen"):
		if strings.Contains(lower, "flash") {
			return codeBuddyModelSpec{multiplier: "0x", free: true, contextLimit: 1000000, outputLimit: 64000, image: true, reasoning: true}
		}
		if strings.Contains(lower, "plus") || strings.Contains(lower, "coder") {
			return codeBuddyModelSpec{multiplier: "0.1x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.40, outputCost: 1.20, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "0.5x", contextLimit: 1000000, outputLimit: 64000, inputCost: 1.20, outputCost: 6.00, image: true, reasoning: true}

	case strings.HasPrefix(lower, "gemini"):
		if strings.Contains(lower, "flash") {
			return codeBuddyModelSpec{multiplier: "0.99x", contextLimit: 1000000, outputLimit: 65536, inputCost: 0.35, outputCost: 1.40, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "1.5x", contextLimit: 1000000, outputLimit: 65536, inputCost: 1.25, outputCost: 5.00, image: true, reasoning: true}

	case strings.HasPrefix(lower, "claude"):
		if strings.Contains(lower, "opus") {
			return codeBuddyModelSpec{multiplier: "3.0x", contextLimit: 200000, outputLimit: 64000, inputCost: 5.00, outputCost: 25.00, image: true, reasoning: true}
		}
		return codeBuddyModelSpec{multiplier: "1.5x", contextLimit: 200000, outputLimit: 64000, inputCost: 3.00, outputCost: 15.00, image: true, reasoning: true}

	case strings.HasPrefix(lower, "gpt"):
		switch {
		case strings.Contains(lower, "luna") || strings.Contains(lower, "mini") || strings.Contains(lower, "nano"):
			return codeBuddyModelSpec{multiplier: "0.14x", contextLimit: 1000000, outputLimit: 128000, inputCost: 0.20, outputCost: 0.80, image: true, reasoning: true}
		case strings.Contains(lower, "terra"):
			return codeBuddyModelSpec{multiplier: "1.39x", contextLimit: 1000000, outputLimit: 128000, inputCost: 1.50, outputCost: 6.00, image: true, reasoning: true}
		case strings.Contains(lower, "sol"):
			return codeBuddyModelSpec{multiplier: "3.47x", contextLimit: 1000000, outputLimit: 128000, inputCost: 3.50, outputCost: 14.00, image: true, reasoning: true}
		case strings.Contains(lower, "astra"):
			return codeBuddyModelSpec{multiplier: "6.67x", contextLimit: 1000000, outputLimit: 128000, inputCost: 6.00, outputCost: 24.00, image: true, reasoning: true}
		default:
			return codeBuddyModelSpec{multiplier: "3.31x", contextLimit: 1000000, outputLimit: 128000, inputCost: 3.00, outputCost: 12.00, image: true, reasoning: true}
		}
	}

	reasoning := !strings.Contains(lower, "fast") && !strings.Contains(lower, "flash") && !strings.Contains(lower, "lite")
	return codeBuddyModelSpec{contextLimit: 200000, outputLimit: 32000, image: true, reasoning: reasoning}
}

func parseCodeBuddyCredits(raw string) (multiplier string, free bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSuffix(s, "credits")
	s = strings.TrimSuffix(s, "credit")
	s = strings.TrimSpace(strings.TrimPrefix(s, "x"))
	if s == "" {
		return "", false
	}
	if s == "0" || s == "0.0" || s == "0.00" {
		return "0x", true
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s + "x", false
}

// readCodeBuddyProductCatalog inspects the npm package `product.json` and `product.internal.json`
// adjacent to the resolved `codebuddy` binary to pick up dynamic model credits and token limits.
func readCodeBuddyProductCatalog(bin string) map[string]codeBuddyModelSpec {
	if bin == "" {
		return nil
	}
	realBin, err := filepath.EvalSymlinks(bin)
	if err != nil || realBin == "" {
		realBin = bin
	}
	pkgDir := filepath.Dir(filepath.Dir(realBin))
	out := make(map[string]codeBuddyModelSpec)
	// Read product.internal.json first, then product.json so international free offers (0x) take precedence.
	for _, fn := range []string{"product.internal.json", "product.json"} {
		raw, err := os.ReadFile(filepath.Join(pkgDir, fn))
		if err != nil {
			continue
		}
		var doc struct {
			Models []struct {
				ID                string   `json:"id"`
				Name              string   `json:"name"`
				Credits           string   `json:"credits"`
				MaxInputTokens    int      `json:"maxInputTokens"`
				MaxOutputTokens   int      `json:"maxOutputTokens"`
				SupportsImages    bool     `json:"supportsImages"`
				SupportsReasoning *bool    `json:"supportsReasoning"`
				Tags              []string `json:"tags"`
			} `json:"models"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		for _, m := range doc.Models {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				continue
			}
			skip := false
			for _, t := range m.Tags {
				if t == "text-to-image" || t == "image-to-image" {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			mult, isFree := parseCodeBuddyCredits(m.Credits)
			prev := out[id]
			if prev.free {
				isFree = true
				mult = "0x"
			}
			if mult == "" {
				mult = prev.multiplier
			}
			inTok := m.MaxInputTokens
			if inTok == 0 {
				inTok = prev.contextLimit
			}
			outTok := m.MaxOutputTokens
			if outTok == 0 {
				outTok = prev.outputLimit
			}
			reason := prev.reasoning
			if m.SupportsReasoning != nil {
				reason = *m.SupportsReasoning
			}
			out[id] = codeBuddyModelSpec{
				name:         stripVendorSuffix(m.Name),
				multiplier:   mult,
				free:         isFree,
				contextLimit: inTok,
				outputLimit:  outTok,
				image:        m.SupportsImages || prev.image,
				reasoning:    reason,
			}
		}
	}
	return out
}

func readCodeBuddyCustomModels() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var extra []string
	if raw, err := os.ReadFile(filepath.Join(home, ".codebuddy", "settings.json")); err == nil {
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) == nil {
			if m, _ := doc["model"].(string); strings.TrimSpace(m) != "" {
				extra = append(extra, strings.TrimSpace(m))
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".codebuddy", "models.json")); err == nil {
		var doc struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			for _, m := range doc.Models {
				if id := strings.TrimSpace(m.ID); id != "" {
					extra = append(extra, id)
				}
			}
		}
	}
	return extra
}

func DiscoverCodeBuddyModels() []RunnerModelItem {
	return DiscoverCodeBuddyModelsWithRefresh(false)
}

func DiscoverCodeBuddyModelsWithRefresh(forceRefresh bool) []RunnerModelItem {
	bin := ResolveCodeBuddyBinary()
	if bin == "" {
		return nil
	}
	codeBuddyModelsMu.Lock()
	defer codeBuddyModelsMu.Unlock()

	if !forceRefresh && codeBuddyCachedBin == bin && len(codeBuddyCachedList) > 0 && time.Since(codeBuddyCachedTime) < 10*time.Minute {
		return buildCodeBuddyModelItems(bin, codeBuddyCachedList)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--help")
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	parsed := ParseCodeBuddyModelsOutput(string(out))

	// Combine default curated order + CLI --help models (filtered by prefix) + custom user models
	var slugs []string
	seen := make(map[string]bool)
	addSlug := func(s string, allowFiltered bool) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		if !allowFiltered && isFilteredModelByPrefix(s) {
			return
		}
		seen[s] = true
		slugs = append(slugs, s)
	}
	for _, s := range defaultCodeBuddyModelSlugs() {
		addSlug(s, false)
	}
	for _, s := range parsed {
		addSlug(s, false)
	}
	for _, s := range readCodeBuddyCustomModels() {
		addSlug(s, true)
	}

	codeBuddyCachedBin = bin
	codeBuddyCachedTime = time.Now()
	codeBuddyCachedList = slugs
	return buildCodeBuddyModelItems(bin, slugs)
}

func buildCodeBuddyModelItems(bin string, slugs []string) []RunnerModelItem {
	pkgCatalog := readCodeBuddyProductCatalog(bin)
	items := make([]RunnerModelItem, 0, len(slugs))
	for _, s := range slugs {
		spec := inferCodeBuddyModelSpec(s)
		if pkgSpec, ok := pkgCatalog[s]; ok {
			if spec.name == "" && pkgSpec.name != "" {
				spec.name = pkgSpec.name
			}
			if pkgSpec.free {
				spec.free = true
				spec.multiplier = "0x"
				spec.inputCost = 0
				spec.outputCost = 0
			} else if pkgSpec.multiplier != "" {
				spec.multiplier = pkgSpec.multiplier
			}
			if pkgSpec.contextLimit > 0 {
				spec.contextLimit = pkgSpec.contextLimit
			}
			if pkgSpec.outputLimit > 0 {
				spec.outputLimit = pkgSpec.outputLimit
			}
			if pkgSpec.image {
				spec.image = true
			}
			spec.reasoning = pkgSpec.reasoning
		}
		name := stripVendorSuffix(spec.name)
		if name == "" {
			name = s
		}
		items = append(items, RunnerModelItem{
			ID:           s,
			Name:         name,
			ProviderID:   string(RuntimeCodeBuddy),
			Runtime:      string(RuntimeCodeBuddy),
			Reasoning:    spec.reasoning,
			Image:        spec.image,
			Free:         spec.free,
			Multiplier:   spec.multiplier,
			ContextLimit: spec.contextLimit,
			OutputLimit:  spec.outputLimit,
			InputCost:    spec.inputCost,
			OutputCost:   spec.outputCost,
		})
	}
	return items
}

func BuildCodeBuddyArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "acceptEdits"
	if strings.EqualFold(strings.TrimSpace(mode), "plan") {
		permMode = "plan"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", permMode,
	}
	if m := strings.TrimSpace(modelID); m != "" && m != "default-model" {
		args = append(args, "--model", m)
	}
	if e := strings.TrimSpace(effort); e != "" {
		args = append(args, "--effort", e)
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	return args
}
