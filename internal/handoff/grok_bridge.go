package handoff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const grokUpstreamBaseURL = "https://cli-chat-proxy.grok.com"

func resolveGrokUpstreamBaseURL() string {
	for _, key := range []string{"HOXI_GROK_UPSTREAM_URL", "GROK_BASE_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(key)), "/"); strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			return v
		}
	}
	if gDir := grokHomeDir(); gDir != "" {
		if setBytes, err := os.ReadFile(filepath.Join(gDir, "settings.json")); err == nil {
			var setDoc map[string]interface{}
			if json.Unmarshal(setBytes, &setDoc) == nil {
				if u := strings.TrimRight(firstNonEmpty(asString(setDoc["baseURL"]), asString(setDoc["base_url"]), asString(setDoc["apiBase"])), "/"); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					return u
				}
			}
		}
	}
	return grokUpstreamBaseURL
}

var grokBridgeTransport http.RoundTripper = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	IdleConnTimeout: 90 * time.Second,
}

type grokLocalCredentials struct {
	APIKey       string
	Version      string
	DefaultModel string
	Models       []string
}

func appendUniqueString(list []string, items ...string) []string {
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		exists := false
		for _, cur := range list {
			if cur == it {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, it)
		}
	}
	return list
}

func loadGrokLocalCredentials() grokLocalCredentials {
	out := grokLocalCredentials{}
	gDir := grokHomeDir()
	if gDir == "" {
		return out
	}

	out.APIKey = readGrokAPIKey(filepath.Join(gDir, "auth.json"), time.Now())

	if verData, err := os.ReadFile(filepath.Join(gDir, "version.json")); err == nil {
		var verObj struct {
			LatestVersion string `json:"latest_version"`
		}
		if json.Unmarshal(verData, &verObj) == nil && strings.TrimSpace(verObj.LatestVersion) != "" {
			out.Version = strings.TrimSpace(verObj.LatestVersion)
		}
	}

	if setBytes, err := os.ReadFile(filepath.Join(gDir, "settings.json")); err == nil {
		var setDoc map[string]interface{}
		if json.Unmarshal(setBytes, &setDoc) == nil {
			out.DefaultModel = firstNonEmpty(asString(setDoc["model"]), asString(setDoc["default_model"]), asString(setDoc["current_model_id"]))
		}
	}
	if tomlBytes, err := os.ReadFile(filepath.Join(gDir, "config.toml")); err == nil {
		for _, line := range strings.Split(string(tomlBytes), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				k := strings.TrimSpace(parts[0])
				v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				if (k == "model" || k == "default_model" || k == "fork_secondary_model") && v != "" {
					out.Models = appendUniqueString(out.Models, v)
					if k != "fork_secondary_model" && out.DefaultModel == "" {
						out.DefaultModel = v
					}
				}
			}
		}
	}

	if modData, err := os.ReadFile(filepath.Join(gDir, "models_cache.json")); err == nil {
		var rawDoc map[string]interface{}
		if json.Unmarshal(modData, &rawDoc) == nil {
			if out.Version == "" {
				out.Version = strings.TrimSpace(asString(rawDoc["grok_version"]))
			}
			if dm := strings.TrimSpace(asString(rawDoc["default_model"])); dm != "" && out.DefaultModel == "" {
				out.DefaultModel = dm
			}
			switch mv := rawDoc["models"].(type) {
			case []interface{}:
				for _, item := range mv {
					if s := strings.TrimSpace(asString(item)); s != "" {
						out.Models = appendUniqueString(out.Models, s)
					}
				}
			case map[string]interface{}:
				var mapModels []string
				for k, v := range mv {
					if vm, ok := v.(map[string]interface{}); ok {
						if info, ok := vm["info"].(map[string]interface{}); ok {
							if hidden, _ := info["hidden"].(bool); hidden {
								continue
							}
							if id := strings.TrimSpace(asString(info["id"])); id != "" {
								mapModels = appendUniqueString(mapModels, id)
								continue
							}
						}
					}
					if strings.TrimSpace(k) != "" {
						mapModels = appendUniqueString(mapModels, strings.TrimSpace(k))
					}
				}
				sort.Slice(mapModels, func(i, j int) bool { return mapModels[i] > mapModels[j] })
				out.Models = appendUniqueString(out.Models, mapModels...)
			}
		}
	}
	if out.DefaultModel == "" && len(out.Models) > 0 {
		out.DefaultModel = out.Models[0]
	}
	if out.DefaultModel != "" {
		out.Models = appendUniqueString([]string{out.DefaultModel}, out.Models...)
	}
	return out
}

