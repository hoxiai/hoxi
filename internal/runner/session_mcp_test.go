package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sampleWorkLocalMCPServers() map[string]any {
	return map[string]any{
		"work-local-cutark": map[string]any{
			"type":             "remote",
			"url":              "http://127.0.0.1:5400/api/hoxi/work/apps/cutark/mcp?sessionID=ses_agy_1&storeId=default",
			"headers":          map[string]string{"Authorization": "Bearer secret_gw_token_123"},
			"startupTimeoutMs": 120000,
			"enabled":          true,
		},
		"work-local-stdio": map[string]any{
			"type":             "local",
			"command":          []string{"node", "/opt/pkg/stdio.mjs"},
			"environment":      map[string]string{"HOXI_WORK_APP_ID": "demo"},
			"startupTimeoutMs": 30000,
			"enabled":          true,
		},
		"disabled-server": map[string]any{
			"type":    "remote",
			"url":     "http://127.0.0.1:5400/disabled",
			"enabled": false,
		},
		"../invalid name": map[string]any{
			"type":    "remote",
			"url":     "http://127.0.0.1:5400/bad",
			"enabled": true,
		},
	}
}

func TestPrepareSessionMCPForAntigravityAndGeminiAgy(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	svc := NewService(t.TempDir(), nil, nil, "")
	svc.SetSessionMCPResolver(func(sessionID, directory string) map[string]any {
		if sessionID != "ses_agy_1" {
			return nil
		}
		return sampleWorkLocalMCPServers()
	})

	for _, tc := range []struct {
		name          string
		runtime       string
		geminiUsesAgy bool
	}{
		{name: "antigravity", runtime: string(RuntimeAntigravity), geminiUsesAgy: false},
		{name: "gemini-via-agy", runtime: string(RuntimeGeminiCLI), geminiUsesAgy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := SessionRecord{ID: "ses_agy_1", Runtime: tc.runtime, WorktreePath: t.TempDir()}
			mcpArgs, mcpEnv, geminiMap, cleanup := svc.prepareSessionMCPForTurn(rec, "msg_1", tc.geminiUsesAgy)
			if len(geminiMap) != 0 {
				t.Fatalf("expected no gemini settings map for agy, got %v", geminiMap)
			}
			if len(mcpArgs) != 2 || mcpArgs[0] != "--add-dir" {
				t.Fatalf("expected [--add-dir <dir>], got %v", mcpArgs)
			}
			addDir := mcpArgs[1]
			cfgPath := filepath.Join(addDir, ".agents", "mcp_config.json")
			raw, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("read agy mcp_config.json: %v", err)
			}
			info, err := os.Stat(cfgPath)
			if err != nil {
				t.Fatalf("stat mcp_config.json: %v", err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatalf("mcp_config.json mode = %o, want 0600", info.Mode().Perm())
			}
			var doc struct {
				MCPServers map[string]map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("unmarshal mcp_config.json: %v", err)
			}
			if len(doc.MCPServers) != 2 {
				t.Fatalf("expected 2 valid enabled servers, got %+v", doc.MCPServers)
			}
			cutark := doc.MCPServers["work-local-cutark"]
			if cutark["serverUrl"] != "http://127.0.0.1:5400/api/hoxi/work/apps/cutark/mcp?sessionID=ses_agy_1&storeId=default" {
				t.Fatalf("unexpected serverUrl: %+v", cutark)
			}
			hdrs, _ := cutark["headers"].(map[string]any)
			if hdrs["Authorization"] != "Bearer secret_gw_token_123" {
				t.Fatalf("missing Authorization header: %+v", cutark)
			}
			stdio := doc.MCPServers["work-local-stdio"]
			if stdio["command"] != "node" {
				t.Fatalf("unexpected stdio entry: %+v", stdio)
			}

			// Loopback NO_PROXY must be injected.
			joinedEnv := strings.Join(mcpEnv, "\n")
			if !strings.Contains(joinedEnv, "NO_PROXY=") || !strings.Contains(joinedEnv, "127.0.0.1") {
				t.Fatalf("expected loopback NO_PROXY in env, got %v", mcpEnv)
			}

			// Merged args must place --add-dir before trailing -p= and never leak token into argv.
			baseArgs := BuildAgyArgs("build", "gemini-4-neon-high", "high", "")
			merged := mergeRuntimeMCPArgs(tc.runtime, tc.geminiUsesAgy, baseArgs, mcpArgs)
			if merged[len(merged)-1] != "-p=" {
				t.Fatalf("expected trailing -p=, got %v", merged)
			}
			if strings.Contains(strings.Join(merged, " "), "secret_gw_token_123") {
				t.Fatalf("gateway token must never appear in argv: %v", merged)
			}

			cleanup()
			if _, err := os.Stat(addDir); !os.IsNotExist(err) {
				t.Fatalf("expected cleanup to remove %s, err=%v", addDir, err)
			}
		})
	}
}

