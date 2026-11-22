package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/hoxiai/hoxi/internal/llm"
)

func (g *Gateway) handleMediaRoles(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "local media role settings are not exposed over remote tunnel")
		return
	}
	if g.providers == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider service is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"roles": g.providers.LoadMediaRolePrefs(),
		})
	case http.MethodPut, http.MethodPatch, http.MethodPost:
		var raw map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&raw); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		patch := map[string]*string{}
		for k, val := range raw {
			switch k {
			case "imageModel", "videoModel", "speechModel", "transcribeModel":
			default:
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown media role field %q", k))
				return
			}
			if val == nil {
				empty := ""
				patch[k] = &empty
				continue
			}
			s, ok := val.(string)
			if !ok {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("media role field %q must be a string or null", k))
				return
			}
			cp := s
			patch[k] = &cp
		}
		updated, err := g.providers.UpdateMediaRolePrefs(patch)
		if err != nil {
			if errors.Is(err, llm.ErrInvalidMediaRole) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"roles": updated,
		})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or PUT required")
	}
}
