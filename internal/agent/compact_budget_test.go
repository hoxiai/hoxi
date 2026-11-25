package agent_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
)

func makeToolStep(callID, toolName string, input any, result string, isErr bool) []agent.Message {
	raw, _ := json.Marshal(input)
	return []agent.Message{
		{
			Role: agent.RoleAssistant,
			Content: []agent.ContentBlock{{
				Type:  agent.ContentToolUse,
				ID:    callID,
				Name:  toolName,
				Input: raw,
			}},
		},
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{{
				Type:      agent.ContentToolResult,
				ToolUseID: callID,
				Text:      result,
				IsError:   isErr,
			}},
		},
	}
}

func TestStaleAndDuplicateReadFoldingRespectsErrorsPaginationAndPatchPaths(t *testing.T) {
	const contextLimit = 8000 // 75% trigger = 6000 tokens, keepRecent = 6

	// Moderate read (~750 chars): below minToolLen (800/2400), above compactStaleReadMinLen (320).
	moderateCode := strings.Repeat("10: func computeOldBehavior() int { return 41 }\n", 16)
	page1Code := strings.Repeat("1: const pageOneKeep = true\n", 20)
	page2Code := strings.Repeat("101: const pageTwoKeep = true\n", 20)

	var msgs []agent.Message
	// Chunk 0 (messages 0..11, 6 steps) — also reuse Gemini-style repeated ID "call_gem_1"
	// across steps to verify per-step pairing never collides!
	// Step 0 (msgs 0..1): read ./pkg/svc.go -> later edited at Step 2 -> MUST fold as stale.
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "./pkg/svc.go"}, moderateCode, false)...)
	// Step 1 (msgs 2..3): read cmd/a/main.go -> Step 3 fails edit on cmd/a/main.go -> MUST NOT fold!
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "cmd/a/main.go"}, moderateCode, false)...)
	// Step 2 (msgs 4..5): successful edit on pkg/svc.go (matches ./pkg/svc.go lexically).
	msgs = append(msgs, makeToolStep("call_gem_1", "edit", map[string]any{"filePath": "pkg/svc.go"}, "edited pkg/svc.go", false)...)
	// Step 3 (msgs 6..7): FAILED edit on cmd/a/main.go (IsError=true) -> must not invalidate Step 1's read.
	msgs = append(msgs, makeToolStep("call_gem_1", "edit", map[string]any{"filePath": "cmd/a/main.go"}, "oldString not found", true)...)
	// Step 4 (msgs 8..9): paginated read of pkg/large.go [1..100].
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/large.go", "offset": 1, "limit": 100}, page1Code, false)...)
	// Step 5 (msgs 10..11): disjoint paginated read of pkg/large.go [101..200] -> MUST NOT fold Step 4!
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/large.go", "offset": 101, "limit": 100}, page2Code, false)...)

	// Chunk 1 (messages 12..23, 6 steps):
	// Step 6 (msgs 12..13): narrow read of pkg/util.go [20..60] -> covered by Step 7 [1..200] -> MUST fold as duplicate.
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/util.go", "offset": 20, "limit": 40}, moderateCode, false)...)
	// Step 7 (msgs 14..15): covering read of pkg/util.go [1..200].
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/util.go", "offset": 1, "limit": 200}, moderateCode, false)...)
	// Step 8 (msgs 16..17): read of pkg/patched.go -> modified by apply_patch in Step 9 -> MUST fold as stale.
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/patched.go"}, moderateCode, false)...)
	// Step 9 (msgs 18..19): apply_patch updating pkg/patched.go.
	patchBody := "*** Begin Patch\n*** Update File: pkg/patched.go\n@@\n-old\n+new\n*** End Patch"
	msgs = append(msgs, makeToolStep("call_gem_1", "apply_patch", map[string]any{"patch": patchBody}, "patched 1 file", false)...)
	// Step 10 (msgs 20..21): read of pkg/trunc.go [1..50] -> Step 11 read [1..2000] is byte-truncated -> MUST NOT fold Step 10!
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/trunc.go", "offset": 1, "limit": 50}, moderateCode, false)...)
	truncReadOut := moderateCode + "\n\n... [8192 bytes truncated] ...\n\ntail"
	msgs = append(msgs, makeToolStep("call_gem_1", "read", map[string]any{"filePath": "pkg/trunc.go", "offset": 1, "limit": 2000}, truncReadOut, false)...)

	// Recent tail (messages 24..29, 3 steps = 6 messages, kept unpruned; push total > 6000 tokens).
	bigRecent := strings.Repeat("recent unpruned context line for active step\n", 420) // ~18.9k chars (~6300 tokens)
	msgs = append(msgs, makeToolStep("rec_1", "read", map[string]any{"filePath": "active.go"}, bigRecent, false)...)
	msgs = append(msgs, makeToolStep("rec_2", "read", map[string]any{"filePath": "active2.go"}, "short", false)...)
	msgs = append(msgs, makeToolStep("rec_3", "read", map[string]any{"filePath": "active3.go"}, "short", false)...)

	pruned := agent.PruneOldToolResults(msgs, "sys", contextLimit)

	// Step 0: folded as stale due to Step 2 edit.
	if got := pruned[1].Content[0].Text; !strings.Contains(got, "stale read of pkg/svc.go superseded by a later edit") {
		t.Fatalf("expected Step 0 read to fold as stale after edit, got: %q", got)
	}
	// Step 1: NOT folded because Step 3 edit failed (IsError=true) and len < 800.
	if got := pruned[3].Content[0].Text; got != moderateCode {
		t.Fatalf("failed edit must never mark earlier read as stale, got: %q", got)
	}
	// Steps 4 & 5: disjoint paginated reads must both stay intact.
	if pruned[9].Content[0].Text != page1Code || pruned[11].Content[0].Text != page2Code {
		t.Fatalf("disjoint paginated reads must not fold each other:\np1=%q\np2=%q", pruned[9].Content[0].Text, pruned[11].Content[0].Text)
	}
	// Step 6: folded as duplicate because Step 7 covers [20..59] within [1..200].
	if got := pruned[13].Content[0].Text; !strings.Contains(got, "duplicate read of pkg/util.go superseded by a later read") {
		t.Fatalf("expected Step 6 narrow read to fold under covering read, got: %q", got)
	}
	// Step 7: covering read itself stays intact (< minToolLen).
	if got := pruned[15].Content[0].Text; got != moderateCode {
		t.Fatalf("expected covering read at Step 7 to remain intact, got: %q", got)
	}
	// Step 8: folded as stale due to Step 9 apply_patch.
	if got := pruned[17].Content[0].Text; !strings.Contains(got, "stale read of pkg/patched.go superseded by a later edit") {
		t.Fatalf("expected Step 8 read to fold after apply_patch, got: %q", got)
	}
	// Step 10: NOT folded because Step 11 covering read was byte-truncated at 32 KiB.
	if got := pruned[21].Content[0].Text; got != moderateCode {
		t.Fatalf("byte-truncated later read must not fold earlier non-truncated page, got: %q", got)
	}

	// Idempotency: running PruneOldToolResults a second time produces byte-identical output.
	prunedAgain := agent.PruneOldToolResults(pruned, "sys", contextLimit)
	b1, _ := json.Marshal(pruned)
	b2, _ := json.Marshal(prunedAgain)
	if string(b1) != string(b2) {
		t.Fatal("PruneOldToolResults must be strictly idempotent across repeated invocations")
	}
}

