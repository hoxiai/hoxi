package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type disconnectProviderRequest struct {
	ProviderID string `json:"providerID"`
}

// SetOnProviderDisconnected registers an optional hook notified after a
// provider's local configuration and credentials are disconnected.
func (s *Service) SetOnProviderDisconnected(fn func(providerID string)) {
	s.configPersistMu.Lock()
	s.onProviderDisconnected = fn
	s.configPersistMu.Unlock()
}

func (g *Service) dismissedExternalProvidersSet() map[string]bool {
	out := map[string]bool{}
	if g == nil {
		return out
	}
	doc := g.loadAutoPoolsDoc()
	raw, ok := doc["dismissedExternalProviders"].(map[string]interface{})
	if !ok || len(raw) == 0 {
		return out
	}
	for k, v := range raw {
		enabled, ok := v.(bool)
		if !ok || !enabled {
			continue
		}
		trimmed := strings.TrimSpace(k)
		if trimmed == "" {
			continue
		}
		out[trimmed] = true
		out[strings.ToLower(trimmed)] = true
	}
	return out
}

// IsExternalProviderDismissed reports whether an external fallback provider
// (e.g. ~/.config/opencode or ~/.local/share/opencode/auth.json) was explicitly
// removed in Hoxi and has not been re-imported or reconnected since.
func (g *Service) IsExternalProviderDismissed(providerID string) bool {
	pid := strings.TrimSpace(providerID)
	if pid == "" {
		return false
	}
	set := g.dismissedExternalProvidersSet()
	return set[pid] || set[strings.ToLower(pid)]
}

func removeProvidersFromDisabledDoc(doc map[string]interface{}, providerIDs ...string) bool {
	if doc == nil || len(providerIDs) == 0 {
		return false
	}
	drop := make(map[string]bool, len(providerIDs))
	for _, raw := range providerIDs {
		if id := strings.TrimSpace(raw); id != "" {
			drop[id] = true
		}
	}
	if len(drop) == 0 {
		return false
	}
	var existing []string
	switch raw := doc["disabled_providers"].(type) {
	case []interface{}:
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				existing = append(existing, strings.TrimSpace(s))
			}
		}
	case []string:
		for _, s := range raw {
			if strings.TrimSpace(s) != "" {
				existing = append(existing, strings.TrimSpace(s))
			}
		}
	default:
		return false
	}
	next := make([]interface{}, 0, len(existing))
	mutated := false
	for _, id := range existing {
		if drop[id] {
			mutated = true
			continue
		}
		next = append(next, id)
	}
	if !mutated {
		return false
	}
	if len(next) == 0 {
		delete(doc, "disabled_providers")
	} else {
		doc["disabled_providers"] = next
	}
	return true
}

func clearDismissedInAutoPoolsDoc(autoDoc map[string]interface{}, providerIDs ...string) bool {
	if autoDoc == nil || len(providerIDs) == 0 {
		return false
	}
	raw, ok := autoDoc["dismissedExternalProviders"].(map[string]interface{})
	if !ok || len(raw) == 0 {
		return false
	}
	targets := make(map[string]bool, len(providerIDs)*2)
	for _, pid := range providerIDs {
		trimmed := strings.TrimSpace(pid)
		if trimmed != "" {
			targets[trimmed] = true
			targets[strings.ToLower(trimmed)] = true
		}
	}
	mutated := false
	for k := range raw {
		if targets[k] || targets[strings.ToLower(strings.TrimSpace(k))] {
			delete(raw, k)
			mutated = true
		}
	}
	if mutated {
		if len(raw) == 0 {
			delete(autoDoc, "dismissedExternalProviders")
		} else {
			autoDoc["dismissedExternalProviders"] = raw
		}
	}
	return mutated
}

func deleteKeyPoolInAutoPoolsDoc(autoDoc map[string]interface{}, providerID string) bool {
	pid := strings.TrimSpace(providerID)
	if autoDoc == nil || pid == "" {
		return false
	}
	keyPools, ok := autoDoc["keyPools"].(map[string]interface{})
	if !ok || len(keyPools) == 0 {
		return false
	}
	if _, exists := keyPools[pid]; !exists {
		return false
	}
	delete(keyPools, pid)
	autoDoc["keyPools"] = keyPools
	return true
}

func (g *Service) clearProviderSuppressionLocked(doc map[string]interface{}, providerIDs ...string) bool {
	docMutated := removeProvidersFromDisabledDoc(doc, providerIDs...)
	autoDoc := g.loadAutoPoolsDoc()
	if clearDismissedInAutoPoolsDoc(autoDoc, providerIDs...) {
		_ = g.persistAutoPoolsDoc(autoDoc)
	}
	return docMutated
}

// ClearDismissedExternalProviders clears any external-dismissal or disabled
// suppression for providerIDs when a user explicitly imports or reconnects them.
func (g *Service) ClearDismissedExternalProviders(providerIDs ...string) error {
	if g == nil || len(providerIDs) == 0 {
		return nil
	}
	g.configPersistMu.Lock()
	doc := g.loadHoxiConfigDoc()
	docMutated := removeProvidersFromDisabledDoc(doc, providerIDs...)
	var cfgErr error
	if docMutated {
		cfgErr = g.persistHoxiConfigFiles(doc)
	}
	autoDoc := g.loadAutoPoolsDoc()
	var autoErr error
	if clearDismissedInAutoPoolsDoc(autoDoc, providerIDs...) {
		autoErr = g.persistAutoPoolsDoc(autoDoc)
	}
	g.configPersistMu.Unlock()
	if docMutated && cfgErr == nil {
		g.reloadOpenCodeGlobalConfig()
	}
	if cfgErr != nil {
		return cfgErr
	}
	return autoErr
}

