package provider

import (
	"net/url"
	"strings"
)

// parseModelVersion extracts the first (major, minor) version pair from a lowercased model ID.
// Only pure 1-2 digit segments are treated as version numbers, ignoring dates (e.g. 20250514)
// and unit/revision tags (e.g. v1, 1m, 100k).
func parseModelVersion(lowerMID string) (major, minor int, ok bool) {
	tokens := strings.FieldsFunc(lowerMID, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == ' '
	})
	parseSmallInt := func(s string) (int, bool) {
		if len(s) == 0 || len(s) > 2 {
			return 0, false
		}
		v := 0
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
			v = v*10 + int(s[i]-'0')
		}
		return v, true
	}
	for i, tok := range tokens {
		maj, valid := parseSmallInt(tok)
		if !valid || maj <= 0 {
			continue
		}
		if i+1 < len(tokens) {
			if min, validMin := parseSmallInt(tokens[i+1]); validMin {
				return maj, min, true
			}
		}
		return maj, 0, true
	}
	return 0, 0, false
}

func isLegacyClaudeBudgetModel(lowerMID string) bool {
	if idx := strings.Index(lowerMID, "claude"); idx >= 0 {
		lowerMID = lowerMID[idx:]
	}
	major, minor, ok := parseModelVersion(lowerMID)
	return ok && (major < 4 || (major == 4 && minor < 6))
}

func isClaude46Model(lowerMID string) bool {
	if idx := strings.Index(lowerMID, "claude"); idx >= 0 {
		lowerMID = lowerMID[idx:]
	}
	major, minor, ok := parseModelVersion(lowerMID)
	return ok && major == 4 && minor == 6
}

func isLegacyGeminiBudgetModel(lowerMID string) bool {
	if strings.Contains(lowerMID, "gemini-flash-latest") ||
		strings.Contains(lowerMID, "gemini-pro-latest") ||
		strings.Contains(lowerMID, "gemini-flash-lite-latest") {
		return true
	}
	if idx := strings.Index(lowerMID, "gemini"); idx >= 0 {
		lowerMID = lowerMID[idx:]
	}
	major, _, ok := parseModelVersion(lowerMID)
	return ok && major < 3
}

// isNonThinkingGeminiChatModel reports whether modelID is a Gemini 1.x or
// non-thinking Gemini 2.0 chat model (e.g. gemini-1.5-pro, gemini-2.0-flash,
// gemini-2.0-flash-lite) that rejects thinkingConfig with HTTP 400.
func isNonThinkingGeminiChatModel(modelID string) bool {
	lowerMID := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelID)), "models/")
	if !strings.Contains(lowerMID, "gemini") || strings.Contains(lowerMID, "thinking") {
		return false
	}
	if idx := strings.Index(lowerMID, "gemini"); idx >= 0 {
		lowerMID = lowerMID[idx:]
	}
	major, minor, ok := parseModelVersion(lowerMID)
	return ok && (major == 1 || (major == 2 && minor < 5))
}

func isGemini30ProModel(lowerMID string) bool {
	if idx := strings.Index(lowerMID, "gemini"); idx >= 0 {
		lowerMID = lowerMID[idx:]
	}
	if !strings.Contains(lowerMID, "pro") || strings.Contains(lowerMID, "flash") {
		return false
	}
	major, minor, ok := parseModelVersion(lowerMID)
	return ok && major == 3 && minor == 0
}

