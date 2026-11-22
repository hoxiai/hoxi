package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestApplyPatchUnifiedDiffAddAndUpdate(t *testing.T) {
	wsDir := t.TempDir()
	existing := filepath.Join(wsDir, "hello.txt")
	if err := os.WriteFile(existing, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	unified := strings.Join([]string{
		"--- /dev/null",
		"+++ b/docs/new.md",
		"@@ -0,0 +1,2 @@",
		"+# Title",
		"+Body line",
		"--- a/hello.txt",
		"+++ b/hello.txt",
		"@@ -1,3 +1,3 @@",
		" line1",
		"-line2",
		"+line2-updated",
		" line3",
	}, "\n") + "\n"

	patchArgs, _ := json.Marshal(map[string]any{"patch": unified})
	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_patch","function":{"name":"apply_patch","arguments":` + mustJSONString(string(patchArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"patched\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sseDone}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_udiff",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Apply unified diff",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	gotNew, err := os.ReadFile(filepath.Join(wsDir, "docs", "new.md"))
	if err != nil {
		t.Fatalf("expected docs/new.md to exist: %v", err)
	}
	if string(gotNew) != "# Title\nBody line\n" {
		t.Fatalf("unexpected docs/new.md content: %q", string(gotNew))
	}

	gotExisting, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotExisting) != "line1\nline2-updated\nline3\n" {
		t.Fatalf("unexpected hello.txt content: %q", string(gotExisting))
	}

	// Regression: an update patch with zero hunks must fail loudly instead of silently reporting success.
	emptyUpdate := "--- a/hello.txt\n+++ b/hello.txt\n"
	badArgs, _ := json.Marshal(map[string]any{"patch": emptyUpdate})
	sseBad := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_bad","function":{"name":"apply_patch","arguments":` + mustJSONString(string(badArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	var badErr string
	scBad := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseBad, sseDone}}
	if err := agent.NewDefaultEngine(scBad).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_udiff_empty",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Apply empty update",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError && evt.CallID == "c_bad" {
				badErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn empty update check failed: %v", err)
	}
	if !strings.Contains(badErr, "no hunks") {
		t.Fatalf("expected empty update patch to fail with 'no hunks', got: %q", badErr)
	}
}

