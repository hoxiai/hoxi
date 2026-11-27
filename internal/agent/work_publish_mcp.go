package agent

import (
	"net/url"
	"strings"
	"time"
)

// Only the local Work gateway's approval operation may hold the MCP request
// while waiting for a person. Remote endpoints retain the normal timeout.
func workPublishMCPTimeout(endpoint string, args map[string]any) time.Duration {
	if args["action"] != "execute" {
		return mcpCallTimeout
	}
	id, ok := args["hoxiPublishProposalId"].(string)
	if !ok || len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return mcpCallTimeout
	}
	u, err := url.Parse(endpoint)
	if err == nil && isLoopbackHost(u.Hostname()) && strings.HasPrefix(u.Path, "/api/hoxi/work/apps/") && strings.HasSuffix(u.Path, "/mcp") {
		return 6 * time.Minute
	}
	return mcpCallTimeout
}
