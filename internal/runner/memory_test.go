package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeContextOmitsInlineBase64DataURLs(t *testing.T) {
	t.Parallel()
	rec := SessionRecord{
		ID:             "ses_ctx",
		Runtime:        "claude-code",
		ContextPending: true,
		Turns: []MessageTurn{
			{
				Info: MessageInfo{ID: "msg_1", Role: "user"},
				Parts: []MessagePart{
					{Type: "text", Text: "look at this screenshot"},
					{
						Type:     "file",
						Filename: "screen.png",
						Mime:     "image/png",
						URL:      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJ",
					},
				},
			},
		},
	}
	ctx := runtimeContext(rec)
	if strings.Contains(ctx, "iVBORw0KGgo") || strings.Contains(ctx, "data:image/png;base64") {
		t.Fatalf("expected runtimeContext to strip inline base64 data URLs, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Attachment screen.png (image/png)") {
		t.Fatalf("expected attachment summary in runtimeContext, got:\n%s", ctx)
	}
}

func TestRuntimeContextCompactsToolStateAndPreservesMidSessionConstraints(t *testing.T) {
	t.Parallel()
	hugeFileBody := strings.Repeat("func veryLargeReadOutputLine() {}\n", 400)
	turns := []MessageTurn{
		{
			Info:  MessageInfo{ID: "msg_u1", Role: "user"},
			Parts: []MessagePart{{Type: "text", Text: "Refactor auth handler to support token rotation"}},
		},
		{
			Info: MessageInfo{ID: "msg_a1", Role: "assistant"},
			Parts: []MessagePart{
				{
					Type: "tool",
					Tool: "read",
					State: &ToolState{
						Status: "completed",
						Input:  map[string]interface{}{"filePath": "internal/auth/handler.go"},
						Output: hugeFileBody,
					},
				},
				{
					Type: "tool",
					Tool: "edit",
					State: &ToolState{
						Status: "completed",
						Input: map[string]interface{}{
							"filePath":  "internal/auth/handler.go",
							"oldString": "old",
							"newString": strings.Repeat("newCodeLine()\n", 200),
						},
						Output: "patched successfully",
					},
				},
				{
					Type: "tool",
					Tool: "bash",
					State: &ToolState{
						Status: "completed",
						Input:  map[string]interface{}{"command": "go test ./internal/auth/..."},
						Output: "ok  \tgithub.com/hoxiai/hoxi/internal/auth\t0.12s\n" + strings.Repeat("verbose log\n", 100),
					},
				},
				{Type: "text", Text: "Updated handler.go and verified tests."},
			},
		},
		{
			Info:  MessageInfo{ID: "msg_u2", Role: "user"},
			Parts: []MessagePart{{Type: "text", Text: "注意：严禁修改公开 API 签名，must not break existing callers"}},
		},
	}
	rec := SessionRecord{
		ID:             "ses_compact_handoff",
		Runtime:        "claude-code",
		ContextPending: true,
		Turns:          turns,
	}
	ctx := runtimeContext(rec)
	if strings.Contains(ctx, "veryLargeReadOutputLine") || strings.Contains(ctx, "newCodeLine()") {
		t.Fatalf("expected runtimeContext to omit raw read/edit bodies, got len=%d", len(ctx))
	}
	if !strings.Contains(ctx, "Tool read [completed]: internal/auth/handler.go") || !strings.Contains(ctx, "Tool edit [completed]: internal/auth/handler.go") {
		t.Fatalf("expected compact tool summary in runtimeContext, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "严禁修改公开 API 签名") {
		t.Fatalf("expected mid-session constraint in runtimeContext, got:\n%s", ctx)
	}
	md := buildEightDimSessionMarkdown(rec)
	if !strings.Contains(md, "严禁修改公开 API 签名") {
		t.Fatalf("expected mid-session constraint in 8-dim session memory, got:\n%s", md)
	}
	if !strings.Contains(md, "`internal/auth/handler.go` (modified)") {
		t.Fatalf("expected modified marker on edited file in 8-dim session memory, got:\n%s", md)
	}
	if !strings.Contains(md, "Verification: `go test ./internal/auth/...` (completed)") {
		t.Fatalf("expected verification command recorded in 8-dim session memory, got:\n%s", md)
	}
}

func TestRuntimeContextPreservesLateLongMessageConstraintAfterManyTurns(t *testing.T) {
	t.Parallel()
	var turns []MessageTurn
	// 10 early turns with their own constraints so an 8-item cap would overflow.
	for i := 1; i <= 10; i++ {
		turns = append(turns,
			MessageTurn{
				Info:  MessageInfo{ID: fmt.Sprintf("u_early_%d", i), Role: "user"},
				Parts: []MessagePart{{Type: "text", Text: fmt.Sprintf("Step %d: must keep module_%d backwards compatible.", i, i)}},
			},
			MessageTurn{
				Info:  MessageInfo{ID: fmt.Sprintf("a_early_%d", i), Role: "assistant"},
				Parts: []MessagePart{{Type: "text", Text: fmt.Sprintf("Completed step %d.", i)}},
			},
		)
	}
	// Turn 11: a long user message (> 600 chars) where a critical constraint is at the very end.
	longPrefix := strings.Repeat("这里是关于线上訂單导出模块背景情况的详细说明，包含字段映射与分页逻辑描述。", 15)
	lateConstraint := "注意：绝对不能修改数据库 migration 文件，禁止 touching schema.sql"
	turns = append(turns,
		MessageTurn{
			Info:  MessageInfo{ID: "u_late_constraint", Role: "user"},
			Parts: []MessagePart{{Type: "text", Text: longPrefix + "\n" + lateConstraint}},
		},
		MessageTurn{
			Info:  MessageInfo{ID: "a_late_constraint", Role: "assistant"},
			Parts: []MessagePart{{Type: "text", Text: "Understood, will not modify migration files or schema.sql."}},
		},
	)
	// 12 subsequent turns with noisy tool outputs so u_late_constraint falls outside the recent 6 turns.
	for i := 1; i <= 12; i++ {
		turns = append(turns,
			MessageTurn{
				Info:  MessageInfo{ID: fmt.Sprintf("u_tail_%d", i), Role: "user"},
				Parts: []MessagePart{{Type: "text", Text: fmt.Sprintf("Continue batch verification part %d", i)}},
			},
			MessageTurn{
				Info: MessageInfo{ID: fmt.Sprintf("a_tail_%d", i), Role: "assistant"},
				Parts: []MessagePart{
					{
						Type: "tool",
						Tool: "bash",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"command": fmt.Sprintf("go test ./pkg/part%d/...", i)},
							Output: "ok\tgithub.com/hoxiai/hoxi/pkg\t0.05s\n" + strings.Repeat("verbose output line\n", 80),
						},
					},
					{Type: "text", Text: fmt.Sprintf("Batch %d verified.", i)},
				},
			},
		)
	}

	rec := SessionRecord{
		ID:             "ses_long_constraints",
		Runtime:        "claude-code",
		ContextPending: true,
		Turns:          turns,
	}
	ctx := runtimeContext(rec)
	if !strings.Contains(ctx, "绝对不能修改数据库 migration 文件") {
		t.Fatalf("expected late constraint in long user message to survive in runtimeContext, got:\n%s", ctx)
	}
	if len(ctx) > maxHandoffContextBytes {
		t.Fatalf("expected runtimeContext len <= %d, got %d", maxHandoffContextBytes, len(ctx))
	}
	md := buildEightDimSessionMarkdown(rec)
	if !strings.Contains(md, "绝对不能修改数据库 migration 文件") {
		t.Fatalf("expected late constraint in long user message to survive in 8-dim session memory, got:\n%s", md)
	}
	rec.ProjectDir = t.TempDir()
	combined := runtimeMemoryContext(rec) + runtimeContext(rec)
	if !strings.Contains(combined, "Carried in quoted conversation history below.") {
		t.Fatalf("expected runtimeMemoryContext to deduplicate Section 6 when runtimeContext is active, got:\n%s", combined)
	}
}