func TestPrepareSessionMCPForClaudeCode(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	svc := NewService(t.TempDir(), nil, nil, "")
	svc.SetSessionMCPResolver(func(sessionID, _ string) map[string]any {
		return sampleWorkLocalMCPServers()
	})
	rec := SessionRecord{ID: "ses_cld_1", Runtime: string(RuntimeClaudeCode), WorktreePath: t.TempDir()}
	mcpArgs, mcpEnv, _, cleanup := svc.prepareSessionMCPForTurn(rec, "msg_1", false)
	defer cleanup()

	if len(mcpArgs) != 2 || mcpArgs[0] != "--mcp-config" {
		t.Fatalf("expected [--mcp-config <path>], got %v", mcpArgs)
	}
	if strings.Contains(strings.Join(mcpArgs, " "), "secret_gw_token_123") {
		t.Fatalf("gateway token must never appear in claude argv: %v", mcpArgs)
	}
	raw, err := os.ReadFile(mcpArgs[1])
	if err != nil {
		t.Fatalf("read claude mcp-config.json: %v", err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal claude mcp-config.json: %v", err)
	}
	cutark := doc.MCPServers["work-local-cutark"]
	if cutark["type"] != "http" || !strings.Contains(cutark["url"].(string), "/api/hoxi/work/apps/cutark/mcp") {
		t.Fatalf("unexpected claude remote entry: %+v", cutark)
	}
	stdio := doc.MCPServers["work-local-stdio"]
	if stdio["type"] != "stdio" || stdio["command"] != "node" {
		t.Fatalf("unexpected claude stdio entry: %+v", stdio)
	}
	if !strings.Contains(strings.Join(mcpEnv, "\n"), "127.0.0.1") {
		t.Fatalf("expected loopback NO_PROXY in env, got %v", mcpEnv)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(mcpArgs[1])); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove config dir, err=%v", err)
	}
}

func TestPrepareSessionMCPForCodexNeverLeaksBearerIntoArgv(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, "")
	svc.SetSessionMCPResolver(func(sessionID, _ string) map[string]any {
		return map[string]any{
			"work-local-cutark": map[string]any{
				"type": "remote",
				"url":  "http://127.0.0.1:5400/api/hoxi/work/apps/cutark/mcp?sessionID=ses_cdx_1&storeId=default",
				"headers": map[string]string{
					"Authorization": "Bearer secret_codex_tok",
					"X-Custom":      "custom-secret",
				},
				"startupTimeoutMs": 90000,
				"enabled":          true,
			},
		}
	})
	rec := SessionRecord{ID: "ses_cdx_1", Runtime: string(RuntimeCodex), WorktreePath: t.TempDir()}
	mcpArgs, mcpEnv, _, cleanup := svc.prepareSessionMCPForTurn(rec, "msg_1", false)
	defer cleanup()

	joinedArgs := strings.Join(mcpArgs, " ")
	if strings.Contains(joinedArgs, "secret_codex_tok") || strings.Contains(joinedArgs, "custom-secret") {
		t.Fatalf("codex MCP secrets must never appear in argv: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, `mcp_servers."work-local-cutark".bearer_token_env_var="HOXI_SESSION_MCP_BEARER_0"`) {
		t.Fatalf("expected bearer_token_env_var override, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, `mcp_servers."work-local-cutark".env_http_headers."X-Custom"="HOXI_SESSION_MCP_HDR_0_0"`) {
		t.Fatalf("expected env_http_headers override, got: %s", joinedArgs)
	}
	joinedEnv := strings.Join(mcpEnv, "\n")
	if !strings.Contains(joinedEnv, "HOXI_SESSION_MCP_BEARER_0=secret_codex_tok") ||
		!strings.Contains(joinedEnv, "HOXI_SESSION_MCP_HDR_0_0=custom-secret") {
		t.Fatalf("expected secrets in env, got: %s", joinedEnv)
	}

	baseArgs := BuildCodexArgsWithFast("build", "gpt-5.4", "high", "", false)
	merged := mergeRuntimeMCPArgs(string(RuntimeCodex), false, baseArgs, mcpArgs)
	if merged[len(merged)-1] != "-" {
		t.Fatalf("expected trailing '-' stdin arg for codex, got %v", merged)
	}
}

func TestPrepareSessionMCPForGeminiCLIReadOnlyAndBuild(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	svc := NewService(t.TempDir(), nil, nil, "")
	svc.SetSessionMCPResolver(func(sessionID, _ string) map[string]any {
		return sampleWorkLocalMCPServers()
	})
	rec := SessionRecord{ID: "ses_gem_1", Runtime: string(RuntimeGeminiCLI), WorktreePath: t.TempDir()}
	_, _, geminiMap, _ := svc.prepareSessionMCPForTurn(rec, "msg_1", false)
	if len(geminiMap) != 2 {
		t.Fatalf("expected 2 gemini MCP entries, got %+v", geminiMap)
	}
	env, cleanup := svc.issueGeminiHookWithMCP("plan", rec.ID, "msg_1", geminiMap)
	defer cleanup()

	var homeDir string
	for _, kv := range env {
		if strings.HasPrefix(kv, geminiCLIHomeEnv+"=") {
			homeDir = strings.TrimPrefix(kv, geminiCLIHomeEnv+"=")
		}
	}
	if homeDir == "" {
		t.Fatalf("expected GEMINI_CLI_HOME in read-only mode with session MCP, got %v", env)
	}
	raw, err := os.ReadFile(filepath.Join(homeDir, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read gemini settings.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	if _, hasHooks := doc["hooks"]; hasHooks {
		t.Fatalf("read-only mode should not install hooks: %+v", doc)
	}
	mcps, _ := doc["mcpServers"].(map[string]any)
	cutark, _ := mcps["work-local-cutark"].(map[string]any)
	if cutark["httpUrl"] != "http://127.0.0.1:5400/api/hoxi/work/apps/cutark/mcp?sessionID=ses_agy_1&storeId=default" {
		t.Fatalf("unexpected gemini cutark entry: %+v", cutark)
	}
	cleanup()
	if _, err := os.Stat(homeDir); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove %s, err=%v", homeDir, err)
	}
}
