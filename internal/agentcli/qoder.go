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

	"github.com/hoxiai/hoxi/internal/subproc"
)

// ResolveQoderBinaries locates both Qoder Global CLI (`qodercli`, English)
// and Qoder CN CLI (`qoderclicn`, Chinese).
func ResolveQoderBinaries() (intlBin, cnBin string) {
	if env := strings.TrimSpace(os.Getenv("QODER_BIN")); env != "" {
		intlBin = FirstExecutableFile(env)
	}
	if envCN := strings.TrimSpace(os.Getenv("QODER_CN_BIN")); envCN != "" {
		cnBin = FirstExecutableFile(envCN)
	}
	if os.Getenv("QODER_BIN") != "" || os.Getenv("QODER_CN_BIN") != "" {
		return intlBin, cnBin
	}
	if qHome := strings.TrimSpace(os.Getenv("QODER_HOME")); qHome != "" {
		intlBin = FirstExecutableFile(
			filepath.Join(qHome, "bin", "qodercli", "qodercli"),
			filepath.Join(qHome, "entry", "qodercli"),
			filepath.Join(qHome, "entry", "qoder"),
			filepath.Join(qHome, "bin", "qodercli"),
		)
		cnBin = FirstExecutableFile(
			filepath.Join(qHome, "bin", "qoderclicn", "qoderclicn"),
			filepath.Join(qHome, "entry", "qodercn"),
			filepath.Join(qHome, "entry", "qoder-cn"),
			filepath.Join(qHome, "bin", "qoderclicn"),
			filepath.Join(qHome, "bin", "qodercn"),
		)
		return intlBin, cnBin
	}

	if p, err := exec.LookPath("qodercli"); err == nil {
		intlBin = p
	}
	for _, name := range []string{"qoderclicn", "qodercn", "qoder-cn"} {
		if p, err := exec.LookPath(name); err == nil {
			cnBin = p
			break
		}
	}

	home, _ := os.UserHomeDir()
	if intlBin == "" {
		var intlCandidates []string
		if home != "" {
			intlCandidates = append(intlCandidates,
				filepath.Join(home, ".local", "bin", "qodercli"),
				filepath.Join(home, ".qoder", "bin", "qodercli", "qodercli"),
				filepath.Join(home, ".qoder", "entry", "qodercli"),
				filepath.Join(home, ".qoder", "entry", "qoder"),
				filepath.Join(home, ".qoder", "bin", "qodercli"),
			)
			if matches, err := filepath.Glob(filepath.Join(home, ".qoder", "bin", "qodercli", "qodercli-*")); err == nil && len(matches) > 0 {
				sort.Strings(matches)
				intlCandidates = append(intlCandidates, matches[len(matches)-1])
			}
		}
		for _, dir := range CommonUserBinDirs() {
			intlCandidates = append(intlCandidates, filepath.Join(dir, "qodercli"))
		}
		intlBin = FirstExecutableFile(intlCandidates...)
	}

	if cnBin == "" {
		var cnCandidates []string
		if home != "" {
			cnCandidates = append(cnCandidates,
				filepath.Join(home, ".local", "bin", "qoderclicn"),
				filepath.Join(home, ".qoder-cn", "bin", "qoderclicn", "qoderclicn"),
				filepath.Join(home, ".qoder-cn", "entry", "qodercn"),
				filepath.Join(home, ".qoder-cn", "entry", "qoder-cn"),
				filepath.Join(home, ".qoder-cn", "bin", "qodercn"),
			)
			if matches, err := filepath.Glob(filepath.Join(home, ".qoder-cn", "bin", "qoderclicn", "qoderclicn-*")); err == nil && len(matches) > 0 {
				sort.Strings(matches)
				cnCandidates = append(cnCandidates, matches[len(matches)-1])
			}
		}
		for _, dir := range CommonUserBinDirs() {
			cnCandidates = append(cnCandidates,
				filepath.Join(dir, "qoderclicn"),
				filepath.Join(dir, "qodercn"),
			)
		}
		cnBin = FirstExecutableFile(cnCandidates...)
	}
	return intlBin, cnBin
}

func ResolveQoderBinary() string {
	intlBin, cnBin := ResolveQoderBinaries()
	qoderModelsMu.Lock()
	pref := qoderPreferredBin
	qoderModelsMu.Unlock()
	if pref != "" && (pref == cnBin || pref == intlBin) {
		return pref
	}
	if cnBin != "" {
		return cnBin
	}
	return intlBin
}

