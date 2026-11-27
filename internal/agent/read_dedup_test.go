package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestReadLongLine2KBGuardAndWriteSafety(t *testing.T) {
	ws := t.TempDir()
	// Place multi-byte UTF-8 runes across the 2048-byte boundary to verify clean rune slicing.
	longLine := "const svgPath = \"" + strings.Repeat("轨", 4000) + "\";"
	body := strings.Join([]string{
		"package render",
		longLine,
		"func TailFunctionStillVisible() int { return 42 }",
		"",
	}, "\n")
	fullPath := filepath.Join(ws, "asset.go")
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := NewFileRevisionTracker()
	rTool := &readTool{}
	rawIn, _ := json.Marshal(map[string]any{"filePath": "asset.go"})
	out, err := rTool.Execute(context.Background(), ToolContext{
		WorktreeDir:   ws,
		FileRevisions: tracker,
	}, rawIn)
	if err != nil || out.IsError {
		t.Fatalf("read failed: err=%v out=%+v", err, out)
	}
	if !utf8.ValidString(out.Output) {
		t.Fatal("expected valid UTF-8 after truncating multi-byte line at 2KB")
	}
	if !strings.Contains(out.Output, "bytes omitted on line 2]") {
		t.Fatalf("expected 2KB per-line omission marker on line 2, got:\n%s", out.Output)
	}
	if !strings.Contains(out.Output, "3: func TailFunctionStillVisible() int { return 42 }") {
		t.Fatalf("expected line 3 after long line to remain visible, got:\n%s", out.Output)
	}
	if len(out.Output) > 3000 {
		t.Fatalf("expected output bounded near ~2.2KB instead of ~12KB, got %d bytes", len(out.Output))
	}

	// Blind full-file write must be refused because line 2 was truncated (`deliveredComplete == false`).
	if wErr := tracker.CheckWrite(fullPath, "asset.go"); wErr == nil {
		t.Fatal("expected full-file write overwrite to be rejected when a long line was truncated during read")
	}

	// Targeted edit on line 3 must still succeed cleanly.
	eTool := &editTool{}
	editIn, _ := json.Marshal(map[string]any{
		"filePath":  "asset.go",
		"oldString": "return 42",
		"newString": "return 99",
	})
	eOut, eErr := eTool.Execute(context.Background(), ToolContext{
		WorktreeDir:   ws,
		FileRevisions: tracker,
	}, editIn)
	if eErr != nil || eOut.IsError {
		t.Fatalf("expected targeted edit on untruncated line 3 to succeed, got err=%v out=%+v", eErr, eOut)
	}
}

func TestFullWriteAfterReadingEveryByteLimitedPage(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "module.ts")
	var src strings.Builder
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&src, "export const value%d = %q;\n", i, strings.Repeat("a", 42))
	}
	if err := os.WriteFile(path, []byte(src.String()), 0600); err != nil {
		t.Fatal(err)
	}
	tc := ToolContext{WorktreeDir: ws, FileRevisions: NewFileRevisionTracker()}
	registry := newToolRegistry()
	run := func(tool string, args map[string]any) (ToolOutput, error) {
		input, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return registry.Execute(context.Background(), tc, ToolCall{Name: tool, Input: input}, nil)
	}
	first, err := run("read", map[string]any{"filePath": "module.ts"})
	if err != nil {
		t.Fatal(err)
	}
	_, hint, ok := strings.Cut(first.Output, "pass offset=")
	var next int
	if _, err := fmt.Sscanf(hint, "%d", &next); !ok || err != nil || next <= 1 {
		t.Fatalf("missing next page: %v", err)
	}
	write := map[string]any{"filePath": "module.ts", "content": "export const updated = true;\n"}
	if _, err := run("write", write); err == nil {
		t.Fatal("overwrite allowed after only one page")
	}
	second, err := run("read", map[string]any{"filePath": "module.ts", "offset": next, "limit": math.MaxInt})
	if err != nil || !strings.Contains(second.Output, "700: export const value699") {
		t.Fatal("missing final lines", err)
	}
	if _, err := run("write", write); err != nil {
		t.Fatal(err)
	}
}

