package provider

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

const AutoProviderID = "hoxi-auto"

type AutoPoolCandidate struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type AutoModelPoolConfig struct {
	ID         string              `json:"id"`
	Name       string              `json:"name,omitempty"`
	Preset     bool                `json:"preset,omitempty"`
	Strategy   string              `json:"strategy"` // "round-robin" | "priority" | "smart-health"
	Customized bool                `json:"customized"`
	Candidates []AutoPoolCandidate `json:"candidates"`
}

var (
	autoRouterMu   sync.Mutex
	autoPoolCursor = map[string]uint64{}
	apiKeyCursor   uint64
)

func userAutoPoolsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "auto-pools.json")
}

func (g *Service) workspaceAutoPoolsPath() string {
	root := strings.TrimSpace(g.rootDir)
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, ".hoxi", "auto-pools.json")
}

func defaultAutoPoolsMap() map[string]interface{} {
	return map[string]interface{}{
		"free": map[string]interface{}{
			"id":         "free",
			"preset":     true,
			"strategy":   "round-robin",
			"customized": false,
			"candidates": []interface{}{},
		},
		"daily": map[string]interface{}{
			"id":         "daily",
			"preset":     true,
			"strategy":   "priority",
			"customized": false,
			"candidates": []interface{}{},
		},
		"frontier": map[string]interface{}{
			"id":         "frontier",
			"preset":     true,
			"strategy":   "smart-health",
			"customized": false,
			"candidates": []interface{}{},
		},
	}
}

func (g *Service) loadAutoPoolsDoc() map[string]interface{} {
	doc := map[string]interface{}{
		"autoPools": defaultAutoPoolsMap(),
		"keyPools":  map[string]interface{}{},
	}
	for _, p := range []string{userAutoPoolsPath(), g.workspaceAutoPoolsPath()} {
		if parsed := readJSONMapFile(p); parsed != nil {
			deepMergeMap(doc, parsed)
		}
	}
	if _, ok := doc["autoPools"].(map[string]interface{}); !ok {
		doc["autoPools"] = defaultAutoPoolsMap()
	}
	if _, ok := doc["keyPools"].(map[string]interface{}); !ok {
		doc["keyPools"] = map[string]interface{}{}
	}
	return doc
}

func (g *Service) persistAutoPoolsDoc(doc map[string]interface{}) error {
	uPath := userAutoPoolsPath()
	wPath := g.workspaceAutoPoolsPath()
	if uPath != "" {
		if err := writeJSONMapFile(uPath, doc); err != nil {
			return err
		}
		if filepath.Clean(uPath) == filepath.Clean(wPath) {
			return nil
		}
	}
	// Omit secret keyPools from the workspace mirror
	mirrorDoc := map[string]interface{}{
		"autoPools": doc["autoPools"],
	}
	return writeJSONMapFile(wPath, mirrorDoc)
}

func (g *Service) loadAutoPoolsMap() map[string]interface{} {
	doc := g.loadAutoPoolsDoc()
	defaults := defaultAutoPoolsMap()
	raw, ok := doc["autoPools"].(map[string]interface{})
	if !ok || raw == nil {
		return defaults
	}
	out := make(map[string]interface{}, len(defaults)+len(raw))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range raw {
		if vm, ok := v.(map[string]interface{}); ok {
			out[k] = vm
		}
	}
	return out
}

