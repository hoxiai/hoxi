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

const handoffPreviewCacheTTL = 10 * time.Second

type cachedHandoffPreview struct {
	detail    *ExternalSessionDetail
	expiresAt time.Time
}

type handoffScanResult struct {
	sessions map[ExternalToolID][]ExternalSessionSummary
	stats    map[ExternalToolID]ExternalSourceStats
}

type handoffScanCall struct {
	wg  sync.WaitGroup
	res handoffScanResult
}

type Service struct {
	openCodePort int
	password     string
	rootDir      string
	wsBaseURL    string
	providers    *provider.Service
	cacheMu      sync.RWMutex
	inFlightScan map[string]*handoffScanCall
	previewCache map[string]cachedHandoffPreview
}

func NewService(openCodePort int, password, rootDir string, providers *provider.Service) *Service {
	return &Service{
		openCodePort: openCodePort,
		password:     password,
		rootDir:      rootDir,
		providers:    providers,
		inFlightScan: make(map[string]*handoffScanCall),
		previewCache: make(map[string]cachedHandoffPreview),
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

var allExternalSources = []ExternalToolID{
	ExternalToolGrok,
	ExternalToolClaude,
	ExternalToolCodex,
	ExternalToolGemini,
	ExternalToolCodeBuddy,
	ExternalToolTrae,
	ExternalToolQoder,
	ExternalToolOpenCode,
}

func runSourceScanner(source ExternalToolID, targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	switch source {
	case ExternalToolGrok:
		return scanGrokSessions(targetDir, query)
	case ExternalToolClaude:
		return scanClaudeSessions(targetDir, query)
	case ExternalToolCodex:
		return scanCodexSessions(targetDir, query)
	case ExternalToolGemini:
		return scanGeminiAntigravitySessions(targetDir, query)
	case ExternalToolCodeBuddy:
		return scanCodeBuddySessions(targetDir, query)
	case ExternalToolTrae:
		return scanTraeSessions(targetDir, query)
	case ExternalToolQoder:
		return scanQoderSessions(targetDir, query)
	case ExternalToolOpenCode:
		return scanOpenCodeSessions(targetDir, query)
	default:
		return nil, ExternalSourceStats{}
	}
}

func (g *Service) scanAllSourcesCoalesced(targetDir, query string) handoffScanResult {
	flightKey := comparableFolderPath(targetDir) + "\x00" + strings.ToLower(strings.TrimSpace(query))
	g.cacheMu.Lock()
	if call, ok := g.inFlightScan[flightKey]; ok {
		g.cacheMu.Unlock()
		call.wg.Wait()
		return call.res
	}
	call := &handoffScanCall{}
	call.wg.Add(1)
	g.inFlightScan[flightKey] = call
	g.cacheMu.Unlock()

	sessionsBySource := make(map[ExternalToolID][]ExternalSessionSummary, len(allExternalSources))
	statsBySource := make(map[ExternalToolID]ExternalSourceStats, len(allExternalSources))
	var resMu sync.Mutex
	var scans sync.WaitGroup
	for _, src := range allExternalSources {
		scans.Add(1)
		go func(source ExternalToolID) {
			defer scans.Done()
			list, st := runSourceScanner(source, targetDir, query)
			resMu.Lock()
			sessionsBySource[source] = list
			statsBySource[source] = st
			resMu.Unlock()
		}(src)
	}
	scans.Wait()

	call.res = handoffScanResult{
		sessions: sessionsBySource,
		stats:    statsBySource,
	}
	call.wg.Done()

	g.cacheMu.Lock()
	delete(g.inFlightScan, flightKey)
	g.cacheMu.Unlock()
	return call.res
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

	scanRes := g.scanAllSourcesCoalesced(targetDir, query)
	specificSource := sourceFilter != "" && sourceFilter != "all"

	var combined []ExternalSessionSummary
	for _, src := range allExternalSources {
		if !specificSource || src == sourceFilter {
			combined = append(combined, scanRes.sessions[src]...)
		}
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
		Sources:  scanRes.stats,
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

	cacheKey := string(source) + "\x00" + sessionID + "\x00" + comparableFolderPath(hintCWD) + "\x00" + comparableFolderPath(g.rootDir)
	now := time.Now()
	g.cacheMu.RLock()
	cached, ok := g.previewCache[cacheKey]
	g.cacheMu.RUnlock()
	if ok && cached.detail != nil && now.Before(cached.expiresAt) {
		writeJSON(w, http.StatusOK, cached.detail)
		return
	}

	detail, err := loadExternalSessionDetail(source, sessionID, hintCWD, g.rootDir)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	g.cacheMu.Lock()
	if len(g.previewCache) >= 32 {
		for k, v := range g.previewCache {
			if now.After(v.expiresAt) {
				delete(g.previewCache, k)
			}
		}
	}
	g.previewCache[cacheKey] = cachedHandoffPreview{
		detail:    detail,
		expiresAt: now.Add(handoffPreviewCacheTTL),
	}
	g.cacheMu.Unlock()

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
	case ExternalToolOpenCode:
		return loadOpenCodeSessionDetail(sessionID, hintCWD, activeRoot)
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
