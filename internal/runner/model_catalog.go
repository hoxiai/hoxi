package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

// engineModelUnavailableError means the running engine's model catalog does not
// contain the requested provider/model. This is almost always a stale engine
// config: Hoxi's models.json was updated after the daemon booted, so the UI
// offers a model the engine has never loaded. Reporting it before the message
// POST keeps the failure synchronous and actionable instead of surfacing later
// as a generic execution error.
type engineModelUnavailableError struct {
	ProviderID string
	ModelID    string
	Known      []string
}

func (e *engineModelUnavailableError) Error() string {
	target := e.ModelID
	if e.ProviderID != "" {
		target = e.ProviderID + "/" + e.ModelID
	}
	base := fmt.Sprintf("model %q is not loaded in the running engine", target)
	if len(e.Known) > 0 {
		base += " (engine has: " + strings.Join(e.Known, ", ") + ")"
	}
	return base + "; reload the model catalog or pick another model, then retry"
}

type openCodeProviderCatalogEntry struct {
	ID     string                 `json:"id"`
	Models map[string]interface{} `json:"models"`
}

type openCodeProvidersCatalogResponse struct {
	Providers []openCodeProviderCatalogEntry `json:"providers"`
}

// ensureOpenCodeModelAvailable verifies provider/model against the live engine
// catalog and returns the canonical model ID loaded in the engine. When a model
// is missing from the running directory instance (for example because global
// dispose was deferred while another workspace was busy) and this directory has
// no active OpenCode turn, it disposes only this directory's OpenCode instance
// so the engine reloads ~/.hoxi/models.json on the spot.
func (s *Service) ensureOpenCodeModelAvailable(ctx context.Context, directory, providerID, modelID string, allowReload bool) (string, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" || !s.openCode.Available() {
		return modelID, nil
	}
	qCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var resp openCodeProvidersCatalogResponse
	if err := s.openCodeJSON(qCtx, http.MethodGet, "/config/providers", directory, nil, &resp); err != nil || len(resp.Providers) == 0 {
		return modelID, nil
	}
	if resolved, ok, _ := findOpenCodeCatalogModel(resp.Providers, providerID, modelID, false); ok {
		return resolved, nil
	}
	if allowReload && !s.isOpenCodeDirectoryBusy(qCtx, directory) {
		if err := s.openCodeJSON(qCtx, http.MethodPost, "/instance/dispose", directory, map[string]interface{}{}, nil); err == nil {
			var refreshed openCodeProvidersCatalogResponse
			if err := s.openCodeJSON(qCtx, http.MethodGet, "/config/providers", directory, nil, &refreshed); err == nil && len(refreshed.Providers) > 0 {
				resp = refreshed
			}
		}
	}
	resolved, ok, known := findOpenCodeCatalogModel(resp.Providers, providerID, modelID, true)
	if ok {
		return resolved, nil
	}
	return modelID, &engineModelUnavailableError{
		ProviderID: providerID,
		ModelID:    modelID,
		Known:      known,
	}
}

func findOpenCodeCatalogModel(providers []openCodeProviderCatalogEntry, providerID, modelID string, allowFold bool) (string, bool, []string) {
	for _, p := range providers {
		if p.ID != providerID {
			continue
		}
		if len(p.Models) == 0 {
			return modelID, true, nil
		}
		if _, ok := p.Models[modelID]; ok {
			return modelID, true, nil
		}
		if allowFold {
			if matched := matchModelIDFold(p.Models, modelID); matched != "" {
				return matched, true, nil
			}
		}
		return "", false, sampleModelIDs(p.Models)
	}
	// The provider itself is absent from the catalog. That can be a partially
	// loaded snapshot, so treat it as unknown rather than blocking the turn.
	return modelID, true, nil
}

func matchModelIDFold(models map[string]interface{}, target string) string {
	var matched string
	for id := range models {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || !strings.EqualFold(trimmed, target) {
			continue
		}
		if matched != "" && matched != id {
			return ""
		}
		matched = id
	}
	return matched
}

func (s *Service) isOpenCodeDirectoryBusy(ctx context.Context, directory string) bool {
	cleanDir := filepath.Clean(strings.TrimSpace(directory))
	s.mu.Lock()
	for sid, ap := range s.procs {
		if ap == nil || ap.hasPendingError() || ap.wasAborted() {
			continue
		}
		rec, ok := s.store.Meta(sid)
		if !ok || rec.Runtime != "opencode" {
			continue
		}
		if cleanDir == "" || filepath.Clean(strings.TrimSpace(rec.WorktreePath)) == cleanDir {
			s.mu.Unlock()
			return true
		}
	}
	s.mu.Unlock()

	var statuses map[string]SessionStatus
	if err := s.openCodeJSON(ctx, http.MethodGet, "/session/status", directory, nil, &statuses); err != nil {
		var httpErr *ocapi.HTTPError
		if errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
			return false
		}
		return true
	}
	for _, st := range statuses {
		if st.Type != "" && st.Type != "idle" {
			return true
		}
	}
	return false
}

// sampleModelIDs returns a few catalog keys so the message shows what the engine
// actually loaded. Model IDs are not secrets.
func sampleModelIDs(models map[string]interface{}) []string {
	const limit = 3
	keys := make([]string, 0, len(models))
	for id := range models {
		if strings.TrimSpace(id) == "" {
			continue
		}
		keys = append(keys, id)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}