func TestCrossChunkEditNeverMutatesEarlierQuantizedCachePrefix(t *testing.T) {
	const contextLimit = 4000 // trigger = 3000, hardCap = 3600, keepRecent = 6

	moderateRead := strings.Repeat("20: func stablePrefixTarget() {}\n", 18) // ~594 chars
	verboseFiller := strings.Repeat("verbose output filler line to cross 75% trigger\n", 45)

	// Chunk 0 (messages 0..11): Step 0 reads target.go; Steps 1..5 are unrelated logs.
	var turn1 []agent.Message
	turn1 = append(turn1, makeToolStep("c0", "read", map[string]any{"filePath": "target.go"}, moderateRead, false)...)
	for i := 1; i < 6; i++ {
		turn1 = append(turn1, makeToolStep(fmt.Sprintf("c%d", i), "bash", map[string]any{"command": "test"}, verboseFiller, false)...)
	}
	// Tail at Turn 1 (messages 12..17): 3 steps so len=18 -> rawCutoff=12 -> stride=12.
	for i := 6; i < 9; i++ {
		turn1 = append(turn1, makeToolStep(fmt.Sprintf("c%d", i), "read", map[string]any{"filePath": "other.go"}, "ok", false)...)
	}

	prunedTurn1 := agent.PruneOldToolResults(turn1, "sys", contextLimit)
	chunk0AtTurn1, _ := json.Marshal(prunedTurn1[:12])

	// Now advance to a later turn where Chunk 1 (messages 12..23) edits target.go AND stride advances to 24!
	turnLater := append([]agent.Message(nil), turn1...)
	turnLater = append(turnLater, makeToolStep("c9", "edit", map[string]any{"filePath": "target.go"}, "edited target.go", false)...)
	turnLater = append(turnLater, makeToolStep("c10", "bash", map[string]any{"command": "build"}, verboseFiller, false)...)
	turnLater = append(turnLater, makeToolStep("c11", "bash", map[string]any{"command": "build2"}, verboseFiller, false)...)
	// Add 3 recent steps (messages 24..29) with enough weight that stride=12 isn't enough and stride=24 runs!
	bigActive := strings.Repeat("active work context\n", 390) // ~7800 chars (~2600 tokens)
	turnLater = append(turnLater, makeToolStep("c12", "read", map[string]any{"filePath": "cur.go"}, bigActive, false)...)
	turnLater = append(turnLater, makeToolStep("c13", "read", map[string]any{"filePath": "cur2.go"}, "ok", false)...)
	turnLater = append(turnLater, makeToolStep("c14", "read", map[string]any{"filePath": "cur3.go"}, "ok", false)...)

	prunedTurnLater := agent.PruneOldToolResults(turnLater, "sys", contextLimit)
	chunk0AtTurnLater, _ := json.Marshal(prunedTurnLater[:12])

	if string(chunk0AtTurn1) != string(chunk0AtTurnLater) {
		t.Fatalf("cross-chunk edit in Chunk 1 retroactively mutated Chunk 0 KV-cache prefix!\nbefore: %s\nafter:  %s", chunk0AtTurn1, chunk0AtTurnLater)
	}
}

