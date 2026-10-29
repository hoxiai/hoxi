package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func extractAuthProviderID(requestPath string) string {
	trimmed := strings.TrimPrefix(requestPath, "/api")
	trimmed = strings.TrimPrefix(trimmed, "/auth/")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" || trimmed == "/auth" || strings.Contains(trimmed, "/") {
		return ""
	}
	if unescaped, err := url.PathUnescape(trimmed); err == nil {
		trimmed = strings.TrimSpace(unescaped)
	}
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

// handleProviderAuth persists PUT/DELETE /auth/<providerID> credentials to
// Hoxi's engine auth.json first and forwards best-effort to OpenCode when
// running, so saving or removing a provider key never fails with HTTP 502 when
// the OpenCode daemon is offline or still starting up.
func (g *Service) handleProviderAuth(w http.ResponseWriter, r *http.Request) {
	providerID := extractAuthProviderID(r.URL.Path)
	if providerID == "" {
		if g.proxy != nil {
			g.proxy.ServeHTTP(w, r)
			return
		}
		writeJSONError(w, http.StatusBadRequest, "providerID is required")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var cred map[string]interface{}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&cred); err != nil || len(cred) == 0 {
			writeJSONError(w, http.StatusBadRequest, "Invalid auth credential payload")
			return
		}
		if err := g.persistEngineProviderAuthMap(providerID, cred); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		g.reloadOpenCodeGlobalConfig()
		writeJSON(w, http.StatusOK, true)

	case http.MethodDelete:
		if err := g.removeEngineProviderAuth(providerID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		g.reloadOpenCodeGlobalConfig()
		writeJSON(w, http.StatusOK, true)

	default:
		if g.proxy != nil {
			g.proxy.ServeHTTP(w, r)
			return
		}
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (g *Service) persistEngineProviderAuthMap(providerID string, cred map[string]interface{}) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || len(cred) == 0 {
		return nil
	}
	g.configPersistMu.Lock()
	var persistErr error
	if dir := hoxipaths.EngineOpenCodeDir(); dir != "" {
		path := filepath.Join(dir, "auth.json")
		merged := map[string]interface{}{}
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &merged)
		}
		if merged == nil {
			merged = map[string]interface{}{}
		}
		merged[providerID] = cred
		persistErr = writeJSONMapFile(path, merged)
	}
	g.configPersistMu.Unlock()
	if persistErr != nil {
		return persistErr
	}
	if g.openCode != nil && g.openCode.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = g.openCode.JSON(ctx, http.MethodPut, "/auth/"+url.PathEscape(providerID), nil, cred, nil)
	}
	return nil
}

func (g *Service) removeEngineProviderAuth(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}
	g.configPersistMu.Lock()
	var persistErr error
	if dir := hoxipaths.EngineOpenCodeDir(); dir != "" {
		path := filepath.Join(dir, "auth.json")
		if raw, err := os.ReadFile(path); err == nil {
			var merged map[string]interface{}
			if json.Unmarshal(raw, &merged) == nil && merged != nil {
				if _, exists := merged[providerID]; exists {
					delete(merged, providerID)
					persistErr = writeJSONMapFile(path, merged)
				}
			}
		}
	}
	g.configPersistMu.Unlock()
	if persistErr != nil {
		return persistErr
	}
	if g.openCode != nil && g.openCode.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = g.openCode.JSON(ctx, http.MethodDelete, "/auth/"+url.PathEscape(providerID), nil, nil, nil)
	}
	return nil
}

// handleGlobalConfigProxy persists PATCH /global/config directly to .hoxi/models.json (never touching ~/.config/opencode)
// and overlays GET /global/config with that document, so routing roles (small_model, subagent_model,
// agent models) and Hoxi-managed provider model keys reflect the latest persisted config.
func (g *Service) handleGlobalConfigProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		raw, err := io.ReadAll(r.Body)
		if err == nil && len(raw) > 0 {
			var patch map[string]interface{}
			if json.Unmarshal(raw, &patch) == nil && patch != nil {
				g.configPersistMu.Lock()
				doc := g.loadHoxiConfigDoc()
				deepMergeMap(doc, patch)
				persistErr := g.persistHoxiConfigFiles(doc)
				g.configPersistMu.Unlock()
				if persistErr != nil {
					writeJSONError(w, http.StatusInternalServerError, persistErr.Error())
					return
				}
				g.reloadOpenCodeGlobalConfig()
			}
		}
	}

	if r.Method == http.MethodGet || r.Method == http.MethodPatch {
		if g.openCode != nil && g.openCode.Available() {
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			status, body, err := g.openCode.Raw(ctx, http.MethodGet, "/global/config", nil, nil)
			cancel()
			if err == nil {
				var cfg map[string]interface{}
				if json.Unmarshal(body, &cfg) == nil && cfg != nil {
					doc := g.loadHoxiConfigDoc()
					overlayHoxiGlobalConfig(cfg, doc)
					if provMap, ok := cfg["provider"].(map[string]interface{}); ok {
						filterProviderModelsByHoxi(provMap, g.loadHoxiProvidersMap())
					}
					writeJSON(w, status, cfg)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(body)
				return
			}
			if status != 0 {
				writeJSONError(w, http.StatusBadGateway, err.Error())
				return
			}
		}
		// Fallback when OpenCode is not running: serve the authoritative Hoxi config document directly.
		doc := g.loadHoxiConfigDoc()
		cfg := map[string]interface{}{
			"provider": g.loadHoxiProvidersMap(),
		}
		overlayHoxiGlobalConfig(cfg, doc)
		writeJSON(w, http.StatusOK, cfg)
		return
	}

	if g.proxy != nil {
		g.proxy.ServeHTTP(w, r)
		return
	}
	writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

// fetchOpenCodeForProxy GETs path from the engine for a filtering proxy
// handler. When the engine is unreachable it hands the request to the plain
// reverse proxy (which reports the outage) and returns ok=false.
func (g *Service) fetchOpenCodeForProxy(w http.ResponseWriter, r *http.Request, path string, query url.Values) (int, []byte, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	status, body, err := g.openCode.Raw(ctx, http.MethodGet, path, query, nil)
	if err != nil && status == 0 {
		g.proxy.ServeHTTP(w, r)
		return 0, nil, false
	}
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return 0, nil, false
	}
	return status, body, true
}

// handleProviderProxy filters GET /provider so any provider managed in .hoxi/models.json only exposes its .hoxi models.
func (g *Service) handleProviderProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		g.proxy.ServeHTTP(w, r)
		return
	}
	status, body, ok := g.fetchOpenCodeForProxy(w, r, r.URL.Path, r.URL.Query())
	if !ok {
		return
	}
	var payload map[string]interface{}
	if json.Unmarshal(body, &payload) == nil {
		hoxiMap := g.loadHoxiProvidersMap()
		if allArr, ok := payload["all"].([]interface{}); ok {
			for _, item := range allArr {
				if pObj, ok := item.(map[string]interface{}); ok {
					pid, _ := pObj["id"].(string)
					if pid == "opencode" || pid == "opencode-go" {
						continue
					}
					if hProv, ok := hoxiMap[pid].(map[string]interface{}); ok {
						if hModels, ok := hProv["models"].(map[string]interface{}); ok && len(hModels) > 0 {
							if pModels, ok := pObj["models"].(map[string]interface{}); ok {
								for mid := range pModels {
									if _, keep := hModels[mid]; !keep {
										delete(pModels, mid)
									}
								}
							}
						}
					}
				}
			}
		}
		writeJSON(w, status, payload)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
