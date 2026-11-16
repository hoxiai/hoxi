package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hoxiai/hoxi/internal/telemetry"
)

// handleTelemetry exposes the local anonymous-usage and silent-offline
// preferences so the UI can toggle them without editing files.
func (g *Gateway) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, telemetry.Current())
	case http.MethodPut:
		var input struct {
			Enabled *bool `json:"enabled"`
			Offline *bool `json:"offline"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || (input.Enabled == nil && input.Offline == nil) {
			writeJSONError(w, http.StatusBadRequest, "an explicit enabled or offline boolean is required")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "expected exactly one JSON object")
			return
		}
		if input.Offline != nil {
			if err := telemetry.SetOffline(*input.Offline); err != nil {
				if errors.Is(err, telemetry.ErrOfflineEnvOverride) {
					writeJSONError(w, http.StatusConflict, "offline mode is locked by the environment")
					return
				}
				writeJSONError(w, http.StatusInternalServerError, "could not persist offline preference")
				return
			}
		}
		if input.Enabled != nil {
			if err := telemetry.SetEnabled(*input.Enabled); err != nil {
				if errors.Is(err, telemetry.ErrEnvOverride) {
					writeJSONError(w, http.StatusConflict, "telemetry is disabled by the environment")
					return
				}
				writeJSONError(w, http.StatusInternalServerError, "could not persist telemetry preference")
				return
			}
		}
		writeJSON(w, http.StatusOK, telemetry.Current())
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
