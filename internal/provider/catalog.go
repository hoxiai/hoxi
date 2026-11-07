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
	importedMap := g.loadOpenCodeImportedProviderMap()
	disabled := g.disabledProvidersSet()
	if providers, ok := g.engineModelCatalog(r.Context(), hoxiMap, importedMap, disabled); ok {
		writeJSON(w, http.StatusOK, modelCatalog{Source: "engine", Providers: providers})
		return
	}
	writeJSON(w, http.StatusOK, modelCatalog{Source: "config", Providers: configModelCatalog(hoxiMap, importedMap, disabled)})
}

// engineModelCatalog lists the engine's connected providers plus any locally
// configured/imported providers in .hoxi/models.json or opencode.json, narrowed
// to the models selected in .hoxi/models.json when applicable.
func (g *Service) engineModelCatalog(parent context.Context, hoxiMap, importedMap map[string]interface{}, disabled map[string]bool) ([]modelCatalogProvider, bool) {
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
		connected[strings.TrimSpace(id)] = true
	}
	seen := make(map[string]bool, len(payload.Connected)+len(hoxiMap))
	out := make([]modelCatalogProvider, 0, len(payload.Connected)+len(hoxiMap))
	for _, p := range payload.All {
		pid, _ := p["id"].(string)
		pid = strings.TrimSpace(pid)
		if pid == "" || pid == AutoProviderID || disabled[pid] {
			continue
		}
		hProv, inHoxi := hoxiMap[pid].(map[string]interface{})
		iProv, inImported := importedMap[pid].(map[string]interface{})
		if !connected[pid] && !inHoxi && !inImported {
			continue
		}
		seen[pid] = true
		var (
			prov modelCatalogProvider
			ok   bool
		)
		switch {
		case inHoxi:
			prov, ok = mergeCatalogProvider(pid, hProv, p, hoxiSelectedModels(pid, hoxiMap))
		case inImported:
			var keep map[string]interface{}
			if !connected[pid] {
				keep, _ = iProv["models"].(map[string]interface{})
			}
			prov, ok = mergeCatalogProvider(pid, iProv, p, keep)
		default:
			prov, ok = catalogProvider(pid, p, nil)
		}
		if ok {
			out = append(out, prov)
		}
	}
	for pid, v := range hoxiMap {
		pid = strings.TrimSpace(pid)
		if pid == "" || pid == AutoProviderID || disabled[pid] || seen[pid] {
			continue
		}
		hProv, _ := v.(map[string]interface{})
		if prov, ok := catalogProvider(pid, hProv, nil); ok {
			seen[pid] = true
			out = append(out, prov)
		}
	}
	sortCatalog(out)
	return out, true
}

// configModelCatalog is the fallback when the engine is not running: providers
// from .hoxi/models.json, then OpenCode-imported ones it does not override.
func configModelCatalog(hoxiMap, importedMap map[string]interface{}, disabled map[string]bool) []modelCatalogProvider {
	out := make([]modelCatalogProvider, 0, len(hoxiMap)+len(importedMap))
	add := func(pid string, v interface{}) {
		pid = strings.TrimSpace(pid)
		if pid == "" || pid == AutoProviderID || disabled[pid] {
			return
		}
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

func mergeCatalogProvider(pid string, primary, fallback map[string]interface{}, keep map[string]interface{}) (modelCatalogProvider, bool) {
	pid = strings.TrimSpace(pid)
	if pid == "" || (primary == nil && fallback == nil) {
		return modelCatalogProvider{}, false
	}
	primaryModels, _ := primary["models"].(map[string]interface{})
	fallbackModels, _ := fallback["models"].(map[string]interface{})
	prov := modelCatalogProvider{
		ID:     pid,
		Name:   catalogName(primary, catalogName(fallback, pid)),
		Models: make([]modelCatalogModel, 0, len(primaryModels)+len(fallbackModels)),
	}
	seenModels := make(map[string]bool, len(primaryModels)+len(fallbackModels))
	addModel := func(mid string) {
		mid = strings.TrimSpace(mid)
		if mid == "" || seenModels[mid] {
			return
		}
		if keep != nil {
			if _, ok := keep[mid]; !ok {
				return
			}
		}
		seenModels[mid] = true
		mPrimary, _ := primaryModels[mid].(map[string]interface{})
		mFallback, _ := fallbackModels[mid].(map[string]interface{})
		prov.Models = append(prov.Models, modelCatalogModel{
			ID:   mid,
			Name: catalogName(mPrimary, catalogName(mFallback, mid)),
		})
	}
	for mid := range primaryModels {
		addModel(mid)
	}
	for mid := range fallbackModels {
		addModel(mid)
	}
	if len(prov.Models) == 0 {
		return modelCatalogProvider{}, false
	}
	sort.Slice(prov.Models, func(i, j int) bool { return prov.Models[i].ID < prov.Models[j].ID })
	return prov, true
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
