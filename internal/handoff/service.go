package handoff

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/provider"
)

var localTransport = &http.Transport{Proxy: nil, IdleConnTimeout: 90 * time.Second}

type Service struct {
	openCodePort int
	password     string
	rootDir      string
	wsBaseURL    string
	providers    *provider.Service
}

func NewService(openCodePort int, password, rootDir string, providers *provider.Service) *Service {
	return &Service{
		openCodePort: openCodePort,
		password:     password,
		rootDir:      rootDir,
		providers:    providers,
	}
}

func (g *Service) SetWSBaseURL(wsURL string) {
	g.wsBaseURL = wsURL
	g.syncLocalCLIBridgeURLs()
}

func (g *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/handoff/sessions", g.handleHandoffSessions)
	mux.HandleFunc("/api/hoxi/handoff/preview", g.handleHandoffPreview)
	mux.HandleFunc("/api/hoxi/handoff/import", g.handleHandoffImport)
	mux.HandleFunc("/api/hoxi/local-cli/status", g.handleLocalCLIStatus)
	mux.HandleFunc("/api/hoxi/local-cli/import", g.handleLocalCLIImport)
	mux.HandleFunc("/api/hoxi/bridge/grok-cli/", g.handleGrokCLIBridge)
	mux.HandleFunc("/api/hoxi/bridge/gemini-antigravity/", g.handleAntigravityCLIBridge)
	mux.HandleFunc("/api/hoxi/bridge/claude-code/", g.handleClaudeCLIBridge)
}

func OpenCodeDataDBPath() string {
	return openCodeDataDBPath()
}

func (g *Service) handleHandoffSessions(w http.ResponseWriter, r *http.Request) {
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

	var (
		grokSessions, claudeSessions, codexSessions, geminiSessions          []ExternalSessionSummary
		codeBuddySessions, traeSessions, qoderSessions                       []ExternalSessionSummary
		grokStats, claudeStats, codexStats, geminiStats                      ExternalSourceStats
		codeBuddyStats, traeStats, qoderStats                                ExternalSourceStats
	)
	var scans sync.WaitGroup
	for _, scan := range []func(){
		func() { grokSessions, grokStats = scanGrokSessions(targetDir, query) },
		func() { claudeSessions, claudeStats = scanClaudeSessions(targetDir, query) },
		func() { codexSessions, codexStats = scanCodexSessions(targetDir, query) },
		func() { geminiSessions, geminiStats = scanGeminiAntigravitySessions(targetDir, query) },
		func() { codeBuddySessions, codeBuddyStats = scanCodeBuddySessions(targetDir, query) },
		func() { traeSessions, traeStats = scanTraeSessions(targetDir, query) },
		func() { qoderSessions, qoderStats = scanQoderSessions(targetDir, query) },
	} {
		scans.Add(1)
		go func(scan func()) {
			defer scans.Done()
			scan()
		}(scan)
	}
	scans.Wait()

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
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolCodeBuddy {
		combined = append(combined, codeBuddySessions...)
	}
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolTrae {
		combined = append(combined, traeSessions...)
	}
	if sourceFilter == "" || sourceFilter == "all" || sourceFilter == ExternalToolQoder {
		combined = append(combined, qoderSessions...)
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
			ExternalToolGrok:      grokStats,
			ExternalToolClaude:    claudeStats,
			ExternalToolCodex:     codexStats,
			ExternalToolGemini:    geminiStats,
			ExternalToolCodeBuddy: codeBuddyStats,
			ExternalToolTrae:      traeStats,
			ExternalToolQoder:     qoderStats,
		},
	})
}

func (g *Service) handleHandoffPreview(w http.ResponseWriter, r *http.Request) {
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

func (g *Service) handleHandoffImport(w http.ResponseWriter, r *http.Request) {
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
	case ExternalToolCodeBuddy:
		return loadCodeBuddySessionDetail(sessionID, hintCWD, activeRoot)
	case ExternalToolTrae:
		return loadTraeSessionDetail(sessionID, hintCWD, activeRoot)
	case ExternalToolQoder:
		return loadQoderSessionDetail(sessionID, hintCWD, activeRoot)
	default:
		return loadGrokSessionDetail(sessionID, hintCWD, activeRoot)
	}
}

func (g *Service) resolveStoredProviderKey(providerID, requestedBaseURL string) string {
	if g.providers == nil {
		return ""
	}
	return g.providers.ResolveStoredProviderKey(providerID, requestedBaseURL)
}

func (g *Service) loadHoxiConfigDoc() map[string]interface{} {
	if g.providers == nil {
		return map[string]interface{}{}
	}
	return g.providers.LoadHoxiConfigDoc()
}

func (g *Service) loadHoxiProvidersMap() map[string]interface{} {
	if g.providers == nil {
		return map[string]interface{}{}
	}
	return g.providers.LoadHoxiProvidersMap()
}

func (g *Service) persistHoxiConfigDoc(doc map[string]interface{}) error {
	if g.providers == nil {
		return nil
	}
	return g.providers.PersistHoxiConfigDoc(doc)
}

func (g *Service) reloadOpenCodeGlobalConfig() {
	if g.providers != nil {
		g.providers.ReloadOpenCodeGlobalConfig()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

func comparableFolderPath(p string) string {
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
