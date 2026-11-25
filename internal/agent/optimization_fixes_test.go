package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestCanonicalToolJSONMatchesReconstructedHistoryAcrossTurns(t *testing.T) {
	// Stream unsorted keys + extra spaces; Turn 1 step 2 must send sorted compact JSON
	// identical to json.Marshal(map[string]any{...}) reconstructed on Turn 2.
	rawStreamArgs := `{"offset": 10, "filePath": "demo.go", "limit": 50}`
	wantCanonical, _ := json.Marshal(map[string]any{
		"filePath": "demo.go",
		"limit":    50,
		"offset":   10,
	})

	cleaned, keep, err := validateCompletedToolJSON(rawStreamArgs, false)
	if err != nil || !keep {
		t.Fatalf("expected valid canonical JSON, got keep=%v err=%v", keep, err)
	}
	if string(cleaned) != string(wantCanonical) {
		t.Fatalf("canonical mismatch:\ngot:  %s\nwant: %s", string(cleaned), string(wantCanonical))
	}

	// Trailing tokens after first JSON object must be rejected (or dropped if truncated).
	if _, keepBad, errBad := validateCompletedToolJSON(`{"a":1}{"b":2}`, false); keepBad || errBad == nil {
		t.Fatalf("expected trailing JSON garbage to error, got keep=%v err=%v", keepBad, errBad)
	}
	if _, keepTrunc, errTrunc := validateCompletedToolJSON(`{"a":1`, true); keepTrunc || errTrunc != nil {
		t.Fatalf("expected truncated JSON to drop cleanly, got keep=%v err=%v", keepTrunc, errTrunc)
	}

	// End-to-end check on OpenAI Chat driver: step 2 replayed tool_calls arguments match Turn 2 history.
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "demo.go"), []byte("package demo\nfunc F() {}\n"), 0o644)
	var step2Args string
	step := 0
	client := &fakeStreamClient{
		target: llm.StreamTarget{
			ProviderID:   "openai",
			ModelID:      "gpt-5",
			BaseURL:      "https://api.openai.com/v1",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 128000,
			OutputLimit:  4096,
		},
		openFn: func(_ context.Context, req llm.StreamHTTPRequest) (string, error) {
			step++
			if step == 1 {
				quoted, _ := json.Marshal(rawStreamArgs)
				return fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":%s}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n", string(quoted)), nil
			}
			var body struct {
				Messages []struct {
					Role      string `json:"role"`
					ToolCalls []struct {
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(req.Body, &body); err != nil {
				return "", err
			}
			for _, m := range body.Messages {
				if m.Role == "assistant" && len(m.ToolCalls) > 0 {
					step2Args = m.ToolCalls[0].Function.Arguments
				}
			}
			return `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n", nil
		},
	}
	if err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_canon_json",
		WorktreeDir: ws,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Read demo.go",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if step2Args != string(wantCanonical) {
		t.Fatalf("step 2 replayed arguments mismatch:\ngot:  %s\nwant: %s", step2Args, string(wantCanonical))
	}
}

func TestAnthropicSubagentStaticSystemSplitAndFourBreakpointCap(t *testing.T) {
	drv := newAnthropicDriver()
	tools := []ToolSpec{{Name: "read", Description: "Read file", Parameters: map[string]any{"type": "object"}}}

	// Build a long conversation with 5 user messages (including a 15-tool batch > 20 blocks).
	var parallelUses []ContentBlock
	var parallelResults []ContentBlock
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("tu_%d", i)
		parallelUses = append(parallelUses, ContentBlock{Type: ContentToolUse, ID: id, Name: "read", Input: json.RawMessage(`{"filePath":"a.go"}`)})
		parallelResults = append(parallelResults, ContentBlock{Type: ContentToolResult, ToolUseID: id, Text: "ok"})
	}
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "turn 1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "ack 1"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "turn 2"}}},
		{Role: RoleAssistant, Content: parallelUses},
		{Role: RoleUser, Content: parallelResults},
		{Role: RoleAssistant, Content: parallelUses},
		{Role: RoleUser, Content: parallelResults},
	}

	reqA, err := drv.BuildRequest(ModelRequest{
		SessionID:    "ses_anth_split",
		Target:       llm.StreamTarget{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", Protocol: llm.ProtocolAnthropic},
		System:       "Static shared system prompt",
		SystemSuffix: "Subagent role: explore (trace auth).",
		Messages:     msgs,
		Tools:        tools,
		MaxTokens:    4096,
	})
	if err != nil {
		t.Fatalf("BuildRequest A failed: %v", err)
	}
	reqB, err := drv.BuildRequest(ModelRequest{
		SessionID:    "ses_anth_split",
		Target:       llm.StreamTarget{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", Protocol: llm.ProtocolAnthropic},
		System:       "Static shared system prompt",
		SystemSuffix: "Subagent role: explore (trace billing).",
		Messages:     msgs,
		Tools:        tools,
		MaxTokens:    4096,
	})
	if err != nil {
		t.Fatalf("BuildRequest B failed: %v", err)
	}

	var payA, payB struct {
		System   []map[string]any `json:"system"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqA.Body, &payA); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reqB.Body, &payB); err != nil {
		t.Fatal(err)
	}
	if len(payA.System) != 2 || len(payB.System) != 2 {
		t.Fatalf("expected 2 Anthropic system blocks (static + suffix), got A=%d B=%d", len(payA.System), len(payB.System))
	}
	sysA0, _ := json.Marshal(payA.System[0])
	sysB0, _ := json.Marshal(payB.System[0])
	if string(sysA0) != string(sysB0) {
		t.Fatalf("expected identical cached system[0] across sibling subagents:\nA=%s\nB=%s", sysA0, sysB0)
	}
	if payA.System[0]["cache_control"] == nil || payA.System[1]["cache_control"] != nil {
		t.Fatalf("expected cache_control ONLY on system[0], got %+v", payA.System)
	}
	if totalBP := strings.Count(string(reqA.Body), `"cache_control"`); totalBP != 4 {
		t.Fatalf("expected exactly 4 cache_control breakpoints (hard limit), got %d", totalBP)
	}
	// Verify breakpoints sit on the penultimate user msg (index 4) and final user msg (index 6).
	lastOf := func(msgIdx int) map[string]any {
		c := payA.Messages[msgIdx].Content
		return c[len(c)-1]
	}
	if lastOf(0)["cache_control"] != nil || lastOf(2)["cache_control"] != nil {
		t.Fatal("earlier user messages beyond penultimate should not carry cache_control")
	}
	if lastOf(4)["cache_control"] == nil || lastOf(6)["cache_control"] == nil {
		t.Fatal("expected cache_control on both penultimate and final user messages")
	}

	// Verify subtask cache keys share per subagent_type while isolating across types and parent.
	parent := TurnRequest{SessionID: "ses_cache_roles"}
	kExplore1 := buildSubtaskCacheKey(parent, SubtaskRequest{ParentCallID: "c1"}, "explore")
	kExplore2 := buildSubtaskCacheKey(parent, SubtaskRequest{ParentCallID: "c2"}, "explore")
	kScout := buildSubtaskCacheKey(parent, SubtaskRequest{ParentCallID: "c3"}, "scout")
	kGeneral := buildSubtaskCacheKey(parent, SubtaskRequest{ParentCallID: "c4"}, "general")
	if kExplore1 != kExplore2 {
		t.Fatalf("same-type siblings should share cache key: %q vs %q", kExplore1, kExplore2)
	}
	if kExplore1 == parent.SessionID || kExplore1 == kScout || kExplore1 == kGeneral || kScout == kGeneral {
		t.Fatalf("distinct subagent types and parent must stay isolated: explore=%q scout=%q general=%q", kExplore1, kScout, kGeneral)
	}
}

func TestPlanToBuildPrefixAndSlimSubagentPromptAndCatalog(t *testing.T) {
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("Keep strict architecture layering."), 0o644)

	planSys := buildSystemPrompt(ws, "Caller sys", "Patch", "plan", 0)
	buildSys := buildSystemPrompt(ws, "Caller sys", "Patch", "build", 0)
	if !strings.HasPrefix(planSys, buildSys) {
		t.Fatalf("expected Plan system prompt to share exact Build prefix with <plan_mode_protocol> at tail:\nplan=%q\nbuild=%q", planSys, buildSys)
	}
	if !strings.HasSuffix(planSys, "</plan_mode_protocol>") {
		t.Fatalf("expected <plan_mode_protocol> at tail of Plan prompt, got:\n%s", planSys)
	}
	if !strings.Contains(buildSys, "Prefer direct tools in the main turn for simple or localized tasks") ||
		strings.Contains(buildSys, "and read-only task calls such as explore/scout") {
		t.Fatalf("expected direct-first anti-delegation guidance without routine explore/scout batching:\n%s", buildSys)
	}

	roSubSys := buildSystemPrompt(ws, "", "", "plan", 1)
	if !strings.Contains(roSubSys, "<native_agent_guidelines>") {
		t.Fatal("read-only subagent prompt must retain <native_agent_guidelines>")
	}
	if strings.Contains(roSubSys, "apply_patch") || strings.Contains(roSubSys, "run_in_background") || strings.Contains(roSubSys, "hoxi_computer") {
		t.Fatalf("read-only subagent prompt should omit write/bash/computer boilerplate:\n%s", roSubSys)
	}
	writeSubSys := buildSystemPrompt(ws, "", "", "build", 1)
	if !strings.Contains(writeSubSys, "apply_patch") || strings.Contains(writeSubSys, "todowrite") || strings.Contains(writeSubSys, "do NOT call task") {
		t.Fatalf("write subagent prompt should keep edit guidance and omit parent-only task/todo rules:\n%s", writeSubSys)
	}

	stripped := stripSkillCatalogSection("Keep rule\nOn-demand skills, listed by name only: alpha, beta.\nTail rule")
	if strings.Contains(stripped, "On-demand skills") || !strings.Contains(stripped, "Keep rule") || !strings.Contains(stripped, "Tail rule") {
		t.Fatalf("unexpected stripSkillCatalogSection result: %q", stripped)
	}

	reg := newToolRegistry()
	buildSpecs := reg.SpecsForTurn("build", 0)
	planSpecs := reg.SpecsForTurn("plan", 0)
	subPlanSpecs := reg.SpecsForTurn("plan", 1)
	if hasToolSpec(buildSpecs, "plan_consent") {
		t.Fatal("build mode must omit plan_consent from tool schema")
	}
	if !hasToolSpec(planSpecs, "plan_consent") {
		t.Fatal("plan mode must expose plan_consent")
	}
	for _, forbidden := range []string{"plan_consent", "todoread", "todowrite", "skill", "task", "question", "goal_report"} {
		if hasToolSpec(subPlanSpecs, forbidden) {
			t.Fatalf("read-only subagent must omit %s from tool schema", forbidden)
		}
	}

	// On-demand host tool loading defers hoxi_data and hoxi_tools, omits MCP when unconfigured,
	// and delivers browser/computer safety notices when loaded via load_host_tool.
	cat := newHostToolCatalog(nil, nil)
	cat.configureMCP(ws, nil, nil, nil)
	defaultBuild := cat.specs(reg, "build", 0)
	for _, deferred := range []string{"hoxi_browser", "hoxi_computer", "hoxi_data", "hoxi_tools", "hoxi_image", "hoxi_video", "hoxi_speech", "mcp_list_tools", "mcp_call_tool"} {
		if hasToolSpec(defaultBuild, deferred) {
			t.Fatalf("expected %s to be omitted/deferred from default build catalog", deferred)
		}
	}
	catWithMCP := newHostToolCatalog(nil, nil)
	catWithMCP.configureMCP(ws, nil, func() map[string]any {
		return map[string]any{"mcp": map[string]any{"docs": map[string]any{"type": "remote", "url": "https://mcp.example.com"}}}
	}, nil)
	if !hasToolSpec(catWithMCP.specs(reg, "build", 0), "mcp_list_tools") {
		t.Fatal("expected mcp_list_tools to be exposed when MCP server is configured")
	}

	loadTool := &loadHostTool{}
	for _, name := range []string{"hoxi_browser", "hoxi_computer"} {
		out, err := loadTool.Execute(context.Background(), ToolContext{Mode: "build"}, json.RawMessage(`{"name":"`+name+`"}`))
		if err != nil || !strings.Contains(out.Output, "<hoxi_tool_safety>") {
			t.Fatalf("expected load_host_tool(%s) to deliver <hoxi_tool_safety>, got err=%v out=%q", name, err, out.Output)
		}
	}
}

