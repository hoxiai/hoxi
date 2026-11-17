package agent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

// TestMCPPersistentStdioHelperProcess runs a long-lived JSON-RPC 2.0 MCP server
// over stdio so tests can verify process reuse, concurrency, config rotation,
// and automatic crash recovery.
func TestMCPPersistentStdioHelperProcess(t *testing.T) {
	if os.Getenv("HOXI_TEST_MCP_PERSISTENT") != "1" {
		return
	}
	tag := os.Getenv("HOXI_TEST_MCP_TAG")
	pid := os.Getpid()
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			_, _ = os.Stdout.Write(append(resp, '\n'))
		case "tools/list":
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "ping", "description": "Return server pid and tag", "inputSchema": map[string]any{"type": "object"}},
					},
				},
			})
			_, _ = os.Stdout.Write(append(resp, '\n'))
		case "tools/call":
			toolName, _ := req.Params["name"].(string)
			if toolName == "crash_after_reply" {
				resp, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("bye:%d:%s", pid, tag)}},
					},
				})
				_, _ = os.Stdout.Write(append(resp, '\n'))
				os.Exit(0)
			}
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("pid=%d tag=%s", pid, tag)}},
				},
			})
			_, _ = os.Stdout.Write(append(resp, '\n'))
		}
	}
	os.Exit(0)
}

func TestStdioMCPSessionPoolingConcurrencyAndRecovery(t *testing.T) {
	agent.ResetStdioMCPPoolForTest()
	defer agent.ResetStdioMCPPoolForTest()

	dir := t.TempDir()
	var cfgMu sync.Mutex
	currentTag := "v1"

	mcpCfg := func() map[string]any {
		cfgMu.Lock()
		tag := currentTag
		cfgMu.Unlock()
		return map[string]any{
			"mcp": map[string]any{
				"keepalive": map[string]any{
					"type":    "local",
					"command": []any{os.Args[0], "-test.run=TestMCPPersistentStdioHelperProcess"},
					"environment": map[string]any{
						"HOXI_TEST_MCP_PERSISTENT": "1",
						"HOXI_TEST_MCP_TAG":        tag,
					},
					"enabled": true,
				},
			},
		}
	}

	// Step 1: 4 concurrent mcp_list_tools calls + 2 sequential mcp_call_tool calls
	// should all share a SINGLE warm child process.
	listArgs, _ := json.Marshal(map[string]any{"server": "keepalive"})
	pingArgs, _ := json.Marshal(map[string]any{"server": "keepalive", "tool": "ping", "arguments": map[string]any{}})
	crashArgs, _ := json.Marshal(map[string]any{"server": "keepalive", "tool": "crash_after_reply", "arguments": map[string]any{}})

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"l1","function":{"name":"mcp_list_tools","arguments":` + mustJSONString(string(listArgs)) + `}},` +
			`{"index":1,"id":"l2","function":{"name":"mcp_list_tools","arguments":` + mustJSONString(string(listArgs)) + `}},` +
			`{"index":2,"id":"l3","function":{"name":"mcp_list_tools","arguments":` + mustJSONString(string(listArgs)) + `}},` +
			`{"index":3,"id":"c1","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(pingArgs)) + `}},` +
			`{"index":4,"id":"c2","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(pingArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\ndata: [DONE]\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sseDone}}
	eng := agent.NewDefaultEngine(sc)

	outputs := make(map[string]string)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_mcp_pool",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Exercise pooled MCP",
		MCPConfig:   mcpCfg,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				outputs[evt.CallID] = strings.TrimSpace(evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if spawns := agent.StdioMCPSpawnCountForTest(); spawns != 1 {
		t.Fatalf("expected exactly 1 stdio MCP process spawn across 5 calls, got %d", spawns)
	}
	if outputs["c1"] == "" || outputs["c1"] != outputs["c2"] || !strings.Contains(outputs["c1"], "tag=v1") {
		t.Fatalf("expected identical pooled PID/tag outputs, got c1=%q c2=%q", outputs["c1"], outputs["c2"])
	}

	// Step 2: server exits after replying; subsequent call must auto-recover transparently.
	sseCrashThenPing := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"cr1","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(crashArgs)) + `}},` +
			`{"index":1,"id":"c3","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(pingArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sc2 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseCrashThenPing, sseDone}}
	eng2 := agent.NewDefaultEngine(sc2)
	if err := eng2.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_mcp_pool",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Crash and recover MCP",
		MCPConfig:   mcpCfg,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				outputs[evt.CallID] = strings.TrimSpace(evt.Text)
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn recovery failed: %v", err)
	}
	if !strings.HasPrefix(outputs["cr1"], "bye:") || !strings.Contains(outputs["c3"], "tag=v1") {
		t.Fatalf("expected crash reply and recovered ping, got cr1=%q c3=%q", outputs["cr1"], outputs["c3"])
	}
	if spawns := agent.StdioMCPSpawnCountForTest(); spawns != 2 {
		t.Fatalf("expected respawn count 2 after crash recovery, got %d", spawns)
	}

	// Step 3: changing MCP env/config must invalidate the old session and spawn tag=v2.
	cfgMu.Lock()
	currentTag = "v2"
	cfgMu.Unlock()

	sseConfigRotate := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c4","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(pingArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sc3 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseConfigRotate, sseDone}}
	eng3 := agent.NewDefaultEngine(sc3)
	if err := eng3.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_mcp_pool",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Verify config rotation",
		MCPConfig:   mcpCfg,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				outputs[evt.CallID] = strings.TrimSpace(evt.Text)
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn config rotation failed: %v", err)
	}
	if !strings.Contains(outputs["c4"], "tag=v2") {
		t.Fatalf("expected rotated MCP env tag=v2, got %q", outputs["c4"])
	}
	if spawns := agent.StdioMCPSpawnCountForTest(); spawns != 3 {
		t.Fatalf("expected spawn count 3 after config rotation, got %d", spawns)
	}
}

