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
