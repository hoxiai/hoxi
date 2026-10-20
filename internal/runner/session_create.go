package runner

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/sandbox"
)

func (s *Service) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title          string `json:"title"`
		Directory      string `json:"directory"`
		Runtime        string `json:"runtime"`
		ModelID        string `json:"modelID"`
		ProviderID     string `json:"providerID"`
		Mode           string `json:"mode"`
		SandboxMode    string `json:"sandboxMode"`
		CommandSandbox bool   `json:"commandSandbox"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid session request")
		return
	}

	rt := strings.TrimSpace(body.Runtime)
	if rt == "" {
		rt = string(RuntimeAntigravity)
	}
	if !IsValidRuntime(rt) {
		writeJSONError(w, http.StatusBadRequest, "unsupported runner runtime: "+rt)
		return
	}
	projDir := strings.TrimSpace(body.Directory)
	if projDir == "" {
		projDir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	if projDir == "" {
		projDir = s.rootDir
	}

	projDir, err := filepath.Abs(projDir)
	if err == nil {
		_, err = existingDirectory(projDir)
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	sandboxMode, err := normalizeSandboxMode(body.SandboxMode)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.CommandSandbox && !sandbox.Available() {
		writeJSONError(w, http.StatusBadRequest, sandbox.UnavailableReason())
		return
	}
	sessionID := GenerateRunnerSessionID(rt)

	modelID := strings.TrimSpace(body.ModelID)
	if modelID == "" {
		switch rt {
		case string(RuntimeClaudeCode):
			if ms := DiscoverClaudeModels(); len(ms) > 0 {
				modelID = ms[0].ID
			}
		case string(RuntimeCodeBuddy):
			if ms := agentcli.DiscoverCodeBuddyModels(); len(ms) > 0 {
				modelID = ms[0].ID
			}
		case string(RuntimeQoder):
			if ms := agentcli.DiscoverQoderModels(); len(ms) > 0 {
				modelID = ms[0].ID
			}
		default:
			if ms, _, _ := DiscoverAgyModels(); len(ms) > 0 {
				modelID = ms[0].ID
			}
		}
	}
	provID := strings.TrimSpace(body.ProviderID)
	if provID == "" {
		provID = rt
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		switch rt {
		case string(RuntimeClaudeCode):
			title = "Claude Code Session"
		case string(RuntimeCodeBuddy):
			title = "CodeBuddy Session"
		case string(RuntimeQoder):
			title = "Qoder Session"
		default:
			title = "Antigravity Session"
		}
	}
	now := time.Now().UnixMilli()
	rec := SessionRecord{
		ID:             sessionID,
		Runtime:        rt,
		ProjectDir:     projDir,
		SandboxMode:    sandboxMode,
		CommandSandbox: body.CommandSandbox,
		ModelID:        modelID,
		ProviderID:     provID,
		Mode:           firstNonEmptyStr(body.Mode, "build"),
		Title:          title,
		CreatedAt:      now,
		UpdatedAt:      now,
		Turns:          []MessageTurn{},
	}
	if err := s.provisionSessionDirectory(&rec); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Put(rec); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.created",
		Properties: map[string]interface{}{"info": info},
	})
	writeJSON(w, http.StatusOK, info)
}
