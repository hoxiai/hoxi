package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeProjectConfig(t *testing.T, root, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "hoxi.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectPermissionsOnlyTightenHost(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, `{"permission":{"edit":"deny","bash":"ask","webfetch":"allow"}}`)
	hostErr := errors.New("host refused")
	var called int
	var forced bool
	req := TurnRequest{WorktreeDir: root, PermissionHook: func(_ context.Context, pr PermissionRequest) error {
		called++
		forced = pr.ForceAsk
		return hostErr
	}}
	if _, err := loadProjectTurnConfig(&req, nil); err != nil {
		t.Fatal(err)
	}
	if err := req.PermissionHook(context.Background(), PermissionRequest{ToolName: "write"}); err == nil || called != 0 {
		t.Fatal("deny reached host")
	}
	if err := req.PermissionHook(context.Background(), PermissionRequest{ToolName: "bash"}); !errors.Is(err, hostErr) || !forced {
		t.Fatal("ask skipped host")
	}
	if err := req.PermissionHook(context.Background(), PermissionRequest{ToolName: "webfetch"}); !errors.Is(err, hostErr) || forced {
		t.Fatal("allow bypassed host")
	}
}

func TestProjectLocalMCPListingRequiresApproval(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, `{"mcp":{"demo":{"type":"local","command":["node","./launch.mjs"]}}}`)
	var forced bool
	req := TurnRequest{WorktreeDir: root, PermissionHook: func(_ context.Context, pr PermissionRequest) error { forced = pr.ForceAsk; return nil }}
	if _, err := loadProjectTurnConfig(&req, nil); err != nil {
		t.Fatal(err)
	}
	if err := req.PermissionHook(context.Background(), PermissionRequest{ToolName: "mcp_list_tools", Input: json.RawMessage(`{}`)}); err != nil || !forced {
		t.Fatal("process spawn did not ask")
	}
	withoutHost := TurnRequest{WorktreeDir: root}
	if _, err := loadProjectTurnConfig(&withoutHost, nil); err == nil {
		t.Fatal("project enabled without host")
	}
}

func TestProjectMCPOverridesCompatibilityFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"demo":{"command":["old"]},"keep":{"command":["keep"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeProjectConfig(t, root, `{"mcp":{"demo":{"enabled":false},"new":{"type":"local","command":["node","./launch.mjs"]}}}`)
	servers, err := resolveMCPServers(ToolContext{WorktreeDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Name != "keep" || servers[1].Name != "new" || servers[1].Command[1] != "./launch.mjs" {
		t.Fatal(servers)
	}
	writeProjectConfig(t, root, `{"mcp":false}`)
	if _, err := resolveMCPServers(ToolContext{WorktreeDir: root}); err == nil {
		t.Fatal("invalid config silently ignored")
	}
}

func TestProjectMCPEndpointChangeDoesNotInheritCredentials(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, `{"mcp":{"demo":{"type":"remote","url":"https://new.example.test/mcp"}}}`)
	base := map[string]any{"mcp": map[string]any{"demo": map[string]any{
		"type": "remote", "url": "https://old.example.test/mcp", "headers": map[string]any{"Authorization": "Bearer local-secret"},
		"oauth": map[string]any{"accessToken": "local-token"},
	}}}
	req := TurnRequest{WorktreeDir: root, PermissionHook: func(context.Context, PermissionRequest) error { return nil }}
	if _, err := loadProjectTurnConfig(&req, base); err != nil {
		t.Fatal(err)
	}
	servers, err := resolveMCPServers(ToolContext{WorktreeDir: root, projectConfig: req.projectConfig, MCPConfig: req.MCPConfig})
	if err != nil || len(servers) != 1 {
		t.Fatalf("%v %v", servers, err)
	}
	if len(servers[0].Headers) != 0 {
		t.Fatal("project endpoint inherited local credentials")
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcp":{"demo":{"type":"remote","url":"https://new.example.test/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err = resolveMCPServers(ToolContext{WorktreeDir: root, projectConfig: req.projectConfig, MCPConfig: req.MCPConfig})
	if err != nil || len(servers) != 1 || len(servers[0].Headers) != 0 {
		t.Fatal("compatibility file laundered old credentials")
	}
	writeProjectConfig(t, root, `{"mcp":{"demo":{"type":"remote","url":"https://old.example.test/mcp"}}}`)
	req = TurnRequest{WorktreeDir: root, PermissionHook: func(context.Context, PermissionRequest) error { return nil }}
	if _, err := loadProjectTurnConfig(&req, base); err != nil {
		t.Fatal(err)
	}
	servers, err = resolveMCPServers(ToolContext{WorktreeDir: root, projectConfig: req.projectConfig, MCPConfig: req.MCPConfig})
	if err != nil || len(servers) != 1 || servers[0].Headers["Authorization"] != "Bearer local-secret" {
		t.Fatalf("matching endpoint lost local auth: %v %v", servers, err)
	}
}

func TestNamedSkillRulesCannotBypassHostOrLocalDeny(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, `{"permission":{"skill":{"private":"ask"}}}`)
	var forced bool
	req := TurnRequest{WorktreeDir: root, PermissionHook: func(_ context.Context, pr PermissionRequest) error { forced = pr.ForceAsk; return nil }}
	if _, err := loadProjectTurnConfig(&req, nil); err != nil {
		t.Fatal(err)
	}
	if err := req.PermissionHook(context.Background(), PermissionRequest{ToolName: "skill", Input: json.RawMessage(`{"name":"private"}`)}); err != nil || !forced {
		t.Fatal("named skill ask did not reach approval")
	}
	writeProjectConfig(t, root, `{"permission":{"skill":{"private":"allow"}}}`)
	req = TurnRequest{WorktreeDir: root, PermissionHook: func(context.Context, PermissionRequest) error { return nil }}
	if _, err := loadProjectTurnConfig(&req, map[string]any{"permission": map[string]any{"skill": map[string]any{"private": "deny"}}}); err != nil {
		t.Fatal(err)
	}
	if err := req.PermissionHook(context.Background(), PermissionRequest{ToolName: "skill", Input: json.RawMessage(`{"name":"private"}`)}); err == nil {
		t.Fatal("project allow overrode local deny")
	}
}
