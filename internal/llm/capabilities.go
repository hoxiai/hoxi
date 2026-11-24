package llm

import (
	"slices"
	"strings"
)

// ModelCapability is a canonical capability tag advertised by OpenAI-compatible
// gateways (via /v1/models `capabilities`) or inferred from legacy metadata.
// A single model may advertise multiple capabilities simultaneously.
type ModelCapability string

const (
	CapabilityChat           ModelCapability = "chat"
	CapabilityVision         ModelCapability = "vision"
	CapabilityTextTranslate  ModelCapability = "text_translate"
	CapabilityImageGenerate  ModelCapability = "image_generate"
	CapabilityImageEdit      ModelCapability = "image_edit"
	CapabilityImageTranslate ModelCapability = "image_translate"
	CapabilityVideo          ModelCapability = "video"
	CapabilityTTS            ModelCapability = "tts"
	CapabilityASR            ModelCapability = "asr"
	CapabilityEmbedding      ModelCapability = "embedding"
	CapabilityRerank         ModelCapability = "rerank"
	CapabilityModeration     ModelCapability = "moderation"
)

var canonicalCapabilityOrder = []ModelCapability{
	CapabilityChat,
	CapabilityVision,
	CapabilityTextTranslate,
	CapabilityImageGenerate,
	CapabilityImageEdit,
	CapabilityImageTranslate,
	CapabilityVideo,
	CapabilityTTS,
	CapabilityASR,
	CapabilityEmbedding,
	CapabilityRerank,
	CapabilityModeration,
}

// ImageTranslateOutputMode distinguishes raster batch image translation from
// editable layered scene translation.
type ImageTranslateOutputMode string

const (
	ImageTranslateModeRaster        ImageTranslateOutputMode = "raster"
	ImageTranslateModeEditableScene ImageTranslateOutputMode = "editable_scene"
)

// ImageTranslateCapability carries sub-capabilities for image_translate models.
type ImageTranslateCapability struct {
	OutputModes []ImageTranslateOutputMode `json:"output_modes,omitempty"`
}

// ModelSlot identifies a business role slot with strict capability filtering.
type ModelSlot string

const (
	ModelSlotChat                 ModelSlot = "chat"
	ModelSlotVision               ModelSlot = "vision"
	ModelSlotTextTranslate        ModelSlot = "text_translate"
	ModelSlotImageGenerate        ModelSlot = "image_generate"
	ModelSlotImageEdit            ModelSlot = "image_edit"
	ModelSlotImageTranslateRaster ModelSlot = "image_translate_raster"
	ModelSlotImageTranslateEditor ModelSlot = "image_translate_editor"
	ModelSlotVideo                ModelSlot = "video"
	ModelSlotTTS                  ModelSlot = "tts"
	ModelSlotASR                  ModelSlot = "asr"
	ModelSlotEmbedding            ModelSlot = "embedding"
	ModelSlotRerank               ModelSlot = "rerank"
)

var allModelSlots = []ModelSlot{
	ModelSlotChat,
	ModelSlotVision,
	ModelSlotTextTranslate,
	ModelSlotImageGenerate,
	ModelSlotImageEdit,
	ModelSlotImageTranslateRaster,
	ModelSlotImageTranslateEditor,
	ModelSlotVideo,
	ModelSlotTTS,
	ModelSlotASR,
	ModelSlotEmbedding,
	ModelSlotRerank,
}

// AllModelSlots returns all standard business slots in canonical order.
func AllModelSlots() []ModelSlot {
	out := make([]ModelSlot, len(allModelSlots))
	copy(out, allModelSlots)
	return out
}

// IsValidModelSlot reports whether raw matches a known ModelSlot.
func IsValidModelSlot(raw string) bool {
	s := ModelSlot(strings.ToLower(strings.TrimSpace(raw)))
	return slices.Contains(allModelSlots, s)
}

// ModelCapabilityInput aggregates explicit and legacy metadata for one model.
type ModelCapabilityInput struct {
	ModelID                           string
	Modality                          string
	Category                          string
	HasExplicitCapabilities           bool
	ExplicitCapabilities              []string
	ExplicitImageTranslateOutputModes []string
	InputModalities                   []string
	OutputModalities                  []string
	Attachment                        *bool
	SupportsImageInput                bool
}