func defaultModelVariants(providerID, npm, modelID string) map[string]interface{} {
	lowerMID := strings.ToLower(strings.TrimSpace(modelID))
	lowerPID := strings.ToLower(strings.TrimSpace(providerID))
	lowerNPM := strings.ToLower(strings.TrimSpace(npm))
	canonKey := canonicalModelID(lowerMID)

	isAnthropic := strings.Contains(lowerNPM, "anthropic") || (lowerNPM == "" && lowerPID == "anthropic")
	isGoogle := !isAnthropic && (strings.Contains(lowerNPM, "google") || strings.Contains(lowerNPM, "gemini") || (lowerNPM == "" && (lowerPID == "google" || lowerPID == "gemini")))

	if isAnthropic {
		if isLegacyClaudeBudgetModel(lowerMID) {
			return map[string]interface{}{
				"low":    map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 4096}},
				"medium": map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 10240}},
				"high":   map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 16000}},
				"max":    map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budgetTokens": 31999}},
			}
		}
		if isClaude46Model(lowerMID) {
			return map[string]interface{}{
				"low":    map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "low"},
				"medium": map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "medium"},
				"high":   map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "high"},
				"max":    map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "max"},
			}
		}
		return map[string]interface{}{
			"low":    map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "low"},
			"medium": map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "medium"},
			"high":   map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "high"},
			"xhigh":  map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "xhigh"},
			"max":    map[string]interface{}{"thinking": map[string]interface{}{"type": "adaptive"}, "effort": "max"},
		}
	}

	if isGoogle {
		if isLegacyGeminiBudgetModel(lowerMID) {
			return map[string]interface{}{
				"low":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 4096}},
				"medium": map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 10240}},
				"high":   map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 16000}},
				"max":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingBudget": 24576}},
			}
		}
		mediumLevel := "medium"
		if isGemini30ProModel(lowerMID) {
			mediumLevel = "high"
		}
		return map[string]interface{}{
			"low":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": "low"}},
			"medium": map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": mediumLevel}},
			"high":   map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": "high"}},
			"max":    map[string]interface{}{"thinkingConfig": map[string]interface{}{"includeThoughts": true, "thinkingLevel": "high"}},
		}
	}

	if strings.Contains(lowerMID, "oss-") {
		return map[string]interface{}{
			"medium": map[string]interface{}{"reasoningEffort": "medium"},
		}
	}

	if hasToken(canonKey, "claude") || strings.Contains(lowerMID, "claude") {
		if isLegacyClaudeBudgetModel(lowerMID) || isClaude46Model(lowerMID) {
			return map[string]interface{}{
				"low":    map[string]interface{}{"reasoningEffort": "low"},
				"medium": map[string]interface{}{"reasoningEffort": "medium"},
				"high":   map[string]interface{}{"reasoningEffort": "high"},
				"max":    map[string]interface{}{"reasoningEffort": "max"},
			}
		}
		return map[string]interface{}{
			"low":    map[string]interface{}{"reasoningEffort": "low"},
			"medium": map[string]interface{}{"reasoningEffort": "medium"},
			"high":   map[string]interface{}{"reasoningEffort": "high"},
			"xhigh":  map[string]interface{}{"reasoningEffort": "xhigh"},
			"max":    map[string]interface{}{"reasoningEffort": "max"},
		}
	}

	if hasToken(canonKey, "gpt") || hasToken(canonKey, "codex") || isOSeriesModel(canonKey) ||
		strings.Contains(lowerMID, "gpt") || strings.Contains(lowerMID, "codex") ||
		strings.Contains(lowerPID, "openai") || strings.Contains(lowerPID, "codex") {
		if strings.HasSuffix(canonKey, "-pro") && versionAfter(canonKey, "gpt") >= 5 {
			return map[string]interface{}{
				"high": map[string]interface{}{"reasoningEffort": "high"},
			}
		}
		if versionAfter(canonKey, "gpt") >= 6 {
			return map[string]interface{}{
				"low":    map[string]interface{}{"reasoningEffort": "low"},
				"medium": map[string]interface{}{"reasoningEffort": "medium"},
				"high":   map[string]interface{}{"reasoningEffort": "high"},
				"xhigh":  map[string]interface{}{"reasoningEffort": "xhigh"},
				"max":    map[string]interface{}{"reasoningEffort": "max"},
			}
		}
		return map[string]interface{}{
			"low":    map[string]interface{}{"reasoningEffort": "low"},
			"medium": map[string]interface{}{"reasoningEffort": "medium"},
			"high":   map[string]interface{}{"reasoningEffort": "high"},
			"xhigh":  map[string]interface{}{"reasoningEffort": "xhigh"},
		}
	}

	if hasToken(canonKey, "gemini") || strings.Contains(lowerMID, "gemini") {
		return map[string]interface{}{
			"low":    map[string]interface{}{"reasoningEffort": "low"},
			"medium": map[string]interface{}{"reasoningEffort": "medium"},
			"high":   map[string]interface{}{"reasoningEffort": "high"},
			"max":    map[string]interface{}{"reasoningEffort": "high"},
		}
	}

	return map[string]interface{}{
		"low":    map[string]interface{}{"reasoningEffort": "low"},
		"medium": map[string]interface{}{"reasoningEffort": "medium"},
		"high":   map[string]interface{}{"reasoningEffort": "high"},
	}
}

