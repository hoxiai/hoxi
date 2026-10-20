package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/skills"
)

type hoxiModelsConfigResponse struct {
	HoxiProviders     map[string]interface{} `json:"hoxiProviders"`
	OpenCodeProviders []string               `json:"openCodeProviders"`
	ConfigPath        string                 `json:"configPath"`
}

type saveHoxiProviderRequest struct {
	ProviderID string                 `json:"providerID"`
	Config     map[string]interface{} `json:"config"`
}

func (g *Service) workspaceHoxiModelsPath() string {
	root := strings.TrimSpace(g.rootDir)
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, ".hoxi", "models.json")
}

func userHoxiModelsPath() string {
	return hoxipaths.Join("models.json")
}

func openCodeConfigDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, ".config", "opencode")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		p := filepath.Join(appData, "opencode")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if home != "" {
		return filepath.Join(home, ".config", "opencode")
	}
	return ""
}

func readJSONMapFile(path string) map[string]interface{} {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func writeJSONMapFile(path string, payload interface{}) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	// Replace rather than truncate so existing permissive files also become private,
	// and a failed write cannot destroy the previous credentials.
	f, err := os.CreateTemp(filepath.Dir(path), ".models-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func persistHoxiModelsFiles(userPath, workspacePath string, doc map[string]interface{}) error {
	if userPath == "" {
		// Without a home directory, the workspace remains the upstream config.
		return writeJSONMapFile(workspacePath, doc)
	}
	if err := writeJSONMapFile(userPath, doc); err != nil {
		return err
	}
	if filepath.Clean(userPath) == filepath.Clean(workspacePath) {
		return nil
	}
	// Keep the full merged config for OPENCODE_CONFIG and Hoxi's own requests.
	// Omit credentials from the workspace mirror, without mutating the caller.
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var workspaceDoc map[string]interface{}
	if err := json.Unmarshal(raw, &workspaceDoc); err != nil {
		return err
	}
	if providers, ok := workspaceDoc["provider"].(map[string]interface{}); ok {
		for _, value := range providers {
			if provider, ok := value.(map[string]interface{}); ok {
				if options, ok := provider["options"].(map[string]interface{}); ok {
					delete(options, "apiKey")
					delete(options, "headers")
				}
			}
		}
	}
	return writeJSONMapFile(workspacePath, workspaceDoc)
}

func loadOpenCodeImportedProvidersFromDisk() map[string]interface{} {
	dir := openCodeConfigDir()
	if dir == "" {
		return map[string]interface{}{}
	}
	result := map[string]interface{}{}
	for _, name := range []string{"config.json", "opencode.json"} {
		parsed := readJSONMapFile(filepath.Join(dir, name))
		if provMap, ok := parsed["provider"].(map[string]interface{}); ok {
			for k, v := range provMap {
				if vm, ok := v.(map[string]interface{}); ok {
					result[k] = vm
				}
			}
		}
	}
	normalizeDocModelVariants(map[string]interface{}{"provider": result})
	return result
}

// ensureHoxiModelsConfig initializes ~/.hoxi/models.json and <rootDir>/.hoxi/models.json,
// cleans up any legacy ~/.config/opencode/opencode.jsonc created by earlier versions,
// and returns the path to pass via OPENCODE_CONFIG.
func ensureHoxiModelsConfig(rootDir string) string {
	uPath := userHoxiModelsPath()
	if strings.TrimSpace(rootDir) == "" {
		rootDir, _ = os.Getwd()
	}
	wPath := filepath.Join(rootDir, ".hoxi", "models.json")

	doc := map[string]interface{}{
		"$schema":  "https://opencode.ai/config.json",
		"provider": map[string]interface{}{},
	}
	skillLayers := make([]interface{}, 0, 2)
	for _, p := range []string{uPath, wPath} {
		parsed := readJSONMapFile(p)
		if parsed == nil {
			continue
		}
		if layer, ok := parsed["skills"]; ok && layer != nil {
			skillLayers = append(skillLayers, layer)
		}
		deepMergeMap(doc, parsed)
	}

	// Clean up legacy ~/.config/opencode/opencode.jsonc so ~/.config/opencode stays 100% untouched
	legacyOverlay := ""
	if dir := openCodeConfigDir(); dir != "" {
		path := filepath.Join(dir, "opencode.jsonc")
		if overlay := readJSONMapFile(path); overlay != nil {
			legacyOverlay = path
			imported := loadOpenCodeImportedProvidersFromDisk()
			provMap, _ := doc["provider"].(map[string]interface{})
			if provMap == nil {
				provMap = map[string]interface{}{}
			}
			if oProv, ok := overlay["provider"].(map[string]interface{}); ok {
				for k, v := range oProv {
					if _, isImp := imported[k]; !isImp {
						if _, exists := provMap[k]; !exists {
							if vm, ok := v.(map[string]interface{}); ok {
								provMap[k] = vm
							}
						}
					}
				}
			}
			doc["provider"] = provMap
		}
	}

	if dir := skills.DefaultUserDir(); dir != "" {
		mergeHoxiSkillsPath(doc, skillLayers, dir)
	}
	normalizeDocModelVariants(doc)
	if err := persistHoxiModelsFiles(uPath, wPath, doc); err == nil && legacyOverlay != "" {
		_ = os.Remove(legacyOverlay)
	}
	if uPath != "" {
		return uPath
	}
	return wPath
}

// mergeHoxiSkillsPath unions every skills layer with Hoxi's bundled skills
// directory. Paths are merged instead of replaced so user-defined skill
// sources survive initialization, and duplicates are dropped.
func mergeHoxiSkillsPath(doc map[string]interface{}, layers []interface{}, dir string) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return
	}
	var paths, urls []string
	seenPaths := make(map[string]bool)
	seenURLs := make(map[string]bool)
	addPath := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seenPaths[value] {
			return
		}
		seenPaths[value] = true
		paths = append(paths, value)
	}
	addURL := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seenURLs[value] {
			return
		}
		seenURLs[value] = true
		urls = append(urls, value)
	}
	addLayer := func(layer interface{}) {
		switch v := layer.(type) {
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					addPath(s)
				}
			}
		case map[string]interface{}:
			if raw, ok := v["paths"].([]interface{}); ok {
				for _, item := range raw {
					if s, ok := item.(string); ok {
						addPath(s)
					}
				}
			}
			if raw, ok := v["urls"].([]interface{}); ok {
				for _, item := range raw {
					if s, ok := item.(string); ok {
						addURL(s)
					}
				}
			}
		}
	}
	addLayer(doc["skills"])
	for _, layer := range layers {
		addLayer(layer)
	}
	addPath(dir)

	// The schema marks the {paths, urls} object as deprecated; the array form
	// accepts both paths and URLs, so emit that instead.
	values := make([]string, 0, len(paths)+len(urls))
	values = append(values, paths...)
	values = append(values, urls...)
	if len(values) > 0 {
		doc["skills"] = stringsToInterfaces(values)
	}
}