// NormalizedModelCapabilities holds deduplicated, canonical capabilities.
type NormalizedModelCapabilities struct {
	Capabilities   []ModelCapability         `json:"capabilities"`
	ImageTranslate *ImageTranslateCapability `json:"image_translate,omitempty"`
}

// Has reports whether c includes cap.
func (c NormalizedModelCapabilities) Has(cap ModelCapability) bool {
	return slices.Contains(c.Capabilities, cap)
}

// HasImageTranslateMode reports whether c supports image_translate with mode.
func (c NormalizedModelCapabilities) HasImageTranslateMode(mode ImageTranslateOutputMode) bool {
	if !c.Has(CapabilityImageTranslate) || c.ImageTranslate == nil {
		return false
	}
	return slices.Contains(c.ImageTranslate.OutputModes, mode)
}

// SlotModelOption describes one configured model eligible for a ModelSlot.
type SlotModelOption struct {
	ProviderID     string                    `json:"providerID"`
	ModelID        string                    `json:"modelID"`
	Name           string                    `json:"name,omitempty"`
	Modality       string                    `json:"modality,omitempty"`
	Capabilities   []ModelCapability         `json:"capabilities,omitempty"`
	ImageTranslate *ImageTranslateCapability `json:"image_translate,omitempty"`
	SupportsIn     bool                      `json:"supportsImageInput,omitempty"`
}

// ParseModelCapability normalizes one capability string into a ModelCapability.
func ParseModelCapability(raw string) (ModelCapability, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "chat":
		return CapabilityChat, true
	case "vision":
		return CapabilityVision, true
	case "text_translate":
		return CapabilityTextTranslate, true
	case "image_generate":
		return CapabilityImageGenerate, true
	case "image_edit":
		return CapabilityImageEdit, true
	case "image_translate":
		return CapabilityImageTranslate, true
	case "video":
		return CapabilityVideo, true
	case "tts":
		return CapabilityTTS, true
	case "asr":
		return CapabilityASR, true
	case "embedding":
		return CapabilityEmbedding, true
	case "rerank":
		return CapabilityRerank, true
	case "moderation":
		return CapabilityModeration, true
	default:
		return "", false
	}
}

// ParseImageTranslateOutputMode normalizes one image_translate output mode.
func ParseImageTranslateOutputMode(raw string) (ImageTranslateOutputMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "raster":
		return ImageTranslateModeRaster, true
	case "editable_scene":
		return ImageTranslateModeEditableScene, true
	default:
		return "", false
	}
}

// IsExcludedUtilityModel reports whether a model is an internal crawler/utility
// model (e.g. crawl, collector-*, codex-auto-review) that must not enter slots.
func IsExcludedUtilityModel(modelID, modality string) bool {
	mod := strings.ToLower(strings.TrimSpace(modality))
	if mod == "crawl" || mod == "crawler" || mod == "utility" || mod == "none" {
		return true
	}
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return false
	}
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	return strings.HasPrefix(id, "collector-") ||
		id == "collector" ||
		strings.Contains(id, "codex-auto-review") ||
		strings.HasPrefix(id, "crawl-") ||
		id == "crawl"
}

func isEditableImageTranslateModelID(lowerID string) bool {
	return strings.Contains(lowerID, "alimt-image-translate") ||
		strings.Contains(lowerID, "image-translate-editor")
}

func isRasterImageTranslateModelID(lowerID string) bool {
	return strings.Contains(lowerID, "mt-image") ||
		strings.Contains(lowerID, "image-translate") ||
		strings.Contains(lowerID, "image_translate")
}

