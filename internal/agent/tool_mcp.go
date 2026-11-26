package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxiconfig"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	mcpCallTimeout            = 45 * time.Second
	mcpDefaultProtocolVersion = "2025-03-26"
)

var (
	mcpHTTPTransport http.RoundTripper
	mcpReqSeq        atomic.Int64
	remoteMCPSessMu  sync.Mutex
	remoteMCPSessMap = map[string]*remoteMCPSessionState{}
)

type remoteMCPSessionState struct {
	mu              sync.Mutex
	initialized     bool
	sessionID       string
	protocolVersion string
}

// SetMCPHTTPTransportForTest overrides the HTTP RoundTripper used by remote MCP calls in tests
// and resets cached remote MCP sessions so each test runs a clean handshake.
func SetMCPHTTPTransportForTest(rt http.RoundTripper) {
	mcpHTTPTransport = rt
	ResetRemoteMCPSessions()
}

// ResetRemoteMCPSessions clears cached remote Streamable HTTP MCP sessions.
func ResetRemoteMCPSessions() {
	remoteMCPSessMu.Lock()
	remoteMCPSessMap = map[string]*remoteMCPSessionState{}
	remoteMCPSessMu.Unlock()
}

type mcpServerSpec struct {
	Name            string
	Type            string // "remote" or "local"
	URL             string
	Headers         map[string]string
	Command         []string
	Env             map[string]string
	StartupBudgetMs int
	OAuthError      string
	FromRepo        bool
}

type mcpCallTool struct{}

func (t *mcpCallTool) Name() string     { return "mcp_call_tool" }
func (t *mcpCallTool) IsReadOnly() bool { return false }
func (t *mcpCallTool) Description() string {
	return "Invoke a tool on a configured remote HTTP or local stdio MCP server."
}
func (t *mcpCallTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server":    map[string]any{"type": "string", "description": "Configured MCP server name"},
			"tool":      map[string]any{"type": "string", "description": "Tool name exposed by the MCP server"},
			"arguments": map[string]any{"type": "object", "description": "JSON object arguments passed to tools/call"},
		},
		"required": []string{"server", "tool"},
	}
}

func (t *mcpCallTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Server    string         `json:"server"`
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "mcp_call_tool", IsError: true}, err
	}
	srvName := strings.TrimSpace(args.Server)
	toolName := strings.TrimSpace(args.Tool)
	if srvName == "" || toolName == "" {
		return ToolOutput{Title: "mcp_call_tool", IsError: true}, fmt.Errorf("both server and tool are required")
	}
	if args.Arguments == nil {
		args.Arguments = map[string]any{}
	}

	servers, err := resolveMCPServers(tc)
	if err != nil {
		return ToolOutput{Title: t.Name(), IsError: true}, err
	}
	var target *mcpServerSpec
	for _, srv := range servers {
		if strings.EqualFold(srv.Name, srvName) {
			cp := srv
			target = &cp
			break
		}
	}
	if target == nil {
		return ToolOutput{Title: srvName + "/" + toolName, IsError: true}, fmt.Errorf("MCP server %q is not configured or is disabled", srvName)
	}
	if target.OAuthError != "" {
		return ToolOutput{Title: srvName + ":" + toolName, Output: target.OAuthError, IsError: true}, fmt.Errorf("%s", target.OAuthError)
	}

	callCtx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
	defer cancel()
	rawRes, err := invokeMCPMethod(callCtx, tc.WorktreeDir, *target, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args.Arguments,
	})
	if err != nil {
		return ToolOutput{Title: srvName + ":" + toolName, IsError: true}, err
	}

	return decodeMCPToolResult(srvName+":"+toolName, rawRes)
}

