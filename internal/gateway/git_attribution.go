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
	worktree.SetAttributionEnabled(worktree.LoadAttribution(worktree.AttributionPath()).Enabled)
}

// handleGitAttribution reads or updates whether Hoxi-created commits carry the
// "Co-authored-by: Hoxi" trailer. Desktop-only: the route is not in the tunnel
// allow-list.
func (g *Gateway) handleGitAttribution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, worktree.AttributionState{Enabled: worktree.AttributionEnabled()})
	case http.MethodPut:
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || input.Enabled == nil {
			writeJSONError(w, http.StatusBadRequest, "an explicit enabled boolean is required")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "expected exactly one JSON object")
			return
		}
		if err := worktree.SaveAttribution(worktree.AttributionPath(), *input.Enabled); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not persist git attribution preference")
			return
		}
		writeJSON(w, http.StatusOK, worktree.AttributionState{Enabled: worktree.AttributionEnabled()})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
