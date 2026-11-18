package agent

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestStdioMCPStartupBudgetAndCooldown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sleep for hung MCP startup simulation")
	}
	ResetStdioMCPPoolForTest()
	defer ResetStdioMCPPoolForTest()

	dir := t.TempDir()
	tc := ToolContext{
		SessionID:   "ses_budget_test",
		WorktreeDir: dir,
		MCPConfig: func() map[string]any {
			return map[string]any{
				"mcp": map[string]any{
					"hung-tool": map[string]any{
						"type":             "local",
						"command":          []any{"/bin/sh", "-c", "sleep 10"},
						"startupTimeoutMs": 300,
						"enabled":          true,
					},
				},
			}
		},
	}

	callTool := &mcpCallTool{}
	rawArgs, _ := json.Marshal(map[string]any{
		"server":    "hung-tool",
		"tool":      "ping",
		"arguments": map[string]any{},
	})

	// First call should fail within the ~300ms startup budget and spawn exactly 1 process.
	_, err := callTool.Execute(context.Background(), tc, rawArgs)
	if err == nil || !strings.Contains(err.Error(), "exceeded startup budget") {
		t.Fatalf("expected startup budget exceeded error, got: %v", err)
	}
	if got := StdioMCPSpawnCountForTest(); got != 1 {
		t.Fatalf("expected 1 spawn on initial budget timeout, got %d", got)
	}

	// Second call during cooldown must fail fast without spawning another hung child process.
	_, err2 := callTool.Execute(context.Background(), tc, rawArgs)
	if err2 == nil || !strings.Contains(err2.Error(), "cooling down") {
		t.Fatalf("expected startup cooldown fast-fail on second call, got: %v", err2)
	}
	if got := StdioMCPSpawnCountForTest(); got != 1 {
		t.Fatalf("expected cooldown to suppress respawn (want 1 spawn total), got %d", got)
	}
}
