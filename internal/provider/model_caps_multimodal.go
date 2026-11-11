package provider

import "strings"

// inferSmallModelFamily returns an OpenCode-recognized small-model family tag
// ("gemini-flash", "gpt-nano", "claude-haiku") for lightweight Flash/Mini/Nano/Haiku
// chat models so OpenCode's Provider.getSmallModel automatically routes internal
// background title/summary calls to a fast, low-cost model within the SAME provider.
func inferSmallModelFamily(modelID string) string {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" || isNonChatUtilityModel(lower) {
		return ""
	}
	if strings.Contains(lower, "haiku") {
		return "claude-haiku"
	}
	if strings.Contains(lower, "nano") || strings.Contains(lower, "-mini") || strings.HasSuffix(lower, "/mini") {
		return "gpt-nano"
	}
	if strings.Contains(lower, "flash") {
		return "gemini-flash"
	}
	return ""
}

// inferRichChatInputModalities derives the native input modalities supported by
// a conversational model (image, pdf, audio, video) so OpenCode and Hoxi's
// attachment pipeline can pass binary PDFs and audio/video clips natively
// instead of degrading them to truncated OCR text.
func inferRichChatInputModalities(proto, modelID string, allowImage bool) []string {
	out := []string{"text"}
	seen := map[string]bool{"text": true}
	add := func(vals ...string) {
		for _, v := range vals {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	if allowImage {
		add("image")
	}

	lower := strings.ToLower(strings.TrimSpace(modelID))
	canon := canonicalModelID(lower)

	// Google Gemini chat models natively accept PDF, audio, and video inputs.
	if proto == "google" || hasToken(canon, "gemini") || strings.Contains(lower, "gemini") {
		if !isGeminiImageModelID(lower) {
			add("image", "pdf", "audio", "video")
		}
		return out
	}

	// Anthropic Claude 3.5+ / 4+ chat models natively accept PDF documents.
	if proto == "anthropic" || hasToken(canon, "claude") || strings.Contains(lower, "claude") {
		add("image", "pdf")
		return out
	}

	// OpenAI GPT-4o / GPT-4.1 / GPT-5 / o3 / o4 models natively accept PDF files.
	if strings.Contains(lower, "gpt-4o") ||
		strings.Contains(lower, "gpt-4.1") ||
		strings.Contains(lower, "gpt-4-1") ||
		strings.Contains(lower, "gpt-5") ||
		strings.HasPrefix(canon, "o3") ||
		strings.HasPrefix(canon, "o4") {
		add("image", "pdf")
		return out
	}

	// Multimodal omni models accept audio and video inputs.
	if strings.Contains(lower, "omni") {
		add("image", "audio", "video")
	}
	return out
}

func explicitModalityOverride(mObj map[string]interface{}, modality string) (bool, bool) {
	caps, ok := mObj["capabilities"].(map[string]interface{})
	if !ok {
		return false, false
	}
	inMap, ok := caps["input"].(map[string]interface{})
	if !ok {
		return false, false
	}
	val, has := inMap[modality].(bool)
	return val, has
}

func applyRichChatModelDefaults(proto, modelID string, mObj map[string]interface{}, allowImage bool) {
	if mObj == nil {
		return
	}
	if fam, _ := mObj["family"].(string); strings.TrimSpace(fam) == "" {
		if inferred := inferSmallModelFamily(modelID); inferred != "" {
			mObj["family"] = inferred
		}
	}

	modMap, _ := mObj["modalities"].(map[string]interface{})
	if modMap == nil {
		modMap = map[string]interface{}{}
	}
	existing := map[string]bool{}
	var merged []string
	addExisting := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || existing[s] {
			return
		}
		if s != "text" {
			if enabled, hasOverride := explicitModalityOverride(mObj, s); hasOverride && !enabled {
				return
			}
		}
		existing[s] = true
		merged = append(merged, s)
	}
	switch cur := modMap["input"].(type) {
	case []interface{}:
		for _, v := range cur {
			if s, ok := v.(string); ok {
				addExisting(s)
			}
		}
	case []string:
		for _, s := range cur {
			addExisting(s)
		}
	}
	for _, m := range inferRichChatInputModalities(proto, modelID, allowImage) {
		addExisting(m)
	}
	for _, extra := range []string{"image", "pdf", "audio", "video"} {
		if enabled, hasOverride := explicitModalityOverride(mObj, extra); hasOverride && enabled {
			addExisting(extra)
		}
	}
	if len(merged) > 0 {
		modMap["input"] = merged
	}
	if _, hasOut := modMap["output"]; !hasOut {
		modMap["output"] = []string{"text"}
	}
	mObj["modalities"] = modMap
}
