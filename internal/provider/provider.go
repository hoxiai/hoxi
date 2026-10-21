package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

type Service struct {
	openCode          *ocapi.Client
	rootDir           string
	proxy             http.Handler
	stats             *modelStatsStore
	assistMu          sync.RWMutex
	assist            map[string]http.HandlerFunc
	isBusyFn          func() bool
	reloadMu          sync.Mutex
	reloadPending     bool
	reloadLoopRunning bool
	relayMu           sync.Mutex
	relayBaseURL      string
}

func NewService(openCodePort int, password, rootDir string, proxy http.Handler) *Service {
	return &Service{
		openCode: ocapi.NewForPort(openCodePort, password),
		rootDir:  rootDir,
		proxy:    proxy,
		stats:    newModelStatsStore(rootDir),
	}
}

// SetOnProviderDegraded wires persistent internal LLM failure notifications
// (e.g. to the macOS status bar Attention badge).
func (s *Service) SetOnProviderDegraded(fn func(providerID string, degraded bool)) {
	if s.stats != nil {
		s.stats.setOnDegradedChange(fn)
	}
}

// SetBusyCheck wires a predicate that reports whether any session or runtime
// process is actively executing, so global OpenCode config reloads (`POST /global/dispose`)
// are deferred until active turns complete instead of killing in-flight tasks.
func (s *Service) SetBusyCheck(fn func() bool) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.isBusyFn = fn
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/provider/fetch-models", s.handleFetchProviderModels)
	mux.HandleFunc("/api/hoxi/provider/test-model", s.handleTestProviderModel)
	for _, action := range assistActions {
		mux.HandleFunc("/api/hoxi/provider/"+action, s.serveAssist(action))
	}
	mux.HandleFunc("/api/hoxi/provider/auto-pools", s.handleAutoPoolsConfig)
	mux.HandleFunc("/api/hoxi/provider/relay/keys", s.handleRelayAdmin)
	mux.HandleFunc("/api/hoxi/provider/models", s.handleModelCatalog)
	mux.HandleFunc("/api/hoxi/models-config", s.handleModelsConfig)
	mux.HandleFunc("/global/config", s.handleGlobalConfigProxy)
	mux.HandleFunc("/global/dispose", s.handleGlobalDispose)
	mux.HandleFunc("/provider", s.handleProviderProxy)
	if s.stats != nil {
		s.stats.registerRoutes(mux)
	}
}

func EnsureHoxiModelsConfig(rootDir string) string {
	return ensureHoxiModelsConfig(rootDir)
}

func UserHoxiModelsPath() string {
	return userHoxiModelsPath()
}

func (s *Service) WorkspaceHoxiModelsPath() string {
	return s.workspaceHoxiModelsPath()
}

func (s *Service) ResolveStoredProviderKey(providerID, requestedBaseURL string) string {
	return s.resolveStoredProviderKey(providerID, requestedBaseURL)
}

func (s *Service) LoadHoxiConfigDoc() map[string]interface{} {
	return s.loadHoxiConfigDoc()
}

func (s *Service) LoadHoxiProvidersMap() map[string]interface{} {
	return s.loadHoxiProvidersMap()
}

func (s *Service) PersistHoxiConfigDoc(doc map[string]interface{}) error {
	return s.persistHoxiConfigDoc(doc)
}

