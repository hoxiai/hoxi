package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type mcpListToolsTool struct{}

func (t *mcpListToolsTool) Name() string     { return "mcp_list_tools" }
func (t *mcpListToolsTool) IsReadOnly() bool { return true }
func (t *mcpListToolsTool) Description() string {
	return "List configured MCP servers (remote HTTP and local stdio) and discover one page of tools and parameter schemas per server. To continue a list, pass the returned server and cursor."
}
func (t *mcpListToolsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server": map[string]any{"type": "string", "description": "Optional MCP server name filter"},
			"cursor": map[string]any{"type": "string", "description": "Continuation cursor returned for this server"},
		},
	}
}

func (t *mcpListToolsTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Server string  `json:"server"`
		Cursor *string `json:"cursor"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return ToolOutput{Title: t.Name()}, err
		}
	}
	filter := strings.TrimSpace(args.Server)

	params := map[string]any{}
	if args.Cursor != nil {
		if filter == "" {
			return ToolOutput{Title: t.Name()}, fmt.Errorf("an MCP server is required with a pagination cursor")
		}
		params["cursor"] = *args.Cursor
	}
	servers, err := resolveMCPServers(tc)
	if err != nil {
		return ToolOutput{Title: t.Name(), IsError: true}, err
	}
	if len(servers) == 0 {
		return ToolOutput{
			Title:  "0 MCP servers",
			Output: "No enabled MCP servers are configured in Hoxi settings or .mcp.json.",
		}, nil
	}

	var full, compact strings.Builder
	matched := 0
	for _, srv := range servers {
		if filter != "" && !strings.EqualFold(srv.Name, filter) {
			continue
		}
		matched++
		if srv.OAuthError != "" {
			line := fmt.Sprintf("## Server: %s (%s) — error: %s\n\n", srv.Name, srv.Type, srv.OAuthError)
			full.WriteString(line)
			compact.WriteString(line)
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		rawRes, err := invokeMCPMethod(callCtx, tc.WorktreeDir, srv, "tools/list", params)
		cancel()
		if err != nil {
			line := fmt.Sprintf("## Server: %s (%s) — error: %v\n\n", srv.Name, srv.Type, err)
			full.WriteString(line)
			compact.WriteString(line)
			continue
		}
		var listPayload struct {
			NextCursor *string `json:"nextCursor"`
			Tools      []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(rawRes, &listPayload); err != nil || listPayload.Tools == nil {
			if filter != "" {
				return ToolOutput{Title: srv.Name}, fmt.Errorf("invalid tools/list response from %s", srv.Name)
			}
			line := fmt.Sprintf("## Server: %s (%s) — invalid tools/list response\n\n", srv.Name, srv.Type)
			full.WriteString(line)
			compact.WriteString(line)
			continue
		}
		header := fmt.Sprintf("## Server: %s (%s) — %d tool(s)\n", srv.Name, srv.Type, len(listPayload.Tools))
		full.WriteString(header)
		compact.WriteString(header)
		if next := listPayload.NextCursor; next != nil {
			if args.Cursor != nil && *next == *args.Cursor {
				return ToolOutput{Title: srv.Name}, fmt.Errorf("incomplete MCP tool list from %s: repeated continuation cursor", srv.Name)
			}
			continuation, _ := json.Marshal(map[string]string{"server": srv.Name, "cursor": *next})
			line := fmt.Sprintf("More tools available. Continue with mcp_list_tools %s\n", continuation)
			full.WriteString(line)
			compact.WriteString(line)
		}
		for _, tool := range listPayload.Tools {
			schemaBytes, _ := json.Marshal(tool.InputSchema)
			fmt.Fprintf(&full, "- %s: %s\n  schema: %s\n", tool.Name, strings.TrimSpace(tool.Description), string(schemaBytes))
			fmt.Fprintf(&compact, "- %s: %s\n", tool.Name, mcpToolSummaryLine(tool.Description))
		}
		full.WriteString("\n")
		compact.WriteString("\n")
	}
	out := full.String()
	if filter == "" && len(out) > maxUnfilteredMCPListBytes {
		// Every server's full schemas would be truncated mid-list (forcing a
		// second, filtered call) and then re-billed on later steps. Return an
		// index instead and let the model fetch schemas for the server it needs.
		out = compact.String() + "Schemas omitted because the full listing is large. Call mcp_list_tools with {\"server\": \"<name>\"} to get parameter schemas before mcp_call_tool.\n"
	}
	if matched == 0 {
		return ToolOutput{Title: filter, IsError: true}, fmt.Errorf("MCP server %q not found", filter)
	}
	return ToolOutput{
		Title:  fmt.Sprintf("%d MCP server(s)", matched),
		Output: strings.TrimSpace(out),
	}, nil
}

// maxUnfilteredMCPListBytes keeps an unfiltered listing well inside the 32 KiB
// tool output cap; larger listings fall back to a name + summary index.
const maxUnfilteredMCPListBytes = 12 * 1024

func mcpToolSummaryLine(desc string) string {
	line := strings.TrimSpace(desc)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if runes := []rune(line); len(runes) > 160 {
		line = string(runes[:160]) + "…"
	}
	return line
}