func TestGrepLongLineMatchCenteredWindow(t *testing.T) {
	ws := t.TempDir()
	minified := strings.Repeat("a", 10000) + "/*NEEDLE_MARKER_7788*/" + strings.Repeat("z", 10000) + "\n"
	if err := os.WriteFile(filepath.Join(ws, "bundle.js"), []byte(minified), 0o644); err != nil {
		t.Fatal(err)
	}

	gTool := &grepTool{}
	rawIn, _ := json.Marshal(map[string]any{"pattern": "NEEDLE_MARKER_7788"})
	out, err := gTool.Execute(context.Background(), ToolContext{WorktreeDir: ws}, rawIn)
	if err != nil || out.IsError {
		t.Fatalf("grep failed: err=%v out=%+v", err, out)
	}
	if !strings.Contains(out.Output, "NEEDLE_MARKER_7788") {
		t.Fatalf("expected match-centered grep snippet to contain NEEDLE_MARKER_7788, got:\n%s", out.Output)
	}
	if !strings.Contains(out.Output, "...") {
		t.Fatalf("expected ellipsis markers on truncated long grep line, got:\n%s", out.Output)
	}
	if len(out.Output) > 620 {
		t.Fatalf("expected grep line snippet bounded near 512 bytes, got %d bytes", len(out.Output))
	}
}

func TestUnchangedReadDedupSubsetEscapeHatchAndPostCompactionReRead(t *testing.T) {
	ws := t.TempDir()
	var src strings.Builder
	src.WriteString("package service\n\n")
	for i := 1; i <= 45; i++ {
		fmt.Fprintf(&src, "func HandlerStep%02d() string { return \"payload-value-%02d\" }\n", i, i)
	}
	targetFile := filepath.Join(ws, "handlers.go")
	if err := os.WriteFile(targetFile, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	fullReadArgs := `{"filePath":"handlers.go"}`
	subsetReadArgs := `{"filePath":"handlers.go","offset":10,"limit":22}`
	editArgsBytes, _ := json.Marshal(map[string]any{
		"filePath":  "handlers.go",
		"oldString": `func HandlerStep15() string { return "payload-value-15" }`,
		"newString": `func HandlerStep15() string { return "updated-value-15" }`,
	})

	mkToolStep := func(callID, toolName, rawArgs string) (string, error) {
		quotedArgs, _ := json.Marshal(rawArgs)
		return fmt.Sprintf(
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
			callID, toolName, string(quotedArgs),
		), nil
	}

	var stepNum atomic.Int32
	mockStream := &fakeStreamClient{
		target: llm.StreamTarget{
			ProviderID:   "openai",
			ModelID:      "gpt-5",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 128000,
			OutputLimit:  8192,
		},
		openFn: func(_ context.Context, _ llm.StreamHTTPRequest) (string, error) {
			switch stepNum.Add(1) {
			case 1:
				return mkToolStep("r_initial", "read", fullReadArgs)
			case 2:
				return mkToolStep("r_dup", "read", fullReadArgs)
			case 3:
				// Consecutive re-read right after [unchanged] must hit escape hatch and return full lines.
				return mkToolStep("r_escape", "read", fullReadArgs)
			case 4:
				// Subset read [10..31] covered by r_escape must be deduplicated cleanly.
				return mkToolStep("r_subset", "read", subsetReadArgs)
			case 5:
				return mkToolStep("e_mutate", "edit", string(editArgsBytes))
			case 6:
				// Subset read after edit must return fresh updated lines in full.
				return mkToolStep("r_after_edit", "read", subsetReadArgs)
			default:
				return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", nil
			}
		},
	}

	outputs := make(map[string]string)
	eng := NewDefaultEngine(mockStream)
	if err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_read_dedup_1",
		WorktreeDir: ws,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Inspect and update handlers.go",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineToolCompleted {
				outputs[evt.CallID] = evt.Text
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if !strings.Contains(outputs["r_initial"], "func HandlerStep15()") {
		t.Fatalf("expected r_initial to return full source, got: %q", outputs["r_initial"])
	}
	if !strings.HasPrefix(outputs["r_dup"], unchangedReadNoticePrefix) {
		t.Fatalf("expected r_dup to return unchanged notice, got: %q", outputs["r_dup"])
	}
	if !strings.Contains(outputs["r_escape"], "func HandlerStep15()") {
		t.Fatalf("expected consecutive r_escape after unchanged notice to deliver full lines, got: %q", outputs["r_escape"])
	}
	if !strings.HasPrefix(outputs["r_subset"], unchangedReadNoticePrefix) {
		t.Fatalf("expected covered r_subset to return unchanged notice, got: %q", outputs["r_subset"])
	}
	if strings.HasPrefix(outputs["r_after_edit"], unchangedReadNoticePrefix) || !strings.Contains(outputs["r_after_edit"], "updated-value-15") {
		t.Fatalf("expected r_after_edit to return fresh edited lines in full, got: %q", outputs["r_after_edit"])
	}

	// Verify post-compaction safety: if the earlier read in VisibleMessages was pruned, re-reading returns full text.
	rTool := &readTool{}
	prunedHistory := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{
			Type:  ContentToolUse,
			ID:    "old_r1",
			Name:  "read",
			Input: json.RawMessage(fullReadArgs),
		}}},
		{Role: RoleUser, Content: []ContentBlock{{
			Type:      ContentToolResult,
			ToolUseID: "old_r1",
			Text:      "[tool output pruned: read handlers.go (2600 chars) compacted]",
		}}},
	}
	reReadOut, err := rTool.Execute(context.Background(), ToolContext{
		WorktreeDir:     ws,
		VisibleMessages: prunedHistory,
		FileRevisions:   NewFileRevisionTracker(),
	}, json.RawMessage(fullReadArgs))
	if err != nil || reReadOut.IsError {
		t.Fatalf("post-compaction read failed: err=%v out=%+v", err, reReadOut)
	}
	if strings.HasPrefix(reReadOut.Output, unchangedReadNoticePrefix) || !strings.Contains(reReadOut.Output, "updated-value-15") {
		t.Fatalf("expected full source when earlier read was pruned by compaction, got: %q", reReadOut.Output)
	}
}

