package runner

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
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

// ensureOpenCodeModelAvailable verifies provider/model against the live engine
// catalog. It never guesses: an unreachable catalog or a provider that reports
// no models at all is treated as unknown and skipped, so a transient engine
// hiccup can never block a valid request.
func (s *Service) ensureOpenCodeModelAvailable(ctx context.Context, directory, providerID, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" || !s.openCode.Available() {
		return nil
	}
	qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var resp struct {
		Providers []struct {
			ID     string                 `json:"id"`
			Models map[string]interface{} `json:"models"`
		} `json:"providers"`
	}
	if err := s.openCodeJSON(qCtx, http.MethodGet, "/config/providers", directory, nil, &resp); err != nil || len(resp.Providers) == 0 {
		return nil
	}
	for _, p := range resp.Providers {
		if p.ID != providerID {
			continue
		}
		if len(p.Models) == 0 {
			return nil
		}
		if _, ok := p.Models[modelID]; ok {
			return nil
		}
		return &engineModelUnavailableError{
			ProviderID: providerID,
			ModelID:    modelID,
			Known:      sampleModelIDs(p.Models),
		}
	}
	// The provider itself is absent from the catalog. That can be a partially
	// loaded snapshot, so treat it as unknown rather than blocking the turn.
	return nil
}

// sampleModelIDs returns a few catalog keys so the message shows what the engine
// actually loaded. Model IDs are not secrets.
func sampleModelIDs(models map[string]interface{}) []string {
	const limit = 3
	out := make([]string, 0, limit)
	for id := range models {
		if strings.TrimSpace(id) == "" {
			continue
		}
		out = append(out, id)
		if len(out) == limit {
			break
		}
	}
	return out
}
