package runner

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// CreateSessionRequest describes a new Hoxi conversation: the JSON body of
// POST /api/hoxi/runner/sessions, also used in process (e.g. by convoys).
type CreateSessionRequest struct {
	Title          string `json:"title"`
	Directory      string `json:"directory"`
	Runtime        string `json:"runtime"`
	ModelID        string `json:"modelID"`
	ProviderID     string `json:"providerID"`
	Mode           string `json:"mode"`
	SandboxMode    string `json:"sandboxMode"`
	CommandSandbox bool   `json:"commandSandbox"`
}

func (s *Service) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid session request")
		return
	}
	info, status, err := s.CreateSession(body, r.URL.Query().Get("directory"))
	if err != nil {
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// CreateSession provisions and stores a new conversation; directory is the
// fallback project directory when body.Directory is empty.
func (s *Service) CreateSession(body CreateSessionRequest, directory string) (SessionInfo, int, error) {

	rt := strings.TrimSpace(body.Runtime)
	if rt == "" {
		rt = string(RuntimeAntigravity)
	}
	if !IsValidRuntime(rt) {
		return SessionInfo{}, http.StatusBadRequest, errors.New("unsupported runner runtime: " + rt)
	}
	projDir := strings.TrimSpace(body.Directory)
	if projDir == "" {
		projDir = strings.TrimSpace(directory)
	}
	if projDir == "" {
		projDir = s.rootDir
	}

	projDir, err := filepath.Abs(projDir)
	if err == nil {
		_, err = existingDirectory(projDir)
	}
	if err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	sandboxMode, commandSandbox, err := s.unattendedSessionShape(body.SandboxMode, body.CommandSandbox)
	if err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	projectID, err := s.ensureProjectID(projDir)
	if err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
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
		ProjectID:      projectID,
		Runtime:        rt,
		ProjectDir:     projDir,
		SandboxMode:    sandboxMode,
		CommandSandbox: commandSandbox,
		ModelID:        modelID,
		ProviderID:     provID,
		Mode:           firstNonEmptyStr(body.Mode, "build"),
		Title:          title,
		CreatedAt:      now,
		UpdatedAt:      now,
		Turns:          []MessageTurn{},
	}
	if err := s.provisionSessionDirectory(&rec); err != nil {
		return SessionInfo{}, http.StatusBadRequest, err
	}
	if err := s.store.Put(rec); err != nil {
		return SessionInfo{}, http.StatusInternalServerError, err
	}
	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.created",
		Properties: map[string]interface{}{"info": info},
	})
	return info, http.StatusOK, nil
}