const (
	grokProactiveRefreshWindow = 5 * time.Minute
	grokRefreshCooldown        = 15 * time.Second
	grokRefreshTimeout         = 15 * time.Second
)

var (
	grokRefreshMu       sync.Mutex
	lastGrokRefreshAt   time.Time
	lastGrokRefreshHome string
	lastGrokForceKey    string
)

// grokAuthEntry is one credential in ~/.grok/auth.json. The Grok CLI refreshes
// the file itself; Hoxi only reads it and must never write a token back.
type grokAuthEntry struct {
	Key          string `json:"key"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
}

type grokAuthState struct {
	apiKey       string
	bestExpiry   time.Time
	isExpired    bool
	expiringSoon bool
	canRefresh   bool
}

func (s grokAuthState) needsRefresh() bool {
	return s.canRefresh && (s.isExpired || s.expiringSoon)
}

func inspectGrokAuthFile(path string, now time.Time) grokAuthState {
	authData, err := os.ReadFile(path)
	if err != nil {
		return grokAuthState{}
	}
	var authMap map[string]grokAuthEntry
	if json.Unmarshal(authData, &authMap) != nil {
		return grokAuthState{}
	}
	var bestKey string
	var bestExpiry time.Time
	var fallbackKey string
	var fallbackExpiry time.Time
	canRefresh := false
	keys := make([]string, 0, len(authMap))
	for k := range authMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry := authMap[k]
		key := firstNonEmpty(strings.TrimSpace(entry.Key), strings.TrimSpace(entry.AccessToken))
		if key == "" {
			continue
		}
		expiry, hasExpiry := parseGrokExpiry(entry.ExpiresAt)
		if !hasExpiry {
			return grokAuthState{apiKey: key}
		}
		if strings.TrimSpace(entry.RefreshToken) != "" {
			canRefresh = true
		}
		if fallbackKey == "" || expiry.After(fallbackExpiry) {
			fallbackKey = key
			fallbackExpiry = expiry
		}
		if !expiry.After(now) {
			continue
		}
		if bestKey == "" || expiry.After(bestExpiry) {
			bestKey = key
			bestExpiry = expiry
		}
	}
	if bestKey != "" {
		return grokAuthState{
			apiKey:       bestKey,
			bestExpiry:   bestExpiry,
			expiringSoon: !bestExpiry.After(now.Add(grokProactiveRefreshWindow)),
			canRefresh:   canRefresh,
		}
	}
	return grokAuthState{
		apiKey:     fallbackKey,
		bestExpiry: fallbackExpiry,
		isExpired:  fallbackKey != "",
		canRefresh: canRefresh,
	}
}

// readGrokAPIKey returns the newest credential that has not expired. A key
// without expires_at never expires. When every key is expired the newest one
// is returned anyway: the CLI may not have refreshed yet, and a stale key
// fails the request instead of silently dropping the provider.
func readGrokAPIKey(path string, now time.Time) string {
	return inspectGrokAuthFile(path, now).apiKey
}

func parseGrokExpiry(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func belongsToOtherGrokHome(binPath, activeGrokHome string) bool {
	if activeGrokHome == "" {
		return false
	}
	want := comparableFolderPath(activeGrokHome)
	for _, cand := range []string{binPath, evalSymlinksOrSelf(binPath)} {
		slash := filepath.ToSlash(filepath.Clean(cand))
		if idx := strings.Index(slash, "/.grok/bin/"); idx > 0 {
			owner := filepath.FromSlash(slash[:idx] + "/.grok")
			if comparableFolderPath(owner) != want {
				return true
			}
		}
	}
	return false
}

func evalSymlinksOrSelf(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != "" {
		return resolved
	}
	return p
}

func resolveGrokBinary() string {
	if env := strings.TrimSpace(os.Getenv("GROK_BIN")); env != "" {
		return agentcli.FirstExecutableFile(env)
	}
	gDir := grokHomeDir()
	if gDir != "" {
		if p := agentcli.FirstExecutableFile(filepath.Join(gDir, "bin", "grok")); p != "" {
			return p
		}
		if strings.TrimSpace(os.Getenv("GROK_HOME")) != "" {
			return ""
		}
	}
	if p, err := exec.LookPath("grok"); err == nil && !belongsToOtherGrokHome(p, gDir) {
		return p
	}
	var candidates []string
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "grok"),
			filepath.Join(home, ".grok", "bin", "grok"),
		)
	}
	for _, dir := range agentcli.CommonUserBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "grok"))
	}
	for _, c := range candidates {
		if p := agentcli.FirstExecutableFile(c); p != "" && !belongsToOtherGrokHome(p, gDir) {
			return p
		}
	}
	return ""
}

// refreshGrokAuthViaCLI asks the local `grok` CLI (`grok models`) to rotate an
// expired or near-expiry OIDC session token in ~/.grok/auth.json using its own
// lock file and refresh_token flow. Hoxi never writes ~/.grok/auth.json itself.
func refreshGrokAuthViaCLI(ctx context.Context, forceInvalidate bool) bool {
	gDir := grokHomeDir()
	if gDir == "" {
		return false
	}
	authPath := filepath.Join(gDir, "auth.json")

	grokRefreshMu.Lock()
	defer grokRefreshMu.Unlock()

	now := time.Now()
	if comparableFolderPath(gDir) != lastGrokRefreshHome {
		lastGrokRefreshHome = comparableFolderPath(gDir)
		lastGrokRefreshAt = time.Time{}
		lastGrokForceKey = ""
	}
	before := inspectGrokAuthFile(authPath, now)
	if before.apiKey == "" || !before.canRefresh {
		return false
	}
	if !forceInvalidate && !before.needsRefresh() {
		return true
	}
	if !forceInvalidate {
		if !lastGrokRefreshAt.IsZero() && now.Sub(lastGrokRefreshAt) < grokRefreshCooldown {
			return false
		}
	} else if lastGrokForceKey == before.apiKey && !lastGrokRefreshAt.IsZero() && now.Sub(lastGrokRefreshAt) < grokRefreshCooldown {
		return false
	}

	grokBin := resolveGrokBinary()
	if grokBin == "" {
		return false
	}
	lastGrokRefreshAt = now
	if forceInvalidate {
		lastGrokForceKey = before.apiKey
	}

	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(baseCtx, grokRefreshTimeout)
	defer cancel()

	cmd := subproc.CommandContext(runCtx, grokBin, "models")
	cmd.WaitDelay = time.Second
	env := agentcli.WithUserBinPath(os.Environ())
	if forceInvalidate && !before.bestExpiry.IsZero() && before.bestExpiry.After(now) {
		remSecs := int(before.bestExpiry.Sub(now).Seconds()) + 60
		if remSecs < 300 {
			remSecs = 300
		}
		if remSecs > 20000 {
			remSecs = 20000
		}
		env = append(env, "GROK_AUTH_EARLY_INVALIDATION_SECS="+strconv.Itoa(remSecs))
	}
	cmd.Env = env
	_ = cmd.Run()

	after := inspectGrokAuthFile(authPath, time.Now())
	return after.apiKey != "" && (!after.isExpired || after.apiKey != before.apiKey)
}

func loadGrokLocalCredentialsWithRefresh(ctx context.Context) grokLocalCredentials {
	if gDir := grokHomeDir(); gDir != "" {
		state := inspectGrokAuthFile(filepath.Join(gDir, "auth.json"), time.Now())
		if state.needsRefresh() {
			refreshGrokAuthViaCLI(ctx, false)
		}
	}
	return loadGrokLocalCredentials()
}

func isImportedGrokProvider(raw interface{}) bool {
	prov, ok := raw.(map[string]interface{})
	return ok && len(prov) > 0
}

// syncGrokCredential proactively renews an imported Grok CLI OIDC token when
// ~/.grok/auth.json is expired or within grokProactiveRefreshWindow of expiry.
func (g *Service) syncGrokCredential(ctx context.Context) {
	if !isImportedGrokProvider(g.loadHoxiProvidersMap()["grok-cli"]) {
		return
	}
	gDir := grokHomeDir()
	if gDir == "" {
		return
	}
	state := inspectGrokAuthFile(filepath.Join(gDir, "auth.json"), time.Now())
	if state.needsRefresh() {
		refreshGrokAuthViaCLI(ctx, false)
	}
}

func detectGrokCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	creds := loadGrokLocalCredentials()
	sessions, stats := scanGrokSessions("", "")
	for _, s := range sessions {
		if s.Model != "" {
			creds.Models = appendUniqueString(creds.Models, s.Model)
			if creds.DefaultModel == "" {
				creds.DefaultModel = s.Model
			}
		}
	}
	_, connected := hoxiMap["grok-cli"]
	return LocalCLIStatusItem{
		ID:            ExternalToolGrok,
		Name:          "Grok CLI",
		Installed:     stats.Installed || creds.APIKey != "",
		Authenticated: creds.APIKey != "",
		Connected:     connected,
		Version:       creds.Version,
		DefaultModel:  creds.DefaultModel,
		Models:        creds.Models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "xai-grok-cli",
		ProviderID:    "grok-cli",
	}
}

func (g *Service) importGrokCLIProvider() LocalCLIImportResponse {
	creds := loadGrokLocalCredentials()
	if creds.APIKey == "" {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolGrok),
			Error:  "Grok CLI credentials not found in ~/.grok/auth.json",
		}
	}
	sessions, _ := scanGrokSessions("", "")
	for _, s := range sessions {
		if s.Model != "" {
			creds.Models = appendUniqueString(creds.Models, s.Model)
		}
	}

	modelsMap := map[string]interface{}{}
	for _, mID := range creds.Models {
		modelsMap[mID] = map[string]interface{}{
			"name":       mID + " (Grok CLI)",
			"tool_call":  true,
			"reasoning":  true,
			"attachment": true,
			"modalities": map[string]interface{}{
				"input":  []string{"text", "image"},
				"output": []string{"text"},
			},
			"limit": map[string]interface{}{
				"context": 256000,
				"output":  32768,
			},
		}
	}

	if err := g.updateHoxiConfigDoc(func(doc map[string]interface{}) error {
		provMap, _ := doc["provider"].(map[string]interface{})
		if provMap == nil {
			provMap = map[string]interface{}{}
		}
		provMap["grok-cli"] = map[string]interface{}{
			"name": "Grok CLI (Local)",
			"npm":  "@ai-sdk/openai-compatible",
			"options": map[string]interface{}{
				"baseURL": grokCLIStoredBridgeURL,
				"apiKey":  "hoxi-local-grok-cli-bridge",
			},
			"models": modelsMap,
		}
		doc["provider"] = provMap
		return nil
	}); err != nil {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolGrok),
			Error:  err.Error(),
		}
	}

	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolGrok),
		ProviderID: "grok-cli",
		Models:     creds.Models,
	}
}

func (g *Service) handleGrokCLIBridge(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/api/hoxi/bridge/grok-cli")
	if subPath == "/v1/models" && r.Method == http.MethodGet {
		creds := loadGrokLocalCredentials()
		items := make([]map[string]interface{}, 0, len(creds.Models))
		for _, m := range creds.Models {
			items = append(items, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"owned_by": "xai-grok-cli",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   items,
		})
		return
	}

	creds := loadGrokLocalCredentialsWithRefresh(r.Context())
	if creds.APIKey == "" {
		writeJSONError(w, http.StatusUnauthorized, "Missing Grok CLI credentials in ~/.grok/auth.json")
		return
	}

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	modelName := creds.DefaultModel
	callerWantsStream := true

	if len(rawBody) > 0 {
		var payload map[string]interface{}
		if json.Unmarshal(rawBody, &payload) == nil && payload != nil {
			if m, ok := payload["model"].(string); ok && strings.TrimSpace(m) != "" {
				modelName = strings.TrimSpace(m)
			}
			if st, ok := payload["stream"].(bool); ok {
				callerWantsStream = st
			} else {
				callerWantsStream = false
			}
			// Grok CLI proxy requires stream: true
			payload["stream"] = true
			if rewritten, err := json.Marshal(payload); err == nil {
				rawBody = rewritten
			}
		}
	}

	upstreamURL := resolveGrokUpstreamBaseURL() + subPath
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	newUpstreamReq := func(c grokLocalCredentials) (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, r.Method, upstreamURL, bytes.NewReader(rawBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
		req.Header.Set("x-grok-client-version", c.Version)
		req.Header.Set("x-grok-model-override", modelName)
		return req, nil
	}

	outReq, err := newUpstreamReq(creds)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	client := &http.Client{
		Transport: grokBridgeTransport,
	}

	resp, err := client.Do(outReq)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	if resp.StatusCode == http.StatusUnauthorized && refreshGrokAuthViaCLI(ctx, true) {
		if fresh := loadGrokLocalCredentials(); fresh.APIKey != "" && fresh.APIKey != creds.APIKey {
			if retryReq, rErr := newUpstreamReq(fresh); rErr == nil {
				if retryResp, doErr := client.Do(retryReq); doErr == nil {
					_ = resp.Body.Close()
					resp = retryResp
				}
			}
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || callerWantsStream {
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, rErr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rErr != nil {
				break
			}
		}
		return
	}

	// Caller requested non-streaming JSON (e.g. test-model, audit, summarize-title): aggregate SSE chunks
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(dataStr), &chunk) == nil && len(chunk.Choices) > 0 {
			contentBuf.WriteString(chunk.Choices[0].Delta.Content)
			reasoningBuf.WriteString(chunk.Choices[0].Delta.ReasoningContent)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      "chatcmpl-grok-bridge",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelName,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           contentBuf.String(),
					"reasoning_content": reasoningBuf.String(),
				},
				"finish_reason": "stop",
			},
		},
	})
}
