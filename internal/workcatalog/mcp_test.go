package workcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStandardMCPHandshakeToolsResourcesAndPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, scope, f, _ := setupCatalog(t)
	handler := NewMCPHandler(s, func(*http.Request) (Connection, error) {
		return Connection{Scope: scope, Platform: func() (Platform, error) { return f, nil },
			Authorize: func(context.Context, Draft) error { return nil }}, nil
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 10 {
		t.Fatal(tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Fatal("missing standard MCP schema or annotations", tool.Name)
		}
		if tool.Name == "work_draft_publish" && (tool.Annotations.ReadOnlyHint || !*tool.Annotations.DestructiveHint || !*tool.Annotations.OpenWorldHint) {
			t.Fatal("publication risk hints are incorrect")
		}
	}
	call := func(name string, args any) *mcp.CallToolResult {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	created := call("work_draft_create", map[string]any{"productId": "123", "changes": map[string]any{"subject": "New wholesale title"}})
	if created.IsError || created.StructuredContent == nil || len(created.Content) == 0 {
		t.Fatal("missing structured result or backward-compatible text", created)
	}
	raw, _ := json.Marshal(created.StructuredContent)
	var d Draft
	if err := json.Unmarshal(raw, &d); err != nil || d.ID == "" || len(d.Changes) != 1 {
		t.Fatal(d, err, string(raw))
	}
	for _, args := range []map[string]any{
		{"productId": "123", "changes": map[string]any{"saleInfo": map[string]any{"amountOnSale": 0}}},
		{"productId": "123", "changes": map[string]any{"subject": "Valid"}, "storeId": "another-store"},
	} {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "work_draft_create", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatal("schema admitted unsupported or cross-store input", result)
		}
	}
	res, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "hoxi-work://stores/store-a/drafts/" + d.ID})
	if err != nil || len(res.Contents) != 1 || !strings.Contains(res.Contents[0].Text, d.Revision) {
		t.Fatal(res, err)
	}
	if _, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "hoxi-work://stores/store-b/drafts/" + d.ID}); err == nil {
		t.Fatal("resource crossed the store boundary")
	}
	published := call("work_draft_publish", map[string]any{"draftId": d.ID, "revision": d.Revision})
	if published.IsError || f.writes != 1 {
		t.Fatal(published, f.writes)
	}
	prompts, err := client.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 1 {
		t.Fatal(prompts, err)
	}
}
