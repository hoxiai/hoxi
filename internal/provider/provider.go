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
	"time"
)

type Service struct {
	openCodePort int
	password     string
	rootDir      string
	proxy        http.Handler
	telemetry    *providerTelemetryStore
}

func NewService(openCodePort int, password, rootDir string, proxy http.Handler) *Service {
	return &Service{
		openCodePort: openCodePort,
		password:     password,
		rootDir:      rootDir,
		proxy:        proxy,
		telemetry:    newProviderTelemetryStore(rootDir),
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/provider/fetch-models", s.handleFetchProviderModels)
	mux.HandleFunc("/api/hoxi/provider/test-model", s.handleTestProviderModel)
	mux.HandleFunc("/api/hoxi/provider/audit", s.handleAuditProviderCode)
	mux.HandleFunc("/api/hoxi/provider/summarize-title", s.handleSummarizeSessionTitle)
	mux.HandleFunc("/api/hoxi/provider/generate-commit-message", s.handleGenerateCommitMessage)
	mux.HandleFunc("/api/hoxi/models-config", s.handleModelsConfig)
	mux.HandleFunc("/global/config", s.handleGlobalConfigProxy)
	mux.HandleFunc("/provider", s.handleProviderProxy)
	if s.telemetry != nil {
		s.telemetry.registerRoutes(mux)
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
	if strings.Contains(modelsURL, "://127.0.0.1") || strings.Contains(modelsURL, "://localhost") {
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

func (g *Service) resolveProviderConnection(providerID, hintBaseURL, hintNPM string) (baseURL, apiKey, npm string, headers map[string]string) {
	baseURL = strings.TrimSpace(hintBaseURL)
	npm = strings.TrimSpace(hintNPM)
	headers = map[string]string{}

	merged := g.loadOpenCodeImportedProviderMap()
	for k, v := range g.loadHoxiProvidersMap() {
		merged[k] = v
	}
	if rawCfg, ok := merged[providerID].(map[string]any); ok {
		if npm == "" {
			if s, ok := rawCfg["npm"].(string); ok {
				npm = strings.TrimSpace(s)
			}
		}
		if opts, ok := rawCfg["options"].(map[string]any); ok {
			if baseURL == "" {
				if u, ok := opts["baseURL"].(string); ok {
					baseURL = strings.TrimSpace(u)
				}
			}
			if k, ok := opts["apiKey"].(string); ok && k != "" && k != "********" {
				apiKey = strings.TrimSpace(k)
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

	if baseURL == "" {
		baseURL = defaultProviderBaseURLs[providerID]
	}
	if apiKey == "" {
		apiKey = g.resolveStoredProviderKey(providerID, baseURL)
	}
	return baseURL, apiKey, npm, headers
}

func (g *Service) resolveStoredProviderKey(providerID, requestedBaseURL string) string {
	// 1. Check OpenCode /provider endpoint first
	if g.openCodePort > 0 {
		u := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/provider"
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err == nil {
			req.SetBasicAuth("opencode", g.password)
			client := &http.Client{Timeout: 3 * time.Second, Transport: localTransport}
			if resp, err := client.Do(req); err == nil {
				defer resp.Body.Close()
				var payload struct {
					All []struct {
						ID      string         `json:"id"`
						Key     string         `json:"key"`
						Options map[string]any `json:"options"`
					} `json:"all"`
				}
				if json.NewDecoder(resp.Body).Decode(&payload) == nil {
					for _, p := range payload.All {
						if p.ID == providerID {
							if p.Key != "" {
								return p.Key
							}
							if optKey, ok := p.Options["apiKey"].(string); ok && optKey != "" {
								return optKey
							}
						}
					}
				}
			}
		}
	}

	// 2. Check .hoxi/models.json configured options
	if hProv, ok := g.loadHoxiProvidersMap()[providerID].(map[string]interface{}); ok {
		if opts, ok := hProv["options"].(map[string]interface{}); ok {
			if optKey, ok := opts["apiKey"].(string); ok && strings.TrimSpace(optKey) != "" {
				return strings.TrimSpace(optKey)
			}
		}
	}

	// 3. Fallback to ~/.local/share/opencode/auth.json (supports both API key and OAuth access token)
	if home, err := os.UserHomeDir(); err == nil {
		authPath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
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
	return ""
}
