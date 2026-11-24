package provider

import (
	"sort"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func stringSliceFromAny(v any) ([]string, bool) {
	switch list := v.(type) {
	case []string:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case []llm.ModelCapability:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s := strings.TrimSpace(string(item)); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case []llm.ImageTranslateOutputMode:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s := strings.TrimSpace(string(item)); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case []interface{}:
		out := make([]string, 0, len(list))
		for _, raw := range list {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func modalitiesDirSlice(mObj map[string]interface{}, dir string) []string {
	modMap, ok := mObj["modalities"].(map[string]interface{})
	if !ok {
		return nil
	}
	list, _ := stringSliceFromAny(modMap[dir])
	return list
}

func extractImageTranslateModesFromConfig(mObj map[string]interface{}) []string {
	if mObj == nil {
		return nil
	}
	for _, key := range []string{"image_translate", "imageTranslate"} {
		if sub, ok := mObj[key].(map[string]interface{}); ok {
			if list, ok := stringSliceFromAny(sub["output_modes"]); ok && len(list) > 0 {
				return list
			}
			if list, ok := stringSliceFromAny(sub["outputModes"]); ok && len(list) > 0 {
				return list
			}
		}
	}
	for _, key := range []string{"image_translate_output_modes", "imageTranslateOutputModes"} {
		if list, ok := stringSliceFromAny(mObj[key]); ok && len(list) > 0 {
			return list
		}
	}
	return nil
}

// extractModelCapabilitiesFromConfig derives canonical capabilities for a
// persisted model object from .hoxi/models.json or imported OpenCode config.
func extractModelCapabilitiesFromConfig(modelID string, mObj map[string]interface{}) llm.NormalizedModelCapabilities {
	in := llm.ModelCapabilityInput{ModelID: modelID}
	if mObj != nil {
		in.Modality, _ = mObj["modality"].(string)
		in.Category, _ = mObj["category"].(string)
		in.InputModalities = modalitiesDirSlice(mObj, "input")
		in.OutputModalities = modalitiesDirSlice(mObj, "output")
		in.ExplicitImageTranslateOutputModes = extractImageTranslateModesFromConfig(mObj)

		if tags, ok := stringSliceFromAny(mObj["capabilityTags"]); ok {
			in.HasExplicitCapabilities = true
			in.ExplicitCapabilities = tags
		} else if list, ok := stringSliceFromAny(mObj["capabilities"]); ok {
			// Handle raw /v1/models string-array capabilities when stored directly.
			in.HasExplicitCapabilities = true
			in.ExplicitCapabilities = list
		}

		if attach, ok := mObj["attachment"].(bool); ok {
			in.Attachment = &attach
		} else if capObj, ok := mObj["capabilities"].(map[string]interface{}); ok {
			if inObj, ok := capObj["input"].(map[string]interface{}); ok {
				if imgIn, ok := inObj["image"].(bool); ok {
					in.Attachment = &imgIn
					if imgIn {
						in.SupportsImageInput = true
					}
				}
			} else if attach, ok := capObj["attachment"].(bool); ok {
				in.Attachment = &attach
			}
		}
		if hasImageInputModality(mObj) {
			in.SupportsImageInput = true
		}
	}
	return llm.NormalizeModelCapabilities(in)
}

func scoreModelForSlot(slot llm.ModelSlot, modelID string) int {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	switch slot {
	case llm.ModelSlotImageGenerate:
		return scoreImageModelPreference(modelID)
	case llm.ModelSlotImageEdit:
		switch {
		case strings.Contains(lower, "gpt-image"):
			return 100
		case strings.Contains(lower, "qwen-image-edit") || strings.Contains(lower, "flux-kontext") || strings.Contains(lower, "flux-fill"):
			return 96
		case strings.Contains(lower, "seedream") || strings.Contains(lower, "inpaint") || strings.Contains(lower, "-edit"):
			return 92
		default:
			return scoreImageModelPreference(modelID)
		}
	case llm.ModelSlotImageTranslateEditor:
		switch {
		case strings.Contains(lower, "alimt-image-translate"):
			return 100
		case strings.Contains(lower, "image-translate-editor"):
			return 96
		default:
			return 80
		}
	case llm.ModelSlotImageTranslateRaster:
		switch {
		case strings.Contains(lower, "qwen-mt-image"):
			return 100
		case strings.Contains(lower, "alimt-image-translate"):
			return 92
		case strings.Contains(lower, "mt-image"):
			return 88
		default:
			return 75
		}
	case llm.ModelSlotVideo:
		return scoreVideoModelPreference(modelID)
	case llm.ModelSlotTTS:
		return scoreSpeechModelPreference(modelID)
	case llm.ModelSlotASR:
		switch {
		case strings.Contains(lower, "whisper-large") || strings.Contains(lower, "gpt-4o-transcribe"):
			return 100
		case strings.Contains(lower, "whisper") || strings.Contains(lower, "sensevoice") || strings.Contains(lower, "paraformer"):
			return 90
		default:
			return 70
		}
	case llm.ModelSlotEmbedding:
		switch {
		case strings.Contains(lower, "text-embedding-3-large") || strings.Contains(lower, "gemini-embedding"):
			return 95
		case strings.Contains(lower, "text-embedding-3") || strings.Contains(lower, "bge-"):
			return 90
		default:
			return 75
		}
	case llm.ModelSlotRerank:
		if strings.Contains(lower, "bge-reranker") || strings.Contains(lower, "gte-rerank") {
			return 95
		}
		return 80
	default:
		return 70
	}
}

func rolePrefForSlot(prefs llm.MediaRolePrefs, slot llm.ModelSlot) string {
	switch slot {
	case llm.ModelSlotImageGenerate:
		return prefs.ImageModel
	case llm.ModelSlotImageEdit:
		return prefs.ImageEditModel
	case llm.ModelSlotImageTranslateRaster:
		return prefs.ImageTranslateRasterModel
	case llm.ModelSlotImageTranslateEditor:
		return prefs.ImageTranslateEditorModel
	case llm.ModelSlotVideo:
		return prefs.VideoModel
	case llm.ModelSlotTTS:
		return prefs.SpeechModel
	case llm.ModelSlotASR:
		return prefs.TranscribeModel
	case llm.ModelSlotVision:
		return prefs.VisionModel
	case llm.ModelSlotTextTranslate:
		return prefs.TextTranslateModel
	case llm.ModelSlotEmbedding:
		return prefs.EmbeddingModel
	case llm.ModelSlotRerank:
		return prefs.RerankModel
	default:
		return ""
	}
}

// ListModelsBySlot lists all configured models on usable local/remote providers
// whose normalized capabilities strictly satisfy slot, sorted by preference.
func (s *Service) ListModelsBySlot(slot llm.ModelSlot) []llm.SlotModelOption {
	if s == nil || !llm.IsValidModelSlot(string(slot)) {
		return nil
	}
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}

	type scoredOpt struct {
		opt   llm.SlotModelOption
		score int
	}
	var found []scoredOpt
	seen := map[string]bool{}

	for _, pid := range sortedKeys(merged) {
		if !s.isProviderUsableForLocalMedia(pid) {
			continue
		}
		pObj, ok := merged[pid].(map[string]interface{})
		if !ok {
			continue
		}
		for _, mapKey := range []string{"models", "mediaModels"} {
			mMap, ok := pObj[mapKey].(map[string]interface{})
			if !ok {
				continue
			}
			for _, mid := range sortedKeys(mMap) {
				mObj, _ := mMap[mid].(map[string]interface{})
				caps := extractModelCapabilitiesFromConfig(mid, mObj)
				if !llm.MatchesModelSlot(caps, slot) {
					continue
				}
				key := pid + "/" + mid
				if seen[key] {
					continue
				}
				seen[key] = true
				name := mid
				mod := ""
				if mObj != nil {
					if n, _ := mObj["name"].(string); strings.TrimSpace(n) != "" {
						name = strings.TrimSpace(n)
					}
					mod, _ = mObj["modality"].(string)
				}
				found = append(found, scoredOpt{
					opt: llm.SlotModelOption{
						ProviderID:     pid,
						ModelID:        mid,
						Name:           name,
						Modality:       strings.TrimSpace(mod),
						Capabilities:   caps.Capabilities,
						ImageTranslate: caps.ImageTranslate,
						SupportsIn:     caps.Has(llm.CapabilityImageEdit) || caps.Has(llm.CapabilityVision),
					},
					score: scoreModelForSlot(slot, mid),
				})
			}
		}
		if pid == "grok-cli" && slot == llm.ModelSlotImageGenerate && !seen["grok-cli/grok-imagine-image"] {
			seen["grok-cli/grok-imagine-image"] = true
			found = append(found, scoredOpt{
				opt: llm.SlotModelOption{
					ProviderID:   "grok-cli",
					ModelID:      "grok-imagine-image",
					Name:         "Grok Imagine Image (Grok CLI)",
					Capabilities: []llm.ModelCapability{llm.CapabilityImageGenerate},
				},
				score: scoreModelForSlot(slot, "grok-imagine-image"),
			})
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		if found[i].opt.ProviderID != found[j].opt.ProviderID {
			return found[i].opt.ProviderID < found[j].opt.ProviderID
		}
		return found[i].opt.ModelID < found[j].opt.ModelID
	})

	out := make([]llm.SlotModelOption, len(found))
	for i, item := range found {
		out[i] = item.opt
	}
	return out
}

// ListAllModelSlots returns strictly capability-filtered candidate models for
// every standard ModelSlot.
func (s *Service) ListAllModelSlots() map[llm.ModelSlot][]llm.SlotModelOption {
	slots := llm.AllModelSlots()
	out := make(map[llm.ModelSlot][]llm.SlotModelOption, len(slots))
	for _, slot := range slots {
		list := s.ListModelsBySlot(slot)
		if list == nil {
			list = []llm.SlotModelOption{}
		}
		out[slot] = list
	}
	return out
}

func (s *Service) modelMatchesSlot(providerID, modelID string, slot llm.ModelSlot) bool {
	if strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelID) == "" {
		return false
	}
	if !s.isProviderUsableForLocalMedia(providerID) {
		return false
	}
	mObj := s.lookupConfiguredModelObject(providerID, modelID)
	caps := extractModelCapabilitiesFromConfig(modelID, mObj)
	return llm.MatchesModelSlot(caps, slot)
}

// ResolveModelForSlot resolves one provider/model for slot:
//  1. Explicit caller override (preferredProviderID / preferredModelID)
//  2. Configured role default in ~/.hoxi/media-roles.json (with legacy
//     imageModel -> image_edit mapping when the model actually supports image_edit)
//  3. Highest-ranked configured model matching slot
func (s *Service) ResolveModelForSlot(
	slot llm.ModelSlot,
	preferredProviderID, preferredModelID string,
) (string, string, string, bool) {
	if s == nil || !llm.IsValidModelSlot(string(slot)) {
		return "", "", "", false
	}
	pid := strings.TrimSpace(preferredProviderID)
	mid := strings.TrimSpace(preferredModelID)
	if pid == "" && strings.Contains(mid, "/") {
		parts := strings.SplitN(mid, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" && s.isProviderUsableForLocalMedia(parts[0]) {
			pid = strings.TrimSpace(parts[0])
			mid = strings.TrimSpace(parts[1])
		}
	}

	candidates := s.ListModelsBySlot(slot)
	if pid != "" || mid != "" {
		if llm.IsExcludedUtilityModel(mid, "") {
			return "", "", "", false
		}
		if pid != "" && mid != "" {
			// If this model exists in local config, enforce strict slot capability matching;
			// only allow unlisted ad-hoc explicit model IDs to proceed unchecked.
			if mObj := s.lookupConfiguredModelObject(pid, mid); mObj != nil {
				if !llm.MatchesModelSlot(extractModelCapabilitiesFromConfig(mid, mObj), slot) {
					return "", "", "", false
				}
			}
			if conn, err := s.resolveImageConnection(pid, mid); err == nil && isHTTPEndpoint(conn.BaseURL) &&
				(conn.APIKey != "" || len(conn.Headers) > 0 || isLoopbackURL(conn.BaseURL)) {
				return pid, mid, "explicit", true
			}
		}
		for _, item := range candidates {
			if pid != "" && item.ProviderID != pid {
				continue
			}
			if mid != "" && !strings.EqualFold(item.ModelID, mid) {
				continue
			}
			return item.ProviderID, item.ModelID, "explicit", true
		}
		if pid == "" && mid != "" {
			for _, item := range candidates {
				if strings.EqualFold(item.ModelID, mid) {
					return item.ProviderID, item.ModelID, "explicit", true
				}
			}
		}
		return "", "", "", false
	}

	prefs := s.LoadMediaRolePrefs()
	if defPID, defMID := splitMediaRoleSpec(rolePrefForSlot(prefs, slot)); defPID != "" && defMID != "" {
		if s.modelMatchesSlot(defPID, defMID, slot) {
			return defPID, defMID, "role_default", true
		}
	}
	// Smooth backward compatibility: legacy imageModel also serves image_edit
	// when the pinned model genuinely advertises image_edit capability.
	if slot == llm.ModelSlotImageEdit {
		if defPID, defMID := splitMediaRoleSpec(prefs.ImageModel); defPID != "" && defMID != "" {
			if s.modelMatchesSlot(defPID, defMID, llm.ModelSlotImageEdit) {
				return defPID, defMID, "role_default_legacy", true
			}
		}
	}

	if len(candidates) == 0 {
		return "", "", "", false
	}
	return candidates[0].ProviderID, candidates[0].ModelID, "configured_model", true
}

// ResolveImageEditModelForSession resolves an image_edit-capable model for
// inpainting/editing, honoring explicit hints, active session self-capability,
// configured imageEditModel (or capable legacy imageModel), and configured pool.
func (s *Service) ResolveImageEditModelForSession(
	preferredProviderID, preferredModelID, sessionProviderID, sessionModelID string,
) (string, string, string, bool) {
	if strings.TrimSpace(preferredProviderID) != "" || strings.TrimSpace(preferredModelID) != "" {
		return s.ResolveModelForSlot(llm.ModelSlotImageEdit, preferredProviderID, preferredModelID)
	}
	sessPID := strings.TrimSpace(sessionProviderID)
	sessMID := strings.TrimSpace(sessionModelID)
	if sessPID != "" && sessMID != "" && s.modelMatchesSlot(sessPID, sessMID, llm.ModelSlotImageEdit) {
		if conn, err := s.resolveImageConnection(sessPID, sessMID); err == nil && isHTTPEndpoint(conn.BaseURL) &&
			(conn.APIKey != "" || len(conn.Headers) > 0 || isLoopbackURL(conn.BaseURL)) {
			return sessPID, sessMID, "session_self", true
		}
	}
	return s.ResolveModelForSlot(llm.ModelSlotImageEdit, "", "")
}
