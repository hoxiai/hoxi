package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStdioMCPEnvScrubsRepoServersAndPreservesMachineServers(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/dev",
		"HTTPS_PROXY=http://user:pw@proxy:8080",
		"OPENAI_API_KEY=sk-proj-secret12345678901234567890",
		"GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"INNOCENT_VAL=AKIA0000000000000000",
		"DATABASE_URL=postgres://app:pw@db/prod",
		"LD_PRELOAD=/tmp/evil.so",
		"DYLD_INSERT_LIBRARIES=/tmp/evil.dylib",
		"BASH_ENV=/tmp/hook.sh",
	}
	explicit := map[string]string{
		"HOXI_TEST_MCP_STDIO":   "1",
		"LD_PRELOAD":            "/tmp/repo-evil.so",
		"DYLD_INSERT_LIBRARIES": "/tmp/repo-evil.dylib",
		"BASH_ENV":              "/tmp/repo-hook.sh",
	}

	repoEnv := buildStdioMCPEnv(base, explicit, true)
	gotRepo := make(map[string]string, len(repoEnv))
	for _, kv := range repoEnv {
		k, v, _ := strings.Cut(kv, "=")
		gotRepo[k] = v
	}
	for _, keep := range []string{"PATH", "HOME", "HTTPS_PROXY", "HOXI_TEST_MCP_STDIO"} {
		if _, ok := gotRepo[keep]; !ok {
			t.Errorf("repo MCP env dropped expected variable %s", keep)
		}
	}
	for _, drop := range []string{
		"OPENAI_API_KEY", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "INNOCENT_VAL", "DATABASE_URL",
		"LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "BASH_ENV",
	} {
		if _, ok := gotRepo[drop]; ok {
			t.Errorf("repo MCP env leaked %s", drop)
		}
	}

	// Repo server with no explicit environment still returns a scrubbed slice (never nil).
	repoNoExplicit := buildStdioMCPEnv(base, nil, true)
	if repoNoExplicit == nil {
		t.Fatal("repo MCP with empty explicit env must still scrub inherited os.Environ")
	}
	for _, kv := range repoNoExplicit {
		k, _, _ := strings.Cut(kv, "=")
		if k == "OPENAI_API_KEY" || k == "GITHUB_TOKEN" || k == "SSH_AUTH_SOCK" || k == "LD_PRELOAD" {
			t.Errorf("repo MCP without explicit env leaked %s", k)
		}
	}

	// Machine-local server without explicit env keeps default nil inheritance.
	if got := buildStdioMCPEnv(base, nil, false); got != nil {
		t.Fatalf("machine MCP without explicit env should return nil, got %v", got)
	}
	// Machine-local server with explicit env preserves inherited credentials.
	machEnv := buildStdioMCPEnv(base, map[string]string{"CUSTOM_FLAG": "1"}, false)
	gotMach := make(map[string]string, len(machEnv))
	for _, kv := range machEnv {
		k, v, _ := strings.Cut(kv, "=")
		gotMach[k] = v
	}
	if gotMach["GITHUB_TOKEN"] == "" || gotMach["CUSTOM_FLAG"] != "1" {
		t.Fatalf("machine MCP should preserve host credentials and explicit vars: %v", gotMach)
	}
}

func TestResolveMCPServersMarksFromRepoFlag(t *testing.T) {
	root := t.TempDir()
	hoxiJSON := `{"version":1,"mcp":{"repo-local":{"type":"local","command":["echo","hi"],"enabled":true}}}`
	if err := os.WriteFile(filepath.Join(root, "hoxi.json"), []byte(hoxiJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	tc := ToolContext{
		WorktreeDir: root,
		MCPConfig: func() map[string]any {
			return map[string]any{
				"mcp": map[string]any{
					"machine-local": map[string]any{
						"type":    "local",
						"command": []any{"echo", "ok"},
						"enabled": true,
					},
				},
			}
		},
	}
	servers, err := resolveMCPServers(tc)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]mcpServerSpec{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if !byName["repo-local"].FromRepo {
		t.Fatalf("expected repo-local.FromRepo=true, got %+v", byName["repo-local"])
	}
	if byName["machine-local"].FromRepo {
		t.Fatalf("expected machine-local.FromRepo=false, got %+v", byName["machine-local"])
	}
	if hashStdioMCPSpec(root, mcpServerSpec{Name: "x", Command: []string{"echo"}, FromRepo: true}) ==
		hashStdioMCPSpec(root, mcpServerSpec{Name: "x", Command: []string{"echo"}, FromRepo: false}) {
		t.Fatal("hashStdioMCPSpec must distinguish repo and machine servers")
	}
}