func TestPruningPreservesSubdirectoryRulesAndMiddleDiagnostics(t *testing.T) {
	const contextLimit = 8000

	subdirRule := `<subdirectory_instructions file="internal/sub/AGENTS.md">` + "\n" +
		strings.Repeat("- Critical subdirectory invariant line that must never be truncated.\n", 14) +
		`</subdirectory_instructions>`

	staleBodyWithRule := strings.Repeat("1: package sub // old version\n", 30) + "\n\n" + subdirRule

	var bashFailLines []string
	for i := 1; i <= 40; i++ {
		if i == 20 {
			bashFailLines = append(bashFailLines, "internal/sub/handler.go:142:9: undefined: missingHelper")
		} else if i == 21 {
			bashFailLines = append(bashFailLines, "--- FAIL: TestSubHandler (0.03s)")
		} else {
			bashFailLines = append(bashFailLines, fmt.Sprintf("=== RUN   TestPassingCase%02d\n--- PASS: TestPassingCase%02d (0.00s)", i, i))
		}
	}
	bashFailLines = append(bashFailLines, "FAIL\tgithub.com/example/sub\t0.412s")
	bashFailOut := strings.Join(bashFailLines, "\n")

	bashPassOut := strings.Repeat("=== RUN   TestSuiteGreen\n--- PASS: TestSuiteGreen (0.01s)\n", 60) + "ok  \tgithub.com/example/green\t0.210s"

	// Source file read containing error strings in code comments/literals — must NOT trigger diagnostic banner.
	goSourceWithErrLiteral := strings.Repeat("// normal source line in Go file\n", 45) +
		`return fmt.Errorf("fatal: simulated error string in source code")` + "\n" +
		strings.Repeat("// trailing source line in Go file\n", 45)

	var msgs []agent.Message
	// Chunk 0 (messages 0..11):
	msgs = append(msgs, makeToolStep("s1", "read", map[string]any{"filePath": "internal/sub/handler.go"}, staleBodyWithRule, false)...)
	msgs = append(msgs, makeToolStep("s2", "edit", map[string]any{"filePath": "internal/sub/handler.go"}, "updated handler.go", false)...)
	msgs = append(msgs, makeToolStep("s3", "bash", map[string]any{"command": "go test ./..."}, bashFailOut, true)...)
	msgs = append(msgs, makeToolStep("s4", "bash", map[string]any{"command": "go test ./green"}, bashPassOut, false)...)
	msgs = append(msgs, makeToolStep("s5", "read", map[string]any{"filePath": "internal/sub/errors.go"}, goSourceWithErrLiteral, false)...)
	msgs = append(msgs, makeToolStep("s6", "grep", map[string]any{"pattern": "Handler"}, strings.Repeat("match line\n", 300), false)...)

	// Tail (6 messages = 3 steps) with weight to trigger pruning.
	bigTail := strings.Repeat("active window content line\n", 700)
	msgs = append(msgs, makeToolStep("t1", "read", map[string]any{"filePath": "main.go"}, bigTail, false)...)
	msgs = append(msgs, makeToolStep("t2", "read", map[string]any{"filePath": "main2.go"}, "ok", false)...)
	msgs = append(msgs, makeToolStep("t3", "read", map[string]any{"filePath": "main3.go"}, "ok", false)...)

	pruned := agent.PruneOldToolResults(msgs, "sys", contextLimit)

	// 1. Stale read folded its body AND preserved the full <subdirectory_instructions> block verbatim.
	gotRuleRead := pruned[1].Content[0].Text
	if !strings.Contains(gotRuleRead, "stale read of internal/sub/handler.go") {
		t.Fatalf("expected stale read notice, got: %q", gotRuleRead)
	}
	if !strings.Contains(gotRuleRead, subdirRule) {
		t.Fatalf("expected full <subdirectory_instructions> block preserved intact, got:\n%s", gotRuleRead)
	}

	// 2. Failed bash output preserved middle file:line compiler error and FAIL line.
	gotBashFail := pruned[5].Content[0].Text
	if !strings.Contains(gotBashFail, "internal/sub/handler.go:142:9: undefined: missingHelper") ||
		!strings.Contains(gotBashFail, "--- FAIL: TestSubHandler") {
		t.Fatalf("expected middle compiler/test diagnostics preserved in pruned bash error, got:\n%s", gotBashFail)
	}

	// 3. Clean passing bash log compacted tightly with accurate (bash) hint and no false diagnostics banner.
	gotBashPass := pruned[7].Content[0].Text
	if !strings.Contains(gotBashPass, "(bash)") || strings.Contains(gotBashPass, "key diagnostics preserved") {
		t.Fatalf("expected clean passing bash output to use compact bash summary, got:\n%s", gotBashPass)
	}
	if len(gotBashPass) > 700 {
		t.Fatalf("expected passing bash log to compact below 700 bytes, got %d bytes:\n%s", len(gotBashPass), gotBashPass)
	}

	// 4. Source code `read` with error string literals must NOT trigger false diagnostic extraction.
	gotSourceRead := pruned[9].Content[0].Text
	if strings.Contains(gotSourceRead, "key diagnostics preserved") || !strings.Contains(gotSourceRead, "use read with offset/limit") {
		t.Fatalf("expected source-code read to use standard read summary without false diagnostics, got:\n%s", gotSourceRead)
	}

	// 5. Non-read search tool (`grep`) gets tool-specific hint instead of misleading `use read with offset/limit`.
	gotGrep := pruned[11].Content[0].Text
	if !strings.Contains(gotGrep, "(grep)") || strings.Contains(gotGrep, "use read with offset/limit") {
		t.Fatalf("expected grep output to receive grep-specific prune header, got:\n%s", gotGrep)
	}
}