func stringsToInterfaces(values []string) []interface{} {
	out := make([]interface{}, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func (g *Service) loadOpenCodeImportedProviderMap() map[string]interface{} {
	return loadOpenCodeImportedProvidersFromDisk()
}

func (g *Service) loadHoxiConfigDoc() map[string]interface{} {
	doc := map[string]interface{}{
		"$schema":  "https://opencode.ai/config.json",
		"provider": map[string]interface{}{},
	}
	for _, p := range []string{userHoxiModelsPath(), g.workspaceHoxiModelsPath()} {
		if parsed := readJSONMapFile(p); parsed != nil {
			deepMergeMap(doc, parsed)
		}
	}
	if _, ok := doc["provider"].(map[string]interface{}); !ok {
		doc["provider"] = map[string]interface{}{}
	}
	normalizeDocModelVariants(doc)
	return doc
}

func (g *Service) loadHoxiProvidersMap() map[string]interface{} {
	doc := g.loadHoxiConfigDoc()
	if provMap, ok := doc["provider"].(map[string]interface{}); ok && provMap != nil {
		return provMap
	}
	return map[string]interface{}{}
}

func (g *Service) persistHoxiConfigDoc(doc map[string]interface{}) error {
	doc["$schema"] = "https://opencode.ai/config.json"
	normalizeDocModelVariants(doc)
	if err := persistHoxiModelsFiles(userHoxiModelsPath(), g.workspaceHoxiModelsPath(), doc); err != nil {
		return err
	}
	g.reloadOpenCodeGlobalConfig()
	return nil
}

func (g *Service) isBusy() bool {
	g.reloadMu.Lock()
	fn := g.isBusyFn
	g.reloadMu.Unlock()
	return fn != nil && fn()
}

func (g *Service) reloadOpenCodeGlobalConfig() {
	if !g.openCode.Available() {
		return
	}
	if g.isBusy() {
		g.scheduleDeferredReload()
		return
	}
	g.reloadMu.Lock()
	g.reloadPending = false
	g.reloadMu.Unlock()
	g.postOpenCodeGlobalDispose()
}

func (g *Service) scheduleDeferredReload() {
	g.reloadMu.Lock()
	g.reloadPending = true
	if g.reloadLoopRunning || !g.openCode.Available() {
		g.reloadMu.Unlock()
		return
	}
	g.reloadLoopRunning = true
	g.reloadMu.Unlock()

	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if !g.openCode.Available() {
				g.reloadMu.Lock()
				g.reloadPending = false
				g.reloadLoopRunning = false
				g.reloadMu.Unlock()
				return
			}
			if g.isBusy() {
				continue
			}
			g.reloadMu.Lock()
			pending := g.reloadPending
			g.reloadPending = false
			g.reloadLoopRunning = false
			g.reloadMu.Unlock()
			if pending {
				g.postOpenCodeGlobalDispose()
			}
			return
		}
	}()
}

