package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/subproc"
)

// CodexHomeDir returns $CODEX_HOME or ~/.codex.
func CodexHomeDir() string {
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return filepath.Clean(env)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// ResolveCodexBinary locates the OpenAI Codex CLI (`codex`) binary.
func ResolveCodexBinary() string {
	if env := strings.TrimSpace(os.Getenv("HOXI_CODEX_BIN")); env != "" {
		return FirstExecutableFile(env)
	}
	if cDir := CodexHomeDir(); cDir != "" {
		if p := FirstExecutableFile(filepath.Join(cDir, "bin", "codex")); p != "" {
			return p
		}
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
		if env := strings.TrimSpace(os.Getenv(key)); env != "" &&
			!strings.EqualFold(env, "opencode") &&
			!strings.EqualFold(env, "opencode.exe") {
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

// CodexModelSpec describes one model from Codex CLI's catalog.
type CodexModelSpec struct {
	Slug          string
	DisplayName   string
	Priority      int
	HasPriority   bool
	UpgradeTo     string
	Efforts       []string
	DefaultEffort string
	Image         bool
	ContextLimit  int
	OutputLimit   int
}

// CodexCatalogSnapshot represents the resolved model catalog and default model for Codex CLI.
type CodexCatalogSnapshot struct {
	Version      string
	DefaultModel string
	Models       []CodexModelSpec
}

func stripTOMLInlineComment(line string) string {
	var inSingle, inDouble, escaped bool
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inDouble {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if ch == '#' && !inSingle && !inDouble {
			return line[:i]
		}
	}
	return line
}

// ReadCodexConfiguredModel reads the top-level `model = "..."` setting from ~/.codex/config.toml.
func ReadCodexConfiguredModel() string {
	cDir := CodexHomeDir()
	if cDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(cDir, "config.toml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(stripTOMLInlineComment(line))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		k, v, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(k) != "model" {
			continue
		}
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		if val != "" && !strings.ContainsAny(val, " \t()") {
			return val
		}
	}
	return ""
}

// FormatCodexModelName formats a Codex model slug into a readable display label when display_name is absent.
func FormatCodexModelName(slug string) string {
	trimmed := strings.TrimSpace(slug)
	lower := strings.ToLower(trimmed)
	if lower == "" || IsOpenAIOSeriesModel(lower) {
		return trimmed
	}
	if !strings.HasPrefix(lower, "gpt-") {
		return trimmed
	}
	rest := strings.TrimPrefix(trimmed, trimmed[:4])
	parts := strings.Split(rest, "-")
	out := []string{"GPT"}
	for i, p := range parts {
		if p == "" {
			continue
		}
		lp := strings.ToLower(p)
		if i == 0 && len(p) > 0 && p[0] >= '0' && p[0] <= '9' {
			out[0] = "GPT-" + p
			continue
		}
		if isAllASCIIAlpha(lp) {
			out = append(out, strings.ToUpper(lp[:1])+lp[1:])
		} else {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func mapCodexEffortToken(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "minimal":
		return "low"
	case "ultra":
		return "max"
	default:
		return NormalizeEffortName(raw)
	}
}

type rawCodexCatalogDoc struct {
	ClientVersion string              `json:"client_version"`
	Models        []rawCodexModelItem `json:"models"`
}

type rawCodexModelItem struct {
	Slug                     string `json:"slug"`
	DisplayName              string `json:"display_name"`
	DefaultReasoningLevel    string `json:"default_reasoning_level"`
	SupportedReasoningLevels []struct {
		Effort string `json:"effort"`
	} `json:"supported_reasoning_levels"`
	Visibility       string   `json:"visibility"`
	Priority         *int     `json:"priority"`
	ContextWindow    int      `json:"context_window"`
	MaxContextWindow int      `json:"max_context_window"`
	InputModalities  []string `json:"input_modalities"`
	Upgrade          *struct {
		Model string `json:"model"`
	} `json:"upgrade"`
}

// ParseCodexCatalogJSON parses `~/.codex/models_cache.json` or `codex debug models` JSON output.
func ParseCodexCatalogJSON(raw []byte) (clientVersion string, visible []CodexModelSpec, hidden map[string]bool) {
	var doc rawCodexCatalogDoc
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &doc) != nil {
		return "", nil, nil
	}
	clientVersion = strings.TrimSpace(doc.ClientVersion)
	hidden = make(map[string]bool)
	seen := make(map[string]bool, len(doc.Models))
	for _, item := range doc.Models {
		slug := strings.TrimSpace(item.Slug)
		if slug == "" || strings.ContainsAny(slug, " \t()") {
			continue
		}
		vis := strings.ToLower(strings.TrimSpace(item.Visibility))
		if vis != "" && vis != "list" {
			hidden[slug] = true
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true

		var rawEfforts []string
		for _, rl := range item.SupportedReasoningLevels {
			if mapped := mapCodexEffortToken(rl.Effort); mapped != "" {
				rawEfforts = append(rawEfforts, mapped)
			}
		}
		efforts := NormalizeEffortList(rawEfforts)
		fallbackEfforts, fallbackDef := InferVendorEffortSpec(RuntimeCodex, slug, true)
		if len(efforts) == 0 {
			efforts = fallbackEfforts
		}
		defEffort := mapCodexEffortToken(item.DefaultReasoningLevel)
		if defEffort == "" {
			defEffort = fallbackDef
		}
		defEffort = ClampEffortToSupported(defEffort, efforts, fallbackDef)

		image := true
		if len(item.InputModalities) > 0 {
			image = false
			for _, mod := range item.InputModalities {
				if strings.EqualFold(strings.TrimSpace(mod), "image") {
					image = true
					break
				}
			}
		}

		ctxLimit := item.MaxContextWindow
		if item.ContextWindow > ctxLimit {
			ctxLimit = item.ContextWindow
		}
		if ctxLimit <= 0 {
			ctxLimit = inferCodexContextLimit(slug)
		}

		display := strings.TrimSpace(item.DisplayName)
		if display == "" {
			display = FormatCodexModelName(slug)
		}

		spec := CodexModelSpec{
			Slug:          slug,
			DisplayName:   display,
			Efforts:       efforts,
			DefaultEffort: defEffort,
			Image:         image,
			ContextLimit:  ctxLimit,
			OutputLimit:   128000,
		}
		if item.Priority != nil {
			spec.Priority = *item.Priority
			spec.HasPriority = true
		}
		if item.Upgrade != nil {
			spec.UpgradeTo = strings.TrimSpace(item.Upgrade.Model)
		}
		visible = append(visible, spec)
	}
	return clientVersion, visible, hidden
}

func inferCodexContextLimit(slug string) int {
	lower := strings.ToLower(strings.TrimSpace(slug))
	switch {
	case strings.Contains(lower, "astra") || majorAfterFamily(lower, "gpt") >= 6 || strings.HasPrefix(lower, "gpt-5.6"):
		return 1050000
	case strings.Contains(lower, "sol") || strings.HasPrefix(lower, "gpt-5.5"):
		return 1000000
	default:
		return 400000
	}
}

func parseCodexSemver(raw string) ([3]int, bool) {
	for _, tok := range strings.Fields(strings.TrimSpace(raw)) {
		clean := strings.TrimPrefix(strings.TrimSpace(tok), "v")
		if dash := strings.IndexByte(clean, '-'); dash > 0 {
			clean = clean[:dash]
		}
		parts := strings.Split(clean, ".")
		if len(parts) < 2 {
			continue
		}
		var ver [3]int
		ok := true
		for i := 0; i < len(parts) && i < 3; i++ {
			n, err := strconv.Atoi(parts[i])
			if err != nil || n < 0 {
				ok = false
				break
			}
			ver[i] = n
		}
		if ok {
			return ver, true
		}
	}
	return [3]int{}, false
}

func compareCodexVersions(a, b string) int {
	va, okA := parseCodexSemver(a)
	vb, okB := parseCodexSemver(b)
	switch {
	case okA && !okB:
		return 1
	case !okA && okB:
		return -1
	case !okA && !okB:
		return 0
	}
	for i := 0; i < 3; i++ {
		if va[i] != vb[i] {
			if va[i] > vb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

var (
	codexBundledMu      sync.Mutex
	codexBundledBin     string
	codexBundledMtime   int64
	codexBundledSize    int64
	codexBundledAt      time.Time
	codexBundledVer     string
	codexBundledVisible []CodexModelSpec
	codexBundledHidden  map[string]bool
)

func cloneCodexModelSpecs(in []CodexModelSpec) []CodexModelSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]CodexModelSpec, len(in))
	for i, s := range in {
		s.Efforts = append([]string(nil), s.Efforts...)
		out[i] = s
	}
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func withIsolatedCodexHome(env []string, tmpHome string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CODEX_HOME="+tmpHome)
}

func probeCodexBundledCatalog(binPath string, forceRefresh bool) (string, []CodexModelSpec, map[string]bool) {
	if binPath == "" {
		return "", nil, nil
	}
	st, err := os.Stat(binPath)
	if err != nil || st.IsDir() {
		return "", nil, nil
	}
	mtime, size := st.ModTime().UnixNano(), st.Size()

	codexBundledMu.Lock()
	if !forceRefresh &&
		codexBundledBin == binPath &&
		codexBundledMtime == mtime &&
		codexBundledSize == size &&
		len(codexBundledVisible) > 0 &&
		time.Since(codexBundledAt) < 5*time.Minute {
		ver := codexBundledVer
		vis := cloneCodexModelSpecs(codexBundledVisible)
		hid := cloneBoolMap(codexBundledHidden)
		codexBundledMu.Unlock()
		return ver, vis, hid
	}
	codexBundledMu.Unlock()

	tmpHome, tmpErr := os.MkdirTemp("", "hoxi-codex-models-*")
	if tmpErr != nil {
		return "", nil, nil
	}
	defer os.RemoveAll(tmpHome)

	env := withIsolatedCodexHome(WithSystemProxy(WithUserBinPath(os.Environ())), tmpHome)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cmd := subproc.CommandContext(ctx, binPath, "debug", "models", "--bundled")
	cmd.Env = env
	cmd.WaitDelay = time.Second
	out, runErr := cmd.Output()
	cancel()

	var ver string
	var visible []CodexModelSpec
	var hidden map[string]bool
	if runErr == nil && len(bytes.TrimSpace(out)) > 0 {
		ver, visible, hidden = ParseCodexCatalogJSON(out)
	}

	if ver == "" {
		vCtx, vCancel := context.WithTimeout(context.Background(), 3*time.Second)
		vCmd := subproc.CommandContext(vCtx, binPath, "--version")
		vCmd.Env = env
		vCmd.WaitDelay = time.Second
		if vOut, vErr := vCmd.Output(); vErr == nil {
			if parsed, ok := parseCodexSemver(string(vOut)); ok {
				ver = strconv.Itoa(parsed[0]) + "." + strconv.Itoa(parsed[1]) + "." + strconv.Itoa(parsed[2])
			} else {
				ver = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(vOut)), "codex-cli"))
			}
		}
		vCancel()
	}

	if len(visible) > 0 {
		codexBundledMu.Lock()
		codexBundledBin = binPath
		codexBundledMtime = mtime
		codexBundledSize = size
		codexBundledAt = time.Now()
		codexBundledVer = ver
		codexBundledVisible = cloneCodexModelSpecs(visible)
		codexBundledHidden = cloneBoolMap(hidden)
		codexBundledMu.Unlock()
	}
	return ver, visible, hidden
}

// DefaultCodexCatalogModels returns the built-in fallback Codex model specs when neither
// `~/.codex/models_cache.json` nor `codex debug models --bundled` is available.
func DefaultCodexCatalogModels() []CodexModelSpec {
	full := []string{"low", "medium", "high", "xhigh", "max"}
	legacy := []string{"low", "medium", "high", "xhigh"}
	return []CodexModelSpec{
		{Slug: "gpt-6.1-sol", DisplayName: "GPT-6.1-Sol", Priority: 1, HasPriority: true, Efforts: full, DefaultEffort: "low", Image: true, ContextLimit: 1050000, OutputLimit: 128000},
		{Slug: "gpt-6-astra", DisplayName: "GPT-6-Astra", Priority: 2, HasPriority: true, Efforts: full, DefaultEffort: "low", Image: true, ContextLimit: 1050000, OutputLimit: 128000},
		{Slug: "gpt-6-sol", DisplayName: "GPT-6-Sol", Priority: 3, HasPriority: true, Efforts: full, DefaultEffort: "medium", Image: true, ContextLimit: 1050000, OutputLimit: 128000},
		{Slug: "gpt-6-luna", DisplayName: "GPT-6-Luna", Priority: 4, HasPriority: true, Efforts: full, DefaultEffort: "medium", Image: true, ContextLimit: 1050000, OutputLimit: 128000},
		{Slug: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Priority: 5, HasPriority: true, UpgradeTo: "gpt-6-sol", Efforts: full, DefaultEffort: "low", Image: true, ContextLimit: 1050000, OutputLimit: 128000},
		{Slug: "gpt-6.1-terra", DisplayName: "GPT-6.1 Terra", Priority: 7, HasPriority: true, Efforts: full, DefaultEffort: "low", Image: true, ContextLimit: 1050000, OutputLimit: 128000},
		{Slug: "gpt-5.5", DisplayName: "GPT-5.5", Priority: 13, HasPriority: true, UpgradeTo: "gpt-6-sol", Efforts: legacy, DefaultEffort: "medium", Image: true, ContextLimit: 1000000, OutputLimit: 128000},
	}
}

func sortCodexModelsByPriority(models []CodexModelSpec) {
	sort.SliceStable(models, func(i, j int) bool {
		a, b := models[i], models[j]
		if a.HasPriority != b.HasPriority {
			return a.HasPriority
		}
		if a.HasPriority && a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return false
	})
}

// DiscoverCodexCatalog resolves the latest Codex CLI model catalog from `~/.codex/models_cache.json`
// and the installed `codex` binary (`codex debug models --bundled`), ordered by Codex priority.
func DiscoverCodexCatalog(forceRefresh bool) CodexCatalogSnapshot {
	var cacheVer string
	var cacheVis []CodexModelSpec
	var cacheHidden map[string]bool
	if cDir := CodexHomeDir(); cDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cDir, "models_cache.json")); err == nil {
			cacheVer, cacheVis, cacheHidden = ParseCodexCatalogJSON(raw)
		}
	}

	binVer, binVis, binHidden := probeCodexBundledCatalog(ResolveCodexBinary(), forceRefresh)

	var merged []CodexModelSpec
	var version string
	cmp := compareCodexVersions(binVer, cacheVer)
	switch {
	case len(binVis) > 0 && (len(cacheVis) == 0 || cmp > 0 || (binVer == "" && forceRefresh)):
		// Installed binary is newer than the on-disk cache (or cache is absent):
		// drop stale pre-upgrade cache models and use the binary's current catalog.
		version = firstNonEmpty(binVer, cacheVer)
		merged = append(merged, binVis...)
	case len(cacheVis) > 0:
		// On-disk cache is from the same or newer client_version: keep server-fetched
		// models primary and append any visible bundled models not hidden by the server.
		version = firstNonEmpty(cacheVer, binVer)
		seen := make(map[string]bool, len(cacheVis)+len(binVis))
		for _, m := range cacheVis {
			seen[m.Slug] = true
			merged = append(merged, m)
		}
		for _, m := range binVis {
			if !seen[m.Slug] && !cacheHidden[m.Slug] && !binHidden[m.Slug] {
				seen[m.Slug] = true
				merged = append(merged, m)
			}
		}
	default:
		version = firstNonEmpty(binVer, cacheVer)
	}

	sortCodexModelsByPriority(merged)
	fromCatalog := len(merged) > 0
	if !fromCatalog {
		merged = DefaultCodexCatalogModels()
	}

	cfgModel := ReadCodexConfiguredModel()
	var defaultModel string
	if cfgModel != "" {
		foundIdx := -1
		for i, m := range merged {
			if m.Slug == cfgModel {
				foundIdx = i
				break
			}
		}
		switch {
		case foundIdx >= 0 && merged[foundIdx].UpgradeTo == "":
			defaultModel = cfgModel
		case !fromCatalog && foundIdx < 0:
			efforts, defEff := InferVendorEffortSpec(RuntimeCodex, cfgModel, true)
			customSpec := CodexModelSpec{
				Slug:          cfgModel,
				DisplayName:   FormatCodexModelName(cfgModel),
				Efforts:       efforts,
				DefaultEffort: defEff,
				Image:         true,
				ContextLimit:  inferCodexContextLimit(cfgModel),
				OutputLimit:   128000,
			}
			merged = append([]CodexModelSpec{customSpec}, merged...)
			defaultModel = cfgModel
		}
	}
	if defaultModel == "" && len(merged) > 0 {
		defaultModel = merged[0].Slug
	}

	return CodexCatalogSnapshot{
		Version:      version,
		DefaultModel: defaultModel,
		Models:       merged,
	}
}

// BuildCodexRunnerModels converts a CodexCatalogSnapshot into runner ModelItem entries.
func BuildCodexRunnerModels(snap CodexCatalogSnapshot) []ModelItem {
	out := make([]ModelItem, 0, len(snap.Models))
	for _, m := range snap.Models {
		out = append(out, ModelItem{
			ID:            m.Slug,
			Name:          m.DisplayName,
			ProviderID:    RuntimeCodex,
			Runtime:       RuntimeCodex,
			Reasoning:     true,
			Efforts:       append([]string(nil), m.Efforts...),
			DefaultEffort: m.DefaultEffort,
			Image:         m.Image,
			ContextLimit:  m.ContextLimit,
			OutputLimit:   m.OutputLimit,
		})
	}
	return out
}

// DiscoverCodexModelsWithRefresh returns available models for the Codex CLI runner.
func DiscoverCodexModelsWithRefresh(forceRefresh bool) []ModelItem {
	return BuildCodexRunnerModels(DiscoverCodexCatalog(forceRefresh))
}
