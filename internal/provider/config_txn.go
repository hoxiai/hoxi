package provider

import "reflect"

// Config writes share one transaction so a writer that read an older snapshot
// cannot drop providers another writer just saved. UpdateHoxiConfigDoc is the
// only way to perform read-modify-write; it reloads the latest document inside
// configPersistMu. PersistHoxiConfigDoc stays available for callers that hold a
// complete document, but it preserves provider entries the caller never saw so
// a stale import cannot erase freshly saved credentials.

// UpdateHoxiConfigDoc applies mutate to the latest on-disk config inside the
// write transaction, then persists and reloads the engine. A mutation that
// changes nothing is a no-op, so callers can probe freely.
func (s *Service) UpdateHoxiConfigDoc(mutate func(doc map[string]interface{}) error) error {
	if mutate == nil {
		return nil
	}
	s.configPersistMu.Lock()
	doc := s.loadHoxiConfigDoc()
	before := cloneConfigDoc(doc)
	if err := mutate(doc); err != nil {
		s.configPersistMu.Unlock()
		return err
	}
	if reflect.DeepEqual(before, doc) {
		s.configPersistMu.Unlock()
		return nil
	}
	err := s.persistHoxiConfigFiles(doc)
	s.configPersistMu.Unlock()
	if err != nil {
		return err
	}
	s.reloadOpenCodeGlobalConfig()
	return nil
}

func (s *Service) persistMergedConfigDoc(doc map[string]interface{}) error {
	s.configPersistMu.Lock()
	merged := mergeConfigDocPreservingProviders(doc, s.loadHoxiConfigDoc())
	err := s.persistHoxiConfigFiles(merged)
	s.configPersistMu.Unlock()
	if err != nil {
		return err
	}
	s.reloadOpenCodeGlobalConfig()
	return nil
}

// mergeConfigDocPreservingProviders returns a copy of incoming where providers
// present on disk but absent from incoming are retained. It never widens a
// delete: removing a provider goes through UpdateHoxiConfigDoc.
func mergeConfigDocPreservingProviders(incoming, current map[string]interface{}) map[string]interface{} {
	out := cloneConfigDoc(incoming)
	if out == nil {
		out = map[string]interface{}{}
	}
	currentProviders, _ := current["provider"].(map[string]interface{})
	if len(currentProviders) == 0 {
		return out
	}
	outProviders, _ := out["provider"].(map[string]interface{})
	if outProviders == nil {
		outProviders = map[string]interface{}{}
	}
	for id, def := range currentProviders {
		if _, seen := outProviders[id]; !seen {
			outProviders[id] = def
		}
	}
	out["provider"] = outProviders
	return out
}

func cloneConfigDoc(doc map[string]interface{}) map[string]interface{} {
	if doc == nil {
		return nil
	}
	out := make(map[string]interface{}, len(doc))
	for key, value := range doc {
		out[key] = cloneConfigValue(value)
	}
	return out
}

func cloneConfigValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = cloneConfigValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = cloneConfigValue(item)
		}
		return out
	default:
		return value
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
	"compaction",
}

// overlayHoxiGlobalConfig makes .hoxi/models.json authoritative over the OpenCode
// config snapshot loaded at process start, so routing roles (small_model,
// agent models and agent permissions) reflect edits immediately without a daemon restart.
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
			dMap := make(map[string]interface{}, len(vMap))
			deepMergeMap(dMap, vMap)
			if len(dMap) > 0 || len(vMap) == 0 {
				dst[k] = dMap
			}
			continue
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
