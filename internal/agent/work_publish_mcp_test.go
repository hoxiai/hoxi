package agent

import (
	"strings"
	"testing"
)

func TestWorkPublishMCPTimeoutOnlyForLocalApproval(t *testing.T) {
	args := map[string]any{"action": "execute", "hoxiPublishProposalId": strings.Repeat("a", 32)}
	if workPublishMCPTimeout("http://127.0.0.1:5420/api/hoxi/work/apps/demo/mcp", args) <= mcpCallTimeout {
		t.Fatal("local proposal needs time for a person to approve")
	}
	for _, endpoint := range []string{"https://example.com/api/hoxi/work/apps/demo/mcp", "http://127.0.0.1:5420/mcp"} {
		if workPublishMCPTimeout(endpoint, args) != mcpCallTimeout {
			t.Fatal(endpoint)
		}
	}
}
