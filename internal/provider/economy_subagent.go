package provider

import (
	"regexp"
	"sort"
	"strings"
)

var (
	nonToolLegacyReasoningRe = regexp.MustCompile(`(?i)^o1(?:$|[-_/.])`)
	economyTier1TokenRe      = regexp.MustCompile(`(?i)(?:^|[-_/.])(?:haiku|flash|mini)(?:$|[-_/.0-9])`)
	economyTier2TokenRe      = regexp.MustCompile(`(?i)(?:^|[-_/.])(?:nano|lite|8b)(?:$|[-_/.0-9])`)
)

// ResolveEconomyModel selects a fast, low-cost, tool-capable chat model strictly
// within the SAME configured provider `providerID` for read-only exploration
// subagents (`explore`, `scout`). It never crosses provider boundaries.
func (s *Service) ResolveEconomyModel(providerID, currentModelID string) (string, bool) {
	if s == nil {
		return "", false
	}
	pid, curMid := s.resolveBackendAutoCandidate(strings.TrimSpace(providerID), strings.TrimSpace(currentModelID))
	if pid == "" || pid == AutoProviderID {
		return "", false
	}
	disabled := s.disabledProvidersSet()
	if disabled[pid] || disabled[strings.ToLower(pid)] {
		return "", false
	}
	baseURL, apiKey, _, _ := s.resolveProviderConnection(pid, "", "")
	if !isHTTPEndpoint(baseURL) || (apiKey == "" && !isLoopbackURL(baseURL)) {
		return "", false
	}
	if curMid != "" && economySubagentTier(curMid, nil) > 0 && !isNonChatUtilityModel(curMid) {
		if s.ResolveStreamTarget(pid, curMid).HasEndpoint {
			return curMid, true
		}
	}

	hoxiMap := s.loadHoxiProvidersMap()
	importedMap := s.loadOpenCodeImportedProviderMap()
	keep := hoxiSelectedModels(pid, hoxiMap)

	mergedModels := make(map[string]map[string]interface{})
	collectFrom := func(provMap map[string]interface{}) {
		pObj, ok := provMap[pid].(map[string]interface{})
		if !ok || pObj == nil {
			return
		}
		mMap, ok := pObj["models"].(map[string]interface{})
		if !ok || mMap == nil {
			return
		}
		for rawMid, rawVal := range mMap {
			mid := strings.TrimSpace(rawMid)
			if mid == "" {
				continue
			}
			if keep != nil {
				if _, allowed := keep[mid]; !allowed {
					continue
				}
			}
			mObj, _ := rawVal.(map[string]interface{})
			if existing, exists := mergedModels[mid]; exists && existing != nil && mObj == nil {
				continue
			}
			mergedModels[mid] = mObj
		}
	}
	collectFrom(importedMap)
	collectFrom(hoxiMap)
	if len(mergedModels) == 0 {
		return "", false
	}

	var tier1, tier2 []string
	for mid, mObj := range mergedModels {
		if nonToolLegacyReasoningRe.MatchString(mid) || isNonChatUtilityCandidate(mid, mObj) {
			continue
		}
		if s.breaker != nil {
			if ok, _ := s.breaker.Evaluate(pid, mid); !ok {
				continue
			}
		}
		switch economySubagentTier(mid, mObj) {
		case 1:
			tier1 = append(tier1, mid)
		case 2:
			tier2 = append(tier2, mid)
		}
	}

	for _, group := range [][]string{tier1, tier2} {
		if len(group) == 0 {
			continue
		}
		sortEconomyModelIDs(group)
		for _, cand := range group {
			if s.ResolveStreamTarget(pid, cand).HasEndpoint {
				return cand, true
			}
		}
	}
	return "", false
}

// economySubagentTier classifies a model into:
//   - 1: primary economy workhorse (haiku, standard flash, standard mini)
//   - 2: ultra-light economy fallback (flash-lite, nano, lite)
//   - 0: non-economy / frontier model
func economySubagentTier(modelID string, mObj map[string]interface{}) int {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" || nonToolLegacyReasoningRe.MatchString(lower) {
		return 0
	}
	isTier2Token := economyTier2TokenRe.MatchString(lower)
	if mObj != nil {
		if fam, ok := mObj["family"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(fam)) {
			case "claude-haiku":
				return 1
			case "gemini-flash":
				if isTier2Token {
					return 2
				}
				return 1
			case "gpt-nano":
				return 2
			}
		}
	}
	if economyTier1TokenRe.MatchString(lower) {
		if isTier2Token {
			return 2
		}
		return 1
	}
	if isTier2Token {
		return 2
	}
	return 0
}

func sortEconomyModelIDs(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		aExp := isExperimentalModelID(a)
		bExp := isExperimentalModelID(b)
		if aExp != bExp {
			return !aExp
		}
		return a > b
	})
}

func isExperimentalModelID(id string) bool {
	lower := strings.ToLower(id)
	return strings.Contains(lower, "preview") || strings.Contains(lower, "-exp") || strings.Contains(lower, "experimental")
}