func TestNativeCLISystemDeduplicatedOnResumedTurnsAndToolSummaryDedup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	rec := SessionRecord{
		ID:           "ses_cli_dedup",
		Runtime:      "claude-code",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
	}
	callerSys := "Workspace context for this session:\n- Primary folder: " + root
	fullSys := strings.TrimSpace(commandExecutionGuidance + "\n\n" + callerSys)

	// Turn 1 (ExternalID == ""): receives full commandExecutionGuidance + callerSys.
	if got := svc.nativeCLISystemForTurn(rec, callerSys, fullSys); got != fullSys {
		t.Fatalf("expected full system on initial turn, got %q", got)
	}
	svc.recordCLISystemDelivered(rec.ID, rec.Runtime, callerSys)

	// Turn 2 (resumed with ExternalID != "" and !ContextPending, unchanged callerSys):
	// omits redundant commandExecutionGuidance and unchanged callerSys.
	rec.ExternalID = "claude_conv_1"
	if got := svc.nativeCLISystemForTurn(rec, callerSys, fullSys); got != "" {
		t.Fatalf("expected empty system on resumed turn with unchanged callerSys, got %q", got)
	}

	// Turn 3 (resumed, callerSys changed): injects only the updated callerSys without repeating commandExecutionGuidance.
	updatedCallerSys := callerSys + "\nUser global custom instructions:\nUse strict types"
	updatedFullSys := strings.TrimSpace(commandExecutionGuidance + "\n\n" + updatedCallerSys)
	if got := svc.nativeCLISystemForTurn(rec, updatedCallerSys, updatedFullSys); got != updatedCallerSys {
		t.Fatalf("expected only updated callerSys on resumed turn, got %q", got)
	}

	// Runtime switch (ContextPending == true): injects full system again.
	rec.Runtime = "antigravity"
	rec.ContextPending = true
	if got := svc.nativeCLISystemForTurn(rec, updatedCallerSys, updatedFullSys); got != updatedFullSys {
		t.Fatalf("expected full system after runtime switch, got %q", got)
	}

	// Verify runtimeContext deduplicates identical tool summaries within the same turn and strips ANSI from excerpts.
	rec.Turns = []MessageTurn{
		{
			Info:  MessageInfo{ID: "u1", Role: "user"},
			Parts: []MessagePart{{Type: "text", Text: "Inspect foo.go"}},
		},
		{
			Info: MessageInfo{ID: "a1", Role: "assistant"},
			Parts: []MessagePart{
				{Type: "tool", Tool: "read", State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": "foo.go"}}},
				{Type: "tool", Tool: "read", State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": "foo.go"}}},
				{Type: "text", Text: "\x1b[32mInspected\x1b[0m foo.go"},
			},
		},
	}
	ctx := runtimeContext(rec)
	if strings.Count(ctx, "Tool read [completed]: foo.go") != 1 {
		t.Fatalf("expected duplicate tool summary in same turn to be deduplicated, got:\n%s", ctx)
	}
	if strings.Contains(ctx, "\x1b[32m") || !strings.Contains(ctx, "Inspected foo.go") {
		t.Fatalf("expected ANSI codes to be stripped in singleLineExcerpt, got:\n%s", ctx)
	}
}

func TestOpenCodeHandoffIncludesSessionMemoryAndSymbolLineQualifiers(t *testing.T) {
	t.Parallel()
	var turns []MessageTurn
	turns = append(turns,
		MessageTurn{
			Info:  MessageInfo{ID: "u_init", Role: "user"},
			Parts: []MessagePart{{Type: "text", Text: "Refactor token verifier and keep backwards compatibility"}},
		},
		MessageTurn{
			Info: MessageInfo{ID: "a_early", Role: "assistant"},
			Parts: []MessagePart{
				{
					Type: "tool",
					Tool: "hoxi_code_index",
					State: &ToolState{
						Status: "completed",
						Input: map[string]interface{}{
							"action":    "inspect",
							"path":      "internal/auth/verifier.go",
							"symbol":    "VerifyToken",
							"startLine": 42,
							"endLine":   98,
						},
						Output: "func VerifyToken() {}",
					},
				},
				{
					Type: "tool",
					Tool: "edit",
					State: &ToolState{
						Status: "completed",
						Input: map[string]interface{}{
							"filePath": "internal/auth/early_modified.go",
						},
						Output: "ok",
					},
				},
			},
		},
	)
	for i := 1; i <= 10; i++ {
		turns = append(turns,
			MessageTurn{
				Info:  MessageInfo{ID: fmt.Sprintf("u_%d", i), Role: "user"},
				Parts: []MessagePart{{Type: "text", Text: fmt.Sprintf("Follow-up step %d", i)}},
			},
			MessageTurn{
				Info: MessageInfo{ID: fmt.Sprintf("a_%d", i), Role: "assistant"},
				Parts: []MessagePart{
					{
						Type: "tool",
						Tool: "read",
						State: &ToolState{
							Status: "completed",
							Input: map[string]interface{}{
								"filePath": "internal/auth/handler.go",
								"offset":   100,
								"limit":    50,
							},
						},
					},
					{Type: "text", Text: fmt.Sprintf("Done step %d", i)},
				},
			},
		)
	}

	rec := SessionRecord{
		ID:               "ses_oc_handoff",
		Runtime:          "opencode",
		ExternalID:       "ses_oc_engine_1",
		ContextPending:   false,
		ContextTurnCount: len(turns),
		Turns:            turns,
	}
	ctx1 := runtimeContext(rec)
	if !strings.Contains(ctx1, "<session_handoff_memory>") {
		t.Fatalf("expected OpenCode handoff context to include <session_handoff_memory>, got:\n%s", ctx1)
	}
	if !strings.Contains(ctx1, "`internal/auth/early_modified.go` (modified)") {
		t.Fatalf("expected early modified file outside recent window to survive via <session_handoff_memory>, got:\n%s", ctx1)
	}
	if !strings.Contains(ctx1, "`internal/auth/verifier.go` [symbol VerifyToken, L42-98]") {
		t.Fatalf("expected symbol and line range qualifier in 8-dim session memory, got:\n%s", ctx1)
	}
	if !strings.Contains(ctx1, "Tool read [completed]: internal/auth/handler.go [L100-149]") {
		t.Fatalf("expected offset/limit line range qualifier in recent tool summary, got:\n%s", ctx1)
	}

	// Subsequent turns in OpenCode append new messages to rec.Turns while ContextTurnCount stays fixed;
	// runtimeContext must remain 100% byte-identical to preserve the LLM KV prefix cache.
	rec.Turns = append(rec.Turns, MessageTurn{
		Info:  MessageInfo{ID: "u_after_switch", Role: "user"},
		Parts: []MessagePart{{Type: "text", Text: "New turn after switch"}},
	})
	ctx2 := runtimeContext(rec)
	if ctx1 != ctx2 {
		t.Fatalf("expected OpenCode handoff context to remain 100%% byte-identical across subsequent turns")
	}
}

func TestOpenCodeHandoffRespectsBudgetAndDeduplicates48Constraints(t *testing.T) {
	t.Parallel()
	var turns []MessageTurn
	for i := 1; i <= 48; i++ {
		turns = append(turns,
			MessageTurn{
				Info: MessageInfo{ID: fmt.Sprintf("u_c_%d", i), Role: "user"},
				Parts: []MessagePart{{
					Type: "text",
					Text: fmt.Sprintf("Constraint %d: 严禁修改核心模块 module_alpha_%02d 的公开接口签名与序列化协议字段，必须保持跨版本严格兼容且不得删除任何现有验证分支 (%s)", i, i, strings.Repeat("详细上下文说明", 12)),
				}},
			},
			MessageTurn{
				Info: MessageInfo{ID: fmt.Sprintf("a_c_%d", i), Role: "assistant"},
				Parts: []MessagePart{
					{
						Type: "tool",
						Tool: "edit",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"filePath": fmt.Sprintf("internal/mod_%02d/service.go", i)},
							Output: "patched",
						},
					},
					{Type: "text", Text: fmt.Sprintf("Recorded constraint %d and updated service %d with full compatibility checks.", i, i)},
				},
			},
		)
	}
	rec := SessionRecord{
		ID:               "ses_oc_budget_48",
		Runtime:          "opencode",
		ExternalID:       "ses_oc_ext_48",
		WorktreePath:     "/tmp/proj/.hoxi/worktrees/ses_oc_budget_48",
		Branch:           "hoxi/ses_oc_budget_48",
		ContextPending:   false,
		ContextTurnCount: len(turns),
		Turns:            turns,
	}
	ctx := runtimeContext(rec)
	if len(ctx) > maxHandoffContextBytes {
		t.Fatalf("expected OpenCode handoff context len <= %d, got %d", maxHandoffContextBytes, len(ctx))
	}
	if !strings.Contains(ctx, "<session_handoff_memory>") {
		t.Fatalf("expected <session_handoff_memory> in OpenCode handoff context")
	}
	if strings.Contains(ctx, "- User Constraint:") {
		t.Fatalf("expected <session_handoff_memory> not to duplicate - User Constraint lines when Active user constraints section is present")
	}
	if !strings.Contains(ctx, "module_alpha_48") {
		t.Fatalf("expected latest constraint to survive within budget, got len=%d", len(ctx))
	}
}