func hasHoxiEngineAuthEntry(providerID string) bool {
	pid := strings.TrimSpace(providerID)
	enginePath := strings.TrimSpace(hoxipaths.EngineAuthPath())
	if pid == "" || enginePath == "" {
		return false
	}
	raw, err := os.ReadFile(enginePath)
	if err != nil {
		return false
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		return false
	}
	return isValidUnrevokedAuthEntry(parsed[pid])
}

func (g *Service) unrevokedAuthProviderIDs() map[string]bool {
	dismissed := g.dismissedExternalProvidersSet()
	if len(dismissed) == 0 {
		return unrevokedAuthProviderIDs()
	}
	authIDs := map[string]bool{}
	decided := map[string]bool{}
	engineClean := filepath.Clean(strings.TrimSpace(hoxipaths.EngineAuthPath()))
	for _, authPath := range candidateProviderAuthPaths() {
		isEngine := engineClean != "" && filepath.Clean(authPath) == engineClean
		raw, err := os.ReadFile(authPath)
		if err != nil {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) != nil {
			continue
		}
		for k, v := range parsed {
			pid := strings.TrimSpace(k)
			if pid == "" || decided[pid] {
				continue
			}
			if !isEngine && (dismissed[pid] || dismissed[strings.ToLower(pid)]) {
				continue
			}
			decided[pid] = true
			if isValidUnrevokedAuthEntry(v) {
				authIDs[pid] = true
			}
		}
	}
	return authIDs
}

func matchingExternalOpenCodeKeys(providerID string) []string {
	pid := strings.TrimSpace(providerID)
	if pid == "" {
		return nil
	}
	lower := strings.ToLower(pid)
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range loadOpenCodeImportedProvidersFromDisk() {
		if k == pid || strings.ToLower(strings.TrimSpace(k)) == lower {
			add(k)
		}
	}
	engineClean := filepath.Clean(strings.TrimSpace(hoxipaths.EngineAuthPath()))
	for _, authPath := range candidateProviderAuthPaths() {
		if engineClean != "" && filepath.Clean(authPath) == engineClean {
			continue
		}
		raw, err := os.ReadFile(authPath)
		if err != nil {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) != nil {
			continue
		}
		for k, v := range parsed {
			if (k == pid || strings.ToLower(strings.TrimSpace(k)) == lower) && isValidUnrevokedAuthEntry(v) {
				add(k)
			}
		}
	}
	return out
}

// DisconnectProvider removes a provider's Hoxi config, Hoxi engine credentials,
// and rotated key pool, while recording a local dismissal marker if an external
// read-only OpenCode config/auth file still holds the same provider on disk.
func (g *Service) DisconnectProvider(providerID string) error {
	pid := strings.TrimSpace(providerID)
	if pid == "" || pid == AutoProviderID {
		return fmt.Errorf("invalid providerID")
	}

	extKeys := matchingExternalOpenCodeKeys(pid)

	g.configPersistMu.Lock()
	var persistErr error

	doc := g.loadHoxiConfigDoc()
	if hoxiMap, ok := doc["provider"].(map[string]interface{}); ok && hoxiMap != nil {
		if _, exists := hoxiMap[pid]; exists {
			delete(hoxiMap, pid)
			doc["provider"] = hoxiMap
			persistErr = g.persistHoxiConfigFiles(doc)
		}
	}

	if persistErr == nil {
		if dir := hoxipaths.EngineOpenCodeDir(); dir != "" {
			path := filepath.Join(dir, "auth.json")
			if raw, err := os.ReadFile(path); err == nil {
				var merged map[string]interface{}
				if err := json.Unmarshal(raw, &merged); err != nil {
					persistErr = fmt.Errorf("read engine credentials: %w", err)
				} else if merged != nil {
					if _, exists := merged[pid]; exists {
						delete(merged, pid)
						persistErr = writeJSONMapFile(path, merged)
					}
				}
			} else if !os.IsNotExist(err) {
				persistErr = err
			}
		}
	}

	if persistErr == nil {
		autoDoc := g.loadAutoPoolsDoc()
		autoMutated := deleteKeyPoolInAutoPoolsDoc(autoDoc, pid)
		if len(extKeys) > 0 || pid == "opencode-go" || pid == "opencode" {
			dismissed, _ := autoDoc["dismissedExternalProviders"].(map[string]interface{})
			if dismissed == nil {
				dismissed = map[string]interface{}{}
			}
			dismissed[pid] = true
			for _, k := range extKeys {
				dismissed[k] = true
			}
			autoDoc["dismissedExternalProviders"] = dismissed
			autoMutated = true
		}
		if autoMutated {
			persistErr = g.persistAutoPoolsDoc(autoDoc)
		}
	}

	notifyFn := g.onProviderDisconnected
	g.configPersistMu.Unlock()

	if persistErr != nil {
		return persistErr
	}

	if g.openCode != nil && g.openCode.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = g.openCode.JSON(ctx, http.MethodDelete, "/auth/"+url.PathEscape(pid), nil, nil, nil)
		cancel()
	}
	g.reloadOpenCodeGlobalConfig()

	if notifyFn != nil {
		notifyFn(pid)
	}
	return nil
}

func (g *Service) handleDisconnectProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST or DELETE required")
		return
	}
	pid := strings.TrimSpace(r.URL.Query().Get("providerID"))
	if pid == "" && r.Body != nil {
		var req disconnectProviderRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err == nil {
			pid = strings.TrimSpace(req.ProviderID)
		}
	}
	if pid == "" || pid == AutoProviderID {
		writeJSONError(w, http.StatusBadRequest, "valid providerID is required")
		return
	}
	if err := g.DisconnectProvider(pid); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "providerID": pid})
}
