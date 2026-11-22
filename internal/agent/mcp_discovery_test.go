package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestMCPToolDiscoveryContinuesWithOpaqueCursor(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	requests := 0
	SetMCPHTTPTransportForTest(mcpRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var rpc struct {
			Method string
			Params struct{ Cursor string }
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatal(err)
		}
		switch rpc.Method {
		case "initialize":
			return mcpJSONResp(200, nil, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}}`), nil
		case "notifications/initialized":
			return mcpJSONResp(202, nil, ""), nil
		case "tools/list":
			requests++
			if rpc.Params.Cursor == "stuck" {
				return mcpJSONResp(200, nil, `{"jsonrpc":"2.0","result":{"tools":[],"nextCursor":"stuck"}}`), nil
			}
			if rpc.Params.Cursor == "page-two" {
				return mcpJSONResp(200, nil, `{"jsonrpc":"2.0","id":3,"result":{"tools":[{"name":"lookup_second","inputSchema":{"type":"object"}}]}}`), nil
			}
			return mcpJSONResp(200, nil, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"lookup_first","inputSchema":{"type":"object"}}],"nextCursor":"page-two"}}`), nil
		default:
			t.Fatalf("unexpected method %s", rpc.Method)
			return nil, nil
		}
	}))
	defer SetMCPHTTPTransportForTest(nil)
	tc := ToolContext{WorktreeDir: t.TempDir(), MCPConfig: func() map[string]any {
		return map[string]any{"mcp": map[string]any{"catalog": map[string]any{"type": "remote", "url": "https://example.invalid/mcp", "oauth": false}}}
	}}
	result, err := (&mcpListToolsTool{}).Execute(context.Background(), tc, json.RawMessage(`{"server":"catalog"}`))
	if err != nil || !strings.Contains(result.Output, "lookup_first") {
		t.Fatalf("no first page: %s %v", result.Output, err)
	}
	if requests != 1 || !strings.Contains(result.Output, `"cursor":"page-two"`) {
		t.Fatalf("missing continuation: %s", result.Output)
	}
	tool := &mcpListToolsTool{}
	result, err = tool.Execute(context.Background(), tc, json.RawMessage(`{"server":"catalog","cursor":"page-two"}`))
	if err != nil || requests != 2 || !strings.Contains(result.Output, "lookup_second") || strings.Contains(result.Output, "Continue with") {
		t.Fatalf("last page: %s %v", result.Output, err)
	}
	_, err = tool.Execute(context.Background(), tc, json.RawMessage(`{"cursor":"page-two"}`))
	if err == nil || requests != 2 {
		t.Fatal("a cursor must be scoped to one server")
	}
	_, err = tool.Execute(context.Background(), tc, json.RawMessage(`{"server":"catalog","cursor":"stuck"}`))
	if err == nil || !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("repeated cursor: %v", err)
	}
}
