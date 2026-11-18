package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

// Exercise the actual model -> registry -> project config -> pooled stdio
// process -> tool-result path without network or real user credentials.
func TestNativeProjectMCPStdioIntegration(t *testing.T) {
	root := t.TempDir()
	config := map[string]any{"mcp": map[string]any{"project-test": map[string]any{
		"type": "local", "command": []string{os.Args[0], "-test.run=^TestMCPStdioHelperProcess$"},
		"environment": map[string]string{"HOXI_TEST_MCP_STDIO": "1"}, "enabled": true,
	}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hoxi.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	defer agent.CloseStdioMCPPool()
	args := `{"server":"project-test","tool":"ping_stdio","arguments":{}}`
	response := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"project-call","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(args) + `}}]}}]}` + "\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n" + `data: [DONE]` + "\n"
	client := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{response, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"}}
	var result string
	var forceAsk bool
	err = agent.NewDefaultEngine(client).RunTurn(context.Background(), agent.TurnRequest{
		SessionID: "ses_project_mcp", WorktreeDir: root, Mode: "build", ProviderID: "test", ModelID: "test", UserPrompt: "Call configured MCP",
		PermissionHook: func(_ context.Context, req agent.PermissionRequest) error { forceAsk = req.ForceAsk; return nil },
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				result = evt.Text
			}
			if evt.Kind == agent.EngineToolError {
				t.Errorf("tool error: %s", evt.ErrorText)
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !forceAsk || !strings.Contains(result, "stdio-mcp-ok") {
		t.Fatalf("forceAsk=%v result=%s", forceAsk, result)
	}
}