func isImageEditCapableModelID(lowerID string) bool {
	if isGeminiImageModelToken(lowerID) {
		return true
	}
	if strings.Contains(lowerID, "imagen") && strings.Contains(lowerID, "-generate") &&
		!strings.Contains(lowerID, "edit") && !strings.Contains(lowerID, "inpaint") {
		return false
	}
	for _, tok := range []string{
		"gpt-image", "dall-e-2", "imagen", "qwen-image",
		"kontext", "inpaint", "flux-fill", "seedream",
		"-edit", "_edit", "gpt-4o-image", "chatgpt-image",
	} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isGeminiImageModelToken(lowerID string) bool {
	if strings.Contains(lowerID, "nano-banana") {
		return true
	}
	if !strings.Contains(lowerID, "gemini") {
		return false
	}
	return strings.Contains(lowerID, "-image") ||
		strings.Contains(lowerID, "_image") ||
		strings.Contains(lowerID, "image-generation") ||
		strings.Contains(lowerID, "image-preview") ||
		strings.HasSuffix(lowerID, "/image")
}

func isImageGenModelID(lowerID string) bool {
	if isEditableImageTranslateModelID(lowerID) || isRasterImageTranslateModelID(lowerID) {
		return false
	}
	if isGeminiImageModelToken(lowerID) {
		return true
	}
	for _, tok := range []string{
		"dall-e", "gpt-image", "chatgpt-image", "gpt-4o-image", "imagen",
		"flux-", "flux.", "flux/", "midjourney", "stable-diffusion", "sdxl",
		"ideogram", "recraft", "cogview", "seedream", "kolors",
		"hunyuan-image", "qwen-image", "wanx-v1", "grok-imagine",
	} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isVideoModelID(lowerID string) bool {
	for _, tok := range []string{
		"seedance", "kling", "veo-", "veo2", "veo3", "sora",
		"wanx", "wan2", "cogvideo", "hailuo", "luma-", "runway", "vidu",
	} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isTTSModelID(lowerID string) bool {
	for _, tok := range []string{"tts", "cosyvoice", "speech", "elevenlabs", "fishaudio", "suno", "udio"} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isASRModelID(lowerID string) bool {
	for _, tok := range []string{"asr", "whisper", "paraformer", "fun-asr", "sensevoice", "transcribe"} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isRerankModelID(lowerID string) bool {
	return strings.Contains(lowerID, "rerank")
}

func isModerationModelID(lowerID string) bool {
	return strings.Contains(lowerID, "moderation")
}

func isEmbeddingModelID(lowerID string) bool {
	for _, tok := range []string{"embedding", "embed", "bge-"} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isKnownPureTextModelID(lowerID string) bool {
	for _, tok := range []string{
		"deepseek-chat", "deepseek-reasoner", "deepseek-r1", "deepseek-v2", "deepseek-v3",
		"qwen-mt-", "qwen-coder", "qwen2.5-coder", "qwen3-coder", "deepseek-coder",
		"codestral", "o1-mini", "gpt-3.5", "text-davinci",
	} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	return false
}

func isKnownVisionChatModelID(lowerID string) bool {
	if isKnownPureTextModelID(lowerID) {
		return false
	}
	for _, tok := range []string{
		"vision", "-vl", "_vl", "/vl", "vl-", "omni",
		"gpt-4o", "chatgpt-4o", "gpt-4.1", "gpt-4-turbo", "gpt-5", "gpt-6",
		"claude-3", "claude-4", "claude-sonnet", "claude-opus", "claude-haiku",
		"gemini", "qwen3.5", "qwen3-5", "qwen3.6", "qwen3-6", "qwen3.8", "qwen3-8", "qwen4",
		"pixtral", "llava", "internvl", "glm-4v", "glm-4.5v", "glm-4.6v", "grok-3", "grok-4",
	} {
		if strings.Contains(lowerID, tok) {
			return true
		}
	}
	if strings.HasPrefix(lowerID, "o3") || strings.HasPrefix(lowerID, "o4") ||
		strings.Contains(lowerID, "/o3") || strings.Contains(lowerID, "/o4") {
		return true
	}
	return false
}

func sliceContainsFold(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func canonicalizeCapabilities(set map[ModelCapability]bool) []ModelCapability {
	out := make([]ModelCapability, 0, len(set))
	for _, c := range canonicalCapabilityOrder {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

func normalizeTranslateModes(raw []string, modelID, modality string) []ImageTranslateOutputMode {
	seen := map[ImageTranslateOutputMode]bool{}
	for _, m := range raw {
		if parsed, ok := ParseImageTranslateOutputMode(m); ok {
			seen[parsed] = true
		}
	}
	if len(seen) == 0 {
		mod := strings.ToLower(strings.TrimSpace(modality))
		id := strings.ToLower(strings.TrimSpace(modelID))
		if mod == "translate_image" || isEditableImageTranslateModelID(id) {
			seen[ImageTranslateModeRaster] = true
			seen[ImageTranslateModeEditableScene] = true
		} else {
			seen[ImageTranslateModeRaster] = true
		}
	}
	out := make([]ImageTranslateOutputMode, 0, 2)
	if seen[ImageTranslateModeRaster] {
		out = append(out, ImageTranslateModeRaster)
	}
	if seen[ImageTranslateModeEditableScene] {
		out = append(out, ImageTranslateModeEditableScene)
	}
	return out
}

// NormalizeModelCapabilities normalizes explicit gateway capability metadata or
// derives canonical capabilities from legacy modality/category/ID fields.
func NormalizeModelCapabilities(in ModelCapabilityInput) NormalizedModelCapabilities {
	id := strings.ToLower(strings.TrimSpace(in.ModelID))
	mod := strings.ToLower(strings.TrimSpace(in.Modality))
	cat := strings.ToLower(strings.TrimSpace(in.Category))

	if in.HasExplicitCapabilities {
		set := make(map[ModelCapability]bool, len(in.ExplicitCapabilities))
		for _, raw := range in.ExplicitCapabilities {
			if capVal, ok := ParseModelCapability(raw); ok {
				set[capVal] = true
			}
		}
		caps := canonicalizeCapabilities(set)
		res := NormalizedModelCapabilities{Capabilities: caps}
		if set[CapabilityImageTranslate] {
			res.ImageTranslate = &ImageTranslateCapability{
				OutputModes: normalizeTranslateModes(in.ExplicitImageTranslateOutputModes, id, mod),
			}
		}
		return res
	}

	if IsExcludedUtilityModel(id, mod) || cat == "utility" || cat == "crawl" {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{}}
	}

	// Explicit legacy modality takes precedence over heuristic ID guessing.
	switch mod {
	case "translate_image":
		return NormalizedModelCapabilities{
			Capabilities: []ModelCapability{CapabilityImageTranslate},
			ImageTranslate: &ImageTranslateCapability{
				OutputModes: normalizeTranslateModes(in.ExplicitImageTranslateOutputModes, id, mod),
			},
		}
	case "translate_image_task":
		modes := in.ExplicitImageTranslateOutputModes
		if len(modes) == 0 {
			modes = []string{string(ImageTranslateModeRaster)}
		}
		return NormalizedModelCapabilities{
			Capabilities: []ModelCapability{CapabilityImageTranslate},
			ImageTranslate: &ImageTranslateCapability{
				OutputModes: normalizeTranslateModes(modes, id, mod),
			},
		}
	case "image":
		set := map[ModelCapability]bool{CapabilityImageGenerate: true}
		if isImageEditCapableModelID(id) || in.SupportsImageInput || sliceContainsFold(in.InputModalities, "image") {
			set[CapabilityImageEdit] = true
		}
		return NormalizedModelCapabilities{Capabilities: canonicalizeCapabilities(set)}
	case "video":
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityVideo}}
	case "tts", "audio":
		if isASRModelID(id) {
			return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityASR}}
		}
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityTTS}}
	case "asr":
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityASR}}
	case "embedding":
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityEmbedding}}
	case "rerank":
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityRerank}}
	case "moderation":
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityModeration}}
	case "vision":
		return NormalizedModelCapabilities{
			Capabilities: []ModelCapability{CapabilityChat, CapabilityVision, CapabilityTextTranslate},
		}
	case "text":
		return NormalizedModelCapabilities{
			Capabilities: []ModelCapability{CapabilityChat, CapabilityTextTranslate},
		}
	}

	// Image translation models by category or model ID.
	if cat == "image_translate" || isEditableImageTranslateModelID(id) || isRasterImageTranslateModelID(id) {
		return NormalizedModelCapabilities{
			Capabilities: []ModelCapability{CapabilityImageTranslate},
			ImageTranslate: &ImageTranslateCapability{
				OutputModes: normalizeTranslateModes(in.ExplicitImageTranslateOutputModes, id, mod),
			},
		}
	}

	// Non-chat dedicated media & utility categories/IDs.
	if cat == "rerank" || isRerankModelID(id) {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityRerank}}
	}
	if cat == "moderation" || isModerationModelID(id) {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityModeration}}
	}
	if cat == "embedding" || isEmbeddingModelID(id) {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityEmbedding}}
	}
	if cat == "asr" || isASRModelID(id) {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityASR}}
	}
	if cat == "tts" || cat == "audio" || isTTSModelID(id) || sliceContainsFold(in.OutputModalities, "audio") {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityTTS}}
	}
	if cat == "video" || isVideoModelID(id) || sliceContainsFold(in.OutputModalities, "video") {
		return NormalizedModelCapabilities{Capabilities: []ModelCapability{CapabilityVideo}}
	}
	if cat == "image" || isImageGenModelID(id) || sliceContainsFold(in.OutputModalities, "image") {
		set := map[ModelCapability]bool{CapabilityImageGenerate: true}
		if isImageEditCapableModelID(id) || in.SupportsImageInput || sliceContainsFold(in.InputModalities, "image") {
			set[CapabilityImageEdit] = true
		}
		return NormalizedModelCapabilities{Capabilities: canonicalizeCapabilities(set)}
	}

	// Conversational models: never silently classify pure-text models as vision.
	set := map[ModelCapability]bool{
		CapabilityChat:          true,
		CapabilityTextTranslate: true,
	}
	if in.Attachment != nil && !*in.Attachment {
		return NormalizedModelCapabilities{Capabilities: canonicalizeCapabilities(set)}
	}
	if len(in.InputModalities) > 0 {
		if sliceContainsFold(in.InputModalities, "image") && !isKnownPureTextModelID(id) {
			set[CapabilityVision] = true
		}
		return NormalizedModelCapabilities{Capabilities: canonicalizeCapabilities(set)}
	}
	if in.SupportsImageInput && !isKnownPureTextModelID(id) {
		set[CapabilityVision] = true
		return NormalizedModelCapabilities{Capabilities: canonicalizeCapabilities(set)}
	}
	if isKnownVisionChatModelID(id) {
		set[CapabilityVision] = true
	}
	return NormalizedModelCapabilities{Capabilities: canonicalizeCapabilities(set)}
}