// ResolveQoderBinaryForModel picks the matching Qoder CLI binary (`qoderclicn` vs `qodercli`)
// and strips any edition prefix (`cn:` / `intl:`) from modelID.
func ResolveQoderBinaryForModel(modelID string) (binPath, cleanModelID string) {
	intlBin, cnBin := ResolveQoderBinaries()
	m := strings.TrimSpace(modelID)
	for _, pfx := range []string{"cn:", "qodercn:", "qoderclicn:", "qoder-cn:"} {
		if strings.HasPrefix(strings.ToLower(m), pfx) {
			clean := strings.TrimSpace(m[len(pfx):])
			return firstNonEmpty(cnBin, intlBin), clean
		}
	}
	for _, pfx := range []string{"intl:", "global:", "qodercli:"} {
		if strings.HasPrefix(strings.ToLower(m), pfx) {
			clean := strings.TrimSpace(m[len(pfx):])
			return firstNonEmpty(intlBin, cnBin), clean
		}
	}
	return ResolveQoderBinary(), m
}

var (
	qoderModelsMu     sync.Mutex
	qoderCachedKey    string
	qoderCachedTime   time.Time
	qoderCachedList   []ModelItem
	qoderPreferredBin string
)

// ParseQoderModelsOutput parses lines from `qoderclicn --list-models` or `qodercli --list-models`.
func ParseQoderModelsOutput(raw string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if line == "" ||
			strings.HasPrefix(lower, "not logged in") ||
			strings.HasPrefix(lower, "available models") ||
			strings.HasPrefix(lower, "error") ||
			strings.HasPrefix(line, "-") ||
			strings.HasPrefix(line, "=") {
			continue
		}
		line = strings.TrimLeft(line, "*•> ")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		slug := strings.Trim(fields[0], "(),:")
		slugLower := strings.ToLower(slug)
		if slug == "" ||
			slugLower == "model" ||
			slugLower == "models" ||
			slugLower == "name" ||
			slugLower == "id" ||
			strings.ContainsAny(slug, " ()`\"'") {
			continue
		}
		if !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}

type qoderBaseModel struct {
	id            string
	name          string
	multiplier    string
	free          bool
	contextLimit  int
	outputLimit   int
	inputCost     float64
	outputCost    float64
	image         bool
	reasoning     bool
	efforts       []string
	defaultEffort string
}