func TestUnifiedAbsoluteAndRelativeWorkspacePathsFoldAcrossReadEditPatchAndSiblingIsolation(t *testing.T) {
	const contextLimit = 8000
	const wsRoot = "/workspace/hoxi"

	moderateCode := strings.Repeat("10: func unifiedPathTarget() int { return 7 }\n", 16)

	var msgs []agent.Message
	// Chunk 0 (messages 0..11, 6 steps):
	// Step 0 (msgs 0..1): absolute read -> relative apply_patch at Step 1 -> MUST fold as stale with workspace-relative displayPath.
	msgs = append(msgs, makeToolStep("u0", "read", map[string]any{"filePath": "/workspace/hoxi/pkg/patched.go"}, moderateCode, false)...)
	patchRel := "*** Begin Patch\n*** Update File: pkg/patched.go\n@@\n-old\n+new\n*** End Patch"
	msgs = append(msgs, makeToolStep("u1", "apply_patch", map[string]any{"patch": patchRel}, "patched 1 file", false)...)

	// Step 2 (msgs 4..5): relative read -> absolute multiedit at Step 3 -> MUST fold as stale.
	msgs = append(msgs, makeToolStep("u2", "read", map[string]any{"filePath": "./internal/svc/handler.go"}, moderateCode, false)...)
	msgs = append(msgs, makeToolStep("u3", "multiedit", map[string]any{"filePath": "/workspace/hoxi/internal/svc/handler.go"}, "applied 2 edits", false)...)

	// Step 4 (msgs 8..9): absolute narrow read [15..45] -> relative covering read [1..120] at Step 5 -> MUST fold as duplicate.
	msgs = append(msgs, makeToolStep("u4", "read", map[string]any{"filePath": "/workspace/hoxi/pkg/cover.go", "offset": 15, "limit": 30}, moderateCode, false)...)
	msgs = append(msgs, makeToolStep("u5", "read", map[string]any{"filePath": "pkg/cover.go", "offset": 1, "limit": 120}, moderateCode, false)...)

	// Chunk 1 (messages 12..23, 6 steps):
	// Step 6 (msgs 12..13): sibling repo with colliding prefix (/workspace/hoxi-extra/pkg/patched.go) -> Step 7 edits workspace pkg/patched.go -> MUST NOT fold Step 6!
	msgs = append(msgs, makeToolStep("u6", "read", map[string]any{"filePath": "/workspace/hoxi-extra/pkg/patched.go"}, moderateCode, false)...)
	msgs = append(msgs, makeToolStep("u7", "edit", map[string]any{"filePath": "pkg/patched.go"}, "edited workspace file", false)...)

	// Step 8 (msgs 16..17): parent-traversing relative read re-entering workspace (../hoxi/cmd/main.go) -> Step 9 writes cmd/main.go -> MUST fold!
	msgs = append(msgs, makeToolStep("u8", "read", map[string]any{"filePath": "../hoxi/cmd/main.go"}, moderateCode, false)...)
	msgs = append(msgs, makeToolStep("u9", "write", map[string]any{"filePath": "cmd/main.go"}, "wrote cmd/main.go", false)...)

	// Step 10 (msgs 20..21): unified diff rename (--- a/pkg/old_name.go +++ b/pkg/new_name.go) invalidating absolute read of /workspace/hoxi/pkg/new_name.go.
	msgs = append(msgs, makeToolStep("u10", "read", map[string]any{"filePath": "/workspace/hoxi/pkg/new_name.go"}, moderateCode, false)...)
	unifiedMove := "--- a/pkg/old_name.go\n+++ b/pkg/new_name.go\n@@ -1 +1 @@\n-old\n+new"
	msgs = append(msgs, makeToolStep("u11", "apply_patch", map[string]any{"diff": unifiedMove}, "patched move", false)...)

	// Tail (6 messages = 3 steps) to push total tokens past 75% trigger.
	bigTail := strings.Repeat("active workspace context line\n", 700)
	msgs = append(msgs, makeToolStep("ut1", "read", map[string]any{"filePath": "active.go"}, bigTail, false)...)
	msgs = append(msgs, makeToolStep("ut2", "read", map[string]any{"filePath": "active2.go"}, "ok", false)...)
	msgs = append(msgs, makeToolStep("ut3", "read", map[string]any{"filePath": "active3.go"}, "ok", false)...)

	// 1. Test automatic extraction from <environment> system prompt (while ignoring spoofed <project_instructions> examples).
	sysWithEnv := "<environment>\nPlatform: darwin/arm64\nWorking directory: " + wsRoot + "\nShell (bash tool): /bin/sh -c (POSIX sh)\n</environment>\n\n" +
		"<project_instructions file=\"AGENTS.md\">\n<environment>\nPlatform: linux/amd64\nWorking directory: /spoofed/wrong/root\n</environment>\n</project_instructions>"
	prunedViaEnv := agent.PruneOldToolResults(msgs, sysWithEnv, contextLimit)

	if got := prunedViaEnv[1].Content[0].Text; !strings.Contains(got, "stale read of pkg/patched.go superseded by a later edit") {
		t.Fatalf("expected absolute read + relative apply_patch to fold with relative display path, got: %q", got)
	}
	if got := prunedViaEnv[5].Content[0].Text; !strings.Contains(got, "stale read of internal/svc/handler.go superseded by a later edit") {
		t.Fatalf("expected relative read + absolute multiedit to fold, got: %q", got)
	}
	if got := prunedViaEnv[9].Content[0].Text; !strings.Contains(got, "duplicate read of pkg/cover.go superseded by a later read") {
		t.Fatalf("expected absolute narrow read + relative covering read to fold, got: %q", got)
	}
	if got := prunedViaEnv[13].Content[0].Text; got != moderateCode {
		t.Fatalf("sibling repo /workspace/hoxi-extra must NEVER collide with /workspace/hoxi, got: %q", got)
	}
	if got := prunedViaEnv[17].Content[0].Text; !strings.Contains(got, "stale read of cmd/main.go superseded by a later edit") {
		t.Fatalf("expected ../hoxi/cmd/main.go re-entering workspace to fold after write, got: %q", got)
	}
	if got := prunedViaEnv[21].Content[0].Text; !strings.Contains(got, "stale read of pkg/new_name.go superseded by a later edit") {
		t.Fatalf("expected move target in unified diff to invalidate earlier absolute read, got: %q", got)
	}

	// 2. Test explicit PruneOldToolResultsInWorkspace entrypoint produces identical output even with a bare system prompt.
	prunedExplicit := agent.PruneOldToolResultsInWorkspace(msgs, "sys", wsRoot, contextLimit)
	bEnv, _ := json.Marshal(prunedViaEnv)
	bExp, _ := json.Marshal(prunedExplicit)
	if string(bEnv) != string(bExp) {
		t.Fatalf("expected <environment> extraction and explicit worktreeDir to produce identical pruned messages")
	}
}