func TestWriteToolRequiresExplicitContentKey(t *testing.T) {
	wsDir := t.TempDir()
	wrongArgs, _ := json.Marshal(map[string]any{"filePath": "wrong.txt", "text": "hello"})
	emptyArgs, _ := json.Marshal(map[string]any{"filePath": "empty.txt", "content": ""})

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"c_wrong","function":{"name":"write","arguments":` + mustJSONString(string(wrongArgs)) + `}},` +
			`{"index":1,"id":"c_empty","function":{"name":"write","arguments":` + mustJSONString(string(emptyArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	var wrongErr string
	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sseDone}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_write_req",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Test write validation",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError && evt.CallID == "c_wrong" {
				wrongErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(wrongErr, "content is required") {
		t.Fatalf("expected missing content key to fail loudly, got: %q", wrongErr)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "wrong.txt")); !os.IsNotExist(err) {
		t.Fatalf("wrong.txt must not be created when content key is missing")
	}
	if st, err := os.Stat(filepath.Join(wsDir, "empty.txt")); err != nil || st.Size() != 0 {
		t.Fatalf("explicit empty content should still create 0-byte empty.txt, err=%v", err)
	}
}

func TestOpenAIResponsesSSEReasoningSummarySeparators(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"id":"rs_1","type":"reasoning"}}`,
		`data: {"type":"response.reasoning_summary_part.added","summary_index":0,"part":{"type":"summary_text","text":""}}`,
		`data: {"type":"response.reasoning_summary_text.delta","summary_index":0,"delta":"**Step One**"}`,
		`data: {"type":"response.reasoning_summary_part.added","summary_index":1,"part":{"type":"summary_text","text":""}}`,
		`data: {"type":"response.reasoning_summary_text.delta","summary_index":1,"delta":"**Step Two**"}`,
		`data: {"type":"response.output_text.delta","delta":"all set"}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, "\n") + "\n"

	var thinking strings.Builder
	sc := &stubStreamClient{proto: llm.ProtocolOpenAIResponses, responses: []string{sse}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_resp_think",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Reason in two parts",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineThinkingDelta {
				thinking.WriteString(evt.Text)
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if got := thinking.String(); got != "**Step One**\n\n**Step Two**" {
		t.Fatalf("expected paragraph break between reasoning summary parts, got: %q", got)
	}
}

func TestOpenAIChatSSEIgnoresDuplicateToolNameChunks(t *testing.T) {
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "a.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_dup","function":{"name":"read","arguments":"{\"filePath\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_dup","function":{"name":"read","arguments":"\"a.txt\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	var toolCompleted string
	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sseDone}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_dup_name",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Read a.txt",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted && evt.CallID == "c_dup" {
				toolCompleted = evt.ToolName
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if toolCompleted != "read" {
		t.Fatalf("expected deduplicated tool name 'read', got: %q", toolCompleted)
	}
}

func TestLoopGuardDoesNotResetOnFailedMutationAndBlocksRepeatedFailures(t *testing.T) {
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]any{"filePath": "note.txt"})
	badEditArgs, _ := json.Marshal(map[string]any{
		"filePath":  "note.txt",
		"oldString": "nonexistent-snippet",
		"newString": "replacement",
	})
	mkStep := func(idx int) string {
		suffix := string(rune('0' + idx))
		return strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[` +
				`{"index":0,"id":"r_` + suffix + `","function":{"name":"read","arguments":` + mustJSONString(string(readArgs)) + `}},` +
				`{"index":1,"id":"e_` + suffix + `","function":{"name":"edit","arguments":` + mustJSONString(string(badEditArgs)) + `}}` +
				`]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}, "\n") + "\n"
	}
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"stopped\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	var readBlocked, editBlocked string
	sc := &stubStreamClient{
		proto:     llm.ProtocolOpenAI,
		responses: []string{mkStep(1), mkStep(2), mkStep(3), mkStep(4), sseDone},
	}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_fail_loop",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Try failing edit repeatedly",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				if evt.ToolName == "read" && strings.Contains(evt.ErrorText, "[loop guard]") {
					readBlocked = evt.ErrorText
				}
				if evt.ToolName == "edit" && strings.Contains(evt.ErrorText, "[loop guard]") {
					editBlocked = evt.ErrorText
				}
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if readBlocked == "" {
		t.Fatalf("expected failed edit NOT to clear identical-read loop guard")
	}
	if editBlocked == "" {
		t.Fatalf("expected 4th identical failing edit to be blocked by loop guard")
	}
}

func TestBashBackgroundOutputNonZeroExitIsError(t *testing.T) {
	wsDir := t.TempDir()
	defer agent.DeleteSessionBackgroundProcs("ses_bg_nonzero")

	startArgs, _ := json.Marshal(map[string]any{
		"command":           "sleep 0.6; exit 2",
		"run_in_background": true,
	})
	pollArgs, _ := json.Marshal(map[string]any{
		"action": "output",
		"procId": "bg_1",
		"waitMs": 3000,
	})
	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_start","function":{"name":"bash","arguments":` + mustJSONString(string(startArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sse2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_poll","function":{"name":"bash","arguments":` + mustJSONString(string(pollArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"noticed failure\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	var pollErr string
	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sse2, sseDone}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bg_nonzero",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Start delayed failing bg command and poll output",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError && evt.CallID == "c_poll" {
				pollErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(pollErr, "exitCode=2") && !strings.Contains(pollErr, "code 2") {
		t.Fatalf("expected non-zero background exit on action=output to emit EngineToolError, got: %q", pollErr)
	}
}

type capturingStreamClient struct {
	stubStreamClient
	bodies [][]byte
}

func (c *capturingStreamClient) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	cp := make([]byte, len(req.Body))
	copy(cp, req.Body)
	c.bodies = append(c.bodies, cp)
	return c.stubStreamClient.OpenStream(ctx, req)
}

func TestOpenAIResponsesSendsPromptCacheKey(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, "\n") + "\n"

	sc := &capturingStreamClient{stubStreamClient: stubStreamClient{proto: llm.ProtocolOpenAIResponses, responses: []string{sse}}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_cache_sticky_123",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "ping",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(sc.bodies) != 1 {
		t.Fatalf("expected 1 captured request, got %d", len(sc.bodies))
	}
	var payload map[string]any
	if err := json.Unmarshal(sc.bodies[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got, _ := payload["prompt_cache_key"].(string); got != "ses_cache_sticky_123" {
		t.Fatalf("expected prompt_cache_key=ses_cache_sticky_123, got %q", got)
	}
	// Verify legacy redundant code-index aliases are hidden from tool schemas sent to the model.
	tools, _ := payload["tools"].([]any)
	seen := map[string]bool{}
	for _, raw := range tools {
		if m, ok := raw.(map[string]any); ok {
			if name, _ := m["name"].(string); name != "" {
				seen[name] = true
			}
		}
	}
	if !seen["hoxi_code_index"] {
		t.Fatalf("expected hoxi_code_index in advertised tool specs")
	}
	for _, legacy := range []string{"code_search", "symbol_inspect", "impact_analysis"} {
		if seen[legacy] {
			t.Fatalf("expected legacy alias %q to be omitted from advertised tool specs", legacy)
		}
	}
}

func TestPruneOldToolResultsCapsLargeContextTrigger(t *testing.T) {
	// Build a ~170K-token conversation (~510K ASCII chars) on a 1,000,000-token context window.
	// Without the 150K regular trigger ceiling, 75% of 1M (750K) would skip pruning completely.
	bigBody := strings.Repeat("x", 32000)
	msgs := make([]agent.Message, 0, 44)
	msgs = append(msgs, agent.Message{
		Role:    agent.RoleUser,
		Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "audit repo"}},
	})
	for i := 0; i < 20; i++ {
		callID := "call_" + string(rune('a'+i))
		msgs = append(msgs,
			agent.Message{
				Role: agent.RoleAssistant,
				Content: []agent.ContentBlock{{
					Type:  agent.ContentToolUse,
					ID:    callID,
					Name:  "read",
					Input: json.RawMessage(`{"filePath":"big.go"}`),
				}},
			},
			agent.Message{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{{
					Type:      agent.ContentToolResult,
					ToolUseID: callID,
					Text:      bigBody,
				}},
			},
		)
	}

	pruned := agent.PruneOldToolResults(msgs, "sys", 1_000_000)
	prunedCount := 0
	for _, m := range pruned {
		for _, b := range m.Content {
			if b.Type == agent.ContentToolResult && strings.HasPrefix(b.Text, "[tool output pruned:") {
				prunedCount++
			}
		}
	}
	if prunedCount == 0 {
		t.Fatalf("expected large-context (1M) conversation above 150K tokens to prune older tool results")
	}
}

func TestGeminiContentFilterUsageAndSignatureOnlyThinkingReplay(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"SAFETY"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":100,"candidatesTokenCount":0,"thoughtsTokenCount":25}}`,
	}, "\n") + "\n"

	var finish string
	var gotUsage agent.TokenUsage
	sc := &stubStreamClient{proto: llm.ProtocolGoogle, responses: []string{sse}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_gem_filter",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "google",
		ModelID:     "gemini-2.5-pro",
		UserPrompt:  "test safety finish and cache-only usage",
		History: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "prev"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
				{Type: agent.ContentThinking, Text: "", Signature: "sig_only_gem_123"},
				{Type: agent.ContentText, Text: "ok"},
			}},
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			switch evt.Kind {
			case agent.EngineTurnUsage:
				if evt.Usage != nil {
					gotUsage = *evt.Usage
				}
			case agent.EngineTurnFinish:
				finish = evt.Finish
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if finish != "content_filter" {
		t.Fatalf("expected Gemini SAFETY finishReason to map to content_filter, got %q", finish)
	}
	if gotUsage.CacheRead != 100 || gotUsage.Reasoning != 25 {
		t.Fatalf("expected cache/reasoning-only usage to be emitted, got %+v", gotUsage)
	}
	if len(sc.bodies) == 0 || !strings.Contains(string(sc.bodies[0]), `"thoughtSignature":"sig_only_gem_123"`) {
		t.Fatalf("expected signature-only Gemini thinking block to be preserved on replay, body=%s", string(sc.bodies[0]))
	}
}

