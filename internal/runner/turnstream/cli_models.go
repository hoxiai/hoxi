package turnstream

import (
	"os"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/claudeconfig"
)

func ResolveAgyBinary() string    { return agentcli.ResolveAgyBinary() }
func ResolveClaudeBinary() string { return agentcli.ResolveClaudeBinary() }
func ResolveGeminiBinary() string { return agentcli.ResolveGeminiBinary() }

func ParseAgyModelsOutput(raw string) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	return agentcli.ParseAgyModelsOutput(raw)
}

func DiscoverAgyModels() ([]agentcli.ModelItem, map[string]string, map[string]string) {
	return DiscoverAgyModelsWithRefresh(false)
}

func DiscoverAgyModelsWithRefresh(forceRefresh bool) ([]agentcli.ModelItem, map[string]string, map[string]string) {
	slugs, dMap, rMap := agentcli.DiscoverAgyCLIModels(forceRefresh)
	if len(slugs) == 0 {
		return nil, nil, nil
	}
	return buildAgyModelItems(slugs, dMap), dMap, rMap
}

func DiscoverGeminiModels() []agentcli.ModelItem {
	return agentcli.DiscoverGeminiModels()
}

func DiscoverGeminiModelsWithRefresh(forceRefresh bool) []agentcli.ModelItem {
	return agentcli.DiscoverGeminiModelsWithRefresh(forceRefresh)
}

func DiscoverClaudeModels() []agentcli.ModelItem {
	slugs := claudeconfig.Models()
	items := make([]agentcli.ModelItem, 0, len(slugs))
	for _, s := range slugs {
		name := agentcli.FormatClaudeModelName(s)
		if name == "" {
			name = s
		}
		ctxLim, outLim, inCost, outCost, image, reasoning := inferOfficialRunnerModelSpec(s)
		efforts, defEffort, supportsEffort := inferClaudeModelEfforts(s)
		if !supportsEffort {
			efforts = nil
			defEffort = ""
		}
		items = append(items, agentcli.ModelItem{
			ID:            s,
			Name:          name,
			ProviderID:    string(RuntimeClaudeCode),
			Runtime:       string(RuntimeClaudeCode),
			Reasoning:     reasoning,
			Efforts:       efforts,
			DefaultEffort: defEffort,
			Image:         image,
			ContextLimit:  ctxLim,
			OutputLimit:   outLim,
			InputCost:     inCost,
			OutputCost:    outCost,
		})
	}
	return items
}

func ShouldGeminiCLIUseAgy(modelID string) bool {
	if strings.TrimSpace(os.Getenv("GEMINI_BIN")) != "" {
		return false
	}
	agyBin := ResolveAgyBinary()
	if agyBin == "" {
		return false
	}
	gemBin := agentcli.ResolveStandaloneGeminiBinary()
	if gemBin == "" || gemBin == agyBin {
		return true
	}
	if !agentcli.HasGeminiCLIDirectAuth() {
		return true
	}
	if agentcli.IsAgyOnlyGeminiModel(modelID) {
		return true
	}
	return false
}

func ResolveGeminiTurnBinary(modelID string) (binPath string, useAgy bool) {
	if ShouldGeminiCLIUseAgy(modelID) {
		return ResolveAgyBinary(), true
	}
	bin := ResolveGeminiBinary()
	if isAgyBinary(bin) {
		return bin, true
	}
	return bin, false
}

func IsAgyBinary(path string) bool { return isAgyBinary(path) }

func isAgyBinary(path string) bool {
	name := path
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(name)
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		if strings.HasSuffix(name, ext) {
			name = strings.TrimSuffix(name, ext)
			break
		}
	}
	return name == "agy"
}

func ResolveGeminiAgyModelAndEffort(modelID, effort string) (string, string) {
	m := strings.TrimSpace(modelID)
	lower := strings.ToLower(m)
	switch {
	case lower == "" || lower == "auto":
		m = ""
	case strings.Contains(lower, "flash"):
		if !agentcli.IsAgyOnlyGeminiModel(lower) {
			m = "gemini-3.8-flash"
		}
	case strings.HasPrefix(lower, "gemini-") && !agentcli.IsAgyOnlyGeminiModel(lower):
		m = "gemini-4-argon"
	}
	return ResolveAgyModelAndEffort(m, effort)
}

