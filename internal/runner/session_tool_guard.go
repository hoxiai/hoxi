package runner

import (
	"fmt"
	"strings"
)

const maxSessionDeniedTools = 32

func normalizeDeniedTools(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		clean := strings.ToLower(strings.TrimSpace(raw))
		if clean == "" || len(clean) > 96 || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
		if len(out) >= maxSessionDeniedTools {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeUniqueToolRules(base, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make([]string, 0, len(base)+len(extra))
	merged = append(merged, base...)
	merged = append(merged, extra...)
	return normalizeDeniedTools(merged)
}

func isReadOnlyBrowserAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "snapshot", "screenshot", "diagnostics", "wait", "state", "status", "list":
		return true
	default:
		return false
	}
}

func isReadOnlyScheduleAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "list", "get":
		return true
	default:
		return false
	}
}

func isHighRiskMCPToolName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	for _, token := range []string{
		"publish", "unpublish", "delete_remote", "remove_remote",
		"price_override", "update_price", "bulk_delete", "withdraw",
		"payout", "transfer", "oauth", "authorize", "credential",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// sessionDeniedToolReason reports whether toolName (with optional args) is
// explicitly disallowed for rec by its session-level DeniedTools policy.
func sessionDeniedToolReason(rec SessionRecord, toolName string, args map[string]interface{}) (string, bool) {
	if len(rec.DeniedTools) == 0 {
		return "", false
	}
	lowerTool := strings.ToLower(strings.TrimSpace(toolName))
	if lowerTool == "" {
		return "", false
	}
	act := strings.ToLower(strings.TrimSpace(firstString(args["action"])))
	mcpTarget := strings.ToLower(strings.TrimSpace(firstString(args["tool"], args["toolName"], args["name"])))

	for _, rule := range rec.DeniedTools {
		r := strings.ToLower(strings.TrimSpace(rule))
		if r == "" {
			continue
		}
		if r == lowerTool {
			return fmt.Sprintf("tool %q is disabled for this session", toolName), true
		}
		if strings.HasPrefix(r, lowerTool+":") {
			sub := strings.TrimPrefix(r, lowerTool+":")
			if sub == "write" {
				switch lowerTool {
				case "hoxi_browser":
					if !isReadOnlyBrowserAction(act) {
						return fmt.Sprintf("interactive browser action %q is disabled for this session", act), true
					}
				case "hoxi_schedule":
					if !isReadOnlyScheduleAction(act) {
						return fmt.Sprintf("schedule mutation %q is disabled for this session", act), true
					}
				default:
					return fmt.Sprintf("write action %q on %q is disabled for this session", act, toolName), true
				}
			} else if sub != "" && act == sub {
				return fmt.Sprintf("tool action %q:%q is disabled for this session", toolName, act), true
			}
		}
		if r == "mcp:publish" && (lowerTool == "mcp_call_tool" || strings.HasPrefix(lowerTool, "mcp__")) {
			checkName := lowerTool
			if lowerTool == "mcp_call_tool" && mcpTarget != "" {
				checkName = mcpTarget
			}
			if isHighRiskMCPToolName(checkName) {
				return fmt.Sprintf("external publication or high-risk MCP tool %q requires desktop owner execution", checkName), true
			}
		}
	}
	return "", false
}
