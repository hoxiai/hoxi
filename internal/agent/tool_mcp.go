package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const mcpCallTimeout = 45 * time.Second

var mcpHTTPTransport http.RoundTripper

// SetMCPHTTPTransportForTest overrides the HTTP RoundTripper used by remote MCP calls in tests.
func SetMCPHTTPTransportForTest(rt http.RoundTripper) { mcpHTTPTransport = rt }

type mcpServerSpec struct {
	Name    string
	Type    string // "remote" or "local"
	URL     string
	Headers map[string]string
	Command []string
	Env     map[string]string
}

type mcpListToolsTool struct{}

func (t *mcpListToolsTool) Name() string     { return "mcp_list_tools" }
func (t *mcpListToolsTool) IsReadOnly() bool { return true }
func (t *mcpListToolsTool) Description() string {
	return "List configured MCP servers (remote HTTP and local stdio) and discover their available tools and parameter schemas."
}
func (t *mcpListToolsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server": map[string]any{"type": "string", "description": "Optional MCP server name filter"},
		},
	}
}

func (t *mcpListToolsTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Server string `json:"server"`
	}
	_ = json.Unmarshal(input, &args)
	filter := strings.TrimSpace(args.Server)

	servers := resolveMCPServers(tc)
	if len(servers) == 0 {
		return ToolOutput{
			Title:  "0 MCP servers",
			Output: "No enabled MCP servers are configured in Hoxi settings or .mcp.json.",
		}, nil
	}

	var b strings.Builder
	matched := 0
	for _, srv := range servers {
		if filter != "" && !strings.EqualFold(srv.Name, filter) {
			continue
		}
		matched++
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		rawRes, err := invokeMCPMethod(callCtx, tc.WorktreeDir, srv, "tools/list", map[string]any{})
		cancel()
		if err != nil {
			fmt.Fprintf(&b, "## Server: %s (%s) — error: %v\n\n", srv.Name, srv.Type, err)
			continue
		}
		var listPayload struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(rawRes, &listPayload)
		fmt.Fprintf(&b, "## Server: %s (%s) — %d tool(s)\n", srv.Name, srv.Type, len(listPayload.Tools))
		for _, tool := range listPayload.Tools {
			schemaBytes, _ := json.Marshal(tool.InputSchema)
			fmt.Fprintf(&b, "- %s: %s\n  schema: %s\n", tool.Name, strings.TrimSpace(tool.Description), string(schemaBytes))
		}
		b.WriteString("\n")
	}
	if matched == 0 {
		return ToolOutput{Title: filter, IsError: true}, fmt.Errorf("MCP server %q not found", filter)
	}
	return ToolOutput{
		Title:  fmt.Sprintf("%d MCP server(s)", matched),
		Output: strings.TrimSpace(b.String()),
	}, nil
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

	var target *mcpServerSpec
	for _, srv := range resolveMCPServers(tc) {
		if strings.EqualFold(srv.Name, srvName) {
			cp := srv
			target = &cp
			break
		}
	}
	if target == nil {
		return ToolOutput{Title: srvName + "/" + toolName, IsError: true}, fmt.Errorf("MCP server %q is not configured or is disabled", srvName)
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

	var callRes struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(rawRes, &callRes) == nil && len(callRes.Content) > 0 {
		var parts []string
		for _, c := range callRes.Content {
			if strings.TrimSpace(c.Text) != "" {
				parts = append(parts, c.Text)
			}
		}
		joined := strings.Join(parts, "\n")
		if callRes.IsError {
			return ToolOutput{Title: srvName + ":" + toolName, Output: joined, IsError: true}, fmt.Errorf("MCP tool returned error")
		}
		return ToolOutput{Title: srvName + ":" + toolName, Output: joined}, nil
	}

	return ToolOutput{
		Title:  srvName + ":" + toolName,
		Output: string(rawRes),
	}, nil
}

func resolveMCPServers(tc ToolContext) []mcpServerSpec {
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
			spec := mcpServerSpec{
				Name:    strings.TrimSpace(name),
				Headers: toStringMap(cfg["headers"]),
				Env:     toStringMap(cfg["environment"]),
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
				collectFromMap(mcpMap)
			}
		}
	}
	if strings.TrimSpace(tc.WorktreeDir) != "" {
		if raw, err := os.ReadFile(filepath.Join(tc.WorktreeDir, ".mcp.json")); err == nil {
			var wsDoc map[string]any
			if json.Unmarshal(raw, &wsDoc) == nil {
				if srvMap, ok := wsDoc["mcpServers"].(map[string]any); ok {
					collectFromMap(srvMap)
				} else if mcpMap, ok := wsDoc["mcp"].(map[string]any); ok {
					collectFromMap(mcpMap)
				}
			}
		}
	}

	names := make([]string, 0, len(merged))
	for k := range merged {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]mcpServerSpec, 0, len(names))
	for _, k := range names {
		out = append(out, merged[k])
	}
	return out
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

func invokeRemoteMCP(ctx context.Context, srv mcpServerSpec, method string, params map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range srv.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: mcpCallTimeout, Transport: mcpHTTPTransport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
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
					return nil, fmt.Errorf("MCP error %d: %s", env.Error.Code, env.Error.Message)
				}
				return env.Result, nil
			}
		}
	}

	var env jsonRPCEnvelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return nil, fmt.Errorf("invalid MCP JSON-RPC response: %w", err)
	}
	if env.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", env.Error.Code, env.Error.Message)
	}
	return env.Result, nil
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