func (s *Service) ReloadOpenCodeGlobalConfig() {
	s.reloadOpenCodeGlobalConfig()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

type fetchProviderModelsRequest struct {
	BaseURL    string            `json:"baseURL"`
	APIKey     string            `json:"apiKey,omitempty"`
	ProviderID string            `json:"providerID,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type fetchedModelItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fetchProviderModelsResponse struct {
	Models []fetchedModelItem `json:"models,omitempty"`
	Error  string             `json:"error,omitempty"`
	Auth   bool               `json:"auth,omitempty"`
}

func (g *Service) handleFetchProviderModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req fetchProviderModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: "Invalid request payload"})
		return
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: "Base URL must start with http:// or https://"})
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "********" {
		apiKey = ""
	}
	// Resolve {env:VAR_NAME} syntax if used
	if strings.HasPrefix(apiKey, "{env:") && strings.HasSuffix(apiKey, "}") {
		envName := strings.TrimSpace(apiKey[5 : len(apiKey)-1])
		apiKey = os.Getenv(envName)
	}
	// If editing an existing provider with untouched key, look up stored key
	if apiKey == "" && strings.TrimSpace(req.ProviderID) != "" {
		apiKey = g.resolveStoredProviderKey(strings.TrimSpace(req.ProviderID), baseURL)
	}

	modelsURL := strings.TrimRight(baseURL, "/") + "/models"
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: err.Error()})
		return
	}

	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", "application/json")
	for k, v := range req.Headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			outReq.Header.Set(k, v)
		}
	}
	if apiKey != "" {
		outReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: providerTransport,
	}

	// For loopback addresses, bypass proxy to avoid 502 from local proxy tools
	if isLoopbackURL(modelsURL) {
		client.Transport = localTransport
	}

	resp, err := client.Do(outReq)
	if err != nil {
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(bodyBytes))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		isAuth := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		msg := "HTTP " + strconv.Itoa(resp.StatusCode)
		if snippet != "" {
			msg += ": " + snippet
		}
		writeJSON(w, http.StatusOK, fetchProviderModelsResponse{
			Error: msg,
			Auth:  isAuth,
		})
		return
	}

	models := parseOpenAIModelsPayload(bodyBytes)
	writeJSON(w, http.StatusOK, fetchProviderModelsResponse{Models: models})
}

func parseOpenAIModelsPayload(raw []byte) []fetchedModelItem {
	type rawModel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var wrapper struct {
		Data   []rawModel `json:"data"`
		Models []rawModel `json:"models"`
	}

	var list []rawModel
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if len(wrapper.Data) > 0 {
			list = wrapper.Data
		} else if len(wrapper.Models) > 0 {
			list = wrapper.Models
		}
	}
	if len(list) == 0 {
		_ = json.Unmarshal(raw, &list)
	}

	seen := make(map[string]struct{}, len(list))
	result := make([]fetchedModelItem, 0, len(list))
	for _, item := range list {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = id
		}
		result = append(result, fetchedModelItem{ID: id, Name: name})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func sameProviderBaseURL(a, b string) bool {
	na := strings.TrimRight(strings.TrimSpace(a), "/")
	nb := strings.TrimRight(strings.TrimSpace(b), "/")
	return na != "" && nb != "" && strings.EqualFold(na, nb)
}

func (g *Service) isAuthorizedProviderBaseURL(providerID, requestedBaseURL string, extraAllowed ...string) bool {
	reqURL := strings.TrimSpace(requestedBaseURL)
	if reqURL == "" {
		return true
	}
	hasConfiguredURL := false
	if defURL := strings.TrimSpace(defaultProviderBaseURLs[providerID]); defURL != "" {
		hasConfiguredURL = true
		if sameProviderBaseURL(reqURL, defURL) {
			return true
		}
	}
	for _, extra := range extraAllowed {
		if strings.TrimSpace(extra) != "" {
			hasConfiguredURL = true
			if sameProviderBaseURL(reqURL, extra) {
				return true
			}
		}
	}
	for _, src := range []map[string]any{g.loadHoxiProvidersMap(), g.loadOpenCodeImportedProviderMap()} {
		if rawCfg, ok := src[providerID].(map[string]any); ok {
			if opts, ok := rawCfg["options"].(map[string]any); ok {
				if u, ok := opts["baseURL"].(string); ok && strings.TrimSpace(u) != "" {
					hasConfiguredURL = true
					if sameProviderBaseURL(reqURL, u) {
						return true
					}
				}
			}
		}
	}
	return !hasConfiguredURL && isLoopbackURL(reqURL)
}

func (g *Service) resolveProviderConnection(providerID, hintBaseURL, hintNPM string) (baseURL, apiKey, npm string, headers map[string]string) {
	baseURL = strings.TrimSpace(hintBaseURL)
	npm = strings.TrimSpace(hintNPM)
	headers = map[string]string{}

	merged := g.loadOpenCodeImportedProviderMap()
	for k, v := range g.loadHoxiProvidersMap() {
		merged[k] = v
	}
	var configuredBaseURL string
	if rawCfg, ok := merged[providerID].(map[string]any); ok {
		if npm == "" {
			if s, ok := rawCfg["npm"].(string); ok {
				npm = strings.TrimSpace(s)
			}
		}
		if opts, ok := rawCfg["options"].(map[string]any); ok {
			if u, ok := opts["baseURL"].(string); ok {
				configuredBaseURL = strings.TrimSpace(u)
			}
			if baseURL == "" {
				baseURL = configuredBaseURL
			}
			if k, ok := opts["apiKey"].(string); ok && k != "" && k != "********" {
				if g.isAuthorizedProviderBaseURL(providerID, baseURL, configuredBaseURL) {
					apiKey = strings.TrimSpace(k)
				}
			}
			if hMap, ok := opts["headers"].(map[string]any); ok {
				for hk, hv := range hMap {
					if vs, ok := hv.(string); ok {
						headers[hk] = vs
					}
				}
			}
		}
	}

	liveURL, liveKey, liveNPM, liveHeaders := g.resolveOpenCodeLiveModelConfig(providerID, "")
	if baseURL == "" && liveURL != "" {
		baseURL = liveURL
	}
	if baseURL == "" {
		baseURL = defaultProviderBaseURLs[providerID]
	}
	if npm == "" && liveNPM != "" {
		npm = liveNPM
	}
	if g.isAuthorizedProviderBaseURL(providerID, baseURL, configuredBaseURL, liveURL) {
		for hk, hv := range liveHeaders {
			if _, exists := headers[hk]; !exists {
				headers[hk] = hv
			}
		}
		if apiKey == "" && liveKey != "" {
			apiKey = liveKey
		}
	}
	if apiKey == "" {
		apiKey = g.resolveStoredProviderKey(providerID, baseURL)
	}
	if apiKey != "" {
		apiKey = g.selectRotatedProviderKey(providerID, apiKey)
	}
	return baseURL, apiKey, npm, headers
}

func (g *Service) resolveOpenCodeLiveModelConfig(providerID, modelID string) (baseURL, apiKey, npm string, headers map[string]string) {
	if !g.openCode.Available() || strings.TrimSpace(providerID) == "" {
		return "", "", "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var payload struct {
		All []struct {
			ID      string         `json:"id"`
			Key     string         `json:"key"`
			Options map[string]any `json:"options"`
			Models  map[string]struct {
				API struct {
					URL string `json:"url"`
					NPM string `json:"npm"`
				} `json:"api"`
				Headers map[string]any `json:"headers"`
			} `json:"models"`
		} `json:"all"`
	}
	if g.openCode.JSON(ctx, http.MethodGet, "/provider", nil, nil, &payload) != nil {
		return "", "", "", nil
	}
	headers = map[string]string{}
	for _, p := range payload.All {
		if p.ID != providerID {
			continue
		}
		if optURL, ok := p.Options["baseURL"].(string); ok {
			baseURL = strings.TrimSpace(optURL)
		}
		if p.Key != "" {
			apiKey = strings.TrimSpace(p.Key)
		} else if optKey, ok := p.Options["apiKey"].(string); ok && optKey != "" {
			apiKey = strings.TrimSpace(optKey)
		}
		if hMap, ok := p.Options["headers"].(map[string]any); ok {
			for hk, hv := range hMap {
				if vs, ok := hv.(string); ok && strings.TrimSpace(hk) != "" && strings.TrimSpace(vs) != "" {
					headers[strings.TrimSpace(hk)] = strings.TrimSpace(vs)
				}
			}
		}
		if modelID != "" {
			if m, ok := p.Models[modelID]; ok {
				if strings.TrimSpace(m.API.URL) != "" {
					baseURL = strings.TrimSpace(m.API.URL)
				}
				if strings.TrimSpace(m.API.NPM) != "" {
					npm = strings.TrimSpace(m.API.NPM)
				}
				for hk, hv := range m.Headers {
					if vs, ok := hv.(string); ok && strings.TrimSpace(hk) != "" && strings.TrimSpace(vs) != "" {
						headers[strings.TrimSpace(hk)] = strings.TrimSpace(vs)
					}
				}
			}
		}
		break
	}
	return baseURL, apiKey, npm, headers
}

func (g *Service) resolveStoredProviderKey(providerID, requestedBaseURL string) string {
	openCodeBaseURL, openCodeKey, _, _ := g.resolveOpenCodeLiveModelConfig(providerID, "")

	if !g.isAuthorizedProviderBaseURL(providerID, requestedBaseURL, openCodeBaseURL) {
		return ""
	}
	if openCodeKey != "" {
		return openCodeKey
	}

	// 2. Check .hoxi/models.json configured options
	if hProv, ok := g.loadHoxiProvidersMap()[providerID].(map[string]interface{}); ok {
		if opts, ok := hProv["options"].(map[string]interface{}); ok {
			if optKey, ok := opts["apiKey"].(string); ok && strings.TrimSpace(optKey) != "" {
				return strings.TrimSpace(optKey)
			}
		}
	}

	// 3. Fallback to Hoxi engine auth.json or ~/.local/share/opencode/auth.json (supports both API key and OAuth access token)
	if home, err := os.UserHomeDir(); err == nil {
		for _, authPath := range []string{
			hoxipaths.EngineAuthPath(),
			filepath.Join(home, ".local", "share", "opencode", "auth.json"),
		} {
			if data, err := os.ReadFile(authPath); err == nil {
				var authMap map[string]struct {
					Key    string `json:"key"`
					Access string `json:"access"`
				}
				if json.Unmarshal(data, &authMap) == nil {
					if entry, ok := authMap[providerID]; ok {
						if strings.TrimSpace(entry.Key) != "" {
							return strings.TrimSpace(entry.Key)
						}
						if strings.TrimSpace(entry.Access) != "" {
							return strings.TrimSpace(entry.Access)
						}
					}
				}
			}
		}
	}
	return ""
}
