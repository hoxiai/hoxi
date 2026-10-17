package main

import (
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

type hoxiModelsConfigResponse struct {
	HoxiProviders     map[string]interface{} `json:"hoxiProviders"`
	OpenCodeProviders []string               `json:"openCodeProviders"`
	ConfigPath        string                 `json:"configPath"`
}

type saveHoxiProviderRequest struct {
	ProviderID string                 `json:"providerID"`
	Config     map[string]interface{} `json:"config"`
}

func (g *desktopGateway) workspaceHoxiModelsPath() string {
	root := strings.TrimSpace(g.rootDir)
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, ".hoxi", "models.json")
}

func userHoxiModelsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "models.json")
}

func openCodeConfigDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "opencode")
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
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
	for _, p := range []string{uPath, wPath} {
		if parsed := readJSONMapFile(p); parsed != nil {
			deepMergeMap(doc, parsed)
		}
	}

	// Clean up legacy ~/.config/opencode/opencode.jsonc so ~/.config/opencode stays 100% untouched
	if dir := openCodeConfigDir(); dir != "" {
		legacyOverlay := filepath.Join(dir, "opencode.jsonc")
		if overlay := readJSONMapFile(legacyOverlay); overlay != nil {
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
			_ = os.Remove(legacyOverlay)
		}
	}

	if uPath != "" {
		_ = writeJSONMapFile(uPath, doc)
	}
	_ = writeJSONMapFile(wPath, doc)
	if uPath != "" {
		return uPath
	}
	return wPath
}

func (g *desktopGateway) loadOpenCodeImportedProviderMap() map[string]interface{} {
	return loadOpenCodeImportedProvidersFromDisk()
}

func (g *desktopGateway) loadHoxiConfigDoc() map[string]interface{} {
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
	return doc
}

func (g *desktopGateway) loadHoxiProvidersMap() map[string]interface{} {
	doc := g.loadHoxiConfigDoc()
	if provMap, ok := doc["provider"].(map[string]interface{}); ok && provMap != nil {
		return provMap
	}
	return map[string]interface{}{}
}

func (g *desktopGateway) persistHoxiConfigDoc(doc map[string]interface{}) error {
	doc["$schema"] = "https://opencode.ai/config.json"
	if uPath := userHoxiModelsPath(); uPath != "" {
		_ = writeJSONMapFile(uPath, doc)
	}
	err := writeJSONMapFile(g.workspaceHoxiModelsPath(), doc)
	g.reloadOpenCodeGlobalConfig()
	return err
}

func (g *desktopGateway) reloadOpenCodeGlobalConfig() {
	if g.openCodePort <= 0 {
		return
	}
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil}}
	base := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort)

	if req, err := http.NewRequest(http.MethodPost, base+"/global/dispose", strings.NewReader("{}")); err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth("opencode", g.password)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
}

func (g *desktopGateway) handleModelsConfig(w http.ResponseWriter, r *http.Request) {
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
		_ = g.persistHoxiConfigDoc(doc)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"hoxiProviders": hoxiMap,
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
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
		hProv, ok := hVal.(map[string]interface{})
		if !ok {
			continue
		}
		hModels, hasHModels := hProv["models"].(map[string]interface{})
		if !hasHModels {
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
// and filters GET /global/config so Hoxi-managed providers strictly reflect .hoxi/models.json's model keys.
func (g *desktopGateway) handleGlobalConfigProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		raw, err := io.ReadAll(r.Body)
		if err == nil && len(raw) > 0 {
			var patch map[string]interface{}
			if json.Unmarshal(raw, &patch) == nil && patch != nil {
				doc := g.loadHoxiConfigDoc()
				deepMergeMap(doc, patch)
				_ = g.persistHoxiConfigDoc(doc)
			}
		}
	}

	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
	target := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/global/config"
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		g.proxy.ServeHTTP(w, r)
		return
	}
	req.SetBasicAuth("opencode", g.password)
	resp, err := client.Do(req)
	if err != nil {
		g.proxy.ServeHTTP(w, r)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	var cfg map[string]interface{}
	if json.Unmarshal(body, &cfg) == nil {
		if provMap, ok := cfg["provider"].(map[string]interface{}); ok {
			filterProviderModelsByHoxi(provMap, g.loadHoxiProvidersMap())
		}
		writeJSON(w, resp.StatusCode, cfg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// handleProviderProxy filters GET /provider so any provider managed in .hoxi/models.json only exposes its .hoxi models.
func (g *desktopGateway) handleProviderProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		g.proxy.ServeHTTP(w, r)
		return
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
	target := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + r.URL.RequestURI()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		g.proxy.ServeHTTP(w, r)
		return
	}
	req.SetBasicAuth("opencode", g.password)
	resp, err := client.Do(req)
	if err != nil {
		g.proxy.ServeHTTP(w, r)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	var payload map[string]interface{}
	if json.Unmarshal(body, &payload) == nil {
		hoxiMap := g.loadHoxiProvidersMap()
		if allArr, ok := payload["all"].([]interface{}); ok {
			for _, item := range allArr {
				if pObj, ok := item.(map[string]interface{}); ok {
					pid, _ := pObj["id"].(string)
					if hProv, ok := hoxiMap[pid].(map[string]interface{}); ok {
						if hModels, ok := hProv["models"].(map[string]interface{}); ok {
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
		writeJSON(w, resp.StatusCode, payload)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
