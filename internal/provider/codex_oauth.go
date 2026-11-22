package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	defaultCodexChatGPTBaseURL   = "https://chatgpt.com/backend-api/codex"
	defaultCodexOriginatorHeader = "codex_cli_rs"
	defaultCodexClientVersion    = "0.165.0"
	defaultCodexInstructions     = llm.DefaultCodexInstructions
)

var (
	codexOAuthMu              sync.RWMutex
	codexOAuthBaseURLOverride string
)

// SetCodexOAuthBaseURLForTest overrides the ChatGPT Codex OAuth backend URL in tests.
func SetCodexOAuthBaseURLForTest(rawURL string) {
	codexOAuthMu.Lock()
	codexOAuthBaseURLOverride = strings.TrimRight(strings.TrimSpace(rawURL), "/")
	codexOAuthMu.Unlock()
}

func effectiveCodexChatGPTBaseURL() string {
	codexOAuthMu.RLock()
	override := codexOAuthBaseURLOverride
	codexOAuthMu.RUnlock()
	if override != "" {
		return override
	}
	return defaultCodexChatGPTBaseURL
}

func isOpenAICodexProviderID(providerID string) bool {
	p := strings.ToLower(strings.TrimSpace(providerID))
	return p == "openai" || p == "codex"
}

func isDefaultOpenAIBaseURL(rawURL string) bool {
	return sameProviderBaseURL(rawURL, defaultProviderBaseURLs["openai"])
}

func isCodexChatGPTBackendURL(rawURL string) bool {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return false
	}
	if override := effectiveCodexChatGPTBaseURL(); override != defaultCodexChatGPTBaseURL && sameProviderBaseURL(trimmed, override) {
		return true
	}
	return llm.IsCodexBackendURL(trimmed)
}

func isSyntheticOAuthDummyKey(key string) bool {
	k := strings.TrimSpace(key)
	return strings.EqualFold(k, "opencode-oauth-dummy-key") || strings.EqualFold(k, "oauth-dummy-key")
}

func parseProviderJWTClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func parseProviderJWTExpiryMs(token string) int64 {
	claims := parseProviderJWTClaims(token)
	if claims == nil {
		return 0
	}
	switch v := claims["exp"].(type) {
	case float64:
		if v > 0 {
			return int64(v * 1000)
		}
	}
	return 0
}

