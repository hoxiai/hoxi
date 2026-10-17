package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ExternalToolID string

const (
	ExternalToolGrok   ExternalToolID = "grok-cli"
	ExternalToolClaude ExternalToolID = "claude-code"
	ExternalToolCodex  ExternalToolID = "codex-cli"
	ExternalToolGemini ExternalToolID = "gemini-antigravity"
)

type ExternalSessionSummary struct {
	ID          string         `json:"id"`
	Source      ExternalToolID `json:"source"`
	SourceLabel string         `json:"sourceLabel"`
	CWD         string         `json:"cwd"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary,omitempty"`
	Model       string         `json:"model,omitempty"`
	GitBranch   string         `json:"gitBranch,omitempty"`
	GitCommit   string         `json:"gitCommit,omitempty"`
	CreatedAt   int64          `json:"createdAt"`
	UpdatedAt   int64          `json:"updatedAt"`
	TurnCount   int            `json:"turnCount,omitempty"`
	MatchesCWD  bool           `json:"matchesCwd"`
}

type NormalizedToolCall struct {
	CallID string                 `json:"callID"`
	Tool   string                 `json:"tool"`
	Title  string                 `json:"title"`
	Input  map[string]interface{} `json:"input"`
	Output string                 `json:"output"`
	Status string                 `json:"status"`
}

type NormalizedTurn struct {
	Role      string               `json:"role"`
	Timestamp int64                `json:"timestamp"`
	Text      string               `json:"text"`
	Reasoning string               `json:"reasoning,omitempty"`
	Tools     []NormalizedToolCall `json:"tools,omitempty"`
	ModelID   string               `json:"modelID,omitempty"`
}

type ExternalPlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type ExternalSessionDetail struct {
	Summary         ExternalSessionSummary `json:"summary"`
	Turns           []NormalizedTurn       `json:"turns"`
	PlanSteps       []ExternalPlanStep     `json:"planSteps,omitempty"`
	ChangedFiles    []string               `json:"changedFiles,omitempty"`
	LastTurnSummary string                 `json:"lastTurnSummary,omitempty"`
	LastRecap       string                 `json:"lastRecap,omitempty"`
}

type ExternalSourceStats struct {
	Installed    bool `json:"installed"`
	SessionCount int  `json:"sessionCount"`
}

type HandoffSessionsResponse struct {
	Sessions []ExternalSessionSummary               `json:"sessions"`
	Sources  map[ExternalToolID]ExternalSourceStats `json:"sources"`
}

type HandoffImportRequest struct {
	Source              ExternalToolID `json:"source"`
	SessionID           string         `json:"sessionID"`
	SourceCWD           string         `json:"sourceCwd,omitempty"`
	TargetDirectory     string         `json:"targetDirectory,omitempty"`
	PreferredProviderID string         `json:"preferredProviderID,omitempty"`
	PreferredModelID    string         `json:"preferredModelID,omitempty"`
}

type HandoffImportResponse struct {
	OK                 bool                   `json:"ok"`
	Session            map[string]interface{} `json:"session,omitempty"`
	ImportedTurns      int                    `json:"importedTurns"`
	MemoryPath         string                 `json:"memoryPath,omitempty"`
	TargetDirectory    string                 `json:"targetDirectory,omitempty"`
	ResolvedProviderID string                 `json:"resolvedProviderID,omitempty"`
	ResolvedModelID    string                 `json:"resolvedModelID,omitempty"`
	Error              string                 `json:"error,omitempty"`
}

type LocalCLIStatusItem struct {
	ID            ExternalToolID `json:"id"`
	Name          string         `json:"name"`
	Installed     bool           `json:"installed"`
	Authenticated bool           `json:"authenticated"`
	Connected     bool           `json:"connected"`
	Version       string         `json:"version,omitempty"`
	DefaultModel  string         `json:"defaultModel,omitempty"`
	Models        []string       `json:"models,omitempty"`
	SessionCount  int            `json:"sessionCount"`
	AuthMode      string         `json:"authMode,omitempty"`
	ProviderID    string         `json:"providerID"`
}

type LocalCLIStatusResponse struct {
	Tools []LocalCLIStatusItem `json:"tools"`
}

type LocalCLIImportRequest struct {
	ToolID ExternalToolID `json:"toolID"`
}