func resolveMCPServers(tc ToolContext) ([]mcpServerSpec, error) {
	project := tc.projectConfig
	if project == nil {
		var err error
		project, err = hoxiconfig.Load(tc.WorktreeDir)
		if err != nil {
			return nil, err
		}
	}
	layers := map[string]any{}
	addLayer := func(m map[string]any) {
		normalized := make(map[string]any, len(m))
		for name, value := range m {
			normalized[strings.ToLower(strings.TrimSpace(name))] = value
		}
		layers = hoxiconfig.Overlay(layers, normalized)
	}
	repoServers := make(map[string]bool)
	markRepoServers := func(m map[string]any) {
		for name, raw := range m {
			key := strings.ToLower(strings.TrimSpace(name))
			cfg, ok := raw.(map[string]any)
			if !ok || key == "" {
				continue
			}
			if isMCPConfigDisabled(cfg) {
				delete(repoServers, key)
				continue
			}
			repoServers[key] = true
		}
	}
	merged := make(map[string]mcpServerSpec)
	collectFromMap := func(m map[string]any) {
		for name, raw := range m {
			cfg, ok := raw.(map[string]any)
			if !ok || strings.TrimSpace(name) == "" {
				continue
			}
			if enabled, has := cfg["enabled"].(bool); has && !enabled {
				delete(merged, name)
				continue
			}
			if disabled, has := cfg["disabled"].(bool); has && disabled {
				delete(merged, name)
				continue
			}
			budgetMs := int(int64FromAny(cfg["startupTimeoutMs"]))
			if budgetMs <= 0 {
				budgetMs = int(int64FromAny(cfg["startupBudgetMs"]))
			}
			spec := mcpServerSpec{
				Name:            strings.TrimSpace(name),
				Headers:         toStringMap(cfg["headers"]),
				Env:             toStringMap(cfg["environment"]),
				StartupBudgetMs: budgetMs,
				FromRepo:        repoServers[strings.ToLower(strings.TrimSpace(name))],
			}
			if len(spec.Env) == 0 {
				spec.Env = toStringMap(cfg["env"])
			}
			urlStr, _ := cfg["url"].(string)
			spec.URL = strings.TrimSpace(urlStr)
			spec.Command = parseCommandSlice(cfg["command"], cfg["args"])
			typ, _ := cfg["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			switch {
			case typ == "remote" || (spec.URL != "" && len(spec.Command) == 0):
				spec.Type = "remote"
				if spec.URL != "" {
					enrichRemoteMCPOAuth(&spec, cfg["oauth"])
					merged[spec.Name] = spec
				}
			case typ == "local" || typ == "stdio" || len(spec.Command) > 0:
				spec.Type = "local"
				if len(spec.Command) > 0 {
					merged[spec.Name] = spec
				}
			}
		}
	}

	if tc.MCPConfig != nil {
		if doc := tc.MCPConfig(); doc != nil {
			if mcpMap, ok := doc["mcp"].(map[string]any); ok {
				addLayer(mcpMap)
			}
		}
	}
	machineServers := hoxiconfig.Overlay(nil, layers)
	if dotMCP := loadWorkspaceDotMCP(tc.WorktreeDir); len(dotMCP) > 0 {
		applyRepoMCPLayer(layers, machineServers, dotMCP)
		markRepoServers(dotMCP)
	}
	// The explicit native project source wins over legacy .mcp.json.
	if m, ok := project["mcp"].(map[string]any); ok {
		applyRepoMCPLayer(layers, machineServers, m)
		markRepoServers(m)
	}
	collectFromMap(layers)
	names := make([]string, 0, len(merged))
	for k := range merged {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]mcpServerSpec, 0, len(names))
	for _, k := range names {
		out = append(out, merged[k])
	}
	return out, nil
}

