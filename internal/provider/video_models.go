package provider

import (
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func scoreVideoModelPreference(modelID string) int {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.Contains(lower, "veo-3") || strings.Contains(lower, "veo3"):
		return 100
	case strings.Contains(lower, "sora-2") || strings.Contains(lower, "sora2"):
		return 98
	case strings.Contains(lower, "seedance"):
		return 95
	case strings.Contains(lower, "kling"):
		return 92
	case strings.Contains(lower, "wan2.6") || strings.Contains(lower, "wan2.5") || strings.Contains(lower, "veo-2") || strings.Contains(lower, "veo2"):
		return 90
	case strings.Contains(lower, "sora") || strings.Contains(lower, "wan2") || strings.Contains(lower, "wanx") || strings.Contains(lower, "hailuo"):
		return 85
	case strings.Contains(lower, "runway") || strings.Contains(lower, "luma-") || strings.Contains(lower, "vidu") || strings.Contains(lower, "cogvideo"):
		return 80
	default:
		return 60
	}
}

// IsVideoModel reports whether providerID/modelID is a dedicated video generation
// model, checking explicit model metadata first and canonical ID patterns second.
func (s *Service) IsVideoModel(providerID, modelID string) bool {
	mid := strings.TrimSpace(modelID)
	if mid == "" {
		return false
	}
	if mObj := s.lookupConfiguredModelObject(providerID, mid); mObj != nil {
		cat, _ := mObj["category"].(string)
		normCat := normalizeModelCategory(cat)
		if normCat == "chat" || normCat == "coding" {
			return false
		}
		if IsVideoGenerationModel(mid, cat, "") || hasVideoOutputModality(mObj) {
			return true
		}
	}
	return IsVideoGenerationModel(mid, "", "")
}

// CanModelGenerateVideo reports whether providerID/modelID itself can natively
// generate videos (either as a dedicated video model or via explicit output.video
// modality metadata).
func (s *Service) CanModelGenerateVideo(providerID, modelID string) bool {
	mid := strings.TrimSpace(modelID)
	if mid == "" {
		return false
	}
	if s.IsVideoModel(providerID, mid) {
		return true
	}
	if mObj := s.lookupConfiguredModelObject(providerID, mid); mObj != nil {
		return hasVideoOutputModality(mObj)
	}
	return false
}

// ListVideoModels returns all video generation models configured on active,
// credentialed providers in Hoxi / OpenCode config, ordered by capability rank.
// Delegates to the single-source-of-truth ModelSlotVideo index.
func (s *Service) ListVideoModels() []llm.VideoModelOption {
	slotOpts := s.ListModelsBySlot(llm.ModelSlotVideo)
	if len(slotOpts) == 0 {
		return nil
	}
	out := make([]llm.VideoModelOption, len(slotOpts))
	for i, item := range slotOpts {
		mObj := s.lookupConfiguredModelObject(item.ProviderID, item.ModelID)
		supportsIn := supportsVideoImageInput(item.ModelID) || (mObj != nil && hasImageInputModality(mObj))
		out[i] = llm.VideoModelOption{
			ProviderID: item.ProviderID,
			ModelID:    item.ModelID,
			Name:       item.Name,
			SupportsIn: supportsIn,
		}
	}
	return out
}

// ResolveVideoModel selects a concrete video generation provider/model, honoring
// explicit caller hints first and otherwise picking the top configured video model.
func (s *Service) ResolveVideoModel(preferredProviderID, preferredModelID string) (string, string, bool) {
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
	list := s.ListVideoModels()
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

// ResolveVideoModelForSession performs a 3-step self-check:
//  1. Explicit caller providerID/modelID override ("explicit")
//  2. Active session model's own native video output capability ("session_self")
//  3. Best configured local video generation model ("configured_model", breaking
//     score ties in favor of the active session's provider)
func (s *Service) ResolveVideoModelForSession(
	preferredProviderID, preferredModelID, sessionProviderID, sessionModelID string,
) (string, string, string, bool) {
	if strings.TrimSpace(preferredProviderID) != "" || strings.TrimSpace(preferredModelID) != "" {
		pid, mid, ok := s.ResolveVideoModel(preferredProviderID, preferredModelID)
		if !ok {
			return "", "", "", false
		}
		return pid, mid, "explicit", true
	}
	sessPID := strings.TrimSpace(sessionProviderID)
	sessMID := strings.TrimSpace(sessionModelID)
	if sessPID != "" && sessMID != "" && s.CanModelGenerateVideo(sessPID, sessMID) {
		if conn, err := s.resolveImageConnection(sessPID, sessMID); err == nil && isHTTPEndpoint(conn.BaseURL) && (conn.APIKey != "" || len(conn.Headers) > 0 || isLoopbackURL(conn.BaseURL)) {
			return sessPID, sessMID, "session_self", true
		}
	}
	if defPID, defMID := splitMediaRoleSpec(s.LoadMediaRolePrefs().VideoModel); defPID != "" && defMID != "" {
		if s.isProviderUsableForLocalMedia(defPID) && s.CanModelGenerateVideo(defPID, defMID) {
			return defPID, defMID, "role_default", true
		}
	}
	list := s.ListVideoModels()
	if len(list) == 0 {
		return "", "", "", false
	}
	best := list[0]
	if sessPID != "" && best.ProviderID != sessPID {
		bestScore := scoreVideoModelPreference(best.ModelID)
		for _, item := range list[1:] {
			if scoreVideoModelPreference(item.ModelID) < bestScore {
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