// MatchesModelSlot checks whether a model's normalized capabilities strictly
// satisfy the requirements of slot.
func MatchesModelSlot(caps NormalizedModelCapabilities, slot ModelSlot) bool {
	switch slot {
	case ModelSlotChat:
		return caps.Has(CapabilityChat)
	case ModelSlotVision:
		return caps.Has(CapabilityVision)
	case ModelSlotTextTranslate:
		return caps.Has(CapabilityTextTranslate)
	case ModelSlotImageGenerate:
		return caps.Has(CapabilityImageGenerate)
	case ModelSlotImageEdit:
		return caps.Has(CapabilityImageEdit)
	case ModelSlotImageTranslateRaster:
		return caps.HasImageTranslateMode(ImageTranslateModeRaster)
	case ModelSlotImageTranslateEditor:
		return caps.HasImageTranslateMode(ImageTranslateModeEditableScene)
	case ModelSlotVideo:
		return caps.Has(CapabilityVideo)
	case ModelSlotTTS:
		return caps.Has(CapabilityTTS)
	case ModelSlotASR:
		return caps.Has(CapabilityASR)
	case ModelSlotEmbedding:
		return caps.Has(CapabilityEmbedding)
	case ModelSlotRerank:
		return caps.Has(CapabilityRerank)
	default:
		return false
	}
}

// SlotsForCapabilities returns all ModelSlots satisfied by caps in canonical order.
func SlotsForCapabilities(caps NormalizedModelCapabilities) []ModelSlot {
	out := make([]ModelSlot, 0, 4)
	for _, slot := range allModelSlots {
		if MatchesModelSlot(caps, slot) {
			out = append(out, slot)
		}
	}
	return out
}