func TestPruneStaleToolScreenshotsPreservesUserMediaAndKVCacheHysteresis(t *testing.T) {
	makeScreenshotStep := func(id, toolName, filename, data string) []agent.Message {
		return []agent.Message{
			{
				Role: agent.RoleAssistant,
				Content: []agent.ContentBlock{{
					Type:  agent.ContentToolUse,
					ID:    id,
					Name:  toolName,
					Input: json.RawMessage(`{"action":"screenshot"}`),
				}},
			},
			{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{
					{Type: agent.ContentToolResult, ToolUseID: id, Text: `{"app":{"name":"Gemini"},"windowID":"w1","observationID":"obs_` + id + `","elements":[{"id":1}],"waitedMs":5000}`},
					{Type: agent.ContentMedia, MimeType: "image/jpeg", Data: data, Filename: filename},
				},
			},
		}
	}

	// User-uploaded image + MCP image must NEVER be pruned by screenshot windowing.
	var msgs []agent.Message
	msgs = append(msgs, agent.Message{
		Role: agent.RoleUser,
		Content: []agent.ContentBlock{
			{Type: agent.ContentText, Text: "Control Gemini using this reference"},
			{Type: agent.ContentMedia, MimeType: "image/png", Data: "USER_IMG", Filename: "ref.png"},
		},
	})
	msgs = append(msgs, makeScreenshotStep("mcp_1", "mcp_call_tool", "diagram.png", "MCP_IMG")...)

	// Up to 4 live hoxi_computer screenshots must stay untouched (KV-cache hysteresis).
	for i := 1; i <= 4; i++ {
		msgs = append(msgs, makeScreenshotStep(fmt.Sprintf("c%d", i), "hoxi_computer", "hoxi_computer.jpg", fmt.Sprintf("FRAME_%d", i))...)
	}
	atFour := agent.PruneOldToolResults(msgs, "sys", 200000)
	bOrig, _ := json.Marshal(msgs)
	bFour, _ := json.Marshal(atFour)
	if string(bOrig) != string(bFour) {
		t.Fatal("expected <= 4 live screenshots to stay byte-identical for KV-cache stability")
	}

	// Adding a 5th screenshot plus a mixed-tool step (hoxi_computer + mcp_call_tool) must NOT drop MCP media.
	mixedStep := []agent.Message{
		{
			Role: agent.RoleAssistant,
			Content: []agent.ContentBlock{
				{Type: agent.ContentToolUse, ID: "c5", Name: "hoxi_computer", Input: json.RawMessage(`{"action":"screenshot"}`)},
				{Type: agent.ContentToolUse, ID: "mcp_mix", Name: "mcp_call_tool", Input: json.RawMessage(`{"tool":"plot"}`)},
			},
		},
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				{Type: agent.ContentToolResult, ToolUseID: "c5", Text: `{"windowID":"w1"}`},
				{Type: agent.ContentToolResult, ToolUseID: "mcp_mix", Text: `{"ok":true}`},
				{Type: agent.ContentMedia, MimeType: "image/jpeg", Data: "FRAME_5", Filename: "hoxi_computer.jpg"},
				{Type: agent.ContentMedia, MimeType: "image/png", Data: "MCP_MIX_IMG", Filename: "chart.png"},
			},
		},
	}
	msgs5 := append(append([]agent.Message(nil), msgs...), mixedStep...)
	pruned5 := agent.PruneOldToolResults(msgs5, "sys", 200000)

	var liveComputerFrames []string
	var omittedCount int
	var hasUserImg, hasMCPImg, hasMixedMCPImg bool
	for _, m := range pruned5 {
		for _, b := range m.Content {
			if b.Type == agent.ContentMedia {
				switch b.Data {
				case "USER_IMG":
					hasUserImg = true
				case "MCP_IMG":
					hasMCPImg = true
				case "MCP_MIX_IMG":
					hasMixedMCPImg = true
				default:
					liveComputerFrames = append(liveComputerFrames, b.Data)
				}
			}
			if b.Type == agent.ContentText && strings.Contains(b.Text, "[omitted earlier screenshot: hoxi_computer.jpg (image/jpeg)]") {
				omittedCount++
			}
		}
	}
	if !hasUserImg || !hasMCPImg || !hasMixedMCPImg {
		t.Fatalf("user or MCP media unexpectedly pruned: user=%v mcp=%v mixedMcp=%v", hasUserImg, hasMCPImg, hasMixedMCPImg)
	}
	if omittedCount != 3 || strings.Join(liveComputerFrames, ",") != "FRAME_4,FRAME_5" {
		t.Fatalf("expected 3 omitted and latest 2 [FRAME_4,FRAME_5] kept, got omitted=%d live=%v", omittedCount, liveComputerFrames)
	}

	// Strictly idempotent on repeated calls.
	prunedAgain := agent.PruneOldToolResults(pruned5, "sys", 200000)
	b1, _ := json.Marshal(pruned5)
	b2, _ := json.Marshal(prunedAgain)
	if string(b1) != string(b2) {
		t.Fatal("screenshot pruning must be strictly idempotent")
	}

	// Large hoxi_computer JSON tool output pruned under context pressure gets structured summary.
	bigElements := make([]map[string]any, 40)
	for i := range bigElements {
		bigElements[i] = map[string]any{"id": i + 1, "role": "AXButton", "name": strings.Repeat("AllowAction", 8)}
	}
	bigJSON, _ := json.Marshal(map[string]any{
		"app":           map[string]any{"name": "Gemini"},
		"windowID":      "win-9",
		"observationID": "obs_big",
		"waitedMs":      10000,
		"elements":      bigElements,
	})
	var tightMsgs []agent.Message
	tightMsgs = append(tightMsgs, makeToolStep("hc1", "hoxi_computer", map[string]any{"action": "wait", "seconds": 10}, string(bigJSON), false)...)
	for i := 0; i < 6; i++ {
		tightMsgs = append(tightMsgs, makeToolStep(fmt.Sprintf("r%d", i), "read", map[string]any{"filePath": "a.go"}, strings.Repeat("line of code\n", 140), false)...)
	}
	prunedTight := agent.PruneOldToolResults(tightMsgs, "sys", 4000)
	gotSummary := prunedTight[1].Content[0].Text
	if !strings.Contains(gotSummary, "(hoxi_computer); app=Gemini window=win-9 observation=obs_big elements=40 waitedMs=10000") {
		t.Fatalf("expected structured hoxi_computer prune summary, got: %q", gotSummary)
	}
}