func hasAuthorizationHeader(headers map[string]string) bool {
	for k, v := range headers {
		if strings.EqualFold(strings.TrimSpace(k), "Authorization") && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// enrichRemoteMCPOAuth resolves stored OAuth Bearer tokens from inline MCP config,
// Hoxi's auth.json, or legacy OpenCode mcp-auth.json / auth.json, surfacing clear
// re-authorization diagnostics when a credential was revoked or expired.
func enrichRemoteMCPOAuth(spec *mcpServerSpec, rawOAuth any) {
	if spec == nil || hasAuthorizationHeader(spec.Headers) {
		return
	}
	if oauthDisabled, ok := rawOAuth.(bool); ok && !oauthDisabled {
		return
	}
	if oauthMap, ok := rawOAuth.(map[string]any); ok {
		for _, field := range []string{"accessToken", "access_token", "access", "token"} {
			if tok, _ := oauthMap[field].(string); strings.TrimSpace(tok) != "" {
				if spec.Headers == nil {
					spec.Headers = make(map[string]string)
				}
				spec.Headers["Authorization"] = "Bearer " + strings.TrimSpace(tok)
				return
			}
		}
	}

	tok, authErr := lookupStoredMCPOAuthBearer(spec.Name, spec.URL)
	if tok != "" {
		if spec.Headers == nil {
			spec.Headers = make(map[string]string)
		}
		spec.Headers["Authorization"] = "Bearer " + tok
		return
	}
	if authErr != "" {
		spec.OAuthError = authErr
	}
}

func lookupStoredMCPOAuthBearer(serverName, serverURL string) (token string, authError string) {
	nowSec := time.Now().Unix()
	nowMs := time.Now().UnixMilli()

	// 1. Check Hoxi EngineAuthPath() and ~/.local/share/opencode/auth.json
	var authPaths []string
	if p := strings.TrimSpace(hoxipaths.EngineAuthPath()); p != "" {
		authPaths = append(authPaths, p)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		authPaths = append(authPaths, filepath.Join(home, ".local", "share", "opencode", "auth.json"))
	}
	for _, p := range authPaths {
		raw, err := os.ReadFile(p)
		if err != nil || len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var doc map[string]map[string]any
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		for _, key := range []string{"mcp:" + serverName, serverName} {
			entry, ok := doc[key]
			if !ok || entry == nil {
				continue
			}
			if rev, _ := entry["revoked"].(bool); rev {
				reason, _ := entry["revokedReason"].(string)
				return "", fmt.Sprintf("MCP server %q OAuth credential was revoked (%s); please re-authorize in Settings", serverName, firstNonEmptyMCP(reason, "revoked"))
			}
			if exp := int64FromAny(entry["expires"]); exp > 0 {
				expMs := exp
				if expMs < 1_000_000_000_000 {
					expMs *= 1000
				}
				if nowMs >= expMs {
					return "", fmt.Sprintf("MCP server %q OAuth token has expired; please re-authorize in Settings", serverName)
				}
			}
			for _, f := range []string{"access", "accessToken", "key"} {
				if v, _ := entry[f].(string); strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v), ""
				}
			}
		}
	}

	// 2. Check legacy OpenCode ~/.local/share/opencode/mcp-auth.json
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		mcpAuthPath := filepath.Join(home, ".local", "share", "opencode", "mcp-auth.json")
		if raw, rErr := os.ReadFile(mcpAuthPath); rErr == nil && len(bytes.TrimSpace(raw)) > 0 {
			var doc map[string]struct {
				ServerURL string `json:"serverUrl"`
				Tokens    struct {
					AccessToken string `json:"accessToken"`
					ExpiresAt   int64  `json:"expiresAt"`
				} `json:"tokens"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				if entry, ok := doc[serverName]; ok && strings.TrimSpace(entry.Tokens.AccessToken) != "" {
					if entry.ServerURL == "" || strings.EqualFold(strings.TrimRight(entry.ServerURL, "/"), strings.TrimRight(serverURL, "/")) {
						if entry.Tokens.ExpiresAt > 0 && nowSec >= entry.Tokens.ExpiresAt {
							return "", fmt.Sprintf("MCP server %q OAuth token in mcp-auth.json has expired; please re-authorize", serverName)
						}
						return strings.TrimSpace(entry.Tokens.AccessToken), ""
					}
				}
			}
		}
	}
	return "", ""
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func firstNonEmptyMCP(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func invokeMCPMethod(ctx context.Context, worktreeDir string, srv mcpServerSpec, method string, params map[string]any) (json.RawMessage, error) {
	if srv.Type == "remote" {
		return invokeRemoteMCP(ctx, srv, method, params)
	}
	return invokeStdioMCP(ctx, worktreeDir, srv, method, params)
}

type jsonRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func remoteMCPSessionKey(srv mcpServerSpec) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(srv.URL)))
	h.Write([]byte{0})
	var keys []string
	for k := range srv.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(strings.ToLower(strings.TrimSpace(k)) + "=" + strings.TrimSpace(srv.Headers[k]) + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func getOrCreateRemoteMCPSession(key string) *remoteMCPSessionState {
	remoteMCPSessMu.Lock()
	defer remoteMCPSessMu.Unlock()
	st := remoteMCPSessMap[key]
	if st == nil {
		st = &remoteMCPSessionState{}
		remoteMCPSessMap[key] = st
	}
	return st
}

func invalidateRemoteMCPSession(key string) {
	remoteMCPSessMu.Lock()
	delete(remoteMCPSessMap, key)
	remoteMCPSessMu.Unlock()
}

func invokeRemoteMCP(ctx context.Context, srv mcpServerSpec, method string, params map[string]any) (json.RawMessage, error) {
	key := remoteMCPSessionKey(srv)
	for attempt := 0; attempt < 2; attempt++ {
		sess := getOrCreateRemoteMCPSession(key)
		sessID, protoVer, err := ensureRemoteMCPInitialized(ctx, srv, sess)
		if err != nil {
			return nil, err
		}
		res, statusCode, callErr := sendRemoteMCPRPC(ctx, srv, sessID, protoVer, method, params, false)
		if callErr == nil {
			return res, nil
		}
		// Streamable HTTP spec: HTTP 404 on an active Mcp-Session-Id indicates server restarted
		// or evicted the session; invalidate once and re-run initialize transparently.
		if attempt == 0 && sessID != "" && statusCode == http.StatusNotFound {
			invalidateRemoteMCPSession(key)
			continue
		}
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			invalidateRemoteMCPSession(key)
		}
		return nil, callErr
	}
	return nil, fmt.Errorf("MCP session handshake retry failed for %s", srv.Name)
}

func ensureRemoteMCPInitialized(ctx context.Context, srv mcpServerSpec, sess *remoteMCPSessionState) (string, string, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.initialized {
		return sess.sessionID, sess.protocolVersion, nil
	}

	initParams := map[string]any{
		"protocolVersion": mcpDefaultProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "hoxi-native",
			"version": "1.0.0",
		},
	}
	rawInit, statusCode, respHeaders, err := sendRemoteMCPRaw(ctx, srv, "", "", "initialize", initParams, false)
	if err != nil {
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			return "", "", err
		}
		// Stateless JSON-RPC servers that do not implement `initialize` (-32601 Method not found):
		// mark initialized without session ID so direct tool calls still proceed.
		if strings.Contains(strings.ToLower(err.Error()), "method not found") || strings.Contains(err.Error(), "-32601") {
			sess.initialized = true
			return "", "", nil
		}
		return "", "", err
	}

	var initRes struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(rawInit, &initRes)

	sid := strings.TrimSpace(respHeaders.Get("Mcp-Session-Id"))
	proto := strings.TrimSpace(initRes.ProtocolVersion)
	if proto == "" {
		proto = mcpDefaultProtocolVersion
	}

	// Best-effort notifications/initialized per MCP lifecycle spec.
	_, _, _ = sendRemoteMCPRPC(ctx, srv, sid, proto, "notifications/initialized", nil, true)

	sess.initialized = true
	sess.sessionID = sid
	sess.protocolVersion = proto
	return sid, proto, nil
}

func sendRemoteMCPRPC(
	ctx context.Context,
	srv mcpServerSpec,
	sessionID, protocolVersion, method string,
	params map[string]any,
	isNotification bool,
) (json.RawMessage, int, error) {
	res, code, _, err := sendRemoteMCPRaw(ctx, srv, sessionID, protocolVersion, method, params, isNotification)
	return res, code, err
}

func sendRemoteMCPRaw(
	ctx context.Context,
	srv mcpServerSpec,
	sessionID, protocolVersion, method string,
	params map[string]any,
	isNotification bool,
) (json.RawMessage, int, http.Header, error) {
	rpcBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if !isNotification {
		rpcBody["id"] = mcpReqSeq.Add(1)
	}
	if params != nil {
		rpcBody["params"] = params
	}
	payload, err := json.Marshal(rpcBody)
	if err != nil {
		return nil, 0, nil, err
	}

	parsedURL, err := validateRemoteMCPURL(srv.URL)
	if err != nil {
		return nil, 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	for k, v := range srv.Headers {
		req.Header.Set(k, v)
	}

	client := newSafeMCPHTTPClient(mcpCallTimeout, parsedURL, srv.Headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		wwwAuth := strings.TrimSpace(resp.Header.Get("WWW-Authenticate"))
		detail := strings.TrimSpace(string(body))
		if detail == "" && wwwAuth != "" {
			detail = wwwAuth
		}
		return nil, resp.StatusCode, resp.Header, fmt.Errorf(
			"MCP server %q requires OAuth authorization or re-authentication (HTTP %d%s) — reconnect %q in Hoxi Settings",
			srv.Name,
			resp.StatusCode,
			func() string {
				if detail != "" {
					return ": " + detail
				}
				return ""
			}(),
			srv.Name,
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if isNotification || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, resp.Header, nil
	}

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	if len(bytes.TrimSpace(rawBody)) == 0 {
		return json.RawMessage(`{}`), resp.StatusCode, resp.Header, nil
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		for _, line := range strings.Split(string(rawBody), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var env jsonRPCEnvelope
			if json.Unmarshal([]byte(data), &env) == nil && (len(env.Result) > 0 || env.Error != nil) {
				if env.Error != nil {
					return nil, resp.StatusCode, resp.Header, fmt.Errorf("MCP error %d: %s", env.Error.Code, env.Error.Message)
				}
				return env.Result, resp.StatusCode, resp.Header, nil
			}
		}
	}

	var env jsonRPCEnvelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("invalid MCP JSON-RPC response: %w", err)
	}
	if env.Error != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("MCP error %d: %s", env.Error.Code, env.Error.Message)
	}
	return env.Result, resp.StatusCode, resp.Header, nil
}

func invokeStdioMCP(ctx context.Context, worktreeDir string, srv mcpServerSpec, method string, params map[string]any) (json.RawMessage, error) {
	return defaultStdioPool.invoke(ctx, worktreeDir, srv, method, params)
}

func toStringMap(raw any) map[string]string {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func parseCommandSlice(cmdRaw, argsRaw any) []string {
	var out []string
	switch v := cmdRaw.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	if list, ok := argsRaw.([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
