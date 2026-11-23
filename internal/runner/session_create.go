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
	ParentID       string `json:"parentID,omitempty"`
	ScheduleID     string `json:"scheduleID,omitempty"`
	Surface        string `json:"surface"`
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
		rt = string(RuntimeNative)
	}
	if !IsValidRuntime(rt) && rt != string(RuntimeOpenCode) {
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
	surface := NormalizeSessionSurface(body.Surface, projDir)
	projectID, err := s.ensureProjectIDWithSurface(projDir, surface)
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
		case string(RuntimeGeminiCLI):
			if ms := DiscoverGeminiModels(); len(ms) > 0 {
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
		case string(RuntimeCodex):
			if snap := agentcli.DiscoverCodexCatalog(false); snap.DefaultModel != "" {
				modelID = snap.DefaultModel
			}
		case string(RuntimeOpenCode):
			if ms := agentcli.DiscoverOpenCodeCLIModelsWithRefresh(false); len(ms) > 0 {
				modelID = ms[0].ID
			}
		case string(RuntimeNative):
			// Native sessions use the user's configured provider/model or "auto".
		default:
			if ms, _, _ := DiscoverAgyModels(); len(ms) > 0 {
				modelID = ms[0].ID
			}
		}
	}
	provID := strings.TrimSpace(body.ProviderID)
	if provID == "" {
		if rt == string(RuntimeNative) {
			provID = "auto"
		} else {
			provID = rt
		}
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		switch rt {
		case string(RuntimeClaudeCode):
			title = "Claude Code Session"
		case string(RuntimeGeminiCLI):
			title = "Gemini CLI Session"
		case string(RuntimeCodeBuddy):
			title = "CodeBuddy Session"
		case string(RuntimeQoder):
			title = "Qoder Session"
		case string(RuntimeCodex):
			title = "Codex CLI Session"
		case string(RuntimeOpenCode):
			title = "OpenCode CLI Session"
		case string(RuntimeNative):
			title = "Hoxi Native Session"
		default:
			title = "Antigravity Session"
		}
	}
	now := time.Now().UnixMilli()
	rec := SessionRecord{
		ID:             sessionID,
		ProjectID:      projectID,
		ParentID:       strings.TrimSpace(body.ParentID),
		ScheduleID:     strings.TrimSpace(body.ScheduleID),
		Surface:        surface,
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

// isPlaceholderSessionTitle reports whether title is an auto-generated default
// that may safely be replaced by an automatic prompt/engine summary.
func isPlaceholderSessionTitle(title string) bool {
	trimmed := strings.TrimSpace(title)
	switch trimmed {
	case "",
		"New Session",
		"New Thread",
		"Untitled",
		"Hoxi Native Session",
		"Claude Code Session",
		"Antigravity Session",
		"Gemini CLI Session",
		"CodeBuddy Session",
		"Qoder Session",
		"Codex CLI Session",
		"OpenCode CLI Session":
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "new session -") || strings.HasPrefix(lower, "child session -")
}
