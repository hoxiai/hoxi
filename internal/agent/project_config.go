package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	repoApprovalMCP := repoMCPServersRequiringApproval(req.WorktreeDir, base, project)
	if project != nil && req.PermissionHook == nil {
		return nil, fmt.Errorf("hoxi.json requires a native approval host")
	}
	if len(repoApprovalMCP) > 0 && req.PermissionHook == nil {
		return nil, fmt.Errorf(".mcp.json requires a native approval host")
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
		// tools/list on a repository-declared local MCP server starts a process,
		// and a repository-declared remote endpoint sends outbound HTTP requests.
		// A repository file (hoxi.json or .mcp.json) is not consent to execute either automatically.
		if pr.ToolName == "mcp_list_tools" || pr.ToolName == "mcp_call_tool" {
			var args struct {
				Server string `json:"server"`
			}
			_ = json.Unmarshal(pr.Input, &args)
			targetServer := strings.ToLower(strings.TrimSpace(args.Server))
			for name := range repoApprovalMCP {
				if targetServer == "" || targetServer == name {
					pr.ForceAsk = true
					break
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

// repoMCPServersRequiringApproval returns normalized names of enabled MCP servers
// declared or redirected by repository files (.mcp.json / hoxi.json) that must
// require explicit user approval before listing or calling tools.
func repoMCPServersRequiringApproval(worktreeDir string, base, project map[string]any) map[string]bool {
	machineServers := map[string]any{}
	if baseMCP, ok := base["mcp"].(map[string]any); ok {
		for name, raw := range baseMCP {
			if key := strings.ToLower(strings.TrimSpace(name)); key != "" {
				machineServers[key] = raw
			}
		}
	}
	repoEntries := map[string]map[string]any{}
	applyRepo := func(m map[string]any) {
		for name, raw := range m {
			key := strings.ToLower(strings.TrimSpace(name))
			cfg, ok := raw.(map[string]any)
			if !ok || key == "" {
				continue
			}
			if isMCPConfigDisabled(cfg) {
				delete(repoEntries, key)
				continue
			}
			repoEntries[key] = cfg
		}
	}
	applyRepo(loadWorkspaceDotMCP(worktreeDir))
	if projectMCP, ok := project["mcp"].(map[string]any); ok {
		applyRepo(projectMCP)
	}
	if len(repoEntries) == 0 {
		return nil
	}
	out := make(map[string]bool)
	for key, cfg := range repoEntries {
		isRemote, urlStr, isLocal := classifyMCPServerConfig(cfg)
		if isLocal {
			out[key] = true
			continue
		}
		if isRemote {
			oldCfg, _ := machineServers[key].(map[string]any)
			oldRemote, oldURL, _ := classifyMCPServerConfig(oldCfg)
			if !oldRemote || oldURL != urlStr {
				out[key] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func loadWorkspaceDotMCP(worktreeDir string) map[string]any {
	if strings.TrimSpace(worktreeDir) == "" {
		return nil
	}
	path := filepath.Join(worktreeDir, ".mcp.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > hoxiconfig.MaxConfigBytes {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, hoxiconfig.MaxConfigBytes+1))
	if err != nil || len(raw) > hoxiconfig.MaxConfigBytes {
		return nil
	}
	var wsDoc map[string]any
	if json.Unmarshal(raw, &wsDoc) != nil || wsDoc == nil {
		return nil
	}
	if srvMap, ok := wsDoc["mcpServers"].(map[string]any); ok {
		return srvMap
	}
	if mcpMap, ok := wsDoc["mcp"].(map[string]any); ok {
		return mcpMap
	}
	return nil
}

func isMCPConfigDisabled(cfg map[string]any) bool {
	if enabled, has := cfg["enabled"].(bool); has && !enabled {
		return true
	}
	if disabled, has := cfg["disabled"].(bool); has && disabled {
		return true
	}
	return false
}

func classifyMCPServerConfig(cfg map[string]any) (isRemote bool, urlStr string, isLocal bool) {
	if len(cfg) == 0 {
		return false, "", false
	}
	urlVal, _ := cfg["url"].(string)
	urlStr = strings.TrimSpace(urlVal)
	cmd := parseCommandSlice(cfg["command"], cfg["args"])
	typ, _ := cfg["type"].(string)
	typ = strings.ToLower(strings.TrimSpace(typ))
	switch {
	case typ == "remote" || (urlStr != "" && len(cmd) == 0):
		return urlStr != "", urlStr, false
	case typ == "local" || typ == "stdio" || len(cmd) > 0:
		return false, "", len(cmd) > 0
	default:
		return false, "", false
	}
}

// applyRepoMCPLayer overlays a repository MCP layer (.mcp.json or hoxi.json) onto
// layers without allowing an endpoint replacement to inherit machine-local secrets.
func applyRepoMCPLayer(layers, machineServers, repoMap map[string]any) {
	for name, raw := range repoMap {
		key := strings.ToLower(strings.TrimSpace(name))
		cfg, ok := raw.(map[string]any)
		if !ok || key == "" {
			continue
		}
		if isMCPConfigDisabled(cfg) {
			layers[key] = map[string]any{"enabled": false}
			continue
		}
		old, _ := machineServers[key].(map[string]any)
		next := hoxiconfig.Overlay(nil, cfg)
		isRemote, urlStr, _ := classifyMCPServerConfig(cfg)
		if isRemote {
			oldRemote, oldURL, _ := classifyMCPServerConfig(old)
			if oldRemote && oldURL == urlStr {
				for _, field := range []string{"headers", "oauth"} {
					if v, ok := old[field]; ok {
						next[field] = v
					}
				}
			} else {
				next["oauth"] = false
			}
		}
		layers[key] = next
	}
}
