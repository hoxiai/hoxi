package provider

import (
	"sort"
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
	baseURL, apiKey, _, headers := s.resolveProviderConnection(pid, "", "")
	if !isHTTPEndpoint(baseURL) {
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
func (s *Service) ListImageModels() []llm.ImageModelOption {
	if s == nil {
		return nil
	}
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}

	type scoredOpt struct {
		opt   llm.ImageModelOption
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
		mMap, ok := pObj["models"].(map[string]interface{})
		if !ok {
			continue
		}
		for _, mid := range sortedKeys(mMap) {
			mObj, _ := mMap[mid].(map[string]interface{})
			cat := ""
			name := mid
			supportsIn := supportsMultimodalImageEditInput(mid)
			if mObj != nil {
				cat, _ = mObj["category"].(string)
				if n, _ := mObj["name"].(string); strings.TrimSpace(n) != "" {
					name = strings.TrimSpace(n)
				}
				if hasImageInputModality(mObj) {
					supportsIn = true
				}
			}
			isImg := IsImageGenerationModel(mid, cat, "") || (mObj != nil && hasImageOutputModality(mObj))
			if !isImg {
				continue
			}
			key := pid + "/" + mid
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, scoredOpt{
				opt: llm.ImageModelOption{
					ProviderID: pid,
					ModelID:    mid,
					Name:       name,
					SupportsIn: supportsIn,
				},
				score: scoreImageModelPreference(mid),
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

	out := make([]llm.ImageModelOption, len(found))
	for i, item := range found {
		out[i] = item.opt
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
