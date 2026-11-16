package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hoxiai/hoxi/internal/telemetry"
)

// handleTelemetry exposes the local anonymous-usage preference so the UI can
// opt out without editing files. It never reports or resends anything itself.
func (g *Gateway) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, telemetry.Current())
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
		if err := telemetry.SetEnabled(*input.Enabled); err != nil {
			if errors.Is(err, telemetry.ErrEnvOverride) {
				writeJSONError(w, http.StatusConflict, "telemetry is disabled by the environment")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "could not persist telemetry preference")
			return
		}
		writeJSON(w, http.StatusOK, telemetry.Current())
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