// isGeminiImageModelID detects conversational Gemini / Nano-Banana image
// generation models (e.g. gemini-2.5-flash-image, gemini-3-pro-image-preview,
// gemini-2.0-flash-preview-image-generation) without matching standard Gemini
// text/vision chat models like gemini-2.5-pro or gemini-2.5-flash.
func isGeminiImageModelID(lowerMID string) bool {
	id := strings.ToLower(strings.TrimSpace(lowerMID))
	if strings.Contains(id, "nano-banana") {
		return true
	}
	if !strings.Contains(id, "gemini") {
		return false
	}
	return strings.Contains(id, "-image") ||
		strings.Contains(id, "_image") ||
		strings.Contains(id, "image-generation") ||
		strings.Contains(id, "image-preview") ||
		strings.HasSuffix(id, "/image")
}

// isConversationalImageModelID reports whether an image generation model uses
// multimodal chat/generateContent turns rather than pure /images/generations.
func isConversationalImageModelID(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	return isGeminiImageModelID(id) ||
		strings.Contains(id, "gpt-4o-image") ||
		strings.Contains(id, "chatgpt-image")
}

// supportsMultimodalImageEditInput reports whether an image generation model
// accepts reference images alongside a prompt for image-to-image / editing.
func supportsMultimodalImageEditInput(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	return isConversationalImageModelID(id) ||
		strings.Contains(id, "gpt-image") ||
		strings.Contains(id, "flux-kontext") ||
		strings.Contains(id, "seedream") ||
		strings.Contains(id, "qwen-image-edit")
}

// IsImageGenerationModel reports whether modelID (or explicit metadata) is a
// dedicated image generation / image-editing model.
func IsImageGenerationModel(modelID, explicitCategory, modality string) bool {
	return inferModelCategory(modelID, explicitCategory, modality) == "image"
}

// normalizeModelCategory normalizes an explicit model category from hoxi.ai or AINode.
// Supported categories: chat | coding | tts | realtime | asr | image | video | embedding.
func normalizeModelCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "chat", "coding", "tts", "realtime", "asr", "image", "video", "embedding":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func isConversationalCategory(category string) bool {
	c := normalizeModelCategory(category)
	return c == "" || c == "chat" || c == "coding"
}

// inferModelCategory resolves the canonical category for a model using explicit
// server metadata first, then modality, then model ID heuristics.
func inferModelCategory(modelID, explicitCategory, modality string) string {
	if c := normalizeModelCategory(explicitCategory); c != "" {
		return c
	}
	id := strings.ToLower(strings.TrimSpace(modelID))
	mod := strings.ToLower(strings.TrimSpace(modality))

	for _, tok := range []string{"realtime", "qwen-omni"} {
		if strings.Contains(id, tok) {
			return "realtime"
		}
	}
	for _, tok := range []string{"tts", "cosyvoice", "speech", "elevenlabs", "fishaudio", "suno", "udio"} {
		if strings.Contains(id, tok) {
			return "tts"
		}
	}
	for _, tok := range []string{"asr", "whisper", "paraformer", "fun-asr", "sensevoice", "transcribe"} {
		if strings.Contains(id, tok) {
			return "asr"
		}
	}
	if mod == "audio" {
		return "tts"
	}
	if mod == "video" {
		return "video"
	}
	if mod == "image" || isGeminiImageModelID(id) {
		return "image"
	}
	for _, tok := range []string{
		"dall-e", "gpt-image", "chatgpt-image", "gpt-4o-image", "imagen",
		"flux-", "flux.", "flux/", "midjourney", "stable-diffusion", "sdxl",
		"ideogram", "recraft", "cogview", "seedream", "kolors",
		"hunyuan-image", "qwen-image", "wanx-v1",
	} {
		if strings.Contains(id, tok) {
			return "image"
		}
	}
	for _, tok := range []string{"seedance", "kling", "veo-", "veo2", "veo3", "sora", "wanx", "wan2", "cogvideo", "hailuo", "luma-", "runway", "vidu"} {
		if strings.Contains(id, tok) {
			return "video"
		}
	}
	for _, tok := range []string{"embedding", "embed", "rerank", "bge-", "moderation"} {
		if strings.Contains(id, tok) {
			return "embedding"
		}
	}
	for _, tok := range []string{"codex", "codestral", "qwen-coder", "qwen2.5-coder", "qwen3-coder", "deepseek-coder"} {
		if strings.Contains(id, tok) {
			return "coding"
		}
	}
	return "chat"
}

