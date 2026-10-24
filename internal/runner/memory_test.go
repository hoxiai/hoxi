package runner

import (
	"fmt"
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