func TestTruncatedToolCallWithVisibleProseTriggersRecovery(t *testing.T) {
	cases := []struct {
		name  string
		proto llm.StreamProtocol
		step1 string
		step2 string
	}{
		{
			name:  "anthropic_max_tokens_with_prose",
			proto: llm.ProtocolAnthropic,
			step1: strings.Join([]string{
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Updating file now:"}}`,
				`data: {"type":"content_block_stop","index":0}`,
				`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_cut","name":"write","input":{}}}`,
				`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"filePath\":\"a.go\",\"content\":\"half..."}}`,
				`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":100}}`,
				`data: {"type":"message_stop"}`,
				"",
			}, "\n"),
			step2: strings.Join([]string{
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Recovered cleanly."}}`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`,
				`data: {"type":"message_stop"}`,
				"",
			}, "\n"),
		},
		{
			name:  "openai_chat_length_with_prose",
			proto: llm.ProtocolOpenAI,
			step1: strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"Applying patch:","tool_calls":[{"index":0,"id":"c_cut","function":{"name":"write","arguments":"{\"filePath\":\"a.go\",\"content\":\"unterminated"}}]},"finish_reason":"length"}]}`,
				`data: [DONE]`,
				"",
			}, "\n"),
			step2: strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"Recovered cleanly."},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				"",
			}, "\n"),
		},
		{
			name:  "openai_responses_incomplete_with_prose",
			proto: llm.ProtocolOpenAIResponses,
			step1: strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"Writing module:"}`,
				`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_cut","name":"write"}}`,
				`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"filePath\":\"a.go\""}`,
				`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`,
				"",
			}, "\n"),
			step2: strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"Recovered cleanly."}`,
				`data: {"type":"response.completed","response":{}}`,
				"",
			}, "\n"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &fakeStreamClient{
				target: llm.StreamTarget{
					ProviderID:   "test",
					ModelID:      "test-model",
					Protocol:     tc.proto,
					HasEndpoint:  true,
					ContextLimit: 128000,
					OutputLimit:  4096,
				},
				openFn: func(_ context.Context, _ llm.StreamHTTPRequest) (string, error) {
					calls++
					if calls == 1 {
						return tc.step1, nil
					}
					return tc.step2, nil
				},
			}
			var finalFinish string
			var textBuf strings.Builder
			err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
				SessionID:   "ses_trunc_" + tc.name,
				WorktreeDir: t.TempDir(),
				Mode:        "build",
				ProviderID:  "test",
				ModelID:     "test-model",
				UserPrompt:  "Update a.go",
				Sink: EventSinkFunc(func(evt EngineEvent) {
					if evt.Kind == EngineTextDelta {
						textBuf.WriteString(evt.Text)
					}
					if evt.Kind == EngineTurnFinish {
						finalFinish = evt.Finish
					}
				}),
			})
			if err != nil {
				t.Fatalf("RunTurn failed: %v", err)
			}
			if calls != 2 || finalFinish != "stop" || !strings.Contains(textBuf.String(), "Recovered cleanly.") {
				t.Fatalf("expected 2-step recovery after truncated tool call with prose; calls=%d finish=%q text=%q", calls, finalFinish, textBuf.String())
			}
		})
	}
}

func TestApplyPatchDeduplicatesProjectScopedDiagnostics(t *testing.T) {
	ws := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module example.com/test\n\ngo 1.24\n")
	mustWrite("pkg/a.go", "package pkg\nconst A = 1\n")
	mustWrite("pkg/b.go", "package pkg\nconst B = 1\n")
	mustWrite("other/c.go", "package other\nconst C = 1\n")
	mustWrite("web/tsconfig.json", "{}\n")
	mustWrite("web/src/x.ts", "export const x = 1\n")
	mustWrite("web/src/y.ts", "export const y = 1\n")
	mustWrite("rust/Cargo.toml", "[package]\nname = \"demo\"\n")
	mustWrite("rust/src/a.rs", "pub fn a() {}\n")
	mustWrite("rust/src/b.rs", "pub fn b() {}\n")
	mustWrite("scripts/p1.py", "x = 1\n")
	mustWrite("scripts/p2.py", "y = 1\n")

	patchText := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: pkg/a.go",
		"@@",
		"-const A = 1",
		"+const A = 2",
		"*** Update File: pkg/b.go",
		"@@",
		"-const B = 1",
		"+const B = 2",
		"*** Update File: other/c.go",
		"@@",
		"-const C = 1",
		"+const C = 2",
		"*** Update File: web/src/x.ts",
		"@@",
		"-export const x = 1",
		"+export const x = 2",
		"*** Update File: web/src/y.ts",
		"@@",
		"-export const y = 1",
		"+export const y = 2",
		"*** Update File: rust/src/a.rs",
		"@@",
		"-pub fn a() {}",
		"+pub fn a() { let _ = 1; }",
		"*** Update File: rust/src/b.rs",
		"@@",
		"-pub fn b() {}",
		"+pub fn b() { let _ = 2; }",
		"*** Update File: scripts/p1.py",
		"@@",
		"-x = 1",
		"+x = 2",
		"*** Update File: scripts/p2.py",
		"@@",
		"-y = 1",
		"+y = 2",
		"*** End Patch",
	}, "\n")

	var diagCalls []string
	args, _ := json.Marshal(map[string]any{"patch": patchText})
	out, err := (&applyPatchTool{}).Execute(context.Background(), ToolContext{
		WorktreeDir:   ws,
		FileRevisions: NewFileRevisionTracker(),
		Diagnostics: func(_ context.Context, _, target string) string {
			rel, _ := filepath.Rel(ws, target)
			diagCalls = append(diagCalls, filepath.ToSlash(rel))
			return "ok: " + filepath.ToSlash(rel)
		},
	}, args)
	if err != nil || out.IsError {
		t.Fatalf("apply_patch failed: err=%v out=%+v", err, out)
	}
	// Expect 6 deduplicated calls (pkg/a.go, other/c.go, web/src/x.ts, rust/src/a.rs, scripts/p1.py, scripts/p2.py) instead of 9.
	if len(diagCalls) != 6 {
		t.Fatalf("expected 6 deduplicated diagnostic calls, got %d: %v", len(diagCalls), diagCalls)
	}
}

func TestOpenAIChatOmitsPriorTurnReasoningContent(t *testing.T) {
	drv := newOpenAIDriver(false)
	httpReq, err := drv.BuildRequest(ModelRequest{
		Target: llm.StreamTarget{
			ProviderID: "deepseek",
			ModelID:    "deepseek-reasoner",
			BaseURL:    "https://api.deepseek.com/v1",
			Protocol:   llm.ProtocolOpenAI,
		},
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Turn 1 prompt"}}},
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: ContentThinking, Text: "Huge old reasoning from turn 1"},
				{Type: ContentText, Text: "Turn 1 answer"},
			}},
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Turn 2 prompt"}}},
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: ContentThinking, Text: "Active turn reasoning before tool"},
				{Type: ContentToolUse, ID: "c1", Name: "read", Input: json.RawMessage(`{"filePath":"a.go"}`)},
			}},
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "c1", Text: "1: ok"}}},
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(httpReq.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, hasOld := payload.Messages[1]["reasoning_content"]; hasOld {
		t.Fatalf("completed prior turn assistant message must omit reasoning_content: %+v", payload.Messages[1])
	}
	if got, _ := payload.Messages[3]["reasoning_content"].(string); got != "Active turn reasoning before tool" {
		t.Fatalf("active turn assistant tool-loop message must retain reasoning_content, got: %+v", payload.Messages[3])
	}
}

func TestCompactBudgetPrunesOversizedToolInputsAndRealisticMediaWeight(t *testing.T) {
	// Verify realistic media & signature weights in estimateTokens.
	baseTok := estimateTokens([]Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "hi"}}}}, "")
	mediaTok := estimateTokens([]Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentMedia, MimeType: "image/png", Data: "AAAA"}}}}, "")
	if mediaTok-baseTok < 1200 {
		t.Fatalf("expected realistic media token weight >= 1200, got delta=%d", mediaTok-baseTok)
	}

	// Verify oversized write/apply_patch inputs in old quantized chunk are pruned to valid JSON
	// while still allowing classifySupersededRead to detect the edit and prune stale reads!
	bigBody := strings.Repeat("func BigGeneratedLine() {}\n", 200) // ~5400 bytes
	writeInput, _ := json.Marshal(map[string]any{"filePath": "big.go", "content": bigBody})
	var msgs []Message
	msgs = append(msgs,
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "r1", Name: "read", Input: json.RawMessage(`{"filePath":"big.go"}`)}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "r1", Text: "1: package big\n" + strings.Repeat("2: old line content here\n", 30)}}},
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "w1", Name: "write", Input: writeInput}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "w1", Text: "wrote big.go"}}},
	)
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: fmt.Sprintf("step %d", i)}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: strings.Repeat("padding text ", 65)}}},
		)
	}
	pruned := PruneOldToolResults(msgs, "sys", 5000)
	prunedWriteIn := string(pruned[2].Content[0].Input)
	if !json.Valid([]byte(prunedWriteIn)) || !strings.Contains(prunedWriteIn, `"filePath":"big.go"`) || !strings.Contains(prunedWriteIn, `"_prunedInput"`) {
		t.Fatalf("expected oversized old write input to be compacted to valid JSON preserving filePath, got: %s", prunedWriteIn)
	}
	if !strings.Contains(pruned[1].Content[0].Text, "stale read of big.go superseded by a later edit") {
		t.Fatalf("expected superseded read detection to remain intact when write input is pruned, got: %q", pruned[1].Content[0].Text)
	}
}

func TestGrepModesContextCaseInsensitiveAndGlobMtimePaging(t *testing.T) {
	ws := t.TempDir()
	mustWrite := func(rel, body string, mtime time.Time) {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(full, mtime, mtime)
	}
	now := time.Now()
	mustWrite("a.go", "line1\nHelloWorld\nline3\nline4\nhelloworld\nline6\n", now.Add(-2*time.Hour))
	mustWrite("b.go", "intro\nHELLOWORLD\noutro\n", now.Add(-1*time.Hour))
	mustWrite("newest.go", "nothing here\n", now)

	g := &grepTool{}
	// 1. files_with_matches + caseInsensitive
	outFiles, err := g.Execute(context.Background(), ToolContext{WorktreeDir: ws}, json.RawMessage(`{"pattern":"helloworld","mode":"files_with_matches","caseInsensitive":true}`))
	if err != nil || outFiles.IsError {
		t.Fatalf("grep files_with_matches failed: err=%v out=%+v", err, outFiles)
	}
	if strings.TrimSpace(outFiles.Output) != "a.go\nb.go" {
		t.Fatalf("unexpected files_with_matches output: %q", outFiles.Output)
	}

	// 2. count + caseInsensitive + offset
	outCount, err := g.Execute(context.Background(), ToolContext{WorktreeDir: ws}, json.RawMessage(`{"pattern":"helloworld","mode":"count","caseInsensitive":true}`))
	if err != nil || strings.TrimSpace(outCount.Output) != "a.go:2\nb.go:1" {
		t.Fatalf("unexpected count output: err=%v out=%q", err, outCount.Output)
	}
	outCountOff, err := g.Execute(context.Background(), ToolContext{WorktreeDir: ws}, json.RawMessage(`{"pattern":"helloworld","mode":"count","caseInsensitive":true,"offset":1}`))
	if err != nil || strings.TrimSpace(outCountOff.Output) != "b.go:1" {
		t.Fatalf("unexpected offset count output: err=%v out=%q", err, outCountOff.Output)
	}

	// 3. content + contextLines=1 + caseInsensitive
	outCtx, err := g.Execute(context.Background(), ToolContext{WorktreeDir: ws}, json.RawMessage(`{"pattern":"helloworld","path":"a.go","contextLines":1,"caseInsensitive":true}`))
	if err != nil || !strings.Contains(outCtx.Output, "a.go-1- line1") || !strings.Contains(outCtx.Output, "a.go:2: HelloWorld") || !strings.Contains(outCtx.Output, "a.go-3- line3") || !strings.Contains(outCtx.Output, "a.go:5: helloworld") {
		t.Fatalf("unexpected contextLines grep output:\n%s", outCtx.Output)
	}

	// 4. glob sorts by mtime descending and supports offset
	gl := &globTool{}
	glOut, err := gl.Execute(context.Background(), ToolContext{WorktreeDir: ws}, json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil || glOut.IsError {
		t.Fatalf("glob failed: err=%v out=%+v", err, glOut)
	}
	lines := strings.Split(strings.TrimSpace(glOut.Output), "\n")
	if len(lines) != 3 || lines[0] != "newest.go" || lines[1] != "b.go" || lines[2] != "a.go" {
		t.Fatalf("expected glob sorted by mtime desc [newest.go b.go a.go], got %v", lines)
	}
	glOff, err := gl.Execute(context.Background(), ToolContext{WorktreeDir: ws}, json.RawMessage(`{"pattern":"*.go","offset":1}`))
	if err != nil || strings.TrimSpace(glOff.Output) != "b.go\na.go" {
		t.Fatalf("unexpected offset glob output: %q", glOff.Output)
	}
}

func TestReadDedupHandlesTruncatedLinesAndPriorCodeIndexInspect(t *testing.T) {
	ws := t.TempDir()
	// 1. Long >2KB line: re-read deduplicates while full-file write remains blocked.
	longLine := "const data = \"" + strings.Repeat("x", 3000) + "\";"
	body := "package asset\n" + longLine + "\nfunc End() {}\n"
	fullPath := filepath.Join(ws, "asset.go")
	_ = os.WriteFile(fullPath, []byte(body), 0o644)

	tracker := NewFileRevisionTracker()
	rTool := &readTool{}
	inArgs := json.RawMessage(`{"filePath":"asset.go"}`)
	first, err := rTool.Execute(context.Background(), ToolContext{WorktreeDir: ws, FileRevisions: tracker}, inArgs)
	if err != nil {
		t.Fatal(err)
	}
	vis := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "r1", Name: "read", Input: inArgs}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "r1", Text: first.Output}}},
	}
	second, err := rTool.Execute(context.Background(), ToolContext{WorktreeDir: ws, VisibleMessages: vis, FileRevisions: tracker}, inArgs)
	if err != nil || !strings.HasPrefix(second.Output, unchangedReadNoticePrefix) {
		t.Fatalf("expected re-read of unchanged file with long line to deduplicate, got err=%v out=%q", err, second.Output)
	}
	if wErr := tracker.CheckWrite(fullPath, "asset.go"); wErr == nil {
		t.Fatal("full-file write must stay blocked when file has truncated long line")
	}

	// 2. Prior hoxi_code_index inspect output deduplicates subsequent read of same range.
	var mod strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&mod, "func Step%02d() string { return \"value-payload-%02d\" }\n", i, i)
	}
	modPath := filepath.Join(ws, "mod.go")
	_ = os.WriteFile(modPath, []byte(mod.String()), 0o644)
	var numbered strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&numbered, "%d: func Step%02d() string { return \"value-payload-%02d\" }\n", i, i, i)
	}
	inspectOut := "# mod.go:1-30 (of 30) [Step01]\n" + strings.TrimSuffix(numbered.String(), "\n")
	visIdx := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "ci1", Name: "hoxi_code_index", Input: json.RawMessage(`{"action":"inspect","symbol":"Step01"}`)}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "ci1", Text: inspectOut}}},
	}
	tracker2 := NewFileRevisionTracker()
	readAfterIdx, err := rTool.Execute(context.Background(), ToolContext{
		WorktreeDir:     ws,
		VisibleMessages: visIdx,
		FileRevisions:   tracker2,
	}, json.RawMessage(`{"filePath":"mod.go","offset":1,"limit":30}`))
	if err != nil || !strings.HasPrefix(readAfterIdx.Output, unchangedReadNoticePrefix) {
		t.Fatalf("expected read covered by prior hoxi_code_index inspect to deduplicate, got err=%v out=%q", err, readAfterIdx.Output)
	}
	// Revision tracker still records the read so edit succeeds cleanly.
	if eErr := tracker2.CheckEdit(modPath, "mod.go", []byte(mod.String())); eErr != nil {
		t.Fatalf("expected revision tracker to allow edit after deduplicated read, got %v", eErr)
	}
}

func TestSubagentResumptionPreservesContextAndEnforcesSecurityScope(t *testing.T) {
	ws := t.TempDir()
	sid := "ses_sub_resume_test"
	DeleteSessionSubtasks(sid)
	t.Cleanup(func() { DeleteSessionSubtasks(sid) })

	var mu sync.Mutex
	type seenReq struct {
		CacheKey string
		Messages int
		Tools    []string
	}
	var seen []seenReq
	callNum := 0
	var capturedSubID string

	client := &fakeStreamClient{
		target: llm.StreamTarget{
			ProviderID:   "openai",
			ModelID:      "gpt-5",
			BaseURL:      "https://api.openai.com/v1",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 128000,
			OutputLimit:  4096,
		},
		openFn: func(_ context.Context, req llm.StreamHTTPRequest) (string, error) {
			mu.Lock()
			callNum++
			idx := callNum
			var body struct {
				PromptCacheKey string           `json:"prompt_cache_key"`
				Messages       []map[string]any `json:"messages"`
				Tools          []struct {
					Function struct{ Name string } `json:"function"`
				} `json:"tools"`
			}
			_ = json.Unmarshal(req.Body, &body)
			toolNames := make([]string, 0, len(body.Tools))
			for _, tl := range body.Tools {
				toolNames = append(toolNames, tl.Function.Name)
			}
			seen = append(seen, seenReq{CacheKey: body.PromptCacheKey, Messages: len(body.Messages), Tools: toolNames})
			mu.Unlock()

			switch idx {
			case 1:
				// Parent spawns explore subagent
				return `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"p_call_1","function":{"name":"task","arguments":"{\"subagent_type\":\"explore\",\"description\":\"inspect auth\",\"prompt\":\"Check auth flow\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n", nil
			case 2:
				// Subagent round 1 answers
				return `data: {"choices":[{"delta":{"content":"Found auth in auth.go:10"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n", nil
			case 3:
				// Parent extracts subagent_id from tool result and resumes, attempting escalation to general
				for _, m := range body.Messages {
					if m["role"] == "tool" {
						txt, _ := m["content"].(string)
						if pos := strings.Index(txt, "[subagent_id: "); pos >= 0 {
							rest := txt[pos+len("[subagent_id: "):]
							if end := strings.IndexByte(rest, ']'); end > 0 {
								capturedSubID = rest[:end]
							}
						}
					}
				}
				if capturedSubID == "" {
					return "", fmt.Errorf("missing subagent_id in parent tool output")
				}
				resumeArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"resume":        capturedSubID,
					"description":   "follow up auth",
					"prompt":        "Also check token refresh",
				})
				quoted, _ := json.Marshal(string(resumeArgs))
				return fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"p_call_2","function":{"name":"task","arguments":%s}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n", string(quoted)), nil
			case 4:
				// Subagent round 2 (resumed) answers
				return `data: {"choices":[{"delta":{"content":"Refresh is in auth.go:42"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n", nil
			default:
				return `data: {"choices":[{"delta":{"content":"All done."},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n", nil
			}
		},
	}

	if err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID:   sid,
		WorktreeDir: ws,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Inspect auth and follow up",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 model calls, got %d", len(seen))
	}
	subRound1, subRound2 := seen[1], seen[3]
	if subRound2.Messages <= subRound1.Messages {
		t.Fatalf("resumed subagent must include prior conversation history: round1=%d msgs round2=%d msgs", subRound1.Messages, subRound2.Messages)
	}
	if subRound1.CacheKey != subRound2.CacheKey {
		t.Fatalf("resumed subagent must share cache key: %q vs %q", subRound1.CacheKey, subRound2.CacheKey)
	}
	for _, tn := range subRound2.Tools {
		if tn == "write" || tn == "edit" || tn == "bash" {
			t.Fatalf("resuming a read-only explore subagent must NOT escalate to mutating tool %q", tn)
		}
	}

	// Cross-session resume must be rejected.
	if _, ok := loadSessionSubtask("other_session", capturedSubID, ws); ok {
		t.Fatal("cross-session subagent resume must be denied")
	}
	DeleteSessionSubtasks(sid)
	if _, ok := loadSessionSubtask(sid, capturedSubID, ws); ok {
		t.Fatal("DeleteSessionSubtasks must clear saved transcripts")
	}
}
