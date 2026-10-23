package gateway

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/hoxiai/hoxi/internal/worktree"
)

// loadGitAttribution applies the stored git attribution preference to the
// worktree commit paths. Called once while the gateway starts.
func loadGitAttribution() {
	worktree.SetAttributionMode(worktree.LoadAttribution(worktree.AttributionPath()).Mode)
}

// handleGitAttribution reads or updates which commits carry the
// "Co-authored-by: Hoxi" trailer. Desktop-only: the route is not in the tunnel
// allow-list.
func (g *Gateway) handleGitAttribution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, attributionState())
	case http.MethodPut:
		var input struct {
			Mode    *string `json:"mode"`
			Enabled *bool   `json:"enabled"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || (input.Mode == nil && input.Enabled == nil) {
			writeJSONError(w, http.StatusBadRequest, "an explicit mode or enabled boolean is required")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "expected exactly one JSON object")
			return
		}
		mode, ok := resolveAttributionInput(input.Mode, input.Enabled)
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "mode must be auto, always or never")
			return
		}
		if err := worktree.SaveAttribution(worktree.AttributionPath(), mode); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not persist git attribution preference")
			return
		}
		writeJSON(w, http.StatusOK, attributionState())
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// resolveAttributionInput prefers mode and falls back to the legacy boolean
// toggle (true = always, false = never).
func resolveAttributionInput(mode *string, enabled *bool) (worktree.AttributionMode, bool) {
	if mode != nil {
		return worktree.ParseAttributionMode(*mode)
	}
	if enabled == nil {
		return "", false
	}
	if *enabled {
		return worktree.AttributionAlways, true
	}
	return worktree.AttributionNever, true
}

func attributionState() worktree.AttributionState {
	mode := worktree.CurrentAttributionMode()
	return worktree.AttributionState{Mode: mode, Enabled: mode != worktree.AttributionNever}
}