func TestOpenAIResponsesSeparatesAdjacentReasoningEncryptedContent(t *testing.T) {
	sse1 := strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"id":"rs_1","type":"reasoning"}}`,
		`data: {"type":"response.reasoning_summary_text.delta","summary_index":0,"delta":"first thought"}`,
		`data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","encrypted_content":"enc_block_1"}}`,
		`data: {"type":"response.output_item.added","item":{"id":"rs_2","type":"reasoning"}}`,
		`data: {"type":"response.reasoning_summary_text.delta","summary_index":0,"delta":"second thought"}`,
		`data: {"type":"response.output_item.done","item":{"id":"rs_2","type":"reasoning","encrypted_content":"enc_block_2"}}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_r1","name":"glob"}}`,
		`data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_r1","name":"glob","arguments":"{\"pattern\":\"*.txt\"}"}}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, "\n") + "\n"
	sse2 := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"all done"}`,
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
	}, "\n") + "\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAIResponses, responses: []string{sse1, sse2}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_resp_multi_enc",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "multi reasoning",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(sc.bodies) < 2 {
		t.Fatalf("expected 2 requests (tool call + replay), got %d", len(sc.bodies))
	}
	replay := string(sc.bodies[1])
	if strings.Contains(replay, "enc_block_1enc_block_2") {
		t.Fatalf("encrypted_content from adjacent reasoning items was concatenated: %s", replay)
	}
	if !strings.Contains(replay, `"encrypted_content":"enc_block_1"`) || !strings.Contains(replay, `"encrypted_content":"enc_block_2"`) {
		t.Fatalf("expected both distinct encrypted_content items on replay, got: %s", replay)
	}
}