func ResolveAgyModelAndEffort(modelID, effort string) (string, string) {
	m := strings.TrimSpace(modelID)
	e := agentcli.NormalizeEffortName(effort)
	if base, suf := splitAgyModelEffortSuffix(m); suf != "" {
		if e == "" {
			return m, ""
		}
		supported := agySupportedEffortsForBase(base)
		e = agentcli.ClampEffortToSupported(e, supported, suf)
		return base + "-" + e, e
	}
	if e != "" {
		supported, def := agentcli.InferVendorEffortSpec(string(RuntimeAntigravity), m, true)
		e = agentcli.ClampEffortToSupported(e, supported, def)
	}
	return m, e
}

func splitAgyModelEffortSuffix(slug string) (base, suffixEffort string) {
	trimmed := strings.TrimSpace(slug)
	lower := strings.ToLower(trimmed)
	for _, eff := range []string{"xhigh", "medium", "high", "low", "max"} {
		suf := "-" + eff
		if strings.HasSuffix(lower, suf) && len(trimmed) > len(suf) {
			return trimmed[:len(trimmed)-len(suf)], eff
		}
	}
	return trimmed, ""
}

func agySupportedEffortsForBase(base string) []string {
	lowerBase := strings.ToLower(strings.TrimSpace(base))
	cached := agentcli.CachedAgyCLISlugs()
	var found []string
	for _, s := range cached {
		if b, suf := splitAgyModelEffortSuffix(s); suf != "" && strings.EqualFold(b, lowerBase) {
			found = append(found, suf)
		}
	}
	if norm := agentcli.NormalizeEffortList(found); len(norm) > 0 {
		return norm
	}
	efforts, _ := agentcli.InferVendorEffortSpec(string(RuntimeAntigravity), lowerBase, true)
	return efforts
}

func InferClaudeModelEfforts(slug string) ([]string, string, bool) {
	return inferClaudeModelEfforts(slug)
}

func inferClaudeModelEfforts(slug string) (efforts []string, defaultEffort string, supportsEffort bool) {
	lower := strings.ToLower(strings.TrimSpace(slug))
	efforts, defaultEffort = agentcli.InferVendorEffortSpec(string(RuntimeClaudeCode), lower, !strings.Contains(lower, "haiku"))
	return efforts, defaultEffort, len(efforts) > 0
}

func InferOfficialRunnerModelSpec(slug string) (int, int, float64, float64, bool, bool) {
	return inferOfficialRunnerModelSpec(slug)
}

func inferOfficialRunnerModelSpec(slug string) (ctxLimit, outLimit int, inCost, outCost float64, image, reasoning bool) {
	lower := strings.ToLower(strings.TrimSpace(slug))
	_, effSuffix := splitAgyModelEffortSuffix(lower)
	image = true
	reasoning = !strings.Contains(lower, "fast") && !strings.Contains(lower, "flash") && !strings.Contains(lower, "lite") && !strings.Contains(lower, "haiku")
	if effSuffix != "" || strings.Contains(lower, "thinking") || strings.Contains(lower, "pro") {
		reasoning = true
	}

	switch {
	case strings.HasPrefix(lower, "claude") || lower == "opus" || lower == "sonnet" || lower == "haiku" || lower == "fable":
		_, _, supportsEffort := inferClaudeModelEfforts(lower)
		if effSuffix != "" {
			supportsEffort = true
		}
		ctxLimit, outLimit = claudeModelLimits(lower)
		switch {
		case strings.Contains(lower, "opus") || strings.Contains(lower, "fable"):
			return ctxLimit, outLimit, 5.00, 25.00, true, true
		case strings.Contains(lower, "haiku"):
			return ctxLimit, outLimit, 0.80, 4.00, true, supportsEffort
		default:
			return ctxLimit, outLimit, 3.00, 15.00, true, true
		}
	case strings.HasPrefix(lower, "gemini"):
		gemOut := 65536
		if familyMajorVersion(lower, "gemini") >= 4 || strings.Contains(lower, "argon") || strings.Contains(lower, "carbon") {
			gemOut = 131072
		}
		switch {
		case strings.Contains(lower, "lite"):
			return 1000000, gemOut, 0.10, 0.40, true, reasoning
		case strings.Contains(lower, "flash"):
			return 1000000, gemOut, 0.35, 1.40, true, reasoning
		default:
			return 1000000, gemOut, 1.25, 10.00, true, true
		}
	case strings.HasPrefix(lower, "gpt") || strings.Contains(lower, "codex") || agentcli.IsOpenAIOSeriesModel(lower):
		switch {
		case strings.Contains(lower, "nano"):
			return 400000, 128000, 0.10, 0.40, true, reasoning
		case familyMajorVersion(lower, "gpt") >= 6 && strings.Contains(lower, "luna"):
			return 1050000, 128000, 0.25, 1.00, true, reasoning
		case strings.Contains(lower, "mini") || strings.Contains(lower, "luna"):
			return 400000, 128000, 0.25, 1.00, true, reasoning
		case strings.Contains(lower, "astra") || familyMajorVersion(lower, "gpt") >= 6 || hasModelVersionRun(lower, "5", "6"):
			return 1050000, 128000, 6.00, 24.00, true, true
		case strings.Contains(lower, "sol") || hasModelVersionRun(lower, "5", "5"):
			return 1000000, 128000, 3.00, 12.00, true, true
		default:
			return 400000, 128000, 2.00, 8.00, true, true
		}
	}
	return 200000, 64000, 0, 0, image, reasoning
}