func TestCompactBudgetUTF8NoDoubleCountAndHysteresisHeadroom(t *testing.T) {
	// 1. Multibyte CJK text (10,000 runes = 30,000 UTF-8 bytes) should estimate ~10K tokens,
	// not ~13.3K via non-ASCII double-counting, so a 15K context window (75% trigger = 11,250)
	// does NOT prematurely prune older reads.
	cjkSystem := strings.Repeat("中", 10000) // 30,000 UTF-8 bytes
	var cjkMsgs []agent.Message
	readBody := strings.Repeat("1: func keepMe() {}\n", 150) // ~3000 bytes (~1000 est tokens)
	cjkMsgs = append(cjkMsgs, makeToolStep("cjk_0", "read", map[string]any{"filePath": "a.go"}, readBody, false)...)
	for i := 1; i < 8; i++ {
		cjkMsgs = append(cjkMsgs, makeToolStep(fmt.Sprintf("cjk_%d", i), "read", map[string]any{"filePath": "b.go"}, "ok", false)...)
	}
	notPruned := agent.PruneOldToolResults(cjkMsgs, cjkSystem, 15000)
	if notPruned[1].Content[0].Text != readBody {
		t.Fatalf("CJK system prompt should not double-count UTF-8 bytes and prematurely prune at 15K limit, got: %q", notPruned[1].Content[0].Text)
	}

	// 2. Hysteresis headroom: with contextLimit=40000 (trigger=30000, 85% target=25500, keepRecent=14),
	// build 38 messages (rawCutoff=24, quantizedCutoff=24 = 2 chunks of 12 messages).
	// Chunk 0 has ~3500 tokens of prunable tool output; Chunk 1 has ~12000 tokens; recent tail has ~16000 tokens.
	// Total = ~31500 (> 30000 trigger).
	// Pruning Chunk 0 alone would leave ~28500 (still > 25500 target), so hysteresis must advance
	// through Chunk 1 in the SAME compaction pass instead of stopping barely under 30000 and
	// busting KV-cache again on the very next step.
	var hystMsgs []agent.Message
	chunk0Out := strings.Repeat("chunk zero verbose output line\n", 140) // ~4340 bytes (~1446 tok) x 3 = ~4340 tok
	for i := 0; i < 6; i++ {
		body := "short"
		if i < 3 {
			body = chunk0Out
		}
		hystMsgs = append(hystMsgs, makeToolStep(fmt.Sprintf("h0_%d", i), "read", map[string]any{"filePath": fmt.Sprintf("f0_%d.go", i)}, body, false)...)
	}
	chunk1Out := strings.Repeat("chunk one verbose output line\n", 260) // ~7800 bytes (~2600 tok) x 5 = ~13000 tok
	for i := 0; i < 6; i++ {
		body := "short"
		if i < 5 {
			body = chunk1Out
		}
		hystMsgs = append(hystMsgs, makeToolStep(fmt.Sprintf("h1_%d", i), "read", map[string]any{"filePath": fmt.Sprintf("f1_%d.go", i)}, body, false)...)
	}
	tailOut := strings.Repeat("recent active tail context line\n", 240) // ~7680 bytes (~2560 tok) x 6 = ~15360 tok
	for i := 0; i < 7; i++ {
		body := "short"
		if i < 6 {
			body = tailOut
		}
		hystMsgs = append(hystMsgs, makeToolStep(fmt.Sprintf("ht_%d", i), "read", map[string]any{"filePath": fmt.Sprintf("ft_%d.go", i)}, body, false)...)
	}

	prunedHyst := agent.PruneOldToolResults(hystMsgs, "sys", 40000)
	if !strings.HasPrefix(prunedHyst[1].Content[0].Text, "[tool output pruned:") {
		t.Fatalf("expected Chunk 0 tool output to be pruned")
	}
	if !strings.HasPrefix(prunedHyst[13].Content[0].Text, "[tool output pruned:") {
		t.Fatalf("expected hysteresis target (85%% of trigger) to prune Chunk 1 in the same pass for KV-cache headroom, got len=%d", len(prunedHyst[13].Content[0].Text))
	}
	if prunedHyst[25].Content[0].Text != tailOut {
		t.Fatalf("expected recent tail message [25] to stay unpruned")
	}

	// Strictly idempotent after hysteresis compaction.
	prunedHystAgain := agent.PruneOldToolResults(prunedHyst, "sys", 40000)
	b1, _ := json.Marshal(prunedHyst)
	b2, _ := json.Marshal(prunedHystAgain)
	if string(b1) != string(b2) {
		t.Fatal("hysteresis pruning must remain strictly idempotent")
	}
}
