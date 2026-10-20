package agentcli

import "strings"

var canonicalEfforts = []string{"low", "medium", "high", "xhigh", "max"}

func effortRank(e string) int {
	switch strings.ToLower(strings.TrimSpace(e)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "xhigh":
		return 4
	case "max":
		return 5
	default:
		return 0
	}
}

// NormalizeEffortName returns raw as a canonical effort level, or "".
func NormalizeEffortName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if effortRank(s) > 0 {
		return s
	}
	return ""
}

// NormalizeEffortList canonicalizes, de-duplicates and orders effort levels.
func NormalizeEffortList(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	for _, r := range raw {
		if n := NormalizeEffortName(r); n != "" {
			seen[n] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, c := range canonicalEfforts {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

// ClampEffortToSupported resolves a requested effort level against a model's supported
// effort levels, choosing the closest available level (breaking ties upward toward deeper reasoning).
func ClampEffortToSupported(requested string, supported []string, defaultEffort string) string {
	req := NormalizeEffortName(requested)
	if req == "" {
		return ""
	}
	normSupported := NormalizeEffortList(supported)
	if len(normSupported) == 0 {
		if def := NormalizeEffortName(defaultEffort); def != "" {
			return def
		}
		return req
	}
	for _, s := range normSupported {
		if s == req {
			return req
		}
	}
	reqRank := effortRank(req)
	best := normSupported[0]
	bestDist := absInt(effortRank(best) - reqRank)
	for _, cand := range normSupported[1:] {
		dist := absInt(effortRank(cand) - reqRank)
		if dist < bestDist || (dist == bestDist && effortRank(cand) > effortRank(best)) {
			best = cand
			bestDist = dist
		}
	}
	return best
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// IsOpenAIOSeriesModel reports whether a lowercased model ID belongs to the OpenAI o-series (e.g., o1, o3, o4, o5).
func IsOpenAIOSeriesModel(lowerMID string) bool {
	if len(lowerMID) < 2 || lowerMID[0] != 'o' || lowerMID[1] < '1' || lowerMID[1] > '9' {
		return false
	}
	i := 1
	for i < len(lowerMID) && lowerMID[i] >= '0' && lowerMID[i] <= '9' {
		i++
	}
	return i == len(lowerMID) || lowerMID[i] == '-' || lowerMID[i] == '.' || lowerMID[i] == '_'
}

// InferVendorEffortSpec returns vendor-level reasoning effort tiers and default effort
// by vendor/protocol family without hardcoding specific model version numbers.
func InferVendorEffortSpec(runtimeOrVendor, modelID string, reasoning bool) (efforts []string, defaultEffort string) {
	if !reasoning {
		return nil, ""
	}
	rv := strings.ToLower(strings.TrimSpace(runtimeOrVendor))
	m := strings.ToLower(strings.TrimSpace(modelID))

	switch {
	case strings.Contains(m, "claude") || rv == RuntimeClaudeCode || strings.Contains(rv, "anthropic"):
		if strings.Contains(m, "haiku") {
			return nil, ""
		}
		return []string{"low", "medium", "high", "xhigh", "max"}, "high"

	case rv == RuntimeAntigravity:
		if strings.Contains(m, "oss-") {
			return []string{"medium"}, "medium"
		}
		return []string{"low", "medium", "high"}, "high"

	case strings.Contains(m, "gpt") || strings.Contains(m, "codex") ||
		IsOpenAIOSeriesModel(m) ||
		strings.Contains(rv, "openai") || strings.Contains(rv, "codex"):
		if strings.Contains(m, "oss-") {
			return []string{"medium"}, "medium"
		}
		return []string{"low", "medium", "high", "xhigh"}, "high"

	case strings.Contains(m, "gemini") || strings.Contains(rv, "google"):
		return []string{"low", "medium", "high", "max"}, "high"

	default:
		return []string{"low", "medium", "high"}, "medium"
	}
}
