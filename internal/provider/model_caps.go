package provider

import "strings"

func defaultModelVariants(providerID, npm, modelID string) map[string]interface{} {
	lowerPID := strings.ToLower(strings.TrimSpace(providerID))
	lowerNPM := strings.ToLower(strings.TrimSpace(npm))
	lowerMID := strings.ToLower(strings.TrimSpace(modelID))

	if strings.Contains(lowerNPM, "anthropic") || lowerPID == "anthropic" {
		if strings.Contains(lowerMID, "opus-4-6") || strings.Contains(lowerMID, "opus-4.6") ||
			strings.Contains(lowerMID, "sonnet-4-6") || strings.Contains(lowerMID, "sonnet-4.6") ||
			strings.Contains(lowerMID, "opus-4-7") || strings.Contains(lowerMID, "opus-4.7") ||
			strings.Contains(lowerMID, "opus-4-8") || strings.Contains(lowerMID, "opus-4.8") ||
			strings.Contains(lowerMID, "opus-5") || strings.Contains(lowerMID, "sonnet-5") ||
			strings.Contains(lowerMID, "haiku-5") || strings.Contains(lowerMID, "fable-5") {
			return map[string]interface{}{
				"low":    map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "low"},
				"medium": map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "medium"},
				"high":   map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "high"},
				"max":    map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "max"},
			}
		}
		return map[string]interface{}{
			"low":    map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 4096}},
			"medium": map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 10240}},
			"high":   map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 16000}},
			"max":    map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 31999}},
		}
	}

	if strings.Contains(lowerNPM, "google") || lowerPID == "google" || lowerPID == "gemini" {
		if strings.Contains(lowerMID, "gemini-3") || strings.Contains(lowerMID, "gemini-4") {
			return map[string]interface{}{
				"low":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": "low"}},
				"medium": map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": "medium"}},
				"high":   map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": "high"}},
			}
		}
		return map[string]interface{}{
			"low":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 4096}},
			"medium": map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 10240}},
			"high":   map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 16000}},
			"max":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 24576}},
		}
	}

	return map[string]interface{}{
		"low":    map[string]interface{}{"reasoningEffort": "low"},
		"medium": map[string]interface{}{"reasoningEffort": "medium"},
		"high":   map[string]interface{}{"reasoningEffort": "high"},
	}
}

// isNonChatUtilityModel returns true only for non-conversational utility endpoints
// (embeddings, rerankers, speech/audio transcription, moderation). All other chat/coding
// models default to supporting image attachments and reasoning unless explicitly disabled.
func isNonChatUtilityModel(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return true
	}
	for _, tok := range []string{"embedding", "embed", "rerank", "bge-", "whisper", "tts", "speech", "moderation"} {
		if strings.Contains(id, tok) {
			return true
		}
	}
	return false
}

func hasImageInputModality(mObj map[string]interface{}) bool {
	modMap, ok := mObj["modalities"].(map[string]interface{})
	if !ok {
		return false
	}
	switch inp := modMap["input"].(type) {
	case []interface{}:
		for _, v := range inp {
			if s, _ := v.(string); strings.EqualFold(strings.TrimSpace(s), "image") {
				return true
			}
		}
	case []string:
		for _, s := range inp {
			if strings.EqualFold(strings.TrimSpace(s), "image") {
				return true
			}
		}
	}
	return false
}

func ensureImageInputModality(mObj map[string]interface{}) {
	modMap, _ := mObj["modalities"].(map[string]interface{})
	if modMap == nil {
		modMap = map[string]interface{}{}
	}
	if !hasImageInputModality(mObj) {
		modMap["input"] = []string{"text", "image"}
	}
	if _, hasOut := modMap["output"]; !hasOut {
		modMap["output"] = []string{"text"}
	}
	mObj["modalities"] = modMap
}

func normalizeDocModelVariants(doc map[string]interface{}) {
	provMap, ok := doc["provider"].(map[string]interface{})
	if !ok {
		return
	}
	for pid, pVal := range provMap {
		pObj, ok := pVal.(map[string]interface{})
		if !ok {
			continue
		}
		npm, _ := pObj["npm"].(string)
		mMap, ok := pObj["models"].(map[string]interface{})
		if !ok {
			continue
		}
		for mid, mVal := range mMap {
			mObj, ok := mVal.(map[string]interface{})
			if !ok {
				continue
			}
			isChat := !isNonChatUtilityModel(mid)
			_, hasExplicitAttachment := mObj["attachment"]
			attachBool, _ := mObj["attachment"].(bool)
			if !attachBool {
				if caps, ok := mObj["capabilities"].(map[string]interface{}); ok {
					attachBool, _ = caps["attachment"].(bool)
				}
			}

			if !hasExplicitAttachment {
				if hasImageInputModality(mObj) || isChat {
					attachBool = true
				}
				mObj["attachment"] = attachBool
			}
			if attachBool || hasImageInputModality(mObj) {
				mObj["attachment"] = true
				ensureImageInputModality(mObj)
			}

			isReasoning, hasExplicitReasoning := mObj["reasoning"].(bool)
			if !isReasoning {
				if caps, ok := mObj["capabilities"].(map[string]interface{}); ok {
					isReasoning, _ = caps["reasoning"].(bool)
				}
			}
			if !isReasoning && (!hasExplicitReasoning || !hasExplicitAttachment) && isChat {
				isReasoning = true
				mObj["reasoning"] = true
			}
			if !isReasoning {
				continue
			}
			vMap, hasVariants := mObj["variants"].(map[string]interface{})
			if !hasVariants || len(vMap) == 0 {
				mObj["variants"] = defaultModelVariants(pid, npm, mid)
			}
		}
	}
}
