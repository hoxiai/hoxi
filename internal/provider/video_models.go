package provider

import (
	"sort"
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
func (s *Service) ListVideoModels() []llm.VideoModelOption {
	if s == nil {
		return nil
	}
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}

	type scoredOpt struct {
		opt   llm.VideoModelOption
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
				cat := ""
				name := mid
				supportsIn := supportsVideoImageInput(mid)
				if mObj != nil {
					cat, _ = mObj["category"].(string)
					if n, _ := mObj["name"].(string); strings.TrimSpace(n) != "" {
						name = strings.TrimSpace(n)
					}
					if hasImageInputModality(mObj) {
						supportsIn = true
					}
				}
				isVid := IsVideoGenerationModel(mid, cat, "") || (mObj != nil && hasVideoOutputModality(mObj))
				if !isVid {
					continue
				}
				key := pid + "/" + mid
				if seen[key] {
					continue
				}
				seen[key] = true
				found = append(found, scoredOpt{
					opt: llm.VideoModelOption{
						ProviderID: pid,
						ModelID:    mid,
						Name:       name,
						SupportsIn: supportsIn,
					},
					score: scoreVideoModelPreference(mid),
				})
			}
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

	out := make([]llm.VideoModelOption, len(found))
	for i, item := range found {
		out[i] = item.opt
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
