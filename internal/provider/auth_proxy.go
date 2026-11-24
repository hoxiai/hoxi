package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func extractAuthProviderID(requestPath string) string {
	var trimmed string
	switch {
	case strings.HasPrefix(requestPath, "/api/hoxi/provider/credentials/"):
		trimmed = strings.TrimPrefix(requestPath, "/api/hoxi/provider/credentials/")
	case requestPath == "/api/hoxi/provider/credentials":
		return ""
	default:
		trimmed = strings.TrimPrefix(requestPath, "/api")
		trimmed = strings.TrimPrefix(trimmed, "/auth/")
	}
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
		if g.proxy != nil && !strings.HasPrefix(r.URL.Path, "/api/hoxi/") {
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
		if g.proxy != nil && !strings.HasPrefix(r.URL.Path, "/api/hoxi/") {
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
	if persistErr == nil && isValidUnrevokedAuthEntry(cred) {
		doc := g.loadHoxiConfigDoc()
		if g.clearProviderSuppressionLocked(doc, providerID) {
			_ = g.persistHoxiConfigFiles(doc)
		}
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
			if err := json.Unmarshal(raw, &merged); err != nil {
				persistErr = fmt.Errorf("read engine credentials: %w", err)
			} else if merged != nil {
				if _, exists := merged[providerID]; exists {
					delete(merged, providerID)
					persistErr = writeJSONMapFile(path, merged)
				}
			}
		} else if !os.IsNotExist(err) {
			persistErr = err
		}
	}
	g.configPersistMu.Unlock()
	var engineErr error
	if g.openCode != nil && g.openCode.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		engineErr = g.openCode.JSON(ctx, http.MethodDelete, "/auth/"+url.PathEscape(providerID), nil, nil, nil)
	}
	if providerID == officialProviderID {
		return errors.Join(persistErr, engineErr)
	}
	return persistErr // Ordinary local provider edits retain offline support.
}

