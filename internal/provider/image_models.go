package provider

import (
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func (s *Service) isProviderUsableForLocalMedia(providerID string) bool {
	pid := strings.TrimSpace(providerID)
	if pid == "" || pid == AutoProviderID || pid == "opencode" || pid == "opencode-go" {
		return false
	}
	if pid == officialProviderID && s.officialBlocked.Load() {
		return false
	}
	if s.disabledProvidersSet()[pid] {
		return false
	}
	baseURL, apiKey, _, headers := s.resolveEffectiveProviderConnection(pid, "", "")
	if !isHTTPEndpoint(baseURL) || isCodexChatGPTBackendURL(baseURL) {
		return false
	}
	if apiKey != "" || len(headers) > 0 || isLoopbackURL(baseURL) {
		return true
	}
	return false
}

func scoreImageModelPreference(modelID string) int {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.Contains(lower, "gemini-3") && strings.Contains(lower, "image"):
		return 100
	case strings.Contains(lower, "gemini-2.5") && isGeminiImageModelID(lower):
		return 96
	case isGeminiImageModelID(lower):
		return 92
	case strings.Contains(lower, "gpt-image"):
		return 90
	case strings.Contains(lower, "flux-kontext") || strings.Contains(lower, "seedream"):
		return 85
	case strings.Contains(lower, "flux") || strings.Contains(lower, "imagen"):
		return 80
	case strings.Contains(lower, "dall-e-3") || strings.Contains(lower, "ideogram") || strings.Contains(lower, "recraft"):
		return 75
	default:
		return 60
	}
}

// ListImageModels returns all image generation models configured on active,
// credentialed providers in Hoxi / OpenCode config, ordered by capability rank.
// Delegates to the single-source-of-truth ModelSlotImageGenerate index.
func (s *Service) ListImageModels() []llm.ImageModelOption {
	slotOpts := s.ListModelsBySlot(llm.ModelSlotImageGenerate)
	if len(slotOpts) == 0 {
		return nil
	}
	out := make([]llm.ImageModelOption, len(slotOpts))
	for i, item := range slotOpts {
		out[i] = llm.ImageModelOption{
			ProviderID:   item.ProviderID,
			ModelID:      item.ModelID,
			Name:         item.Name,
			SupportsIn:   item.SupportsIn,
			Capabilities: item.Capabilities,
		}
	}
	return out
}

// ResolveImageModel selects a concrete image generation provider/model, honoring
// explicit caller hints first and otherwise picking the top configured image model.
func (s *Service) ResolveImageModel(preferredProviderID, preferredModelID string) (string, string, bool) {
	pid := strings.TrimSpace(preferredProviderID)
	mid := strings.TrimSpace(preferredModelID)
	if pid == "" && strings.Contains(mid, "/") {
		parts := strings.SplitN(mid, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			if s.isProviderUsableForLocalMedia(parts[0]) {
				pid = strings.TrimSpace(parts[0])
				mid = strings.TrimSpace(parts[1])
			}
		}
	}
	if pid != "" && mid != "" {
		if conn, err := s.resolveImageConnection(pid, mid); err == nil && isHTTPEndpoint(conn.BaseURL) {
			return pid, mid, true
		}
	}
	list := s.ListImageModels()
	if mid != "" {
		for _, item := range list {
			if strings.EqualFold(item.ModelID, mid) {
				return item.ProviderID, item.ModelID, true
			}
		}
	}
	if pid != "" {
		for _, item := range list {
			if item.ProviderID == pid {
				return item.ProviderID, item.ModelID, true
			}
		}
	}
	if len(list) > 0 {
		return list[0].ProviderID, list[0].ModelID, true
	}
	return "", "", false
}

// ResolveImageModelForSession performs a 3-step self-check:
//  1. Explicit caller providerID/modelID override ("explicit")
//  2. Active session model's own native image output capability ("session_self")
//  3. Best configured local image generation model ("configured_model", breaking
//     score ties in favor of the active session's provider)
func (s *Service) ResolveImageModelForSession(
	preferredProviderID, preferredModelID, sessionProviderID, sessionModelID string,
) (string, string, string, bool) {
	if strings.TrimSpace(preferredProviderID) != "" || strings.TrimSpace(preferredModelID) != "" {
		pid, mid, ok := s.ResolveImageModel(preferredProviderID, preferredModelID)
		if !ok {
			return "", "", "", false
		}
		return pid, mid, "explicit", true
	}
	sessPID := strings.TrimSpace(sessionProviderID)
	sessMID := strings.TrimSpace(sessionModelID)
	if sessPID != "" && sessMID != "" && s.CanModelGenerateImage(sessPID, sessMID) {
		if conn, err := s.resolveImageConnection(sessPID, sessMID); err == nil && isHTTPEndpoint(conn.BaseURL) && (conn.APIKey != "" || len(conn.Headers) > 0 || isLoopbackURL(conn.BaseURL)) {
			return sessPID, sessMID, "session_self", true
		}
	}
	if defPID, defMID := splitMediaRoleSpec(s.LoadMediaRolePrefs().ImageModel); defPID != "" && defMID != "" {
		if s.isProviderUsableForLocalMedia(defPID) && s.CanModelGenerateImage(defPID, defMID) {
			return defPID, defMID, "role_default", true
		}
	}
	list := s.ListImageModels()
	if len(list) == 0 {
		return "", "", "", false
	}
	best := list[0]
	if sessPID != "" && best.ProviderID != sessPID {
		bestScore := scoreImageModelPreference(best.ModelID)
		for _, item := range list[1:] {
			if scoreImageModelPreference(item.ModelID) < bestScore {
				break
			}
			if item.ProviderID == sessPID {
				best = item
				break
			}
		}
	}
	return best.ProviderID, best.ModelID, "configured_model", true
}

func normalizeAspectRatio(raw string) string {
	switch strings.TrimSpace(raw) {
	case "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "4:5", "5:4", "21:9":
		return strings.TrimSpace(raw)
	default:
		return ""
	}
}

func normalizeImageSizeTier(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "1K", "2K", "4K":
		return strings.ToUpper(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func clampImageCount(n int) int {
	if n <= 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

func openAIImageSizeForAspectRatio(modelID, aspectRatio string) string {
	ar := normalizeAspectRatio(aspectRatio)
	if ar == "" || ar == "1:1" {
		return "1024x1024"
	}
	lower := strings.ToLower(strings.TrimSpace(modelID))
	isDalle3 := strings.Contains(lower, "dall-e-3")
	switch ar {
	case "16:9", "21:9", "3:2", "4:3", "5:4":
		if isDalle3 {
			return "1792x1024"
		}
		return "1536x1024"
	case "9:16", "2:3", "3:4", "4:5":
		if isDalle3 {
			return "1024x1792"
		}
		return "1024x1536"
	default:
		return "1024x1024"
	}
}