// isNonChatUtilityModel returns true only for non-conversational utility or pure
// media-generation endpoints (embeddings, rerankers, speech/audio, moderation,
// dedicated image/video generators). Conversational models default to supporting
// image attachments and reasoning unless explicitly disabled.
func isNonChatUtilityModel(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return true
	}
	return !isConversationalCategory(inferModelCategory(id, "", ""))
}

func hasModalityEntry(mObj map[string]interface{}, dir, target string) bool {
	modMap, ok := mObj["modalities"].(map[string]interface{})
	if !ok {
		return false
	}
	switch list := modMap[dir].(type) {
	case []interface{}:
		for _, v := range list {
			if s, _ := v.(string); strings.EqualFold(strings.TrimSpace(s), target) {
				return true
			}
		}
	case []string:
		for _, s := range list {
			if strings.EqualFold(strings.TrimSpace(s), target) {
				return true
			}
		}
	}
	return false
}

func hasImageInputModality(mObj map[string]interface{}) bool {
	return hasModalityEntry(mObj, "input", "image")
}

func hasImageOutputModality(mObj map[string]interface{}) bool {
	return hasModalityEntry(mObj, "output", "image")
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

func ensureImageOutputModality(mObj map[string]interface{}, allowImageInput bool) {
	modMap, _ := mObj["modalities"].(map[string]interface{})
	if modMap == nil {
		modMap = map[string]interface{}{}
	}
	if allowImageInput {
		modMap["input"] = []string{"text", "image"}
	} else if _, hasIn := modMap["input"]; !hasIn {
		modMap["input"] = []string{"text"}
	}
	if !hasImageOutputModality(mObj) {
		modMap["output"] = []string{"text", "image"}
	}
	mObj["modalities"] = modMap
}

// normalizeGoogleBaseURL ensures @ai-sdk/google baseURL ends with an API version
// segment (default /v1beta) and does not keep a trailing /models suffix that
// would cause @ai-sdk/google to request /v1beta/models/models/...
func normalizeGoogleBaseURL(rawBaseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return trimmed
	}
	cleanPath := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(strings.ToLower(cleanPath), "/models") {
		cleanPath = strings.TrimRight(cleanPath[:len(cleanPath)-len("/models")], "/")
	}
	lowerPath := strings.ToLower(cleanPath)
	if cleanPath == "" {
		cleanPath = "/v1beta"
	} else if !strings.HasSuffix(lowerPath, "/v1beta") &&
		!strings.HasSuffix(lowerPath, "/v1alpha") &&
		!strings.HasSuffix(lowerPath, "/v1") {
		cleanPath = cleanPath + "/v1beta"
	}
	u.Path = cleanPath
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/")
}

func providerRequiresOpenAIResponsesSDK(providerID, baseURL string, pObj map[string]interface{}) bool {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	if pid != "openai" && !isOfficialOpenAIHost(baseURL) {
		return false
	}
	mMap, _ := pObj["models"].(map[string]interface{})
	for mid := range mMap {
		canon := canonicalModelID(mid)
		if hasToken(canon, "codex") || (hasToken(canon, "gpt") && versionAfter(canon, "gpt") >= 6) {
			return true
		}
	}
	return false
}

func normalizeProviderProtocolAndBaseURL(providerID string, pObj map[string]interface{}) string {
	npm, _ := pObj["npm"].(string)
	opts, _ := pObj["options"].(map[string]interface{})
	baseURL, _ := opts["baseURL"].(string)
	proto := effectiveProviderProtocol(providerID, npm, baseURL)
	if proto == "google" {
		if strings.TrimSpace(npm) == "" || strings.EqualFold(strings.TrimSpace(npm), "@ai-sdk/openai-compatible") {
			npm = "@ai-sdk/google"
			pObj["npm"] = npm
		}
		if opts != nil && strings.TrimSpace(baseURL) != "" {
			opts["baseURL"] = normalizeGoogleBaseURL(baseURL)
		}
	} else if proto == "openai" && strings.Contains(strings.ToLower(npm), "google") {
		// e.g. https://generativelanguage.googleapis.com/v1beta/openai
		npm = "@ai-sdk/openai-compatible"
		pObj["npm"] = npm
	} else if proto == "openai" && (strings.TrimSpace(npm) == "" || strings.EqualFold(strings.TrimSpace(npm), "@ai-sdk/openai-compatible")) {
		if providerRequiresOpenAIResponsesSDK(providerID, baseURL, pObj) {
			npm = "@ai-sdk/openai"
			pObj["npm"] = npm
		}
	}
	return npm
}

