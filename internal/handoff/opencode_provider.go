package handoff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
)

func externalOpenCodeConfigDir() string {
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); custom != "" {
		return filepath.Clean(custom)
	}
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

func externalOpenCodeAuthPaths() []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(p string) {
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dir != "" {
		add(filepath.Join(dir, "auth.json"))
	}
	if xdgData := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdgData != "" {
		add(filepath.Join(xdgData, "opencode", "auth.json"))
	}
	if home, _ := os.UserHomeDir(); home != "" {
		add(filepath.Join(home, ".local", "share", "opencode", "auth.json"))
	}
	if localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localApp != "" {
		add(filepath.Join(localApp, "opencode", "auth.json"))
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		add(filepath.Join(appData, "opencode", "auth.json"))
	}
	return out
}

func sanitizeOpenCodeJSONC(raw []byte) []byte {
	noComments := make([]byte, 0, len(raw))
	inStr := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inStr {
			noComments = append(noComments, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			noComments = append(noComments, c)
			continue
		}
		if c == '/' && i+1 < len(raw) {
			if raw[i+1] == '/' {
				i += 2
				for i < len(raw) && raw[i] != '\n' {
					i++
				}
				if i < len(raw) {
					noComments = append(noComments, '\n')
				}
				continue
			}
			if raw[i+1] == '*' {
				i += 2
				for i+1 < len(raw) && !(raw[i] == '*' && raw[i+1] == '/') {
					i++
				}
				i++
				continue
			}
		}
		noComments = append(noComments, c)
	}
	out := make([]byte, 0, len(noComments))
	inStr = false
	escaped = false
	for i := 0; i < len(noComments); i++ {
		c := noComments[i]
		if inStr {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(noComments) && (noComments[j] == ' ' || noComments[j] == '\t' || noComments[j] == '\r' || noComments[j] == '\n') {
				j++
			}
			if j < len(noComments) && (noComments[j] == '}' || noComments[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func readOpenCodeAuthFile(path string) map[string]interface{} {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil && json.Unmarshal(sanitizeOpenCodeJSONC(raw), &doc) != nil {
		return nil
	}
	return doc
}

func readExternalOpenCodeAuth() map[string]interface{} {
	for _, path := range externalOpenCodeAuthPaths() {
		if doc := readOpenCodeAuthFile(path); len(doc) > 0 {
			return doc
		}
	}
	return nil
}

func readExternalOpenCodeConfigProviders() map[string]interface{} {
	dir := externalOpenCodeConfigDir()
	if dir == "" {
		return nil
	}
	out := map[string]interface{}{}
	for _, name := range []string{"config.json", "opencode.json", "opencode.jsonc"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) != nil && json.Unmarshal(sanitizeOpenCodeJSONC(raw), &doc) != nil {
			continue
		}
		provMap, _ := doc["provider"].(map[string]interface{})
		for pid, rawProv := range provMap {
			if prov, ok := rawProv.(map[string]interface{}); ok {
				out[pid] = prov
			}
		}
	}
	return out
}

func readExternalOpenCodeDefaultModel() string {
	dir := externalOpenCodeConfigDir()
	if dir == "" {
		return ""
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc", "config.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) == nil || json.Unmarshal(sanitizeOpenCodeJSONC(raw), &doc) == nil {
			if m := strings.TrimSpace(asString(doc["model"])); m != "" {
				return m
			}
		}
	}
	return ""
}

func engineOpenCodeAuthIDs() map[string]struct{} {
	dir := hoxipaths.EngineOpenCodeDir()
	if dir == "" {
		return nil
	}
	doc := readOpenCodeAuthFile(filepath.Join(dir, "auth.json"))
	ids := make(map[string]struct{}, len(doc))
	for pid := range doc {
		ids[pid] = struct{}{}
	}
	return ids
}

func openCodeProviderModelIDs(provDefs map[string]interface{}, pid string) []string {
	prov, _ := provDefs[pid].(map[string]interface{})
	models, _ := prov["models"].(map[string]interface{})
	ids := make([]string, 0, len(models))
	for mid := range models {
		if s := strings.TrimSpace(mid); s != "" {
			ids = append(ids, s)
		}
	}
	sort.Strings(ids)
	return ids
}

func sortedStringKeys(in map[string]interface{}) []string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveOpenCodeSyncTarget picks a concrete provider/model pair that Hoxi can
// select after sync. Config-defined providers are preferred because their model
// list is known up front; auth-only or session-derived providers act as fallbacks.
func resolveOpenCodeSyncTarget(provDefs, authMap map[string]interface{}, latestProvider, latestModel string) (providerID, modelID string, models []string) {
	if latestProvider != "" && latestModel != "" {
		// Case-insensitive lookup to match case-folding elsewhere in this file
		lowerLookup := lowerKeyLookup(provDefs)
		if resolved, ok := lowerLookup[strings.ToLower(latestProvider)]; ok {
			if ids := openCodeProviderModelIDs(provDefs, resolved); len(ids) > 0 {
				for _, id := range ids {
					if id == latestModel {
						return resolved, latestModel, ids
					}
				}
			}
		}
	}
	for _, pid := range sortedStringKeys(provDefs) {
		if ids := openCodeProviderModelIDs(provDefs, pid); len(ids) > 0 {
			return pid, ids[0], ids
		}
	}
	if latestProvider != "" {
		return latestProvider, latestModel, nil
	}
	for _, pid := range sortedStringKeys(authMap) {
		return pid, "", nil
	}
	return "opencode", "", nil
}

func cloneStringAnyMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// mergeOpenCodeProviderDefinition additively refreshes an existing Hoxi provider
// with models and connection defaults discovered from the external OpenCode CLI,
// without clobbering user edits or the stored credential.
func mergeOpenCodeProviderDefinition(dst, src map[string]interface{}) bool {
	if dst == nil || src == nil {
		return false
	}
	changed := false
	if strings.TrimSpace(asString(dst["name"])) == "" {
		if name := strings.TrimSpace(asString(src["name"])); name != "" {
			dst["name"] = name
			changed = true
		}
	}
	if strings.TrimSpace(asString(dst["npm"])) == "" {
		if npm := strings.TrimSpace(asString(src["npm"])); npm != "" {
			dst["npm"] = npm
			changed = true
		}
	}
	if srcOpts, ok := src["options"].(map[string]interface{}); ok && len(srcOpts) > 0 {
		dstOpts, _ := dst["options"].(map[string]interface{})
		if dstOpts == nil {
			dstOpts = map[string]interface{}{}
		}
		optsChanged := false
		for k, v := range srcOpts {
			if k == "apiKey" || k == "headers" {
				continue
			}
			if _, exists := dstOpts[k]; exists {
				continue
			}
			dstOpts[k] = v
			optsChanged = true
		}
		if optsChanged {
			dst["options"] = dstOpts
			changed = true
		}
	}
	srcModels, _ := src["models"].(map[string]interface{})
	if len(srcModels) == 0 {
		return changed
	}
	dstModels, _ := dst["models"].(map[string]interface{})
	if dstModels == nil {
		dstModels = map[string]interface{}{}
	}
	modelsChanged := false
	for mid, m := range srcModels {
		if _, exists := dstModels[mid]; exists {
			continue
		}
		dstModels[mid] = m
		modelsChanged = true
	}
	if modelsChanged {
		dst["models"] = dstModels
		changed = true
	}
	return changed
}

func lowerKeyLookup(m map[string]interface{}) map[string]string {
	out := make(map[string]string, len(m))
	// Deterministic iteration order: prefer the lexicographically first exact key
	// when multiple keys map to the same lowercase (e.g. "Hoxi" vs "hoxi").
	for _, k := range sortedStringKeys(m) {
		lower := strings.ToLower(k)
		if _, exists := out[lower]; !exists {
			out[lower] = k
		}
	}
	return out
}

// resolveOpenCodeProviderID maps an external OpenCode provider id onto the id Hoxi
// should write. auth.json wins because OpenCode resolves credentials by exact
// provider id; otherwise reuse an existing Hoxi provider regardless of case.
func resolveOpenCodeProviderID(pid string, authMap, hoxiProvMap map[string]interface{}) string {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return ""
	}
	if _, ok := authMap[pid]; ok {
		return pid
	}
	lower := strings.ToLower(pid)
	if key, ok := lowerKeyLookup(authMap)[lower]; ok {
		return key
	}
	if key, ok := lowerKeyLookup(hoxiProvMap)[lower]; ok {
		return key
	}
	return pid
}

func stringListFromAny(v interface{}) []string {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...)
	case []interface{}:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s := strings.TrimSpace(asString(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writePrivateJSONFile(path string, doc map[string]interface{}) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".auth-*")
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

// mergeOpenCodeEngineAuthFile keeps the Hoxi engine's auth.json in step with the
// external OpenCode CLI so credentials survive restarts even when no server is running.
func (g *Service) mergeOpenCodeEngineAuthFile(authMap map[string]interface{}) {
	dir := hoxipaths.EngineOpenCodeDir()
	if dir == "" || len(authMap) == 0 {
		return
	}
	path := filepath.Join(dir, "auth.json")
	merged := map[string]interface{}{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &merged)
	}
	if merged == nil {
		merged = map[string]interface{}{}
	}
	for pid, cred := range authMap {
		merged[pid] = cred
	}
	_ = writePrivateJSONFile(path, merged)
}

// sendEngineAuth mirrors one credential into the running engine's
// /auth/<providerID>; a nil cred deletes it. Best effort: the engine auth.json
// written next to it is the durable copy.
func (g *Service) sendEngineAuth(providerID string, cred map[string]interface{}, timeout time.Duration) {
	if !g.openCode.Available() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if cred == nil {
		_ = g.openCode.JSON(ctx, http.MethodDelete, "/auth/"+url.PathEscape(providerID), nil, nil, nil)
		return
	}
	_ = g.openCode.JSON(ctx, http.MethodPut, "/auth/"+url.PathEscape(providerID), nil, cred, nil)
}

func (g *Service) syncOpenCodeEngineAuth(authMap map[string]interface{}) {
	if len(authMap) == 0 {
		return
	}
	for _, pid := range sortedStringKeys(authMap) {
		if cred, ok := authMap[pid].(map[string]interface{}); ok {
			g.sendEngineAuth(pid, cred, 5*time.Second)
		}
	}
	g.mergeOpenCodeEngineAuthFile(authMap)
}

// purgeStaleConsoleSessionAuth removes any manually-pasted OpenCode Console
// session tokens (`st_...`) for `opencode` / `opencode-go` from Hoxi's engine
// auth.json when a live Console account is synced in hoxi.db and external
// auth.json does not define a static API key for that provider.
func (g *Service) purgeStaleConsoleSessionAuth(externalAuth map[string]interface{}) {
	dir := hoxipaths.EngineOpenCodeDir()
	if dir == "" {
		return
	}
	path := filepath.Join(dir, "auth.json")
	merged := map[string]interface{}{}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &merged) != nil {
		return
	}
	modified := false
	for _, pid := range []string{"opencode-go", "opencode"} {
		if _, inExternal := externalAuth[pid]; inExternal {
			continue
		}
		cred, ok := merged[pid].(map[string]interface{})
		if !ok {
			continue
		}
		key := strings.TrimSpace(asString(cred["key"]))
		if strings.HasPrefix(key, "st_") {
			delete(merged, pid)
			modified = true
			g.sendEngineAuth(pid, nil, 3*time.Second)
		}
	}
	if modified {
		_ = writePrivateJSONFile(path, merged)
	}
}

func (g *Service) fetchLiveOpenCodeProviderModels(providerID string) (defaultModel string, models []string) {
	providerID = strings.TrimSpace(providerID)
	if g == nil || !g.openCode.Available() || providerID == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var payload struct {
		All []struct {
			ID     string                 `json:"id"`
			Models map[string]interface{} `json:"models"`
		} `json:"all"`
		Default map[string]string `json:"default"`
	}
	if g.openCode.JSON(ctx, http.MethodGet, "/provider", nil, nil, &payload) != nil {
		return "", nil
	}
	defaultModel = strings.TrimSpace(payload.Default[providerID])
	for _, p := range payload.All {
		if p.ID == providerID {
			for mid := range p.Models {
				if s := strings.TrimSpace(mid); s != "" {
					models = append(models, s)
				}
			}
			sort.Strings(models)
			break
		}
	}
	return defaultModel, models
}

// importOpenCodeCLIProvider merges providers/models from the standalone OpenCode
// CLI config into Hoxi and copies its credentials into the managed engine.
func (g *Service) importOpenCodeCLIProvider() LocalCLIImportResponse {
	provDefs := readExternalOpenCodeConfigProviders()
	authMap := readExternalOpenCodeAuth()
	consoleState := ocstate.ReadExternalConsoleState()
	if len(provDefs) == 0 && len(authMap) == 0 && consoleState == nil {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolOpenCode),
			Error:  "No external OpenCode providers found (~/.config/opencode/opencode.json, ~/.local/share/opencode/auth.json, or ~/.local/share/opencode/opencode.db)",
		}
	}
	if consoleState != nil {
		if err := ocstate.SyncConsoleState(consoleState, hoxipaths.DBPath()); err != nil {
			return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolOpenCode), Error: err.Error()}
		}
		g.purgeStaleConsoleSessionAuth(authMap)
	}

	authByLower := make(map[string]map[string]interface{}, len(authMap))
	for pid, raw := range authMap {
		if cred, ok := raw.(map[string]interface{}); ok {
			authByLower[strings.ToLower(pid)] = cred
		}
	}
	g.syncOpenCodeEngineAuth(authMap)
	var mutated bool
	if err := g.updateHoxiConfigDoc(func(doc map[string]interface{}) error {
		provMap, _ := doc["provider"].(map[string]interface{})
		if provMap == nil {
			provMap = map[string]interface{}{}
		}
		enabledIDs := make([]string, 0, len(provDefs))
		foldedAliases := make([]string, 0, len(provDefs))
		for _, pid := range sortedStringKeys(provDefs) {
			def, _ := provDefs[pid].(map[string]interface{})
			if def == nil {
				continue
			}
			targetID := resolveOpenCodeProviderID(pid, authMap, provMap)
			if targetID == "" {
				targetID = pid
			}
			enabledIDs = append(enabledIDs, targetID)

			// Fold a case-only duplicate (e.g. config "Hoxi" vs credential "hoxi")
			// into the credentialed provider instead of leaving a dead second entry.
			if targetID != pid {
				foldedAliases = append(foldedAliases, pid)
				if dupRaw, exists := provMap[pid]; exists {
					if dup, ok := dupRaw.(map[string]interface{}); ok {
						if targetRaw, ok := provMap[targetID].(map[string]interface{}); ok {
							mergeOpenCodeProviderDefinition(targetRaw, dup)
						} else {
							provMap[targetID] = cloneStringAnyMap(dup)
						}
					}
					delete(provMap, pid)
					mutated = true
				}
			}

			targetRaw, exists := provMap[targetID]
			target, _ := targetRaw.(map[string]interface{})
			if target == nil {
				target = map[string]interface{}{}
				exists = false
			}
			if mergeOpenCodeProviderDefinition(target, def) || !exists {
				provMap[targetID] = target
				mutated = true
			}
			if cred := authByLower[strings.ToLower(targetID)]; cred != nil {
				if key := strings.TrimSpace(asString(cred["key"])); key != "" {
					opts, _ := target["options"].(map[string]interface{})
					if opts == nil {
						opts = map[string]interface{}{}
					}
					if strings.TrimSpace(asString(opts["apiKey"])) == "" {
						opts["apiKey"] = key
						target["options"] = opts
						mutated = true
					}
				}
			}
		}

		// Sync is an explicit request to make the merged provider usable, so enable
		// the credential-backed id. The folded alias must stay disabled: upstream
		// OpenCode also loads ~/.config/opencode/opencode.json, and without the flag
		// that file would re-register the duplicate (e.g. "Hoxi") next to the
		// synced provider (e.g. "hoxi").
		if len(enabledIDs) > 0 || len(foldedAliases) > 0 {
			disabled := stringListFromAny(doc["disabled_providers"])
			// Provider ids are case-sensitive upstream, so only the exact target id
			// is re-enabled; the differently-cased alias is disabled on purpose.
			enabledExact := make(map[string]struct{}, len(enabledIDs))
			for _, id := range enabledIDs {
				enabledExact[id] = struct{}{}
			}
			next := make([]string, 0, len(disabled)+len(foldedAliases))
			seen := make(map[string]struct{}, len(disabled)+len(foldedAliases))
			for _, id := range disabled {
				if _, drop := enabledExact[id]; drop {
					continue
				}
				if _, duplicate := seen[id]; duplicate {
					continue
				}
				seen[id] = struct{}{}
				next = append(next, id)
			}
			for _, alias := range foldedAliases {
				if _, isTarget := enabledExact[alias]; isTarget {
					continue
				}
				if _, duplicate := seen[alias]; duplicate {
					continue
				}
				seen[alias] = struct{}{}
				next = append(next, alias)
			}
			if !equalStringSlices(next, disabled) {
				doc["disabled_providers"] = next
				mutated = true
			}
		}
		doc["provider"] = provMap
		return nil
	}); err != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolOpenCode), Error: err.Error()}
	}
	if !mutated && (len(authMap) > 0 || consoleState != nil) {
		g.reloadOpenCodeGlobalConfig()
	}

	latestProvider, latestModel := "", ""
	if defProvider, defModel := parseOpenCodeModelField(readExternalOpenCodeDefaultModel()); defProvider != "" {
		latestProvider, latestModel = defProvider, defModel
	} else if sessions, _ := scanOpenCodeSessions("", ""); len(sessions) > 0 {
		latestProvider, latestModel = parseOpenCodeModelField(sessions[0].Model)
	}
	if consoleState != nil && latestProvider == "" && len(provDefs) == 0 && len(authMap) == 0 {
		latestProvider = "opencode-go"
	}
	providerID, _, models := resolveOpenCodeSyncTarget(provDefs, authMap, latestProvider, latestModel)
	latestProviders := g.loadHoxiProvidersMap()
	providerID = resolveOpenCodeProviderID(providerID, authMap, latestProviders)
	models = appendUniqueString(models, openCodeProviderModelIDs(latestProviders, providerID)...)
	_, liveModels := g.fetchLiveOpenCodeProviderModels(providerID)
	models = appendUniqueString(models, liveModels...)
	// If no models found, return empty to signal the need for user configuration.
	// Returning provider IDs as model IDs would be semantically incorrect.
	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolOpenCode),
		ProviderID: providerID,
		Models:     models,
	}
}