type LocalCLIImportResponse struct {
	OK         bool     `json:"ok"`
	ToolID     string   `json:"toolID"`
	ProviderID string   `json:"providerID"`
	Models     []string `json:"models,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (g *desktopGateway) httpBridgeBaseURL() string {
	ws := strings.TrimSpace(g.wsBaseURL)
	if strings.HasPrefix(ws, "ws://") {
		return "http://" + strings.TrimPrefix(ws, "ws://")
	}
	if strings.HasPrefix(ws, "wss://") {
		return "https://" + strings.TrimPrefix(ws, "wss://")
	}
	return "http://127.0.0.1:5088"
}

func normalizeExternalToolName(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "read", "read_file", "view_file", "cat", "view":
		return "read"
	case "edit", "multiedit", "replace_file_content", "apply_patch", "patch", "str_replace_editor":
		return "edit"
	case "write", "write_file", "create_file", "write_to_file":
		return "write"
	case "bash", "exec_command", "shell", "run_command", "terminal", "command":
		return "bash"
	case "grep", "search", "ripgrep", "rg", "grep_search":
		return "grep"
	case "glob", "list_dir", "ls", "find", "find_by_name", "file_search":
		return "glob"
	default:
		if lower == "" {
			return "bash"
		}
		return lower
	}
}

func cleanDirPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func sameOrSubPath(candidate, target string) bool {
	c := comparableFolderPath(candidate)
	t := comparableFolderPath(target)
	if c == "" || t == "" || c == "." || t == "." {
		return false
	}
	if c == t {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(c, t+sep) || strings.HasPrefix(t, c+sep)
}

func extractChangedFilesFromTurns(turns []NormalizedTurn) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, turn := range turns {
		for _, tc := range turn.Tools {
			if tc.Tool != "edit" && tc.Tool != "write" {
				continue
			}
			for _, key := range []string{"filePath", "file_path", "path", "target_file", "file"} {
				if val, ok := tc.Input[key].(string); ok && strings.TrimSpace(val) != "" {
					p := strings.TrimSpace(val)
					if _, exists := seen[p]; !exists {
						seen[p] = struct{}{}
						out = append(out, p)
					}
				}
			}
		}
	}
	return out
}

func (g *desktopGateway) handleHandoffSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	targetDir := strings.TrimSpace(r.URL.Query().Get("directory"))
	if targetDir == "" {
		targetDir = g.rootDir
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	sourceFilter := ExternalToolID(strings.TrimSpace(r.URL.Query().Get("source")))
	onlyCurrent := r.URL.Query().Get("onlyCurrent") == "1" || r.URL.Query().Get("onlyCurrent") == "true"
	limit := 80
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 300 {
		limit = l
	}

	grokSessions, grokStats := scanGrokSessions(targetDir, query)
	claudeSessions, claudeStats := scanClaudeSessions(targetDir, query)
	codexSessions, codexStats := scanCodexSessions(targetDir, query)
	geminiSessions, geminiStats := scanGeminiAntigravitySessions(targetDir, query)

	var combined []ExternalSessionSummary
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolGrok {
		combined = append(combined, grokSessions...)
	}
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolClaude {
		combined = append(combined, claudeSessions...)
	}
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolCodex {
		combined = append(combined, codexSessions...)
	}
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolGemini {
		combined = append(combined, geminiSessions...)
	}

	if onlyCurrent && targetDir != "" {
		filtered := make([]ExternalSessionSummary, 0, len(combined))
		for _, item := range combined {
			if item.MatchesCWD {
				filtered = append(filtered, item)
			}
		}
		combined = filtered
	}

	sort.Slice(combined, func(i, j int) bool {
		if combined[i].MatchesCWD != combined[j].MatchesCWD {
			return combined[i].MatchesCWD
		}
		return combined[i].UpdatedAt > combined[j].UpdatedAt
	})

	if len(combined) > limit {
		combined = combined[:limit]
	}

	writeJSON(w, http.StatusOK, HandoffSessionsResponse{
		Sessions: combined,
		Sources: map[ExternalToolID]ExternalSourceStats{
			ExternalToolGrok:   grokStats,
			ExternalToolClaude: claudeStats,
			ExternalToolCodex:  codexStats,
			ExternalToolGemini: geminiStats,
		},
	})
}

func (g *desktopGateway) handleHandoffPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	source := ExternalToolID(strings.TrimSpace(r.URL.Query().Get("source")))
	sessionID := strings.TrimSpace(r.URL.Query().Get("id"))
	hintCWD := strings.TrimSpace(r.URL.Query().Get("cwd"))
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}

	detail, err := loadExternalSessionDetail(source, sessionID, hintCWD, g.rootDir)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (g *desktopGateway) handleHandoffImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req HandoffImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, HandoffImportResponse{Error: "Invalid JSON payload"})
		return
	}
	resp, err := g.materializeExternalSession(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusOK, HandoffImportResponse{
			OK:    false,
			Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (g *desktopGateway) handleLocalCLIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	hoxiMap := g.loadHoxiProvidersMap()
	tools := []LocalCLIStatusItem{
		detectGrokCLIStatus(hoxiMap),
		detectClaudeCLIStatus(hoxiMap),
		detectCodexCLIStatus(g, hoxiMap),
		detectGeminiCLIStatus(g, hoxiMap),
	}
	writeJSON(w, http.StatusOK, LocalCLIStatusResponse{Tools: tools})
}

func (g *desktopGateway) handleLocalCLIImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req LocalCLIImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, LocalCLIImportResponse{Error: "Invalid JSON payload"})
		return
	}
	var resp LocalCLIImportResponse
	switch req.ToolID {
	case ExternalToolGrok:
		resp = g.importGrokCLIProvider()
	case ExternalToolClaude:
		resp = g.importClaudeCLIProvider()
	case ExternalToolCodex:
		resp = g.importCodexCLIProvider()
	case ExternalToolGemini:
		resp = g.importGeminiCLIProvider()
	default:
		resp = LocalCLIImportResponse{Error: "Unsupported toolID: " + string(req.ToolID)}
	}
	writeJSON(w, http.StatusOK, resp)
}

func loadExternalSessionDetail(source ExternalToolID, sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	switch source {
	case ExternalToolGrok:
		return loadGrokSessionDetail(sessionID, hintCWD, activeRoot)
	case ExternalToolClaude:
		return loadClaudeSessionDetail(sessionID, hintCWD, activeRoot)
	case ExternalToolCodex:
		return loadCodexSessionDetail(sessionID, hintCWD, activeRoot)
	case ExternalToolGemini:
		return loadGeminiAntigravitySessionDetail(sessionID, hintCWD, activeRoot)
	default:
		return loadGrokSessionDetail(sessionID, hintCWD, activeRoot)
	}
}

func readClaudeSettingsEnv() (baseURL, apiKey, defaultModel string, models []string) {
	cDir := claudeHomeDir()
	if cDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cDir, "settings.json")); err == nil {
			var doc map[string]interface{}
			if json.Unmarshal(raw, &doc) == nil {
				defaultModel = strings.TrimSpace(asString(doc["model"]))
				if defaultModel != "" {
					models = appendUniqueString(models, defaultModel)
				}
				if envMap, ok := doc["env"].(map[string]interface{}); ok {
					baseURL = asString(envMap["ANTHROPIC_BASE_URL"])
					apiKey = firstNonEmpty(asString(envMap["ANTHROPIC_AUTH_TOKEN"]), asString(envMap["ANTHROPIC_API_KEY"]))
					for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
						if m := strings.TrimSpace(asString(envMap[k])); m != "" {
							models = appendUniqueString(models, m)
							if defaultModel == "" {
								defaultModel = m
							}
						}
					}
				}
			}
		}
	}
	sessions, _ := scanClaudeSessions("", "")
	for _, s := range sessions {
		if m := strings.TrimSpace(s.Model); m != "" && !strings.HasPrefix(m, "<") {
			models = appendUniqueString(models, m)
			if defaultModel == "" {
				defaultModel = m
			}
		}
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return baseURL, apiKey, defaultModel, models
}

func detectClaudeCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	_, stats := scanClaudeSessions("", "")
	_, apiKey, defaultModel, models := readClaudeSettingsEnv()
	_, connected := hoxiMap["claude-code"]
	return LocalCLIStatusItem{
		ID:            ExternalToolClaude,
		Name:          "Claude Code",
		Installed:     stats.Installed,
		Authenticated: apiKey != "" || stats.SessionCount > 0,
		Connected:     connected,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "claude-settings",
		ProviderID:    "claude-code",
	}
}

func (g *desktopGateway) importClaudeCLIProvider() LocalCLIImportResponse {
	baseURL, apiKey, _, models := readClaudeSettingsEnv()
	if apiKey == "" {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolClaude),
			Error:  "No ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY found in ~/.claude/settings.json",
		}
	}
	modelsMap := map[string]interface{}{}
	for _, m := range models {
		modelsMap[m] = map[string]interface{}{"name": m + " (Claude Code)", "tool_call": true, "reasoning": true}
	}
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		provMap = map[string]interface{}{}
	}
	provMap["claude-code"] = map[string]interface{}{
		"name":    "Claude Code (Local)",
		"npm":     "@ai-sdk/anthropic",
		"options": map[string]interface{}{"baseURL": baseURL, "apiKey": apiKey},
		"models":  modelsMap,
	}
	doc["provider"] = provMap
	if err := g.persistHoxiConfigDoc(doc); err != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolClaude), Error: err.Error()}
	}
	return LocalCLIImportResponse{OK: true, ToolID: string(ExternalToolClaude), ProviderID: "claude-code", Models: models}
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