func claudeModelLimits(lower string) (ctxLimit, outLimit int) {
	ctxLimit, outLimit = 200000, 64000
	if strings.Contains(lower, "[1m]") {
		ctxLimit = 1000000
	}
	if claudeMajorVersion(lower) >= 5 {
		ctxLimit, outLimit = 1000000, 128000
	}
	return ctxLimit, outLimit
}

func claudeMajorVersion(lower string) int {
	return familyMajorVersion(lower, "claude")
}

func modelKeyTokens(lower string) []string {
	return strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' ' || r == '/'
	})
}

func familyMajorVersion(lower, family string) int {
	parts := modelKeyTokens(lower)
	for i, part := range parts {
		if part != family {
			continue
		}
		for _, next := range parts[i+1:] {
			major, digits := 0, 0
			for _, r := range next {
				if r < '0' || r > '9' {
					break
				}
				major = major*10 + int(r-'0')
				digits++
			}
			if digits > 0 {
				return major
			}
		}
		return 0
	}
	return 0
}

func hasModelVersionRun(lower, firstSeg, secondSeg string) bool {
	parts := modelKeyTokens(lower)
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == firstSeg && parts[i+1] == secondSeg {
			return true
		}
	}
	return false
}

func buildAgyModelItems(slugs []string, displayBySlug map[string]string) []agentcli.ModelItem {
	familyEfforts := make(map[string][]string)
	for _, s := range slugs {
		if base, suf := splitAgyModelEffortSuffix(s); suf != "" {
			familyEfforts[strings.ToLower(base)] = append(familyEfforts[strings.ToLower(base)], suf)
		}
	}
	items := make([]agentcli.ModelItem, 0, len(slugs))
	for _, s := range slugs {
		name := displayBySlug[s]
		if name == "" {
			name = s
		}
		ctxLim, outLim, inCost, outCost, image, reasoning := inferOfficialRunnerModelSpec(s)
		var efforts []string
		var defaultEffort string
		if base, suf := splitAgyModelEffortSuffix(s); suf != "" {
			reasoning = true
			defaultEffort = suf
			efforts = agentcli.NormalizeEffortList(familyEfforts[strings.ToLower(base)])
			if len(efforts) == 0 {
				efforts = []string{suf}
			}
		} else if reasoning {
			efforts, defaultEffort = agentcli.InferVendorEffortSpec(string(RuntimeAntigravity), s, true)
		}
		items = append(items, agentcli.ModelItem{
			ID:            s,
			Name:          name,
			ProviderID:    string(RuntimeAntigravity),
			Runtime:       string(RuntimeAntigravity),
			Reasoning:     reasoning,
			Efforts:       efforts,
			DefaultEffort: defaultEffort,
			Image:         image,
			ContextLimit:  ctxLim,
			OutputLimit:   outLim,
			InputCost:     inCost,
			OutputCost:    outCost,
		})
	}
	return items
}