func extractChatGPTAccountIDFromJWT(token string) string {
	claims := parseProviderJWTClaims(token)
	if claims == nil {
		return ""
	}
	if authObj, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if s, _ := authObj["chatgpt_account_id"].(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	for _, k := range []string{"chatgpt_account_id", "account_id"} {
		if s, _ := claims[k].(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func extractChatGPTAccountID(entry storedProviderAuthEntry) string {
	if id := strings.TrimSpace(entry.AccountID); id != "" {
		return id
	}
	return extractChatGPTAccountIDFromJWT(entry.Access)
}

func (g *Service) isImportedCodexLocalProvider(providerID string) bool {
	if !strings.EqualFold(strings.TrimSpace(providerID), "openai") {
		return false
	}
	rawProv, ok := g.loadHoxiProvidersMap()["openai"].(map[string]any)
	if !ok || rawProv == nil {
		return false
	}
	name, _ := rawProv["name"].(string)
	return strings.TrimSpace(name) == "OpenAI Codex (Local)"
}

func (g *Service) trySyncNewerCodexCLIOAuthEntry(providerID string, current storedProviderAuthEntry) (storedProviderAuthEntry, bool) {
	if !g.isImportedCodexLocalProvider(providerID) {
		return storedProviderAuthEntry{}, false
	}
	cDir := agentcli.CodexHomeDir()
	if cDir == "" {
		return storedProviderAuthEntry{}, false
	}
	raw, err := os.ReadFile(filepath.Join(cDir, "auth.json"))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return storedProviderAuthEntry{}, false
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return storedProviderAuthEntry{}, false
	}
	if k, _ := doc["OPENAI_API_KEY"].(string); strings.TrimSpace(k) != "" {
		return storedProviderAuthEntry{}, false
	}
	tokens, ok := doc["tokens"].(map[string]any)
	if !ok {
		return storedProviderAuthEntry{}, false
	}
	access, _ := tokens["access_token"].(string)
	refresh, _ := tokens["refresh_token"].(string)
	acctID, _ := tokens["account_id"].(string)
	idTok, _ := tokens["id_token"].(string)
	access = strings.TrimSpace(access)
	refresh = strings.TrimSpace(refresh)
	acctID = strings.TrimSpace(acctID)
	if access == "" || refresh == "" {
		return storedProviderAuthEntry{}, false
	}
	cliExpMs := parseProviderJWTExpiryMs(access)
	if cliExpMs == 0 {
		cliExpMs = parseProviderJWTExpiryMs(idTok)
	}
	curExpMs := normalizeExpiresMs(current.Expires)
	nowMs := time.Now().UnixMilli()

	shouldAdopt := false
	if current.Revoked {
		if cliExpMs > nowMs+oauthRefreshSkewMs || (refresh != strings.TrimSpace(current.Refresh) && cliExpMs == 0) {
			shouldAdopt = true
		}
	} else if cliExpMs > curExpMs && cliExpMs > nowMs+oauthRefreshSkewMs {
		shouldAdopt = true
	} else if refresh != strings.TrimSpace(current.Refresh) && cliExpMs > nowMs+oauthRefreshSkewMs {
		shouldAdopt = true
	}
	if !shouldAdopt {
		return storedProviderAuthEntry{}, false
	}

	if acctID == "" {
		acctID = extractChatGPTAccountIDFromJWT(access)
	}
	adopted := storedProviderAuthEntry{
		Type:      "oauth",
		Access:    access,
		Refresh:   refresh,
		Expires:   cliExpMs,
		AccountID: acctID,
	}
	saveMap := map[string]any{
		"type":    "oauth",
		"access":  adopted.Access,
		"refresh": adopted.Refresh,
		"expires": adopted.Expires,
	}
	if adopted.AccountID != "" {
		saveMap["accountId"] = adopted.AccountID
	}
	_ = g.persistEngineProviderAuthMap(providerID, saveMap)
	return adopted, true
}

func (g *Service) hasActiveCodexOAuthEntry(providerID string) bool {
	if !isOpenAICodexProviderID(providerID) {
		return false
	}
	for _, src := range []map[string]any{g.loadHoxiProvidersMap(), g.loadOpenCodeImportedProviderMap()} {
		if rawCfg, ok := src[providerID].(map[string]any); ok {
			if opts, ok := rawCfg["options"].(map[string]any); ok {
				if k, _ := opts["apiKey"].(string); strings.TrimSpace(k) != "" && strings.TrimSpace(k) != "********" {
					return false
				}
				if u, _ := opts["baseURL"].(string); strings.TrimSpace(u) != "" {
					loc := g.localizeBridgeURL(u)
					if !isDefaultOpenAIBaseURL(loc) && !isCodexChatGPTBackendURL(loc) {
						return false
					}
				}
			}
		}
	}
	engineClean := filepath.Clean(strings.TrimSpace(hoxipaths.EngineAuthPath()))
	for _, authPath := range candidateProviderAuthPaths() {
		if (engineClean == "" || filepath.Clean(authPath) != engineClean) && g.IsExternalProviderDismissed(providerID) {
			continue
		}
		entry, ok := readProviderAuthEntry(authPath, providerID)
		if !ok {
			continue
		}
		if strings.TrimSpace(entry.Key) != "" {
			return false
		}
		if !entry.Revoked && (strings.TrimSpace(entry.Access) != "" || strings.TrimSpace(entry.Refresh) != "") {
			return true
		}
	}
	if g.isImportedCodexLocalProvider(providerID) {
		if adopted, ok := g.trySyncNewerCodexCLIOAuthEntry(providerID, storedProviderAuthEntry{Revoked: true}); ok && strings.TrimSpace(adopted.Access) != "" {
			return true
		}
	}
	return false
}

func (g *Service) resolveCodexOAuthAuthEntry(ctx context.Context, providerID string) (storedProviderAuthEntry, string, bool) {
	engineClean := filepath.Clean(strings.TrimSpace(hoxipaths.EngineAuthPath()))
	var initial storedProviderAuthEntry
	var found bool
	for _, authPath := range candidateProviderAuthPaths() {
		if (engineClean == "" || filepath.Clean(authPath) != engineClean) && g.IsExternalProviderDismissed(providerID) {
			continue
		}
		entry, ok := readProviderAuthEntry(authPath, providerID)
		if !ok {
			continue
		}
		if strings.TrimSpace(entry.Key) != "" {
			return storedProviderAuthEntry{}, "", false
		}
		initial = entry
		found = true
		break
	}
	if !found {
		if adopted, ok := g.trySyncNewerCodexCLIOAuthEntry(providerID, storedProviderAuthEntry{Revoked: true}); ok {
			return adopted, strings.TrimSpace(adopted.Access), true
		}
		return storedProviderAuthEntry{}, "", false
	}

	expMs := normalizeExpiresMs(initial.Expires)
	nowMs := time.Now().UnixMilli()
	if initial.Revoked || (expMs > 0 && nowMs+oauthRefreshSkewMs >= expMs) {
		if adopted, ok := g.trySyncNewerCodexCLIOAuthEntry(providerID, initial); ok {
			return adopted, strings.TrimSpace(adopted.Access), true
		}
	}

	secret, _, err := g.resolveAuthFileCredential(ctx, providerID)
	secret = strings.TrimSpace(secret)
	if err != nil || secret == "" {
		if adopted, ok := g.trySyncNewerCodexCLIOAuthEntry(providerID, storedProviderAuthEntry{Revoked: true, Refresh: initial.Refresh}); ok {
			return adopted, strings.TrimSpace(adopted.Access), true
		}
		return storedProviderAuthEntry{}, "", false
	}

	latest := initial
	for _, authPath := range candidateProviderAuthPaths() {
		if (engineClean == "" || filepath.Clean(authPath) != engineClean) && g.IsExternalProviderDismissed(providerID) {
			continue
		}
		if entry, ok := readProviderAuthEntry(authPath, providerID); ok && strings.TrimSpace(entry.Key) == "" {
			latest = entry
			break
		}
	}
	latest.Access = secret
	return latest, secret, true
}

func (g *Service) resolveCodexOAuthConnection(ctx context.Context, providerID, configuredBaseURL, hintBaseURL, explicitAPIKey, configuredCodexVer string) (bool, string, string, map[string]string) {
	if !isOpenAICodexProviderID(providerID) || strings.TrimSpace(explicitAPIKey) != "" {
		return false, "", "", nil
	}
	if configuredBaseURL != "" && !isDefaultOpenAIBaseURL(configuredBaseURL) && !isCodexChatGPTBackendURL(configuredBaseURL) {
		return false, "", "", nil
	}
	if hintBaseURL != "" && !isDefaultOpenAIBaseURL(hintBaseURL) && !isCodexChatGPTBackendURL(hintBaseURL) {
		return false, "", "", nil
	}
	entry, secret, ok := g.resolveCodexOAuthAuthEntry(ctx, providerID)
	if !ok || strings.TrimSpace(secret) == "" {
		return false, "", "", nil
	}

	resolvedURL := effectiveCodexChatGPTBaseURL()
	if isCodexChatGPTBackendURL(configuredBaseURL) {
		resolvedURL = configuredBaseURL
	} else if isCodexChatGPTBackendURL(hintBaseURL) {
		resolvedURL = hintBaseURL
	}

	ver := firstNonEmptyString(
		strings.TrimPrefix(strings.TrimSpace(configuredCodexVer), "v"),
		agentcli.ResolveCodexClientVersion(),
		defaultCodexClientVersion,
	)
	headers := map[string]string{
		"originator": defaultCodexOriginatorHeader,
		"version":    ver,
		"User-Agent": defaultCodexOriginatorHeader + "/" + ver,
	}
	if acctID := extractChatGPTAccountID(entry); acctID != "" {
		headers["ChatGPT-Account-Id"] = acctID
	}
	return true, resolvedURL, strings.TrimSpace(secret), headers
}

func (g *Service) resolveEffectiveProviderConnection(providerID, hintBaseURL, hintNPM string) (baseURL, apiKey, npm string, headers map[string]string) {
	if providerID == officialProviderID && g.officialBlocked.Load() {
		return "", "", "", nil
	}
	hintURL := strings.TrimSpace(g.localizeBridgeURL(hintBaseURL))
	merged := g.loadOpenCodeImportedProviderMap()
	for k, v := range g.loadHoxiProvidersMap() {
		merged[k] = v
	}
	var configuredBaseURL, explicitAPIKey, configuredCodexVer, configuredNPM string
	userHeaders := map[string]string{}
	if rawCfg, ok := merged[providerID].(map[string]any); ok {
		if s, ok := rawCfg["npm"].(string); ok {
			configuredNPM = strings.TrimSpace(s)
		}
		if opts, ok := rawCfg["options"].(map[string]any); ok {
			if u, ok := opts["baseURL"].(string); ok {
				configuredBaseURL = strings.TrimSpace(g.localizeBridgeURL(u))
			}
			if ver, ok := opts["codexVersion"].(string); ok {
				configuredCodexVer = strings.TrimSpace(ver)
			}
			if k, ok := opts["apiKey"].(string); ok && k != "" && k != "********" {
				explicitAPIKey = strings.TrimSpace(k)
			}
			if hMap, ok := opts["headers"].(map[string]any); ok {
				for hk, hv := range hMap {
					if vs, ok := hv.(string); ok {
						userHeaders[hk] = vs
					}
				}
			}
		}
	}

	if isOpenAICodexProviderID(providerID) && explicitAPIKey == "" && g.hasActiveCodexOAuthEntry(providerID) {
		if hintURL != "" && !isDefaultOpenAIBaseURL(hintURL) && !isCodexChatGPTBackendURL(hintURL) {
			return hintURL, "", strings.TrimSpace(hintNPM), nil
		}
		if active, oauthURL, oauthToken, oauthHeaders := g.resolveCodexOAuthConnection(context.Background(), providerID, configuredBaseURL, hintURL, explicitAPIKey, configuredCodexVer); active {
			outNPM := firstNonEmptyString(strings.TrimSpace(hintNPM), configuredNPM, "@ai-sdk/openai")
			for hk, hv := range oauthHeaders {
				if _, exists := userHeaders[hk]; !exists {
					userHeaders[hk] = hv
				}
			}
			return oauthURL, oauthToken, outNPM, userHeaders
		}
	}

	baseURL, apiKey, npm, headers = g.resolveProviderConnection(providerID, hintBaseURL, hintNPM)
	if isSyntheticOAuthDummyKey(apiKey) {
		apiKey = ""
		if g.isAuthorizedProviderBaseURL(providerID, baseURL, configuredBaseURL) {
			if k, _, err := g.resolveAuthFileCredential(context.Background(), providerID); err == nil && strings.TrimSpace(k) != "" {
				apiKey = strings.TrimSpace(k)
			}
		}
	}
	return baseURL, apiKey, npm, headers
}

func buildCodexModelsEndpointURL(trimmedBase, version string) string {
	target := trimmedBase + "/models"
	if strings.HasSuffix(strings.ToLower(trimmedBase), "/models") {
		target = trimmedBase
	}
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	if q.Get("client_version") == "" {
		q.Set("client_version", firstNonEmptyString(strings.TrimSpace(version), agentcli.ResolveCodexClientVersion(), defaultCodexClientVersion))
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func normalizeCodexBackendResponsesMap(payload map[string]any) {
	llm.NormalizeCodexResponsesMap(payload)
}

func normalizeCodexBackendResponsesPayload(raw []byte) []byte {
	return llm.NormalizeCodexResponsesPayload(raw)
}

func extractSSETextContent(raw []byte) (string, bool) {
	if !bytes.Contains(raw, []byte("data:")) {
		return "", false
	}
	var deltas strings.Builder
	var completedText string
	sawSSEData := false

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(data), &evt) != nil {
			continue
		}
		sawSSEData = true
		evtType, _ := evt["type"].(string)
		switch evtType {
		case "response.output_text.delta":
			if d, _ := evt["delta"].(string); d != "" {
				deltas.WriteString(d)
			}
		case "response.completed":
			if respObj, ok := evt["response"].(map[string]any); ok {
				if respBytes, err := json.Marshal(respObj); err == nil {
					if t := extractLLMTextContent(respBytes); t != "" {
						completedText = t
					}
				}
			}
		}
	}
	if !sawSSEData {
		return "", false
	}
	if s := strings.TrimSpace(deltas.String()); s != "" {
		return s, true
	}
	return strings.TrimSpace(completedText), true
}
