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

func TestLoopGuardResetsAfterMutatingGeneralSubagentTask(t *testing.T) {
	t.Run("success", func(t *testing.T) { verifyChildChangeResetsGuard(t, false) })
	t.Run("error after edit", func(t *testing.T) { verifyChildChangeResetsGuard(t, true) })
}

func verifyChildChangeResetsGuard(t *testing.T, failAfterEdit bool) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cfg.go"), []byte("package cfg\n\nconst Port = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readStep := func(id string) string {
		return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"" + id + "\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"filePath\\\":\\\"cfg.go\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
	}
	exploreArgs, _ := json.Marshal(map[string]any{
		"subagent_type": "explore",
		"description":   "check cfg",
		"prompt":        "Summarize cfg.go without edits",
	})
	generalArgs, _ := json.Marshal(map[string]any{
		"subagent_type": "general",
		"description":   "update port",
		"prompt":        "Change Port to 9090 in cfg.go",
	})
	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "cfg.go",
		"oldString": "const Port = 8080",
		"newString": "const Port = 9090",
	})

	parentStep := 0
	childEdited := false
	sc := &inspectableStreamClient{
		responder: func(_ int, call recordedLLMCall) string {
			if strings.Contains(call.System, "Subagent role: explore (check cfg).") {
				return "data: {\"choices\":[{\"delta\":{\"content\":\"read-only explore ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			if strings.Contains(call.System, "Subagent role: general (update port).") {
				if !childEdited {
					childEdited = true
					return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"sub_ed\",\"function\":{\"name\":\"edit\",\"arguments\":" + mustJSONString(string(editArgs)) + "}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
				}
				if failAfterEdit {
					return "data: {\"error\":{\"message\":\"child failed after editing\"}}\n\n"
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"updated Port to 9090\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			parentStep++
			switch parentStep {
			case 1:
				return readStep("r1")
			case 2:
				return readStep("r2")
			case 3:
				return readStep("r3")
			case 4:
				// Read-only explore subagent must NOT reset the loop guard.
				return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t_exp\",\"function\":{\"name\":\"task\",\"arguments\":" + mustJSONString(string(exploreArgs)) + "}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
			case 5:
				// 4th identical read after read-only explore must still be blocked.
				return readStep("r4_blocked")
			case 6:
				// Mutating general subagent MUST reset the parent's loop guard.
				return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t_gen\",\"function\":{\"name\":\"task\",\"arguments\":" + mustJSONString(string(generalArgs)) + "}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
			case 7:
				// Re-reading cfg.go after general subagent modified it must succeed cleanly.
				return readStep("r5_after_subtask")
			default:
				return "data: {\"choices\":[{\"delta\":{\"content\":\"verified subagent edit\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
		},
	}

	var toolErrors []string
	var verifiedOutput string
	engine := agent.NewDefaultEngine(sc)
	engine.SetStepRetryDelays(nil)
	err := engine.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_loop_subtask",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Inspect and update cfg.go via subagent",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.ParentCallID != "" {
				return
			}
			if evt.Kind == agent.EngineToolError {
				toolErrors = append(toolErrors, evt.CallID+":"+evt.ErrorText)
			}
			if evt.Kind == agent.EngineToolCompleted && evt.CallID == "r5_after_subtask" {
				verifiedOutput = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	wantErrors := 1
	if failAfterEdit {
		wantErrors = 2
	}
	if len(toolErrors) != wantErrors || !strings.HasPrefix(toolErrors[0], "r4_blocked:[loop guard] Blocked repeated read call (#4") {
		t.Fatalf("expected r4_blocked to stay blocked after read-only explore subtask, got errors: %v", toolErrors)
	}
	if failAfterEdit && !strings.HasPrefix(toolErrors[1], "t_gen:") {
		t.Fatalf("expected error from the child after editing, got %v", toolErrors)
	}
	if !strings.Contains(verifiedOutput, "const Port = 9090") {
		t.Fatalf("parent must see the child's updated file: %q", verifiedOutput)
	}
}

func TestStepBudgetNoticeWarnsOnceBeforeHardCutoffWithoutPollutingUI(t *testing.T) {
	dir := t.TempDir()
	var capturedBodies [][]byte
	var uiToolOutputs []string

	sc := &capturingStreamClient{
		stubStreamClient: stubStreamClient{
			proto: llm.ProtocolOpenAI,
			responses: func() []string {
				// MaxSteps=8 -> lead=max(2, 8/5)=2 -> fires once on step 6 (2 remaining).
				var resps []string
				for step := 1; step <= 7; step++ {
					todoArgs, _ := json.Marshal(map[string]any{
						"todos": []map[string]any{
							{"id": fmt.Sprintf("s%d", step), "content": "work", "status": "in_progress"},
						},
					})
					resps = append(resps, strings.Join([]string{
						`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"td_` + fmt.Sprintf("%d", step) + `","function":{"name":"todowrite","arguments":` + mustJSONString(string(todoArgs)) + `}}]}}]}`,
						`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
						`data: [DONE]`,
					}, "\n")+"\n")
				}
				resps = append(resps, "data: {\"choices\":[{\"delta\":{\"content\":\"Wrapped up cleanly before cutoff.\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
				return resps
			}(),
		},
	}

	err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_budget_notice",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		MaxSteps:    8,
		UserPrompt:  "Work with bounded budget",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				uiToolOutputs = append(uiToolOutputs, evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	capturedBodies = sc.bodies
	if len(capturedBodies) != 8 {
		t.Fatalf("expected 8 model calls, got %d", len(capturedBodies))
	}

	// Steps 1..6 model requests (indices 0..5) must NOT see the notice yet.
	for i := 0; i < 6; i++ {
		if strings.Contains(string(capturedBodies[i]), "[step budget notice:") {
			t.Fatalf("step %d request unexpectedly saw early budget notice", i+1)
		}
	}
	// Step 7 model request (index 6) carries step 6's tool result -> must see the notice exactly once.
	step7Body := string(capturedBodies[6])
	wantNotice := "[step budget notice: 6/8 steps used (2 remaining)."
	if !strings.Contains(step7Body, wantNotice) {
		t.Fatalf("expected step 7 request to include %q, body=%s", wantNotice, step7Body)
	}
	// Step 8 model request (index 7) carries step 7's tool result -> must NOT append a second notice.
	if cnt := strings.Count(string(capturedBodies[7]), "[step budget notice:"); cnt != 1 {
		t.Fatalf("expected step budget notice to be appended only once across turn, got %d occurrences", cnt)
	}
	// User-facing EngineToolCompleted events must stay clean of internal notices.
	for i, out := range uiToolOutputs {
		if strings.Contains(out, "[step budget notice:") {
			t.Fatalf("UI tool output #%d leaked internal step budget notice: %q", i, out)
		}
	}
}
