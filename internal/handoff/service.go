package handoff

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/provider"
)

type Service struct {
	openCodePort int
	password     string
	rootDir      string
	wsBaseURL    string
	providers    *provider.Service
}

type desktopGateway = Service

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