// FlushPendingReload immediately disposes OpenCode's global instances if a
// deferred reload is waiting and no session is currently busy.
func (g *Service) FlushPendingReload() bool {
	if !g.openCode.Available() || g.isBusy() {
		return false
	}
	g.reloadMu.Lock()
	pending := g.reloadPending
	g.reloadPending = false
	g.reloadMu.Unlock()
	if !pending {
		return false
	}
	g.postOpenCodeGlobalDispose()
	return true
}

// HasPendingReload reports whether an OpenCode global config reload is queued.
func (g *Service) HasPendingReload() bool {
	g.reloadMu.Lock()
	defer g.reloadMu.Unlock()
	return g.reloadPending
}

func (g *Service) postOpenCodeGlobalDispose() {
	if !g.openCode.Available() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, _, _ = g.openCode.Raw(ctx, http.MethodPost, "/global/dispose", nil, []byte("{}"))
}

func (g *Service) handleGlobalDispose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if g.proxy != nil {
			g.proxy.ServeHTTP(w, r)
			return
		}
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	g.reloadOpenCodeGlobalConfig()
	writeJSON(w, http.StatusOK, true)
}

func (g *Service) handleModelsConfig(w http.ResponseWriter, r *http.Request) {
	importedMap := g.loadOpenCodeImportedProviderMap()
	doc := g.loadHoxiConfigDoc()
	hoxiMap, _ := doc["provider"].(map[string]interface{})
	if hoxiMap == nil {
		hoxiMap = map[string]interface{}{}
	}

	switch r.Method {
	case http.MethodGet:
		openCodeIDs := make([]string, 0, len(importedMap))
		for k := range importedMap {
			if _, inHoxi := hoxiMap[k]; !inHoxi {
				openCodeIDs = append(openCodeIDs, k)
			}
		}
		sort.Strings(openCodeIDs)
		writeJSON(w, http.StatusOK, hoxiModelsConfigResponse{
			HoxiProviders:     hoxiMap,
			OpenCodeProviders: openCodeIDs,
			ConfigPath:        ".hoxi/models.json",
		})

	case http.MethodPost, http.MethodPut:
		var req saveHoxiProviderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		pid := strings.TrimSpace(req.ProviderID)
		if pid == "" || req.Config == nil {
			writeJSONError(w, http.StatusBadRequest, "providerID and config are required")
			return
		}
		cleaned := stripNullsMap(req.Config)

		// If taking over an OpenCode-imported provider and apiKey wasn't re-entered, inherit apiKey from imported config
		if impProv, ok := importedMap[pid].(map[string]interface{}); ok {
			if impOpts, ok := impProv["options"].(map[string]interface{}); ok {
				if impKey, ok := impOpts["apiKey"].(string); ok && strings.TrimSpace(impKey) != "" {
					opts, _ := cleaned["options"].(map[string]interface{})
					if opts == nil {
						opts = map[string]interface{}{}
					}
					if curKey, _ := opts["apiKey"].(string); strings.TrimSpace(curKey) == "" {
						opts["apiKey"] = impKey
					}
					cleaned["options"] = opts
				}
			}
		}

		g.normalizeAndStoreMultiKeys(pid, cleaned)
		hoxiMap[pid] = cleaned
		doc["provider"] = hoxiMap
		if err := g.persistHoxiConfigDoc(doc); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"hoxiProviders": hoxiMap,
			"configPath":    ".hoxi/models.json",
		})

	case http.MethodDelete:
		pid := strings.TrimSpace(r.URL.Query().Get("providerID"))
		if pid == "" {
			writeJSONError(w, http.StatusBadRequest, "providerID is required")
			return
		}
		if _, inHoxi := hoxiMap[pid]; !inHoxi {
			if _, isImported := importedMap[pid]; isImported {
				writeJSONError(w, http.StatusForbidden, "OpenCode imported provider is read-only")
				return
			}
		}
		delete(hoxiMap, pid)
		doc["provider"] = hoxiMap
		if err := g.persistHoxiConfigDoc(doc); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"hoxiProviders": hoxiMap,
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// managedGlobalConfigKeys are top-level config keys written through PATCH /global/config.
// When a key is removed from .hoxi/models.json it must also disappear from the GET response,
// otherwise OpenCode's boot-time config keeps serving the stale value.
var managedGlobalConfigKeys = []string{
	"model",
	"small_model",
	"subagent_model",
	"agent",
	"permission",
	"mcp",
	"disabled_providers",
}