func TestAnthropicCacheBreakpointsStayWithinFourBlockLimit(t *testing.T) {
	client := &inspectStreamClient{
		proto:    llm.ProtocolAnthropic,
		response: "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n",
	}
	eng := agent.NewDefaultEngine(client)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_cache_bp",
		WorktreeDir: t.TempDir(),
		Mode:        "code",
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-5",
		System:      "Keep architecture clean.",
		History: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "Read main.go"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "Done."}}},
		},
		UserPrompt: "Now add unit tests",
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	var payload struct {
		System []map[string]any `json:"system"`
		Tools  []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(client.lastReq.Body, &payload); err != nil {
		t.Fatalf("unmarshal anthropic payload: %v", err)
	}
	if len(payload.System) == 0 || payload.System[0]["cache_control"] == nil {
		t.Fatalf("expected system[0] cache_control breakpoint, got: %+v", payload.System)
	}
	if len(payload.Tools) < 10 {
		t.Fatalf("expected full builtin toolset, got %d tools", len(payload.Tools))
	}
	for i := 0; i < len(payload.Tools)-1; i++ {
		if payload.Tools[i]["cache_control"] != nil {
			t.Fatalf("unexpected cache_control on non-final tool index %d", i)
		}
	}
	if payload.Tools[len(payload.Tools)-1]["cache_control"] == nil {
		t.Fatal("expected cache_control breakpoint on final tool entry")
	}
	if totalBP := strings.Count(string(client.lastReq.Body), `"cache_control"`); totalBP != 3 {
		t.Fatalf("expected exactly 3 cache_control blocks (<= 4 Anthropic hard limit), got %d", totalBP)
	}
}

func TestCompactBudgetQuantizedPrefixStabilityAndHardCap(t *testing.T) {
	const contextLimit = 4000
	bigToolOut := strings.Repeat("line of verbose tool output for cache stability test\n", 30) // ~1590 chars (~530 tokens)

	// Build 18 messages (9 assistant/user-tool-result pairs) so total > 75% of 4000 (~3000 tokens),
	// rawCutoff = 18 - 6 = 12 -> snaps cleanly to quantizedCutoff = 12.
	var base []agent.Message
	for i := 0; i < 9; i++ {
		base = append(base,
			agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{{Type: agent.ContentToolUse, ID: fmt.Sprintf("call_%d", i), Name: "read", Input: json.RawMessage(`{"filePath":"a.go"}`)}},
			},
			agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: fmt.Sprintf("call_%d", i), Text: bigToolOut}},
			},
		)
	}

	prunedAt18 := agent.PruneOldToolResults(base, "sys", contextLimit)
	prefix18JSON, _ := json.Marshal(prunedAt18[:16])

	// Append 2 more turns (messages 18..21 -> len=22, rawCutoff=16, quantizedCutoff STILL 12!).
	// Because runner rebuilds unpruned history each turn, messages 12..15 MUST remain
	// unpruned so the prefix [0:16] is 100% byte-identical across consecutive turns!
	extended := append([]agent.Message(nil), base...)
	for i := 9; i < 11; i++ {
		extended = append(extended,
			agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{{Type: agent.ContentToolUse, ID: fmt.Sprintf("call_%d", i), Name: "read", Input: json.RawMessage(`{"filePath":"b.go"}`)}},
			},
			agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: fmt.Sprintf("call_%d", i), Text: "short output"}},
			},
		)
	}
	prunedAt22 := agent.PruneOldToolResults(extended, "sys", contextLimit)
	prefix22JSON, _ := json.Marshal(prunedAt22[:16])

	if string(prefix18JSON) != string(prefix22JSON) {
		t.Fatalf("expected quantized compaction to keep prefix [0:16] byte-identical across turns")
	}
	if !strings.HasPrefix(prunedAt22[1].Content[0].Text, "[tool output pruned:") {
		t.Fatalf("expected message 1 (<12) to be pruned, got %q", prunedAt22[1].Content[0].Text)
	}
	if prunedAt22[13].Content[0].Text != bigToolOut {
		t.Fatal("expected message 13 (between quantizedCutoff=12 and rawCutoff=16) to stay unpruned for KV-cache stability")
	}

	// Safety fallback: if a huge tool output in [12:16] pushes post-quantized tokens > 90% of contextLimit,
	// PruneOldToolResults must advance to rawCutoff to prevent HTTP 400 context overflow.
	huge := append([]agent.Message(nil), extended...)
	huge[13] = agent.Message{
		Role:    agent.RoleUser,
		Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: "call_6", Text: strings.Repeat("X", 12000)}},
	}
	prunedHuge := agent.PruneOldToolResults(huge, "sys", contextLimit)
	if !strings.HasPrefix(prunedHuge[13].Content[0].Text, "[tool output pruned:") {
		t.Fatal("expected >90% safety ceiling to prune message 13 and prevent context overflow")
	}
}
