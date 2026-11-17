package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestDefaultMaxStepsRaisedAbove48AndConfigOverride(t *testing.T) {
	dir := t.TempDir()
	const stepsToRun = 55 // Exceeds legacy 48-step hard ceiling.

	var stepIdx atomic.Int32
	sc := &inspectableStreamClient{
		responder: func(_ int, _ recordedLLMCall) string {
			n := int(stepIdx.Add(1))
			if n <= stepsToRun {
				todoArgs, _ := json.Marshal(map[string]any{
					"todos": []map[string]any{
						{"id": fmt.Sprintf("t_%d", n), "content": "step progress", "status": "in_progress"},
					},
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"td_` + fmt.Sprintf("%d", n) + `","function":{"name":"todowrite","arguments":` + mustJSONString(string(todoArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"Completed 55-step build workflow.\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	eng := agent.NewDefaultEngine(sc)
	var finalOut strings.Builder
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_55_steps",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Run multi-step build task",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineTextDelta {
				finalOut.WriteString(evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected 55-step build workflow to succeed under raised default step budget, got: %v", err)
	}
	if got := finalOut.String(); got != "Completed 55-step build workflow." {
		t.Fatalf("unexpected final text: %q", got)
	}
}

func TestGracefulFinalizeOnStepLimitAndExhaustedFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Case 1: MaxSteps=2 reached while model still emits tool calls -> step 2 tool executes,
	// then engine performs one tool-free synthesis request and finishes cleanly without error.
	scGraceful := &inspectableStreamClient{
		responder: func(idx int, call recordedLLMCall) string {
			switch idx {
			case 0:
				return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"g1\",\"function\":{\"name\":\"glob\",\"arguments\":\"{\\\"pattern\\\":\\\"*.go\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
			case 1:
				return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"r1\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"filePath\\\":\\\"app.go\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
			default:
				if len(call.ToolNames) != 0 {
					t.Errorf("expected graceful finalize pass to omit tools, got: %v", call.ToolNames)
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"Handoff summary: inspected app.go; ready to apply edits next turn.\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
		},
	}

	eng := agent.NewDefaultEngine(scGraceful)
	var gotSummary strings.Builder
	var completedTools []string
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_graceful",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		MaxSteps:    2,
		UserPrompt:  "Inspect workspace",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				completedTools = append(completedTools, evt.ToolName)
			}
			if evt.Kind == agent.EngineTextDelta {
				gotSummary.WriteString(evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected graceful finalizer to complete turn cleanly, got: %v", err)
	}
	if len(completedTools) != 2 || completedTools[1] != "read" {
		t.Fatalf("expected both step-1 and step-2 tools to execute before finalizer, got: %v", completedTools)
	}
	if !strings.Contains(gotSummary.String(), "Handoff summary:") {
		t.Fatalf("expected streamed handoff summary, got: %q", gotSummary.String())
	}

	// Case 2: If upstream fails/produces no final summary on graceful pass, fallback error is preserved.
	sseTool := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"g1\",\"function\":{\"name\":\"glob\",\"arguments\":\"{\\\"pattern\\\":\\\"*.go\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
	scExhausted := &stubStreamClient{
		proto:     llm.ProtocolOpenAI,
		responses: []string{sseTool, sseTool}, // 3rd call (graceful pass) receives empty stream -> fails
	}
	errFallback := agent.NewDefaultEngine(scExhausted).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_fallback",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		MaxSteps:    2,
		UserPrompt:  "Loop forever",
	})
	if errFallback == nil || !strings.Contains(errFallback.Error(), "reached maximum step limit (2)") {
		t.Fatalf("expected fallback step limit error when finalizer stream is empty, got: %v", errFallback)
	}
}

func TestLoopGuardWarnsBlocksAndResetsAfterWorkspaceEdit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cfg.go")
	if err := os.WriteFile(target, []byte("package cfg\n\nconst Port = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "cfg.go",
		"oldString": "const Port = 8080",
		"newString": "const Port = 9090",
	})

	readStep := func(id string) string {
		return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"" + id + "\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"filePath\\\":\\\"cfg.go\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
	}
	editStep := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"ed1\",\"function\":{\"name\":\"edit\",\"arguments\":" + mustJSONString(string(editArgs)) + "}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
	doneStep := "data: {\"choices\":[{\"delta\":{\"content\":\"Port updated and verified.\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	sc := &stubStreamClient{
		proto: llm.ProtocolOpenAI,
		responses: []string{
			readStep("r1"), // #1 ok
			readStep("r2"), // #2 ok
			readStep("r3"), // #3 ok + loop guard warning appended to tool result
			readStep("r4"), // #4 blocked by loop guard (EngineToolError)
			editStep,       // workspace mutation resets loop guard!
			readStep("r5"), // post-mutation read #1 succeeds cleanly
			doneStep,
		},
	}

	var toolErrors []string
	var completedIDs []string
	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_loop_guard",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Update cfg.go",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				toolErrors = append(toolErrors, evt.CallID+":"+evt.ErrorText)
			}
			if evt.Kind == agent.EngineToolCompleted {
				completedIDs = append(completedIDs, evt.CallID)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(toolErrors) != 1 || !strings.HasPrefix(toolErrors[0], "r4:[loop guard] Blocked repeated read call (#4") {
		t.Fatalf("expected r4 to be blocked by loop guard, got errors: %v", toolErrors)
	}
	joinedCompleted := strings.Join(completedIDs, ",")
	if !strings.Contains(joinedCompleted, "r5") {
		t.Fatalf("expected post-edit read r5 to succeed after mutation reset, got completed: %s", joinedCompleted)
	}
}

func TestLargeWindowStrideCompactionProtectsRecentReads(t *testing.T) {
	const contextLimit = 64000                                                              // 75% trigger = 48,000 tokens
	hugeLog := strings.Repeat("verbose compiler output line from early build step\n", 3200) // ~163k chars (~54k tokens)
	recentSource := strings.Repeat("func activeTarget() { /* 40 lines of code */ }\n", 35)  // ~1645 chars (< 2400 threshold & inside recent 20-msg window)

	var msgs []agent.Message
	// Early steps 0..5 (messages 0..11): Step 0 has a massive bash log pushing total > 75% of 64k.
	for i := 0; i < 6; i++ {
		out := "ok"
		if i == 0 {
			out = hugeLog
		}
		msgs = append(msgs,
			agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{{Type: agent.ContentToolUse, ID: fmt.Sprintf("old_%d", i), Name: "bash", Input: json.RawMessage(`{"command":"make"}`)}},
			},
			agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: fmt.Sprintf("old_%d", i), Text: out}},
			},
		)
	}
	// Recent 12 steps (messages 12..35): active file reads that previously got mutilated at cutoff = len-6!
	for i := 6; i < 18; i++ {
		msgs = append(msgs,
			agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{{Type: agent.ContentToolUse, ID: fmt.Sprintf("rec_%d", i), Name: "read", Input: json.RawMessage(`{"filePath":"svc.go"}`)}},
			},
			agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: fmt.Sprintf("rec_%d", i), Text: recentSource}},
			},
		)
	}

	pruned := agent.PruneOldToolResults(msgs, "system prompt", contextLimit)
	if !strings.HasPrefix(pruned[1].Content[0].Text, "[tool output pruned:") {
		t.Fatalf("expected early massive log at msg[1] to be pruned")
	}
	// Every recent source read from msg[13]..msg[35] must remain 100% intact!
	for idx := 13; idx < len(pruned); idx += 2 {
		if pruned[idx].Content[0].Text != recentSource {
			t.Fatalf("expected source read at msg[%d] to stay intact without re-read thrashing, got pruned: %.80s", idx, pruned[idx].Content[0].Text)
		}
	}
}
