package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
		if userHoxiModelsPath() != "" {
			return ""
		}
		root, _ = os.Getwd()
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".hoxi", "models.json")
}

func userHoxiModelsPath() string {
	return hoxipaths.Join("models.json")
}

func openCodeConfigDir() string {
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

func readJSONMapFile(path string) map[string]interface{} {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		if sanitized := sanitizeJSONC(data); json.Unmarshal(sanitized, &out) == nil && out != nil {
			return out
		}
		if dir := openCodeConfigDir(); dir == "" || !strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(filepath.Separator)) {
			_ = os.Rename(path, path+".corrupt-"+strconv.FormatInt(time.Now().UnixMilli(), 10))
		}
		return nil
	}
	return out
}

// sanitizeJSONC strips // line comments, /* */ block comments, and trailing
// commas outside string literals so user-edited opencode.json / models.json
// files with JSONC syntax can still be parsed.
func sanitizeJSONC(raw []byte) []byte {
	s := make([]byte, 0, len(raw))
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			s = append(s, c)
			if c == '\\' && i+1 < len(raw) {
				i++
				s = append(s, raw[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			s = append(s, c)
			continue
		}
		if c == '/' && i+1 < len(raw) {
			if raw[i+1] == '/' {
				i += 2
				for i < len(raw) && raw[i] != '\n' {
					i++
				}
				if i < len(raw) {
					s = append(s, '\n')
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
		s = append(s, c)
	}
	out := make([]byte, 0, len(s))
	inString = false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(s) {
				i++
				out = append(out, s[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r' || s[j] == '\n') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		out = append(out, c)
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
	_ = f.Sync()
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
	if workspacePath == "" || filepath.Clean(userPath) == filepath.Clean(workspacePath) {
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
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" && uPath == "" {
		rootDir, _ = os.Getwd()
	}
	wPath := ""
	if rootDir != "" {
		wPath = filepath.Join(rootDir, ".hoxi", "models.json")
	}

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
			if layer, ok := overlay["skills"]; ok && layer != nil {
				skillLayers = append(skillLayers, layer)
			}
			for k, v := range overlay {
				if k == "$schema" || k == "provider" || k == "skills" || v == nil {
					continue
				}
				if oMap, ok := v.(map[string]interface{}); ok {
					merged := make(map[string]interface{}, len(oMap))
					deepMergeMap(merged, oMap)
					if curMap, ok := doc[k].(map[string]interface{}); ok {
						deepMergeMap(merged, curMap)
					}
					doc[k] = merged
					continue
				}
				if _, exists := doc[k]; !exists {
					doc[k] = v
				}
			}
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
	ensureCompactionDefaults(doc)
	normalizeApprovalPermissions(doc, approvalPolicyExists())
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

// ensureCompactionDefaults makes automatic compaction explicit instead of
// relying on the engine default. A model whose window Hoxi had to infer used to
// arrive as `limit.context == 0`, which short-circuits the engine's overflow
// check and silently disables compaction; now that the window is filled in, the
// auto flag is the last thing that must not be off by accident.
//
// A user setting is never overwritten: only a missing `compaction` object, or a
// missing `auto` key inside it, is filled in.
func ensureCompactionDefaults(doc map[string]interface{}) {
	cur, _ := doc["compaction"].(map[string]interface{})
	if cur == nil {
		cur = map[string]interface{}{}
	}
	if _, hasAuto := cur["auto"]; !hasAuto {
		cur["auto"] = true
	}
	doc["compaction"] = cur
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
	ensureCompactionDefaults(doc)
	return doc
}

func (g *Service) loadHoxiProvidersMap() map[string]interface{} {
	doc := g.loadHoxiConfigDoc()
	if provMap, ok := doc["provider"].(map[string]interface{}); ok && provMap != nil {
		if g.officialBlocked.Load() {
			delete(provMap, officialAinodeProviderID)
		}
		return provMap
	}
	return map[string]interface{}{}
}

func (g *Service) persistHoxiConfigFiles(doc map[string]interface{}) error {
	doc["$schema"] = "https://opencode.ai/config.json"
	normalizeDocModelVariants(doc)
	ensureCompactionDefaults(doc)
	normalizeApprovalPermissions(doc, approvalPolicyExists())
	return persistHoxiModelsFiles(userHoxiModelsPath(), g.workspaceHoxiModelsPath(), doc)
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
	switch r.Method {
	case http.MethodGet:
		g.configPersistMu.Lock()
		importedMap := g.loadOpenCodeImportedProviderMap()
		doc := g.loadHoxiConfigDoc()
		g.configPersistMu.Unlock()
		hoxiMap, _ := doc["provider"].(map[string]interface{})
		if g.officialBlocked.Load() {
			delete(hoxiMap, officialAinodeProviderID)
		}
		if hoxiMap == nil {
			hoxiMap = map[string]interface{}{}
		}
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

		g.configPersistMu.Lock()
		importedMap := g.loadOpenCodeImportedProviderMap()
		doc := g.loadHoxiConfigDoc()
		hoxiMap, _ := doc["provider"].(map[string]interface{})
		if hoxiMap == nil {
			hoxiMap = map[string]interface{}{}
		}

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
		err := g.persistHoxiConfigFiles(doc)
		g.configPersistMu.Unlock()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		g.reloadOpenCodeGlobalConfig()
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
		g.configPersistMu.Lock()
		importedMap := g.loadOpenCodeImportedProviderMap()
		doc := g.loadHoxiConfigDoc()
		hoxiMap, _ := doc["provider"].(map[string]interface{})
		if hoxiMap == nil {
			hoxiMap = map[string]interface{}{}
		}
		if _, inHoxi := hoxiMap[pid]; !inHoxi {
			if _, isImported := importedMap[pid]; isImported {
				g.configPersistMu.Unlock()
				writeJSONError(w, http.StatusForbidden, "OpenCode imported provider is read-only")
				return
			}
		}
		delete(hoxiMap, pid)
		doc["provider"] = hoxiMap
		err := g.persistHoxiConfigFiles(doc)
		g.configPersistMu.Unlock()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		g.reloadOpenCodeGlobalConfig()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"hoxiProviders": hoxiMap,
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