func hasAutoInjectedThinkingVariants(vMap map[string]interface{}) bool {
	if len(vMap) == 0 {
		return false
	}
	for _, entry := range vMap {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasTC := em["thinkingConfig"]; hasTC {
			return true
		}
		if _, hasRE := em["reasoningEffort"]; hasRE {
			return true
		}
	}
	return false
}

func normalizeGoogleModelMapKeys(mMap map[string]interface{}) {
	var prefixedKeys []string
	for mid := range mMap {
		if strings.HasPrefix(mid, "models/") && len(strings.TrimPrefix(mid, "models/")) > 0 {
			prefixedKeys = append(prefixedKeys, mid)
		}
	}
	for _, oldKey := range prefixedKeys {
		cleanKey := strings.TrimSpace(strings.TrimPrefix(oldKey, "models/"))
		if cleanKey == "" {
			continue
		}
		if _, exists := mMap[cleanKey]; !exists {
			mMap[cleanKey] = mMap[oldKey]
		}
		delete(mMap, oldKey)
	}
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
		npm := normalizeProviderProtocolAndBaseURL(pid, pObj)
		opts, _ := pObj["options"].(map[string]interface{})
		baseURL, _ := opts["baseURL"].(string)
		proto := effectiveProviderProtocol(pid, npm, baseURL)
		mMap, ok := pObj["models"].(map[string]interface{})
		if !ok {
			continue
		}
		if proto == "google" {
			normalizeGoogleModelMapKeys(mMap)
		}
		for mid, mVal := range mMap {
			mObj, ok := mVal.(map[string]interface{})
			if !ok {
				continue
			}
			explicitCat, _ := mObj["category"].(string)
			cat := inferModelCategory(mid, explicitCat, "")
			isImageGen := cat == "image" || hasImageOutputModality(mObj)
			isChat := !isImageGen && isConversationalCategory(cat)
			if isChat {
				ensureModelTokenLimit(mObj, mid)
			}

			_, hasExplicitAttachment := mObj["attachment"]
			attachBool, _ := mObj["attachment"].(bool)
			if !attachBool {
				if caps, ok := mObj["capabilities"].(map[string]interface{}); ok {
					attachBool, _ = caps["attachment"].(bool)
				}
			}

			if isImageGen {
				if !hasExplicitAttachment {
					attachBool = hasImageInputModality(mObj) || supportsMultimodalImageEditInput(mid)
					mObj["attachment"] = attachBool
				}
				ensureImageOutputModality(mObj, attachBool || hasImageInputModality(mObj))
				// Clean up legacy auto-injected thinking variants on image models
				// that reject thinkingConfig / reasoning_effort with HTTP 400.
				isReasoning, _ := mObj["reasoning"].(bool)
				if vMap, hasV := mObj["variants"].(map[string]interface{}); hasV && hasAutoInjectedThinkingVariants(vMap) {
					delete(mObj, "variants")
					if isReasoning {
						mObj["reasoning"] = false
					}
				}
				continue
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
			if isChat {
				applyRichChatModelDefaults(proto, mid, mObj, attachBool || hasImageInputModality(mObj))
				if strings.Contains(strings.ToLower(mid), "-speciale") {
					if _, hasTC := mObj["tool_call"]; !hasTC {
						mObj["tool_call"] = false
					}
				}
			}

			if isNonThinkingGeminiChatModel(mid) {
				// Non-thinking Gemini 1.x / 2.0 chat models reject thinkingConfig
				// with HTTP 400; OpenCode injects thinkingConfig whenever
				// capabilities.reasoning is true on @ai-sdk/google.
				if vMap, hasV := mObj["variants"].(map[string]interface{}); hasV && hasAutoInjectedThinkingVariants(vMap) {
					delete(mObj, "variants")
					mObj["reasoning"] = false
				} else if proto == "google" {
					mObj["reasoning"] = false
				}
				continue
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
