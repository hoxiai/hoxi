package main

import (
	"bytes"
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

type hoxiModelsFile struct {
	Schema   string                 `json:"$schema,omitempty"`
	Provider map[string]interface{} `json:"provider"`
}

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

// loadOpenCodeImportedProviders returns provider IDs defined in OpenCode's own base opencode.json / config.json.
func (g *desktopGateway) loadOpenCodeImportedProviderMap() map[string]interface{} {
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

func (g *desktopGateway) loadHoxiProvidersMap() map[string]interface{} {
	providers := map[string]interface{}{}

	for _, p := range []string{userHoxiModelsPath(), g.workspaceHoxiModelsPath()} {
		parsed := readJSONMapFile(p)
		if provMap, ok := parsed["provider"].(map[string]interface{}); ok {
			for k, v := range provMap {
				if vm, ok := v.(map[string]interface{}); ok {
					providers[k] = vm
				}
			}
		}
	}

	// Migrate any non-OpenCode-base provider from opencode.jsonc into .hoxi/models.json on first load
	if len(providers) == 0 {
		imported := g.loadOpenCodeImportedProviderMap()
		dir := openCodeConfigDir()
		if dir != "" {
			overlay := readJSONMapFile(filepath.Join(dir, "opencode.jsonc"))
			if provMap, ok := overlay["provider"].(map[string]interface{}); ok {
				migrated := false
				for k, v := range provMap {
					if _, isImported := imported[k]; !isImported {
						if vm, ok := v.(map[string]interface{}); ok {
							providers[k] = vm
							migrated = true
						}
					}
				}
				if migrated {
					_ = g.persistHoxiProvidersMap(providers)
				}
			}
		}
	}

	return providers
}

func (g *desktopGateway) persistHoxiProvidersMap(providers map[string]interface{}) error {
	doc := hoxiModelsFile{
		Schema:   "https://opencode.ai/config.json",
		Provider: providers,
	}
	if uPath := userHoxiModelsPath(); uPath != "" {
		_ = writeJSONMapFile(uPath, doc)
	}
	return writeJSONMapFile(g.workspaceHoxiModelsPath(), doc)
}

func (g *desktopGateway) syncHoxiProviderToOpenCodeOverlay(providerID string, cfg map[string]interface{}) {
	dir := openCodeConfigDir()
	if dir == "" {
		return
	}
	overlayPath := filepath.Join(dir, "opencode.jsonc")
	overlay := readJSONMapFile(overlayPath)
	if overlay == nil {
		overlay = map[string]interface{}{
			"$schema": "https://opencode.ai/config.json",
		}
	}
	provMap, ok := overlay["provider"].(map[string]interface{})
	if !ok || provMap == nil {
		provMap = map[string]interface{}{}
	}
	if cfg == nil {
		delete(provMap, providerID)
	} else {
		provMap[providerID] = stripNullsMap(cfg)
	}
	overlay["provider"] = provMap
	_ = writeJSONMapFile(overlayPath, overlay)
	g.reloadOpenCodeGlobalConfig()
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
	if req, err := http.NewRequest(http.MethodPatch, base+"/global/config", strings.NewReader("{}")); err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth("opencode", g.password)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
}

func (g *desktopGateway) handleModelsConfig(w http.ResponseWriter, r *http.Request) {
	importedMap := g.loadOpenCodeImportedProviderMap()
	hoxiMap := g.loadHoxiProvidersMap()

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
		if _, isImported := importedMap[pid]; isImported {
			if _, inHoxi := hoxiMap[pid]; !inHoxi {
				writeJSONError(w, http.StatusForbidden, "OpenCode imported provider is read-only")
				return
			}
		}
		cleaned := stripNullsMap(req.Config)
		hoxiMap[pid] = cleaned
		if err := g.persistHoxiProvidersMap(hoxiMap); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		g.syncHoxiProviderToOpenCodeOverlay(pid, cleaned)
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
		if _, isImported := importedMap[pid]; isImported {
			if _, inHoxi := hoxiMap[pid]; !inHoxi {
				writeJSONError(w, http.StatusForbidden, "OpenCode imported provider is read-only")
				return
			}
		}
		delete(hoxiMap, pid)
		_ = g.persistHoxiProvidersMap(hoxiMap)
		g.syncHoxiProviderToOpenCodeOverlay(pid, nil)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"hoxiProviders": hoxiMap,
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func applyNullDeletionsMap(target, patch map[string]interface{}) bool {
	mutated := false
	for k, v := range patch {
		if v == nil {
			if _, exists := target[k]; exists {
				delete(target, k)
				mutated = true
			}
			continue
		}
		if vMap, ok := v.(map[string]interface{}); ok {
			if tMap, ok := target[k].(map[string]interface{}); ok {
				if applyNullDeletionsMap(tMap, vMap) {
					mutated = true
				}
			}
		}
	}
	returnated := mutated
	return returnated
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

// handleGlobalConfigProxy intercepts PATCH /global/config to safely process null key deletions on disk
// and strip null values before forwarding to OpenCode so Zod never throws "Expected object, got null".
func (g *desktopGateway) handleGlobalConfigProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		raw, err := io.ReadAll(r.Body)
		if err == nil && len(raw) > 0 {
			var patch map[string]interface{}
			if json.Unmarshal(raw, &patch) == nil && patch != nil {
				dir := openCodeConfigDir()
				if dir != "" {
					overlayPath := filepath.Join(dir, "opencode.jsonc")
					if overlay := readJSONMapFile(overlayPath); overlay != nil {
						if applyNullDeletionsMap(overlay, patch) {
							_ = writeJSONMapFile(overlayPath, overlay)
							g.reloadOpenCodeGlobalConfig()
						}
					}
				}
				cleaned := stripNullsMap(patch)
				if cleanBytes, err := json.Marshal(cleaned); err == nil {
					r.Body = io.NopCloser(bytes.NewReader(cleanBytes))
					r.ContentLength = int64(len(cleanBytes))
				}
			} else {
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
		}
	}
	g.proxy.ServeHTTP(w, r)
}
