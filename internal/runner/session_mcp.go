package runner

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// SessionMCPResolver returns dynamic per-session MCP server definitions
// (keyed by server name, matching native MCP config entries) contributed by
// host extensions such as Work app packages.
type SessionMCPResolver func(sessionID, directory string) map[string]any

var sessionMCPServerNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var sessionMCPPathSegRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type sessionMCPServer struct {
	Name             string
	Type             string // "remote" or "local"
	URL              string
	Headers          map[string]string
	Command          []string
	Env              map[string]string
	StartupTimeoutMs int
}

// SetSessionMCPResolver registers an optional per-session MCP server resolver
// used to mount dynamic session-scoped MCP servers (such as Work package
// work-local-<appId> services) on external CLI runtimes as well as native turns.
func (s *Service) SetSessionMCPResolver(fn SessionMCPResolver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionMCP = fn
	s.mu.Unlock()
}

func (s *Service) resolveSessionMCPServers(sessionID, directory string) map[string]any {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	s.mu.Lock()
	fn := s.sessionMCP
	s.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(sessionID, directory)
}

func parseSessionMCPServers(raw map[string]any) []sessionMCPServer {
	if len(raw) == 0 {
		return nil
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]sessionMCPServer, 0, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" || !sessionMCPServerNameRe.MatchString(name) {
			continue
		}
		cfg, ok := raw[rawName].(map[string]any)
		if !ok || len(cfg) == 0 {
			continue
		}
		if enabled, ok := cfg["enabled"].(bool); ok && !enabled {
			continue
		}
		if disabled, ok := cfg["disabled"].(bool); ok && disabled {
			continue
		}

		srv := sessionMCPServer{
			Name:             name,
			Headers:          extractStringMap(cfg["headers"]),
			Env:              extractStringMap(cfg["environment"]),
			StartupTimeoutMs: extractPositiveInt(cfg["startupTimeoutMs"]),
		}
		if len(srv.Env) == 0 {
			srv.Env = extractStringMap(cfg["env"])
		}
		if srv.StartupTimeoutMs <= 0 {
			srv.StartupTimeoutMs = extractPositiveInt(cfg["startupBudgetMs"])
		}
		if rawURL, ok := cfg["url"].(string); ok {
			srv.URL = strings.TrimSpace(rawURL)
		}
		srv.Command = extractCommandSlice(cfg["command"], cfg["args"])

		typ, _ := cfg["type"].(string)
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch {
		case typ == "remote" || (srv.URL != "" && len(srv.Command) == 0):
			if srv.URL == "" {
				continue
			}
			srv.Type = "remote"
			out = append(out, srv)
		case typ == "local" || typ == "stdio" || len(srv.Command) > 0:
			if len(srv.Command) == 0 {
				continue
			}
			srv.Type = "local"
			out = append(out, srv)
		}
	}
	return out
}

