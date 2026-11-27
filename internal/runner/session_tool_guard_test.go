package runner

import "testing"

func TestSessionDeniedToolReason(t *testing.T) {
	rec := SessionRecord{
		DeniedTools: normalizeDeniedTools([]string{
			"hoxi_computer",
			"hoxi_browser:write",
			"hoxi_schedule:write",
			"hoxi_remote",
			"hoxi_data",
			"mcp:publish",
		}),
	}

	if _, denied := sessionDeniedToolReason(rec, "hoxi_computer", map[string]interface{}{"action": "screenshot"}); !denied {
		t.Fatal("expected hoxi_computer to be denied")
	}
	if _, denied := sessionDeniedToolReason(rec, "hoxi_browser", map[string]interface{}{"action": "snapshot"}); denied {
		t.Fatal("expected readonly hoxi_browser snapshot to be allowed when only write is denied")
	}
	if _, denied := sessionDeniedToolReason(rec, "hoxi_browser", map[string]interface{}{"action": "click"}); !denied {
		t.Fatal("expected interactive hoxi_browser click to be denied")
	}
	if _, denied := sessionDeniedToolReason(rec, "hoxi_schedule", map[string]interface{}{"action": "list"}); denied {
		t.Fatal("expected readonly hoxi_schedule list to be allowed")
	}
	if _, denied := sessionDeniedToolReason(rec, "hoxi_schedule", map[string]interface{}{"action": "create"}); !denied {
		t.Fatal("expected hoxi_schedule create to be denied")
	}
	if _, denied := sessionDeniedToolReason(rec, "mcp_call_tool", map[string]interface{}{"tool": "commerce.draft.publish"}); !denied {
		t.Fatal("expected high-risk MCP publish tool to be denied")
	}
	if _, denied := sessionDeniedToolReason(rec, "mcp_call_tool", map[string]interface{}{"tool": "commerce.catalog.list"}); denied {
		t.Fatal("expected catalog list MCP tool to be allowed")
	}
}
