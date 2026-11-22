package handoff

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
)

func (g *Service) handleLocalCLIImportRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		g.handleLocalCLIImport(w, r)
		return
	}
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, LocalCLIImportResponse{Error: "Invalid JSON payload"})
		return
	}
	var req LocalCLIImportRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, LocalCLIImportResponse{Error: "Invalid JSON payload"})
		return
	}
	if req.ToolID == ExternalToolOpenCode && req.Overwrite {
		resp := g.importOpenCodeCLIProviderWithOptions(true)
		if resp.OK && strings.TrimSpace(resp.ProviderID) != "" {
			g.clearDismissedExternalProviders(resp.ProviderID)
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Pre-clear suppression for target provider before delegating to standard import.
	switch req.ToolID {
	case ExternalToolGrok:
		g.clearDismissedExternalProviders("grok-cli")
	case ExternalToolCodex:
		g.clearDismissedExternalProviders("openai")
	case ExternalToolClaude:
		g.clearDismissedExternalProviders("claude-code")
	case ExternalToolGemini:
		g.clearDismissedExternalProviders("google")
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	g.handleLocalCLIImport(w, r)
}

func (g *Service) handleLocalCLIDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req LocalCLIDisconnectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, LocalCLIDisconnectResponse{Error: "Invalid JSON payload"})
		return
	}
	var targetIDs []string
	switch req.ToolID {
	case ExternalToolGrok:
		targetIDs = []string{"grok-cli"}
	case ExternalToolCodex:
		targetIDs = []string{"openai"}
	case ExternalToolClaude:
		targetIDs = []string{"claude-code"}
	case ExternalToolGemini:
		targetIDs = []string{"google"}
	case ExternalToolOpenCode:
		provDefs := readExternalOpenCodeConfigProviders()
		authMap := readExternalOpenCodeAuth()
		hoxiMap := g.loadHoxiProvidersMap()
		for pid := range provDefs {
			targetIDs = appendUniqueString(targetIDs, pid, resolveOpenCodeProviderID(pid, authMap, hoxiMap))
		}
		for pid := range authMap {
			targetIDs = appendUniqueString(targetIDs, pid)
		}
		if ocstate.ReadExternalConsoleState() != nil {
			targetIDs = appendUniqueString(targetIDs, "opencode-go", "opencode")
			_ = ocstate.ClearConsoleState(hoxipaths.DBPath())
		}
		if len(targetIDs) == 0 {
			targetIDs = []string{"opencode"}
		}
	default:
		writeJSON(w, http.StatusBadRequest, LocalCLIDisconnectResponse{
			OK:     false,
			ToolID: string(req.ToolID),
			Error:  "Unsupported toolID: " + string(req.ToolID),
		})
		return
	}
	for _, pid := range targetIDs {
		if err := g.disconnectProvider(pid); err != nil {
			writeJSON(w, http.StatusInternalServerError, LocalCLIDisconnectResponse{
				OK:     false,
				ToolID: string(req.ToolID),
				Error:  err.Error(),
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, LocalCLIDisconnectResponse{
		OK:           true,
		ToolID:       string(req.ToolID),
		Disconnected: targetIDs,
	})
}
