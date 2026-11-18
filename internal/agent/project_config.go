package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxiconfig"
)

// Snapshot the project config once per turn, including child turns, rather than
// re-reading it for every tool in a concurrent batch. Do not change host policy
// or persist project values in the provider's machine-local configuration.
func prepareProjectTurnConfig(req *TurnRequest) (map[string]any, error) {
	var base map[string]any
	if req.MCPConfig != nil {
		base = req.MCPConfig()
	}
	return loadProjectTurnConfig(req, base)
}

func loadProjectTurnConfig(req *TurnRequest, base map[string]any) (map[string]any, error) {
	project, err := hoxiconfig.Load(req.WorktreeDir)
	if err != nil {
		return nil, err
	}
	if project != nil && req.PermissionHook == nil {
		return nil, fmt.Errorf("hoxi.json requires a native approval host")
	}
	// Tool approval is handled by the wrapper, not by replacing host rules.
	// task/skill named rules retain their existing domain-specific evaluators.
	effective := hoxiconfig.Overlay(base, project)
	req.projectConfig = project
	if req.projectConfig == nil {
		req.projectConfig = map[string]any{}
	}
	toolConfig := hoxiconfig.Overlay(nil, effective)
	toolConfig["mcp"] = base["mcp"] // resolver applies the project after .mcp.json
	req.MCPConfig = func() map[string]any { return toolConfig }
	if req.PermissionHook == nil {
		return effective, nil
	}
	host := req.PermissionHook
	req.PermissionHook = func(ctx context.Context, pr PermissionRequest) error {
		if pr.ToolName == "task" {
			var args struct {
				SubagentType string `json:"subagent_type"`
			}
			_ = json.Unmarshal(pr.Input, &args)
			for _, doc := range []map[string]any{base, project} {
				perm, _ := doc["permission"].(map[string]any)
				switch evalTaskPolicyValue(perm["task"], args.SubagentType) {
				case "deny":
					return fmt.Errorf("task %s is denied by configuration", args.SubagentType)
				case "ask":
					pr.ForceAsk = true
				}
			}
		}
		if pr.ToolName == "skill" {
			var args struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(pr.Input, &args)
			for _, doc := range []map[string]any{base, project} {
				perm, _ := doc["permission"].(map[string]any)
				switch evalTaskPolicyValue(perm["skill"], args.Name) {
				case "deny":
					return fmt.Errorf("skill %s is denied by configuration", args.Name)
				case "ask":
					pr.ForceAsk = true
				}
			}
		}
		// tools/list on a local MCP server starts a process. A repository
		// declaration is not consent to execute that process automatically.
		if pr.ToolName == "mcp_list_tools" || pr.ToolName == "mcp_call_tool" {
			var args struct {
				Server string `json:"server"`
			}
			_ = json.Unmarshal(pr.Input, &args)
			servers, _ := project["mcp"].(map[string]any)
			for name, raw := range servers {
				cfg, _ := raw.(map[string]any)
				if (args.Server == "" || strings.EqualFold(strings.TrimSpace(args.Server), name)) && cfg["type"] == "local" && cfg["enabled"] != false {
					pr.ForceAsk = true
				}
			}
		}
		switch hoxiconfig.ToolAction(project, pr.ToolName) {
		case "deny":
			return fmt.Errorf("tool %s is denied by hoxi.json", pr.ToolName)
		case "ask":
			pr.ForceAsk = true
		}
		if pr.ForceAsk && host == nil {
			return fmt.Errorf("tool %s requires approval under hoxi.json but no approval host is available", pr.ToolName)
		}
		if host != nil {
			return host(ctx, pr)
		}
		return nil
	}
	return effective, nil
}