// overlayHoxiGlobalConfig makes .hoxi/models.json authoritative over the OpenCode
// config snapshot loaded at process start, so routing roles (small_model,
// subagent_model, agent models) reflect edits immediately without a daemon restart.
func overlayHoxiGlobalConfig(cfg, doc map[string]interface{}) {
	for key, value := range doc {
		if key == "$schema" || key == "provider" {
			continue
		}
		cfg[key] = value
	}
	for _, key := range managedGlobalConfigKeys {
		if _, ok := doc[key]; !ok {
			delete(cfg, key)
		}
	}
}

func deepMergeMap(dst, src map[string]interface{}) {
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		if vMap, ok := v.(map[string]interface{}); ok {
			if dMap, ok := dst[k].(map[string]interface{}); ok {
				deepMergeMap(dMap, vMap)
				continue
			}
		}
		dst[k] = v
	}
}

func stripNullsMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		if v == nil {
			continue
		}
		if vMap, ok := v.(map[string]interface{}); ok {
			out[k] = stripNullsMap(vMap)
		} else {
			out[k] = v
		}
	}
	return out
}

func filterProviderModelsByHoxi(provMap, hoxiMap map[string]interface{}) {
	for pid, hVal := range hoxiMap {
		if pid == "opencode" || pid == "opencode-go" {
			continue
		}
		hProv, ok := hVal.(map[string]interface{})
		if !ok {
			continue
		}
		hModels, hasHModels := hProv["models"].(map[string]interface{})
		if !hasHModels || len(hModels) == 0 {
			continue
		}
		pObj, ok := provMap[pid].(map[string]interface{})
		if !ok {
			continue
		}
		pModels, ok := pObj["models"].(map[string]interface{})
		if !ok {
			continue
		}
		for mid := range pModels {
			if _, keep := hModels[mid]; !keep {
				delete(pModels, mid)
			}
		}
	}
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
				doc := g.loadHoxiConfigDoc()
				deepMergeMap(doc, patch)
				if err := g.persistHoxiConfigDoc(doc); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
	}

	status, body, ok := g.fetchOpenCodeForProxy(w, r, "/global/config", nil)
	if !ok {
		return
	}
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