// inferQoderModelSpec infers credit multiplier, free status, context limits, and reference
// token pricing by model family prefix & tier keyword so new CLI model versions work automatically.
func inferQoderModelSpec(id string) qoderBaseModel {
	cleanID := strings.TrimSpace(id)
	lower := strings.ToLower(cleanID)
	name := stripVendorSuffix(cleanID)
	withVendorEffort := func(spec qoderBaseModel) qoderBaseModel {
		if len(spec.efforts) == 0 && spec.reasoning {
			spec.efforts, spec.defaultEffort = InferVendorEffortSpec(RuntimeQoder, lower, true)
		}
		return spec
	}

	switch lower {
	case "", "auto":
		return withVendorEffort(qoderBaseModel{id: firstNonEmpty(cleanID, "Auto"), name: "Auto", multiplier: "0.5x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.50, outputCost: 2.00, image: true, reasoning: true})
	case "efficient":
		return withVendorEffort(qoderBaseModel{id: cleanID, name: "Efficient", multiplier: "0x", free: true, contextLimit: 200000, outputLimit: 32000, image: true, reasoning: true})
	case "fast":
		return withVendorEffort(qoderBaseModel{id: cleanID, name: "Fast", multiplier: "0.8x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.80, outputCost: 3.20, image: true, reasoning: true})
	case "performance":
		return withVendorEffort(qoderBaseModel{id: cleanID, name: "Performance", multiplier: "1.1x", contextLimit: 1000000, outputLimit: 64000, inputCost: 1.10, outputCost: 4.40, image: true, reasoning: true})
	case "ultimate":
		return withVendorEffort(qoderBaseModel{id: cleanID, name: "Ultimate", multiplier: "2.0x", contextLimit: 1000000, outputLimit: 64000, inputCost: 2.50, outputCost: 10.00, image: true, reasoning: true})
	}

	switch {
	case strings.HasPrefix(lower, "qwen"):
		if strings.Contains(lower, "flash") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0x", free: true, contextLimit: 1000000, outputLimit: 64000, image: true, reasoning: true})
		}
		if strings.Contains(lower, "plus") || strings.Contains(lower, "coder") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.1x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.40, outputCost: 1.20, image: true, reasoning: true})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.5x", contextLimit: 1000000, outputLimit: 64000, inputCost: 1.20, outputCost: 6.00, image: true, reasoning: true})

	case strings.HasPrefix(lower, "deepseek"):
		if strings.Contains(lower, "flash") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.1x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.14, outputCost: 0.28, image: true, reasoning: false})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.5x", contextLimit: 1000000, outputLimit: 128000, inputCost: 0.55, outputCost: 2.19, image: true, reasoning: true})

	case strings.HasPrefix(lower, "glm"):
		if strings.Contains(lower, "flash") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.1x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.10, outputCost: 0.40, image: true, reasoning: false})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.8x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.70, outputCost: 2.80, image: true, reasoning: true})

	case strings.HasPrefix(lower, "kimi"):
		if strings.Contains(lower, "code") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.3x", contextLimit: 256000, outputLimit: 32000, inputCost: 0.60, outputCost: 2.50, image: true, reasoning: true})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "1.4x", contextLimit: 1000000, outputLimit: 64000, inputCost: 1.50, outputCost: 6.00, image: true, reasoning: true})

	case strings.HasPrefix(lower, "claude"):
		if strings.Contains(lower, "opus") || strings.Contains(lower, "fable") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "2.0x", contextLimit: 200000, outputLimit: 64000, inputCost: 5.00, outputCost: 25.00, image: true, reasoning: true})
		}
		if strings.Contains(lower, "haiku") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.4x", contextLimit: 200000, outputLimit: 64000, inputCost: 0.80, outputCost: 4.00, image: true, reasoning: false})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "1.1x", contextLimit: 200000, outputLimit: 64000, inputCost: 3.00, outputCost: 15.00, image: true, reasoning: true})

	case strings.HasPrefix(lower, "gpt") || IsOpenAIOSeriesModel(lower):
		if strings.Contains(lower, "mini") || strings.Contains(lower, "nano") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.2x", contextLimit: 400000, outputLimit: 128000, inputCost: 0.25, outputCost: 1.00, image: true, reasoning: true})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "1.1x", contextLimit: 400000, outputLimit: 128000, inputCost: 1.75, outputCost: 14.00, image: true, reasoning: true})

	case strings.HasPrefix(lower, "gemini"):
		if strings.Contains(lower, "flash") {
			return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "0.5x", contextLimit: 1000000, outputLimit: 64000, inputCost: 0.35, outputCost: 1.40, image: true, reasoning: true})
		}
		return withVendorEffort(qoderBaseModel{id: cleanID, name: name, multiplier: "1.1x", contextLimit: 1000000, outputLimit: 64000, inputCost: 1.25, outputCost: 5.00, image: true, reasoning: true})
	}

	reasoning := !strings.Contains(lower, "fast") && !strings.Contains(lower, "flash") && !strings.Contains(lower, "lite")
	return withVendorEffort(qoderBaseModel{
		id:           cleanID,
		name:         name,
		contextLimit: 200000,
		outputLimit:  32000,
		image:        true,
		reasoning:    reasoning,
	})
}

func buildQoderModelsFromSlugs(slugs []string) []qoderBaseModel {
	models := make([]qoderBaseModel, 0, len(slugs)+1)
	seen := make(map[string]bool, len(slugs)+1)
	for _, id := range slugs {
		if isFilteredModelByPrefix(id) {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(id))
		if lower == "" || seen[lower] {
			continue
		}
		seen[lower] = true
		models = append(models, inferQoderModelSpec(id))
	}
	if !seen["efficient"] {
		models = append(models, inferQoderModelSpec("Efficient"))
	}
	// Keep Auto first, then Free (0x) models right below Auto so users immediately spot free options.
	sort.SliceStable(models, func(i, j int) bool {
		rank := func(m qoderBaseModel) int {
			if strings.EqualFold(m.id, "auto") {
				return 0
			}
			if m.free {
				return 1
			}
			return 2
		}
		return rank(models[i]) < rank(models[j])
	})
	return models
}

var defaultQoderCNSlugs = []string{
	"Auto", "Qwen3.8-Flash", "Efficient", "Fast",
	"Qwen3.8-Max", "Qwen3.7-Plus", "DeepSeek-V4-Pro", "DeepSeek-Flash",
	"GLM-5.3", "GLM-5.3-Flash", "Kimi-K2.8-Preview", "Kimi-K3",
}