func TestReadNoticePreservesRulesAndCannotSupersedeSource(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "pkg")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("evidence that the agent should keep\n", 18)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	tc := ToolContext{WorktreeDir: ws}
	args := json.RawMessage(`{"filePath":"pkg/file.txt"}`)
	read := func(id string) string {
		t.Helper()
		out, err := (&readTool{}).Execute(context.Background(), tc, args)
		if err != nil {
			t.Fatal(err)
		}
		tc.VisibleMessages = append(tc.VisibleMessages,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: id, Name: "read", Input: args}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: id, Text: out.Output}}})
		return out.Output
	}
	first := read("r1")
	const rule = "Preserve integer rounding"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	tc.RulesTracker = NewSubdirRuleTracker(nil, DefaultSubdirRuleBudgetBytes)
	if got := read("r2"); !strings.HasPrefix(got, unchangedReadNoticePrefix) || !strings.Contains(got, rule) {
		t.Fatalf("missing fresh rules or reference: %q", got)
	}
	for i := 0; i < 9; i++ {
		tc.VisibleMessages = append(tc.VisibleMessages, Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: strings.Repeat("fill context ", 300)}}})
	}
	pruned := PruneOldToolResultsInWorkspace(tc.VisibleMessages, "system", ws, 8000)
	if pruned[1].Content[0].Text != first {
		t.Fatalf("lost the only copy of source: %q", pruned[1].Content[0].Text)
	}
}

func TestReadDedupPairsLegacyCallIDsByStep(t *testing.T) {
	ws := t.TempDir()
	const id = "call_gem_1"
	rendered := "1: " + strings.Repeat("x", 700) + "\n"
	var history []Message
	for _, entry := range []struct{ path, result string }{
		{"target.txt", "[tool output pruned: earlier read]"},
		{"other.txt", rendered},
	} {
		input, _ := json.Marshal(map[string]string{"filePath": entry.path})
		history = append(history,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: id, Name: "read", Input: input}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: id, Text: entry.result}}})
	}
	if _, ok := tryBuildUnchangedReadNotice(history, ws, filepath.Join(ws, "target.txt"), "target.txt", rendered, nil, 1, 1, 1); ok {
		t.Fatal("a different file's result must not stand in for the pruned read")
	}
}