// splitMultiAPIKeys splits a comma- or newline-delimited key string into clean individual keys.
func splitMultiAPIKeys(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		k := strings.TrimSpace(p)
		if k == "" || k == "********" {
			continue
		}
		if _, exists := seen[k]; exists {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// normalizeAndStoreMultiKeys keeps options.apiKey as a single key for OpenCode schema
// compatibility while storing any multi-key rotation list in ~/.hoxi/auto-pools.json.
func (g *Service) normalizeAndStoreMultiKeys(providerID string, cleaned map[string]interface{}) {
	if cleaned == nil || strings.TrimSpace(providerID) == "" {
		return
	}
	opts, _ := cleaned["options"].(map[string]interface{})
	if opts == nil {
		return
	}
	rawKey, _ := opts["apiKey"].(string)
	keys := splitMultiAPIKeys(rawKey)
	if len(keys) == 0 {
		return
	}
	opts["apiKey"] = keys[0]
	cleaned["options"] = opts

	doc := g.loadAutoPoolsDoc()
	keyPools, _ := doc["keyPools"].(map[string]interface{})
	if keyPools == nil {
		keyPools = map[string]interface{}{}
	}
	if len(keys) > 1 {
		keyPools[providerID] = keys
	} else {
		delete(keyPools, providerID)
	}
	doc["keyPools"] = keyPools
	_ = g.persistAutoPoolsDoc(doc)
}

func (g *Service) selectRotatedProviderKey(providerID, primaryKey string) string {
	doc := g.loadAutoPoolsDoc()
	if keyPools, ok := doc["keyPools"].(map[string]interface{}); ok {
		if rawList, ok := keyPools[providerID].([]interface{}); ok && len(rawList) > 0 {
			keys := make([]string, 0, len(rawList))
			for _, item := range rawList {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" && s != "********" {
					keys = append(keys, strings.TrimSpace(s))
				}
			}
			if len(keys) > 0 {
				idx := atomic.AddUint64(&apiKeyCursor, 1) - 1
				return keys[idx%uint64(len(keys))]
			}
		}
		if strList, ok := keyPools[providerID].([]string); ok && len(strList) > 0 {
			idx := atomic.AddUint64(&apiKeyCursor, 1) - 1
			return strList[idx%uint64(len(strList))]
		}
	}
	keys := splitMultiAPIKeys(primaryKey)
	if len(keys) > 1 {
		idx := atomic.AddUint64(&apiKeyCursor, 1) - 1
		return keys[idx%uint64(len(keys))]
	}
	return primaryKey
}

// resolveBackendAutoCandidate resolves "hoxi-auto/<poolID>" into a concrete (providerID, modelID)
// when internal gateway tasks (title summary, commit message, code audit) use an Auto Model.
func (g *Service) resolveBackendAutoCandidate(providerID, modelID string) (string, string) {
	if strings.TrimSpace(providerID) != AutoProviderID {
		return providerID, modelID
	}
	poolID := strings.TrimSpace(modelID)
	if poolID == "" {
		poolID = "free"
	}
	pools := g.loadAutoPoolsMap()
	var candidates []AutoPoolCandidate
	if poolObj, ok := pools[poolID].(map[string]interface{}); ok {
		if rawList, ok := poolObj["candidates"].([]interface{}); ok {
			for _, item := range rawList {
				if cm, ok := item.(map[string]interface{}); ok {
					pid, _ := cm["providerID"].(string)
					mid, _ := cm["modelID"].(string)
					if strings.TrimSpace(pid) != "" && strings.TrimSpace(mid) != "" && pid != AutoProviderID {
						candidates = append(candidates, AutoPoolCandidate{
							ProviderID: strings.TrimSpace(pid),
							ModelID:    strings.TrimSpace(mid),
						})
					}
				}
			}
		}
	}

	if len(candidates) == 0 {
		merged := g.loadOpenCodeImportedProviderMap()
		for k, v := range g.loadHoxiProvidersMap() {
			merged[k] = v
		}
		for pid, pVal := range merged {
			pObj, ok := pVal.(map[string]interface{})
			if !ok {
				continue
			}
			mMap, ok := pObj["models"].(map[string]interface{})
			if !ok {
				continue
			}
			for mid := range mMap {
				candidates = append(candidates, AutoPoolCandidate{
					ProviderID: pid,
					ModelID:    mid,
				})
			}
		}
	}

	if len(candidates) == 0 {
		return providerID, modelID
	}

	autoRouterMu.Lock()
	cur := autoPoolCursor[poolID]
	autoPoolCursor[poolID] = cur + 1
	autoRouterMu.Unlock()

	chosen := candidates[cur%uint64(len(candidates))]
	return chosen.ProviderID, chosen.ModelID
}

type saveAutoPoolRequest struct {
	PoolID    string                 `json:"poolID,omitempty"`
	Pool      map[string]interface{} `json:"pool,omitempty"`
	AutoPools map[string]interface{} `json:"autoPools,omitempty"`
}

func (g *Service) handleAutoPoolsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		doc := g.loadAutoPoolsDoc()
		keyCounts := map[string]int{}
		if kp, ok := doc["keyPools"].(map[string]interface{}); ok {
			for pid, list := range kp {
				if arr, ok := list.([]interface{}); ok && len(arr) > 1 {
					keyCounts[pid] = len(arr)
				} else if arrStr, ok := list.([]string); ok && len(arrStr) > 1 {
					keyCounts[pid] = len(arrStr)
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"autoPools": g.loadAutoPoolsMap(),
			"keyCounts": keyCounts,
		})

	case http.MethodPost, http.MethodPut:
		var req saveAutoPoolRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
		doc := g.loadAutoPoolsDoc()
		existing, _ := doc["autoPools"].(map[string]interface{})
		if existing == nil {
			existing = defaultAutoPoolsMap()
		}
		if req.AutoPools != nil {
			for k, v := range req.AutoPools {
				if vm, ok := v.(map[string]interface{}); ok {
					existing[strings.TrimSpace(k)] = stripNullsMap(vm)
				}
			}
		} else if pid := strings.TrimSpace(req.PoolID); pid != "" && req.Pool != nil {
			cleaned := stripNullsMap(req.Pool)
			cleaned["id"] = pid
			existing[pid] = cleaned
		} else {
			writeJSONError(w, http.StatusBadRequest, "poolID/pool or autoPools is required")
			return
		}
		doc["autoPools"] = existing
		if err := g.persistAutoPoolsDoc(doc); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"autoPools": g.loadAutoPoolsMap(),
		})

	case http.MethodDelete:
		poolID := strings.TrimSpace(r.URL.Query().Get("poolID"))
		if poolID == "" {
			writeJSONError(w, http.StatusBadRequest, "poolID is required")
			return
		}
		doc := g.loadAutoPoolsDoc()
		existing, _ := doc["autoPools"].(map[string]interface{})
		if existing != nil {
			defaults := defaultAutoPoolsMap()
			if defPool, isPreset := defaults[poolID]; isPreset {
				existing[poolID] = defPool
			} else {
				delete(existing, poolID)
			}
			doc["autoPools"] = existing
			if err := g.persistAutoPoolsDoc(doc); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"autoPools": g.loadAutoPoolsMap(),
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