var defaultQoderIntlSlugs = []string{
	"Auto", "Qwen3.8-Flash", "Efficient", "Fast", "Ultimate",
	"claude-sonnet-4-6", "claude-opus-4-6", "gpt-5.2",
	"Qwen3.8-Max", "Qwen3.7-Plus", "DeepSeek-V4-Pro", "DeepSeek-Flash",
	"GLM-5.3", "GLM-5.3-Flash", "Kimi-K2.8-Preview", "Kimi-K3",
}

// qoderProbeTimeout bounds `--list-models`; tests raise it because a freshly
// written executable can take seconds to pass macOS's first-launch checks.
var qoderProbeTimeout = 4 * time.Second

func probeQoderBinaryModels(bin string) ([]qoderBaseModel, bool) {
	if bin == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), qoderProbeTimeout)
	defer cancel()
	cmd := subproc.CommandContext(ctx, bin, "--list-models")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return nil, false
	}
	parsed := ParseQoderModelsOutput(string(out))
	if len(parsed) == 0 {
		return nil, false
	}
	return buildQoderModelsFromSlugs(parsed), true
}

func DiscoverQoderModels() []ModelItem {
	return DiscoverQoderModelsWithRefresh(false)
}

func DiscoverQoderModelsWithRefresh(forceRefresh bool) []ModelItem {
	intlBin, cnBin := ResolveQoderBinaries()
	if intlBin == "" && cnBin == "" {
		return nil
	}
	cacheKey := intlBin + "|" + cnBin
	qoderModelsMu.Lock()
	defer qoderModelsMu.Unlock()

	if !forceRefresh && qoderCachedKey == cacheKey && len(qoderCachedList) > 0 && time.Since(qoderCachedTime) < 10*time.Minute {
		out := make([]ModelItem, len(qoderCachedList))
		copy(out, qoderCachedList)
		return out
	}

	cnModels, cnAuth := probeQoderBinaryModels(cnBin)
	intlModels, intlAuth := probeQoderBinaryModels(intlBin)

	if intlAuth && !cnAuth {
		qoderPreferredBin = intlBin
	} else if cnAuth {
		qoderPreferredBin = cnBin
	} else {
		qoderPreferredBin = firstNonEmpty(cnBin, intlBin)
	}

	// If neither CLI is logged in yet, fall back to the installed edition's defaults.
	if !cnAuth && !intlAuth {
		if cnBin != "" {
			cnModels = buildQoderModelsFromSlugs(defaultQoderCNSlugs)
		}
		if intlBin != "" {
			intlModels = buildQoderModelsFromSlugs(defaultQoderIntlSlugs)
		}
	}

	bothActive := len(cnModels) > 0 && len(intlModels) > 0
	var items []ModelItem

	for _, m := range cnModels {
		id := m.id
		name := m.name
		if bothActive {
			id = "cn:" + m.id
			name = m.name + " (CN)"
		}
		items = append(items, ModelItem{
			ID:            id,
			Name:          name,
			ProviderID:    RuntimeQoder,
			Runtime:       RuntimeQoder,
			Reasoning:     m.reasoning,
			Efforts:       m.efforts,
			DefaultEffort: m.defaultEffort,
			Image:         m.image,
			Free:          m.free,
			Multiplier:    m.multiplier,
			ContextLimit:  m.contextLimit,
			OutputLimit:   m.outputLimit,
			InputCost:     m.inputCost,
			OutputCost:    m.outputCost,
		})
	}
	for _, m := range intlModels {
		id := m.id
		name := m.name
		if bothActive {
			id = "intl:" + m.id
			name = m.name + " (Intl)"
		}
		items = append(items, ModelItem{
			ID:            id,
			Name:          name,
			ProviderID:    RuntimeQoder,
			Runtime:       RuntimeQoder,
			Reasoning:     m.reasoning,
			Efforts:       m.efforts,
			DefaultEffort: m.defaultEffort,
			Image:         m.image,
			Free:          m.free,
			Multiplier:    m.multiplier,
			ContextLimit:  m.contextLimit,
			OutputLimit:   m.outputLimit,
			InputCost:     m.inputCost,
			OutputCost:    m.outputCost,
		})
	}

	qoderCachedKey = cacheKey
	qoderCachedTime = time.Now()
	qoderCachedList = items
	return items
}

// QoderReasoningEffort clamps requested to the efforts modelID supports, or
// returns "" when the model does not reason.
func QoderReasoningEffort(modelID, requested string) string {
	e := NormalizeEffortName(requested)
	if e == "" {
		return ""
	}
	spec := inferQoderModelSpec(strings.TrimSpace(modelID))
	if !spec.reasoning {
		return ""
	}
	return ClampEffortToSupported(e, spec.efforts, spec.defaultEffort)
}