func TestToolHeavyAssistantTurnRetainsLatestReplyAndPathEditsOnFallback(t *testing.T) {
	t.Parallel()
	var assistantParts []MessagePart
	assistantParts = append(assistantParts, MessagePart{
		Type: "text",
		Text: strings.Repeat("Transitional investigation step across workspace files. ", 12),
	})
	for i := 1; i <= 90; i++ {
		assistantParts = append(assistantParts, MessagePart{
			Type: "tool",
			Tool: "read_file",
			State: &ToolState{
				Status: "completed",
				Input: map[string]interface{}{
					"Path":      fmt.Sprintf("internal/module_%03d/handler_%03d.go", i, i),
					"StartLine": 1,
					"EndLine":   120,
				},
			},
		})
	}
	editedFiles := []string{
		"web/src/components/layout/Sidebar.tsx",
		"web/src/store/sessions.ts",
		"internal/runner/sessions_crud.go",
	}
	for _, path := range editedFiles {
		assistantParts = append(assistantParts, MessagePart{
			Type: "tool",
			Tool: "edit_file",
			State: &ToolState{
				Status: "completed",
				Input: map[string]interface{}{
					"Path": path,
				},
				Output: "Made 1 replacement",
			},
		})
	}
	for i := 91; i <= 180; i++ {
		assistantParts = append(assistantParts, MessagePart{
			Type: "tool",
			Tool: "read_file",
			State: &ToolState{
				Status: "completed",
				Input: map[string]interface{}{
					"Path":      fmt.Sprintf("web/src/components/view_%03d/Panel_%03d.tsx", i, i),
					"StartLine": 10,
					"EndLine":   90,
				},
			},
		})
	}
	assistantParts = append(assistantParts,
		MessagePart{
			Type: "tool",
			Tool: "run_command",
			State: &ToolState{
				Status: "completed",
				Input: map[string]interface{}{
					"CommandLine": "go test ./internal/runner/... && npm --prefix web test",
				},
				Output: "ok",
			},
		},
		MessagePart{
			Type: "text",
			Text: "FINAL_ASSISTANT_SUMMARY: Fixed deleted-session guard in Sidebar.tsx, sessions.ts, and sessions_crud.go; all unit tests passed.",
		},
	)

	rec := SessionRecord{
		ID:             "ses_agy_heavy_turn",
		Runtime:        "antigravity",
		ExternalID:     "",
		ContextPending: true,
		WorktreePath:   "/tmp/proj",
		Branch:         "main",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u1", Role: "user"},
				Parts: []MessagePart{{Type: "text", Text: "Fix session deletion race where deleted sessions can still send prompts"}},
			},
			{
				Info:  MessageInfo{ID: "msg_a1", Role: "assistant"},
				Parts: assistantParts,
			},
		},
	}

	md := buildEightDimSessionMarkdown(rec)
	for _, path := range editedFiles {
		if !strings.Contains(md, fmt.Sprintf("`%s` (modified)", path)) {
			t.Fatalf("expected 8-dim memory to record Path edit %s as modified, got:\n%s", path, md)
		}
	}
	if !strings.Contains(md, "`go test ./internal/runner/... && npm --prefix web test` (completed)") {
		t.Fatalf("expected 8-dim memory to record CommandLine verification, got:\n%s", md)
	}

	ctx := runtimeContext(rec)
	if len(ctx) > maxHandoffContextBytes {
		t.Fatalf("expected handoff context len <= %d, got %d", maxHandoffContextBytes, len(ctx))
	}
	if !strings.Contains(ctx, "FINAL_ASSISTANT_SUMMARY: Fixed deleted-session guard") {
		t.Fatalf("expected latest assistant turn summary to survive 180+ tool calls, got:\n%s", ctx)
	}
	for _, path := range editedFiles {
		if !strings.Contains(ctx, "Tool edit_file [completed]: "+path) {
			t.Fatalf("expected prioritized mutating tool summary for %s to survive, got:\n%s", path, ctx)
		}
	}
}