// handleGlobalConfigProxy persists PATCH /global/config directly to .hoxi/models.json (never touching ~/.config/opencode)
// and overlays GET /global/config with that document, so routing roles (small_model, subagent_model,
// agent models) and Hoxi-managed provider model keys reflect the latest persisted config.
func (g *Service) handleGlobalConfigProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.TrimSpace(r.URL.Query().Get("directory")) != "" {
		doc, err := g.LoadProjectConfigDoc(r.URL.Query().Get("directory"))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if g.officialBlocked.Load() {
			if providers, ok := doc["provider"].(map[string]any); ok {
				delete(providers, officialProviderID)
			}
		}
		writeJSON(w, http.StatusOK, doc)
		return
	}
	if r.Method == http.MethodPatch {
		const maxGlobalConfigPatchBytes = 4 << 20
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxGlobalConfigPatchBytes+1))
		if err == nil && int64(len(raw)) > maxGlobalConfigPatchBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "config patch exceeds maximum size")
			return
		}
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
						hoxiMap := g.loadHoxiProvidersMap()
						filterProviderModelsByHoxi(provMap, hoxiMap)
						if dismissed := g.dismissedExternalProvidersSet(); len(dismissed) > 0 {
							for pid := range provMap {
								if (dismissed[pid] || dismissed[strings.ToLower(pid)]) && hoxiMap[pid] == nil && !hasHoxiEngineAuthEntry(pid) {
									delete(provMap, pid)
								}
							}
						}
						if g.officialBlocked.Load() {
							delete(provMap, officialProviderID)
						}
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

	if g.proxy != nil && !strings.HasPrefix(r.URL.Path, "/api/hoxi/") {
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

// handleProviderProxy serves GET /api/hoxi/provider/catalog (and legacy GET /provider)
// from OpenCode when running and synthesizes the full Hoxi provider catalog offline
// when OpenCode is absent.
func (g *Service) handleProviderProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if g.proxy != nil && g.openCode != nil && g.openCode.Available() && !strings.HasPrefix(r.URL.Path, "/api/hoxi/") {
			g.proxy.ServeHTTP(w, r)
			return
		}
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if g.openCode != nil && g.openCode.Available() {
		upstreamPath := r.URL.Path
		if strings.HasPrefix(upstreamPath, "/api/hoxi/") {
			upstreamPath = "/provider"
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		status, body, err := g.openCode.Raw(ctx, http.MethodGet, upstreamPath, r.URL.Query(), nil)
		cancel()
		if err == nil && status >= 200 && status < 300 {
			var payload map[string]interface{}
			if json.Unmarshal(body, &payload) == nil {
				g.filterProviderProxyPayload(payload)
				writeJSON(w, status, payload)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
	}
	writeJSON(w, http.StatusOK, g.buildOfflineProviderListPayload())
}

func (g *Service) filterProviderProxyPayload(payload map[string]interface{}) {
	if g.officialBlocked.Load() {
		for _, field := range []string{"all", "connected"} {
			if items, ok := payload[field].([]interface{}); ok {
				kept := make([]interface{}, 0, len(items))
				for _, item := range items {
					id, _ := item.(string)
					if p, ok := item.(map[string]interface{}); ok {
						id, _ = p["id"].(string)
					}
					if id != officialProviderID {
						kept = append(kept, item)
					}
				}
				payload[field] = kept
			}
		}
	}
	hoxiMap := g.loadHoxiProvidersMap()
	dismissed := g.dismissedExternalProvidersSet()
	isDismissedExternalOnly := func(pid string) bool {
		pid = strings.TrimSpace(pid)
		return pid != "" && (dismissed[pid] || dismissed[strings.ToLower(pid)]) && hoxiMap[pid] == nil && !hasHoxiEngineAuthEntry(pid)
	}
	if len(dismissed) > 0 {
		if connArr, ok := payload["connected"].([]interface{}); ok {
			kept := make([]interface{}, 0, len(connArr))
			for _, item := range connArr {
				id, _ := item.(string)
				if !isDismissedExternalOnly(id) {
					kept = append(kept, item)
				}
			}
			payload["connected"] = kept
		}
		if allArr, ok := payload["all"].([]interface{}); ok {
			kept := make([]interface{}, 0, len(allArr))
			for _, item := range allArr {
				if pObj, ok := item.(map[string]interface{}); ok {
					pid, _ := pObj["id"].(string)
					if isDismissedExternalOnly(pid) {
						if _, _, hasTpl := builtinProviderConfigMap(pid); !hasTpl {
							continue
						}
					}
				}
				kept = append(kept, item)
			}
			payload["all"] = kept
		}
	}
	if allArr, ok := payload["all"].([]interface{}); ok {
		for _, item := range allArr {
			if pObj, ok := item.(map[string]interface{}); ok {
				pid, _ := pObj["id"].(string)
				if pid == "opencode" || pid == "opencode-go" {
					continue
				}
				if hProv, ok := hoxiMap[pid].(map[string]interface{}); ok {
					hModels, _ := hProv["models"].(map[string]interface{})
					hMedia, _ := hProv["mediaModels"].(map[string]interface{})
					if len(hModels) > 0 || len(hMedia) > 0 {
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
}

func (g *Service) buildOfflineProviderListPayload() map[string]interface{} {
	hoxiMap := g.loadHoxiProvidersMap()
	importedMap := g.loadOpenCodeImportedProviderMap()
	disabled := g.disabledProvidersSet()
	authIDs := g.unrevokedAuthProviderIDs()

	all := make([]interface{}, 0, len(hoxiMap)+len(importedMap)+len(builtinProviderTemplates))
	connected := make([]string, 0, len(hoxiMap)+len(authIDs))
	defaults := map[string]string{}
	seen := map[string]bool{}

	appendConfigured := func(pid string, rawProv interface{}, source string) {
		pid = strings.TrimSpace(pid)
		if pid == "" || pid == AutoProviderID || seen[pid] || disabled[pid] || disabled[strings.ToLower(pid)] {
			return
		}
		if g.officialBlocked.Load() && pid == officialProviderID {
			return
		}
		pMap, ok := rawProv.(map[string]interface{})
		if !ok {
			return
		}
		seen[pid] = true
		tplMap, tplDefault, hasTpl := builtinProviderConfigMap(pid)
		name, _ := pMap["name"].(string)
		if strings.TrimSpace(name) == "" && hasTpl {
			name, _ = tplMap["name"].(string)
		}
		if strings.TrimSpace(name) == "" {
			name = pid
		}
		npmPkg, _ := pMap["npm"].(string)
		if strings.TrimSpace(npmPkg) == "" && hasTpl {
			npmPkg, _ = tplMap["npm"].(string)
		}
		modelsRaw, _ := pMap["models"].(map[string]interface{})
		mediaRaw, _ := pMap["mediaModels"].(map[string]interface{})
		if len(modelsRaw) == 0 && len(mediaRaw) == 0 && hasTpl {
			modelsRaw, _ = tplMap["models"].(map[string]interface{})
		}
		opts, _ := pMap["options"].(map[string]interface{})
		modelsOut := map[string]interface{}{}
		sortedIDs := make([]string, 0, len(modelsRaw))
		for mid, mRaw := range modelsRaw {
			if mObj, ok := mRaw.(map[string]interface{}); ok {
				cp := make(map[string]interface{}, len(mObj)+2)
				for k, v := range mObj {
					cp[k] = v
				}
				if cp["id"] == nil {
					cp["id"] = mid
				}
				if cp["name"] == nil {
					cp["name"] = mid
				}
				modelsOut[mid] = cp
				sortedIDs = append(sortedIDs, mid)
			}
		}
		sort.Strings(sortedIDs)
		firstModelID := ""
		if defOpt, _ := opts["defaultModel"].(string); strings.TrimSpace(defOpt) != "" && modelsOut[strings.TrimSpace(defOpt)] != nil {
			firstModelID = strings.TrimSpace(defOpt)
		} else if rawOrder, ok := opts["modelOrder"].([]interface{}); ok {
			for _, item := range rawOrder {
				if id, ok := item.(string); ok && modelsOut[id] != nil {
					firstModelID = id
					break
				}
			}
		}
		if firstModelID == "" && len(sortedIDs) > 0 {
			firstModelID = sortedIDs[0]
		}
		if firstModelID == "" && tplDefault != "" {
			firstModelID = tplDefault
		}
		if firstModelID != "" {
			defaults[pid] = firstModelID
		}
		apiKey, _ := opts["apiKey"].(string)
		baseURL, _ := opts["baseURL"].(string)
		isConnected := authIDs[pid] || strings.TrimSpace(apiKey) != "" || g.ResolveStoredProviderKey(pid, baseURL) != "" || isLoopbackURL(baseURL)
		if isConnected {
			connected = append(connected, pid)
		}
		entry := map[string]interface{}{
			"id":     pid,
			"name":   name,
			"npm":    npmPkg,
			"source": source,
			"models": modelsOut,
		}
		if hasTpl {
			if envList, ok := tplMap["env"]; ok {
				entry["env"] = envList
			}
		}
		all = append(all, entry)
	}

	for pid, rawProv := range hoxiMap {
		appendConfigured(pid, rawProv, "custom")
	}
	for pid, rawProv := range importedMap {
		appendConfigured(pid, rawProv, "config")
	}
	for _, tpl := range builtinProviderTemplates {
		pid := tpl.ID
		if seen[pid] || disabled[pid] || (g.officialBlocked.Load() && pid == officialProviderID) {
			continue
		}
		tplMap, tplDefault, ok := builtinProviderConfigMap(pid)
		if !ok {
			continue
		}
		seen[pid] = true
		if tplDefault != "" {
			defaults[pid] = tplDefault
		}
		if authIDs[pid] || g.ResolveStoredProviderKey(pid, DefaultBaseURL(pid)) != "" {
			connected = append(connected, pid)
		}
		all = append(all, tplMap)
	}

	return map[string]interface{}{
		"all":       all,
		"default":   defaults,
		"connected": connected,
	}
}