func extractStringMap(raw any) map[string]string {
	switch m := raw.(type) {
	case map[string]string:
		if len(m) == 0 {
			return nil
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			if strings.TrimSpace(k) != "" {
				out[k] = v
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		if len(m) == 0 {
			return nil
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			if strings.TrimSpace(k) == "" {
				continue
			}
			if sv, ok := v.(string); ok {
				out[k] = sv
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func extractCommandSlice(cmdRaw, argsRaw any) []string {
	var out []string
	switch c := cmdRaw.(type) {
	case string:
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			out = append(out, trimmed)
		}
	case []string:
		for _, s := range c {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, item := range c {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	switch a := argsRaw.(type) {
	case []string:
		out = append(out, a...)
	case []any:
		for _, item := range a {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func extractPositiveInt(raw any) int {
	switch v := raw.(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return 0
}

func hasLoopbackMCPServer(servers []sessionMCPServer) bool {
	for _, srv := range servers {
		if srv.Type != "remote" || srv.URL == "" {
			continue
		}
		u, err := url.Parse(srv.URL)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return true
		}
	}
	return false
}

func sanitizeSessionMCPPathToken(raw, fallback string) string {
	cleaned := sessionMCPPathSegRe.ReplaceAllString(strings.TrimSpace(raw), "_")
	cleaned = strings.Trim(cleaned, "._-")
	if cleaned == "" {
		return fallback
	}
	if len(cleaned) > 64 {
		return cleaned[:64]
	}
	return cleaned
}

func createSessionMCPDir(sessionID, messageID string) (string, error) {
	baseDir := filepath.Join(hoxipaths.Home(), "sandbox")
	if strings.TrimSpace(hoxipaths.Home()) == "" {
		baseDir = os.TempDir()
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", err
	}
	prefix := fmt.Sprintf("session-mcp-%s-%s-",
		sanitizeSessionMCPPathToken(sessionID, "session"),
		sanitizeSessionMCPPathToken(messageID, "turn"),
	)
	dir, err := os.MkdirTemp(baseDir, prefix)
	if err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	return dir, nil
}

// prepareSessionMCPForTurn materializes dynamic per-session MCP server configs
// (for example Work package work-local-<appId> services) in the format each
// external CLI runtime understands, returning extra CLI flags, environment
// variables, optional Gemini settings.json entries, and a cleanup callback.
func (s *Service) prepareSessionMCPForTurn(
	rec SessionRecord,
	asstMsgID string,
	geminiUsesAgy bool,
) ([]string, []string, map[string]any, func()) {
	noop := func() {}
	raw := s.resolveSessionMCPServers(rec.ID, rec.WorktreePath)
	servers := parseSessionMCPServers(raw)
	if len(servers) == 0 {
		return nil, nil, nil, noop
	}

	var extraEnv []string
	if hasLoopbackMCPServer(servers) {
		extraEnv = withLoopbackNoProxy(extraEnv)
	}

	switch {
	case rec.Runtime == string(RuntimeAntigravity) || (rec.Runtime == string(RuntimeGeminiCLI) && geminiUsesAgy):
		args, cleanup, err := writeAgySessionMCPConfig(rec.ID, asstMsgID, servers)
		if err != nil {
			return nil, nil, nil, noop
		}
		return args, extraEnv, nil, cleanup

	case rec.Runtime == string(RuntimeClaudeCode):
		args, cleanup, err := writeClaudeSessionMCPConfig(rec.ID, asstMsgID, servers)
		if err != nil {
			return nil, nil, nil, noop
		}
		return args, extraEnv, nil, cleanup

	case rec.Runtime == string(RuntimeGeminiCLI):
		return nil, extraEnv, buildGeminiSessionMCPMap(servers), noop

	case rec.Runtime == string(RuntimeCodex):
		args, env := buildCodexSessionMCPArgsAndEnv(servers)
		extraEnv = append(extraEnv, env...)
		return args, extraEnv, nil, noop

	default:
		return nil, nil, nil, noop
	}
}

func writeAgySessionMCPConfig(sessionID, messageID string, servers []sessionMCPServer) ([]string, func(), error) {
	dir, err := createSessionMCPDir(sessionID, messageID)
	if err != nil {
		return nil, func() {}, err
	}
	agentsDir := filepath.Join(dir, ".agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, func() {}, err
	}
	mcpMap := make(map[string]any, len(servers))
	for _, srv := range servers {
		entry := map[string]any{}
		timeoutSec := 180
		if srv.StartupTimeoutMs > 0 {
			if sec := int(math.Ceil(float64(srv.StartupTimeoutMs) / 1000.0)); sec > timeoutSec {
				timeoutSec = sec
			}
		}
		entry["timeoutSeconds"] = timeoutSec
		if srv.Type == "remote" {
			entry["serverUrl"] = srv.URL
			if len(srv.Headers) > 0 {
				entry["headers"] = srv.Headers
			}
		} else {
			entry["command"] = srv.Command[0]
			if len(srv.Command) > 1 {
				entry["args"] = srv.Command[1:]
			}
			if len(srv.Env) > 0 {
				entry["env"] = srv.Env
			}
		}
		mcpMap[srv.Name] = entry
	}
	data := append(marshalJSON(map[string]any{"mcpServers": mcpMap}, true), '\n')
	cfgPath := filepath.Join(agentsDir, "mcp_config.json")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return []string{"--add-dir", dir}, cleanup, nil
}

func writeClaudeSessionMCPConfig(sessionID, messageID string, servers []sessionMCPServer) ([]string, func(), error) {
	dir, err := createSessionMCPDir(sessionID, messageID)
	if err != nil {
		return nil, func() {}, err
	}
	mcpMap := make(map[string]any, len(servers))
	for _, srv := range servers {
		if srv.Type == "remote" {
			entry := map[string]any{
				"type": "http",
				"url":  srv.URL,
			}
			if len(srv.Headers) > 0 {
				entry["headers"] = srv.Headers
			}
			mcpMap[srv.Name] = entry
		} else {
			entry := map[string]any{
				"type":    "stdio",
				"command": srv.Command[0],
			}
			if len(srv.Command) > 1 {
				entry["args"] = srv.Command[1:]
			}
			if len(srv.Env) > 0 {
				entry["env"] = srv.Env
			}
			mcpMap[srv.Name] = entry
		}
	}
	data := append(marshalJSON(map[string]any{"mcpServers": mcpMap}, true), '\n')
	cfgPath := filepath.Join(dir, "mcp-config.json")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return []string{"--mcp-config", cfgPath}, cleanup, nil
}

func buildGeminiSessionMCPMap(servers []sessionMCPServer) map[string]any {
	mcpMap := make(map[string]any, len(servers))
	for _, srv := range servers {
		entry := map[string]any{}
		if srv.StartupTimeoutMs > 0 {
			entry["timeout"] = srv.StartupTimeoutMs
		}
		if srv.Type == "remote" {
			entry["httpUrl"] = srv.URL
			if len(srv.Headers) > 0 {
				entry["headers"] = srv.Headers
			}
		} else {
			entry["command"] = srv.Command[0]
			if len(srv.Command) > 1 {
				entry["args"] = srv.Command[1:]
			}
			if len(srv.Env) > 0 {
				entry["env"] = srv.Env
			}
		}
		mcpMap[srv.Name] = entry
	}
	return mcpMap
}

func buildCodexSessionMCPArgsAndEnv(servers []sessionMCPServer) ([]string, []string) {
	var args []string
	var env []string
	for idx, srv := range servers {
		prefix := "mcp_servers." + strconv.Quote(srv.Name)
		if srv.Type == "remote" {
			args = append(args, "-c", prefix+".url="+strconv.Quote(srv.URL))
			if srv.StartupTimeoutMs > 0 {
				sec := int(math.Ceil(float64(srv.StartupTimeoutMs) / 1000.0))
				args = append(args, "-c", prefix+".startup_timeout_sec="+strconv.Itoa(sec))
			}
			if len(srv.Headers) > 0 {
				keys := make([]string, 0, len(srv.Headers))
				for k := range srv.Headers {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				hdrIdx := 0
				for _, k := range keys {
					val := srv.Headers[k]
					if strings.EqualFold(strings.TrimSpace(k), "Authorization") {
						trimmed := strings.TrimSpace(val)
						if len(trimmed) > 7 && strings.EqualFold(trimmed[:7], "Bearer ") {
							tok := strings.TrimSpace(trimmed[7:])
							if tok != "" {
								envName := fmt.Sprintf("HOXI_SESSION_MCP_BEARER_%d", idx)
								env = append(env, envName+"="+tok)
								args = append(args, "-c", prefix+".bearer_token_env_var="+strconv.Quote(envName))
								continue
							}
						}
					}
					envName := fmt.Sprintf("HOXI_SESSION_MCP_HDR_%d_%d", idx, hdrIdx)
					hdrIdx++
					env = append(env, envName+"="+val)
					args = append(args, "-c", prefix+".env_http_headers."+strconv.Quote(k)+"="+strconv.Quote(envName))
				}
			}
			continue
		}
		args = append(args, "-c", prefix+".command="+strconv.Quote(srv.Command[0]))
		if len(srv.Command) > 1 {
			quoted := make([]string, len(srv.Command)-1)
			for i, a := range srv.Command[1:] {
				quoted[i] = strconv.Quote(a)
			}
			args = append(args, "-c", prefix+".args=["+strings.Join(quoted, ",")+"]")
		}
		if srv.StartupTimeoutMs > 0 {
			sec := int(math.Ceil(float64(srv.StartupTimeoutMs) / 1000.0))
			args = append(args, "-c", prefix+".startup_timeout_sec="+strconv.Itoa(sec))
		}
		if len(srv.Env) > 0 {
			keys := make([]string, 0, len(srv.Env))
			for k := range srv.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				args = append(args, "-c", prefix+".env."+strconv.Quote(k)+"="+strconv.Quote(srv.Env[k]))
			}
		}
	}
	return args, env
}

// mergeRuntimeMCPArgs places runtime-specific MCP flags where each CLI expects
// them (before trailing "-p=" for agy and before trailing "-" for codex).
func mergeRuntimeMCPArgs(runtime string, geminiUsesAgy bool, args, mcpArgs []string) []string {
	if len(mcpArgs) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+len(mcpArgs))
	switch {
	case runtime == string(RuntimeAntigravity) || (runtime == string(RuntimeGeminiCLI) && geminiUsesAgy):
		if n := len(args); n > 0 && args[n-1] == "-p=" {
			out = append(out, args[:n-1]...)
			out = append(out, mcpArgs...)
			out = append(out, args[n-1])
			return out
		}
	case runtime == string(RuntimeCodex):
		if n := len(args); n > 0 && args[n-1] == "-" {
			out = append(out, args[:n-1]...)
			out = append(out, mcpArgs...)
			out = append(out, args[n-1])
			return out
		}
	}
	out = append(out, args...)
	out = append(out, mcpArgs...)
	return out
}
