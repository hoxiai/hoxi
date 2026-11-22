package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestSubdirRulesDeduplicatedAcrossTurnsFromHistory(t *testing.T) {
	monoRoot := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(monoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subPkg := filepath.Join(monoRoot, "packages", "billing")
	if err := os.MkdirAll(subPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	subRule := "SUBDIR_DEDUP_SENTINEL: always use integer cents."
	if err := os.WriteFile(filepath.Join(subPkg, "AGENTS.md"), []byte(subRule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subPkg, "a.go"), []byte("package billing\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subPkg, "b.go"), []byte("package billing\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runReadTurn := func(file string, history []Message) (toolOut string, finalMsgs []Message) {
		stream := &stubRulesStreamClient{
			handler: func(step int, _ llm.StreamHTTPRequest) string {
				if step == 1 {
					return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"r_%s\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"filePath\\\":\\\"%s\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", filepath.Base(file), file)
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			},
		}
		eng := NewDefaultEngine(stream)
		err := eng.RunTurn(context.Background(), TurnRequest{
			SessionID:   "ses_subdir_dedup",
			WorktreeDir: monoRoot,
			Mode:        "build",
			ProviderID:  "openai",
			ModelID:     "gpt-5",
			History:     history,
			UserPrompt:  "Read " + file,
			onFinalMessages: func(msgs []Message) {
				finalMsgs = append([]Message(nil), msgs...)
			},
			Sink: EventSinkFunc(func(evt EngineEvent) {
				if evt.Kind == EngineToolCompleted && evt.ToolName == "read" {
					toolOut = evt.Text
				}
			}),
		})
		if err != nil {
			t.Fatalf("RunTurn(%s) failed: %v", file, err)
		}
		return toolOut, finalMsgs
	}

	// Turn 1: first read in packages/billing injects subdirectory rules.
	out1, hist1 := runReadTurn("packages/billing/a.go", nil)
	if !strings.Contains(out1, subRule) {
		t.Fatalf("Turn 1 expected subdirectory rule to be injected, got:\n%s", out1)
	}

	// Turn 2: history already contains <subdirectory_instructions file="packages/billing/AGENTS.md">;
	// reading another file in the same directory must NOT re-inject the rule.
	out2, _ := runReadTurn("packages/billing/b.go", hist1)
	if strings.Contains(out2, subRule) || strings.Contains(out2, "<subdirectory_instructions") {
		t.Fatalf("Turn 2 must not re-inject subdirectory rule already present in history, got:\n%s", out2)
	}

	// Turn 3: after compaction clears old tool results from history, rule is eligible again.
	compactedHistory := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "<context_checkpoint>summary</context_checkpoint>"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Understood."}}},
	}
	out3, _ := runReadTurn("packages/billing/b.go", compactedHistory)
	if !strings.Contains(out3, subRule) {
		t.Fatalf("Turn 3 after compaction expected subdirectory rule to be re-injected, got:\n%s", out3)
	}
}

func TestReadWithLargeSubdirRulesNotTruncatedByGenericOutputCap(t *testing.T) {
	monoRoot := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(monoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subPkg := filepath.Join(monoRoot, "packages", "big")
	if err := os.MkdirAll(subPkg, 0o755); err != nil {
		t.Fatal(err)
	}

	// ~20 KB source file + ~25 KB subdirectory AGENTS.md => ~45 KB combined (> 32 KiB maxToolOutputBytes).
	srcBody := "package big\n" + strings.Repeat("// source padding line to reach 20KB\n", 540) + "func TailSentinel() int { return 42 }\n"
	ruleBody := "# Big Subdir Rules\n" + strings.Repeat("- invariant line for big package\n", 700) + "RULE_TAIL_SENTINEL: keep intact.\n"
	if err := os.WriteFile(filepath.Join(subPkg, "source.go"), []byte(srcBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subPkg, "AGENTS.md"), []byte(ruleBody), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := newToolRegistry()
	revs := NewFileRevisionTracker()
	tc := ToolContext{
		WorktreeDir:   monoRoot,
		Mode:          "build",
		FileRevisions: revs,
		RulesTracker:  NewSubdirRuleTracker(nil, DefaultSubdirRuleBudgetBytes),
	}
	out, err := reg.Execute(context.Background(), tc, ToolCall{
		ID:    "read_big",
		Name:  "read",
		Input: json.RawMessage(`{"filePath":"packages/big/source.go"}`),
	}, nil)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if strings.Contains(out.Output, "bytes truncated]") {
		t.Fatalf("expected 20KB read body not to be sliced by generic 32KiB cap when 25KB subdir rules are attached")
	}
	if !strings.Contains(out.Output, "func TailSentinel() int { return 42 }") {
		t.Fatalf("expected source file tail sentinel to remain intact")
	}
	if !strings.Contains(out.Output, "RULE_TAIL_SENTINEL: keep intact.") || !strings.HasSuffix(strings.TrimSpace(out.Output), "</subdirectory_instructions>") {
		t.Fatalf("expected trailing <subdirectory_instructions> block to remain intact with closing tag")
	}
}

func TestGracefulFinalizeStepPreservesSubtaskResumableID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	taskArgs, _ := json.Marshal(map[string]any{
		"subagent_type": "tiny",
		"description":   "quick scan",
		"prompt":        "Scan workspace",
	})
	childStep := 0
	parentStep := 0
	stream := &stubRulesStreamClient{
		handler: func(_ int, req llm.StreamHTTPRequest) string {
			body := string(req.Body)
			if strings.Contains(body, "Subagent role: tiny (quick scan).") {
				childStep++
				if childStep == 1 {
					return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"g1\",\"type\":\"function\",\"function\":{\"name\":\"glob\",\"arguments\":\"{\\\"pattern\\\":\\\"*.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"Graceful handoff from tiny subagent.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			}
			parentStep++
			if parentStep == 1 {
				return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t1\",\"type\":\"function\",\"function\":{\"name\":\"task\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(taskArgs))
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"Parent received subagent handoff.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		},
	}

	var taskOutput string
	eng := NewDefaultEngine(stream)
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_subtask_graceful_resume",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Delegate to tiny subagent",
		MCPConfig: func() map[string]any {
			return map[string]any{
				"agent": map[string]any{
					"tiny": map[string]any{
						"mode":  "subagent",
						"steps": 1,
					},
				},
			}
		},
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == EngineToolCompleted && evt.ToolName == "task" {
				taskOutput = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(taskOutput, "Graceful handoff from tiny subagent.") {
		t.Fatalf("expected graceful handoff text in task output, got: %q", taskOutput)
	}
	if !strings.Contains(taskOutput, "[subagent_id: sub_") {
		t.Fatalf("expected graceful-finalized subagent to persist resumable [subagent_id: ...], got: %q", taskOutput)
	}
}

func TestStepLoopGuardFailedReadsDoNotConsumeReadQuota(t *testing.T) {
	g := newStepLoopGuard()
	call := ToolCall{ID: "r1", Name: "read", Input: json.RawMessage(`{"filePath":"missing.go"}`)}

	// 1st call fails (transient error): should NOT consume readsSinceMutation quota.
	if blocked, _ := g.CheckBeforeExecute(call); blocked {
		t.Fatal("first call must not be blocked")
	}
	g.ObserveStepCalls([]ToolCall{call}, []ToolOutput{{IsError: true, Output: "no such file"}}, func(string) bool { return true })

	// Next 3 calls succeed (file created externally): all 3 must be allowed.
	for i := 1; i <= 3; i++ {
		if blocked, msg := g.CheckBeforeExecute(call); blocked {
			t.Fatalf("successful read #%d after single failure was prematurely blocked: %s", i, msg)
		}
		g.ObserveStepCalls([]ToolCall{call}, []ToolOutput{{IsError: false, Output: "1: ok"}}, func(string) bool { return true })
	}

	// 4th identical successful read must now be blocked.
	if blocked, msg := g.CheckBeforeExecute(call); !blocked || !strings.Contains(msg, "Blocked repeated read call (#4") {
		t.Fatalf("expected 4th identical read to be blocked, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestCompactBudgetSmallCutoffQuantizesToStride4(t *testing.T) {
	const contextLimit = 64000                                                                 // keepRecent = 20, trigger = 48k, hardCap = 57.6k
	mediumOut := strings.Repeat("tool output payload line for small stride quantization\n", 240) // ~13.2 KB (~4.4k tokens); 12 pairs ~= 53k tokens (between 48k trigger and 57.6k hardCap)

	buildMsgs := func(totalPairs int) []Message {
		var msgs []Message
		for i := 0; i < totalPairs; i++ {
			msgs = append(msgs,
				Message{
					Role:    RoleAssistant,
					Content: []ContentBlock{{Type: ContentToolUse, ID: fmt.Sprintf("c%d", i), Name: "read", Input: json.RawMessage(`{"filePath":"x.go"}`)}},
				},
				Message{
					Role:    RoleUser,
					Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: fmt.Sprintf("c%d", i), Text: mediumOut}},
				},
			)
		}
		return msgs
	}

	// 13 pairs = 26 msgs -> rawCutoff = 6 -> smallCutoff = 4.
	p26 := PruneOldToolResults(buildMsgs(13), "sys", contextLimit)
	// 14 pairs = 28 msgs -> rawCutoff = 8 -> Wait: test 25 vs 27 (rawCutoff 5 vs 7 -> both snap to 4!).
	m25 := append([]Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "start"}}}}, buildMsgs(12)...)
	m27 := append(append([]Message(nil), m25...),
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "c12", Name: "read", Input: json.RawMessage(`{"filePath":"y.go"}`)}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "c12", Text: "short"}}},
	)
	_ = p26
	p25 := PruneOldToolResults(m25, "sys", contextLimit)
	p27 := PruneOldToolResults(m27, "sys", contextLimit)

	b25, _ := json.Marshal(p25[:8])
	b27, _ := json.Marshal(p27[:8])
	if string(b25) != string(b27) {
		t.Fatalf("expected rawCutoff=5 and rawCutoff=7 to both snap to smallStride=4 so prefix [0:8] stays byte-identical")
	}
	if !strings.HasPrefix(p25[2].Content[0].Text, "[tool output pruned:") {
		t.Fatalf("expected msg[2] (<4) to be pruned")
	}
	if p25[4].Content[0].Text != mediumOut {
		t.Fatalf("expected msg[4] (between smallCutoff=4 and rawCutoff=5) to stay unpruned for KV-cache stability")
	}
}
