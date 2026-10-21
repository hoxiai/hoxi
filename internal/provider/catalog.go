package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// modelCatalog is the read-only model list served by GET /api/hoxi/provider/models.
// It is built field by field from provider and model ids/names only, so API
// keys, base URLs, headers and other provider options can never leak through
// it; this is the only model listing the remote tunnel exposes.
type modelCatalog struct {
	// Source is "engine" when the running OpenCode engine answered, otherwise
	// "config" (Hoxi and OpenCode config files on disk).
	Source    string                 `json:"source"`
	Providers []modelCatalogProvider `json:"providers"`
}

type modelCatalogProvider struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Models []modelCatalogModel `json:"models"`
}

type modelCatalogModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (g *Service) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	hoxiMap := g.loadHoxiProvidersMap()
	if providers, ok := g.engineModelCatalog(r.Context(), hoxiMap); ok {
		writeJSON(w, http.StatusOK, modelCatalog{Source: "engine", Providers: providers})
		return
	}
	writeJSON(w, http.StatusOK, modelCatalog{Source: "config", Providers: configModelCatalog(hoxiMap, g.loadOpenCodeImportedProviderMap())})
}

// engineModelCatalog lists the engine's connected providers, narrowed to the
// models selected in .hoxi/models.json like the /provider proxy.
func (g *Service) engineModelCatalog(parent context.Context, hoxiMap map[string]interface{}) ([]modelCatalogProvider, bool) {
	if !g.openCode.Available() {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	status, body, err := g.openCode.Raw(ctx, http.MethodGet, "/provider", nil, nil)
	if err != nil || status < 200 || status >= 300 {
		return nil, false
	}
	var payload struct {
		All       []map[string]interface{} `json:"all"`
		Connected []string                 `json:"connected"`
	}
	// Without the connected list every models.dev provider would be listed;
	// fall back to the configured providers instead.
	if json.Unmarshal(body, &payload) != nil || payload.All == nil || payload.Connected == nil {
		return nil, false
	}
	connected := make(map[string]bool, len(payload.Connected))
	for _, id := range payload.Connected {
		connected[id] = true
	}
	out := make([]modelCatalogProvider, 0, len(payload.Connected))
	for _, p := range payload.All {
		pid, _ := p["id"].(string)
		if !connected[pid] {
			continue
		}
		if prov, ok := catalogProvider(pid, p, hoxiSelectedModels(pid, hoxiMap)); ok {
			out = append(out, prov)
		}
	}
	sortCatalog(out)
	return out, true
}

// configModelCatalog is the fallback when the engine is not running: providers
// from .hoxi/models.json, then OpenCode-imported ones it does not override.
func configModelCatalog(hoxiMap, importedMap map[string]interface{}) []modelCatalogProvider {
	out := make([]modelCatalogProvider, 0, len(hoxiMap)+len(importedMap))
	add := func(pid string, v interface{}) {
		p, _ := v.(map[string]interface{})
		if prov, ok := catalogProvider(pid, p, nil); ok {
			out = append(out, prov)
		}
	}
	for pid, v := range hoxiMap {
		add(pid, v)
	}
	for pid, v := range importedMap {
		if _, overridden := hoxiMap[pid]; !overridden {
			add(pid, v)
		}
	}
	sortCatalog(out)
	return out
}

// hoxiSelectedModels returns the model ids a Hoxi-managed provider is limited
// to, or nil when every engine model should be listed.
func hoxiSelectedModels(pid string, hoxiMap map[string]interface{}) map[string]interface{} {
	if pid == "opencode" || pid == "opencode-go" {
		return nil
	}
	hProv, _ := hoxiMap[pid].(map[string]interface{})
	models, _ := hProv["models"].(map[string]interface{})
	if len(models) == 0 {
		return nil
	}
	return models
}

func catalogProvider(pid string, p map[string]interface{}, keep map[string]interface{}) (modelCatalogProvider, bool) {
	pid = strings.TrimSpace(pid)
	if pid == "" || p == nil {
		return modelCatalogProvider{}, false
	}
	models, _ := p["models"].(map[string]interface{})
	prov := modelCatalogProvider{ID: pid, Name: catalogName(p, pid), Models: make([]modelCatalogModel, 0, len(models))}
	for mid, v := range models {
		mid = strings.TrimSpace(mid)
		if mid == "" {
			continue
		}
		if keep != nil {
			if _, ok := keep[mid]; !ok {
				continue
			}
		}
		m, _ := v.(map[string]interface{})
		prov.Models = append(prov.Models, modelCatalogModel{ID: mid, Name: catalogName(m, mid)})
	}
	if len(prov.Models) == 0 {
		return modelCatalogProvider{}, false
	}
	sort.Slice(prov.Models, func(i, j int) bool { return prov.Models[i].ID < prov.Models[j].ID })
	return prov, true
}

func catalogName(obj map[string]interface{}, fallback string) string {
	if name, ok := obj["name"].(string); ok && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return fallback
}

func sortCatalog(providers []modelCatalogProvider) {
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
}
