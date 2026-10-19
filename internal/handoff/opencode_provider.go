package handoff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func hoxiEngineOpenCodeDir() string {
	if custom := strings.TrimSpace(os.Getenv("HOXI_ENGINE_DATA_DIR")); custom != "" {
		return filepath.Join(filepath.Clean(custom), "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "engine-data", "opencode")
}

func externalOpenCodeConfigDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		return filepath.Join(home, ".config", "opencode")
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		return filepath.Join(appData, "opencode")
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

func readOpenCodeAuthFile(path string) map[string]interface{} {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil {
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
	for _, name := range []string{"config.json", "opencode.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) != nil {
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
	for _, name := range []string{"opencode.json", "config.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) == nil {
			if m := strings.TrimSpace(asString(doc["model"])); m != "" {
				return m
			}
		}
	}
	return ""
}

func engineOpenCodeAuthIDs() map[string]struct{} {
	dir := hoxiEngineOpenCodeDir()
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
		if ids := openCodeProviderModelIDs(provDefs, latestProvider); len(ids) > 0 {
			for _, id := range ids {
				if id == latestModel {
					return latestProvider, latestModel, ids
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

// mergeOpenCodeProviderModels additively refreshes an existing Hoxi provider with
// models discovered from the external OpenCode CLI, without clobbering user edits.
func mergeOpenCodeProviderModels(dst, src map[string]interface{}) bool {
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
	srcModels, _ := src["models"].(map[string]interface{})
	if len(srcModels) == 0 {
		return changed
	}
	dstModels, _ := dst["models"].(map[string]interface{})
	if dstModels == nil {
		dstModels = map[string]interface{}{}
	}
	for mid, m := range srcModels {
		if _, exists := dstModels[mid]; exists {
			continue
		}
		dstModels[mid] = m
		changed = true
	}
	if changed {
		dst["models"] = dstModels
	}
	return changed
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
	dir := hoxiEngineOpenCodeDir()
	if dir == "" || len(authMap) == 0 {
		return
	}
	path := filepath.Join(dir, "auth.json")
	merged := map[string]interface{}{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &merged)
	}
	for pid, cred := range authMap {
		merged[pid] = cred
	}
	_ = writePrivateJSONFile(path, merged)
}

func (g *Service) syncOpenCodeEngineAuth(authMap map[string]interface{}) {
	if len(authMap) == 0 {
		return
	}
	if g.openCodePort > 0 && g.password != "" {
		client := &http.Client{Timeout: 5 * time.Second, Transport: localTransport}
		for _, pid := range sortedStringKeys(authMap) {
			cred, ok := authMap[pid].(map[string]interface{})
			if !ok {
				continue
			}
			body, err := json.Marshal(cred)
			if err != nil {
				continue
			}
			u := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/auth/" + url.PathEscape(pid)
			req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			req.SetBasicAuth("opencode", g.password)
			if resp, err := client.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
	}
	g.mergeOpenCodeEngineAuthFile(authMap)
}

// importOpenCodeCLIProvider merges providers/models from the standalone OpenCode
// CLI config into Hoxi and copies its credentials into the managed engine.
func (g *Service) importOpenCodeCLIProvider() LocalCLIImportResponse {
	provDefs := readExternalOpenCodeConfigProviders()
	authMap := readExternalOpenCodeAuth()
	if len(provDefs) == 0 && len(authMap) == 0 {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolOpenCode),
			Error:  "No external OpenCode providers found (~/.config/opencode/opencode.json or ~/.local/share/opencode/auth.json)",
		}
	}

	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		provMap = map[string]interface{}{}
	}
	authByLower := make(map[string]map[string]interface{}, len(authMap))
	for pid, raw := range authMap {
		if cred, ok := raw.(map[string]interface{}); ok {
			authByLower[strings.ToLower(pid)] = cred
		}
	}
	changed := false
	for _, pid := range sortedStringKeys(provDefs) {
		def, _ := provDefs[pid].(map[string]interface{})
		if existingRaw, exists := provMap[pid]; exists {
			if existing, ok := existingRaw.(map[string]interface{}); ok && mergeOpenCodeProviderModels(existing, def) {
				provMap[pid] = existing
				changed = true
			}
			continue
		}
		entry := cloneStringAnyMap(def)
		if entry == nil {
			continue
		}
		cred, ok := authMap[pid].(map[string]interface{})
		if !ok {
			cred = authByLower[strings.ToLower(pid)]
		}
		if key := strings.TrimSpace(asString(cred["key"])); key != "" {
			opts, _ := entry["options"].(map[string]interface{})
			if opts == nil {
				opts = map[string]interface{}{}
			}
			if strings.TrimSpace(asString(opts["apiKey"])) == "" {
				opts["apiKey"] = key
				entry["options"] = opts
			}
		}
		provMap[pid] = entry
		changed = true
	}
	if changed {
		doc["provider"] = provMap
		if err := g.persistHoxiConfigDoc(doc); err != nil {
			return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolOpenCode), Error: err.Error()}
		}
	}

	g.syncOpenCodeEngineAuth(authMap)
	g.reloadOpenCodeGlobalConfig()

	latestProvider, latestModel := "", ""
	if defProvider, defModel := parseOpenCodeModelField(readExternalOpenCodeDefaultModel()); defProvider != "" {
		latestProvider, latestModel = defProvider, defModel
	} else if sessions, _ := scanOpenCodeSessions("", ""); len(sessions) > 0 {
		latestProvider, latestModel = parseOpenCodeModelField(sessions[0].Model)
	}
	providerID, _, models := resolveOpenCodeSyncTarget(provDefs, authMap, latestProvider, latestModel)
	if len(models) == 0 {
		models = sortedStringKeys(authMap)
	}
	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolOpenCode),
		ProviderID: providerID,
		Models:     models,
	}
}
