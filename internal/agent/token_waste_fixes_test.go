package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

// isCompactionRequestBody reports whether an OpenAI chat body is an in-place
// compaction request or a standalone checkpoint summarization request.
func isCompactionRequestBody(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "context_compaction_request") || strings.Contains(s, "context compaction engine")
}

func TestProactiveCompactionFailureIsNotRetriedEveryStep(t *testing.T) {
	bigHistory := strings.Repeat("existing architecture context line for checkpoint\n", 180)
	history := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Initial spec: " + bigHistory}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Acknowledged spec."}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "More constraints: " + bigHistory}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Ready to implement."}}},
	}
	toolStep := func(id string) string {
		return strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"` + id + `","function":{"name":"glob","arguments":"{\"pattern\":\"*.go\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20000,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":18500}}}`,
			`data: [DONE]`,
		}, "\n") + "\n"
	}
	mainSteps := 0
	client := &inPlaceCompactionClient{
		target: llm.StreamTarget{
			BaseURL: "https://api.openai.com/v1", Protocol: llm.ProtocolOpenAI,
			HasEndpoint: true, ContextLimit: 24_000, OutputLimit: 4096,
		},
	}
	client.reply = func(call int, _ map[string]any) (string, error) {
		if isCompactionRequestBody(client.reqs[call-1].Body) {
			return "", errors.New("summary provider unavailable")
		}
		mainSteps++
		switch mainSteps {
		case 1, 2, 3:
			return toolStep(fmt.Sprintf("t%d", mainSteps)), nil
		default:
			return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n", nil
		}
	}

	err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID: "ses_compact_fail_budget", WorktreeDir: t.TempDir(), Mode: "build",
		ProviderID: "openai", ModelID: "gpt-5", History: history, UserPrompt: "Continue working",
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	compactionCalls := 0
	for _, r := range client.reqs {
		if isCompactionRequestBody(r.Body) {
			compactionCalls++
		}
	}
	// One failed proactive attempt = in-place request + standalone fallback.
	// Later over-budget steps must not pay for further summary attempts.
	if compactionCalls != 2 {
		t.Fatalf("expected exactly 2 compaction requests (one failed attempt), got %d of %d total", compactionCalls, len(client.reqs))
	}
	if mainSteps != 4 {
		t.Fatalf("expected 4 main steps, got %d", mainSteps)
	}
}

type finishSummaryClient struct {
	inputs []string
	reply  func(n int, input string) (text, finish string)
}

func (c *finishSummaryClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	return llm.StreamTarget{ProviderID: p, ModelID: m, Protocol: llm.ProtocolOpenAI,
		HasEndpoint: true, ContextLimit: 128000, OutputLimit: 4096}
}

func (c *finishSummaryClient) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var body struct {
		Messages []struct{ Role, Content string }
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, err
	}
	input := body.Messages[len(body.Messages)-1].Content
	c.inputs = append(c.inputs, input)
	text, finish := c.reply(len(c.inputs), input)
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"content": text}, "finish_reason": finish,
	}}})
	return &http.Response{Body: io.NopCloser(strings.NewReader("data: " + string(chunk) + "\n\ndata: [DONE]\n\n"))}, nil
}

func TestSummaryTruncatedSegmentIsResplitInsteadOfDiscarded(t *testing.T) {
	transcript := strings.Repeat("requirement line kept verbatim\n", 700) // ~21.7 KB, one initial segment
	c := &finishSummaryClient{reply: func(n int, input string) (string, string) {
		if n == 1 {
			return "partial checkpoint that ran out of output", "length"
		}
		if strings.Contains(input, "[Checkpoint ") {
			return "merged checkpoint", "stop"
		}
		return fmt.Sprintf("segment checkpoint %d", n), "stop"
	}}
	got, err := NewDefaultEngine(c).Summarize(context.Background(), "selected", "selected-model", transcript)
	if err != nil || got != "merged checkpoint" {
		t.Fatalf("summary=%q err=%v", got, err)
	}
	// 1 truncated attempt + 2 half-size segments + 1 merge.
	if len(c.inputs) != 4 {
		t.Fatalf("expected 4 requests after re-splitting the truncated segment, got %d", len(c.inputs))
	}
}

func TestSummaryTruncatedSmallSegmentStillFailsExplicitly(t *testing.T) {
	c := &finishSummaryClient{reply: func(int, string) (string, string) {
		return "never finishes", "length"
	}}
	got, err := NewDefaultEngine(c).Summarize(context.Background(), "selected", "selected-model", strings.Repeat("x", 3000))
	if err == nil || got != "" || !errors.Is(err, errSummaryTruncated) {
		t.Fatalf("expected explicit truncation failure, got summary=%q err=%v", got, err)
	}
	if len(c.inputs) != 1 {
		t.Fatalf("segments at the minimum size must not be re-split, got %d requests", len(c.inputs))
	}
}

func TestMCPResultSkipsTextMirrorOfStructuredContent(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"{\n  \"nested\": {\"status\": \"verified\"},\n  \"answer\": 42\n}"},{"type":"text","text":"Human readable note"}],` +
		`"structuredContent":{"answer":42,"nested":{"status":"verified"}}}`
	out, err := decodeMCPToolResult("server:tool", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.Output, "verified"); n != 1 {
		t.Fatalf("structured payload must be projected once, found %d copies: %s", n, out.Output)
	}
	if !strings.Contains(out.Output, "Human readable note") || !strings.Contains(out.Output, `"answer":42`) {
		t.Fatalf("lost non-duplicate content: %s", out.Output)
	}
}

func TestMCPResultKeepsTextThatDiffersFromStructuredContent(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"{\"answer\":43}"},{"type":"text","text":"[1,2]"}],"structuredContent":{"answer":42}}`
	out, err := decodeMCPToolResult("server:tool", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"answer":42`, `{"answer":43}`, "[1,2]"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("differing text must be kept (%s): %s", want, out.Output)
		}
	}
	if mcpTextMirrorsStructured(`{"answer":42}`, nil) || mcpTextMirrorsStructured("not json", json.RawMessage(`{"a":1}`)) {
		t.Fatal("mirror detection must reject missing structured content and non-JSON text")
	}
}

func TestFailedExecutorRunDropsStickyResumePointer(t *testing.T) {
	sc := &orchestratorMockStream{responderFn: func(idx int, _ capturedOrchestratorCall) string {
		task := func(id, prompt string) string {
			return sseToolCallChunk(id, "task", map[string]any{"subagent_type": "executor", "description": prompt, "prompt": prompt})
		}
		switch idx {
		case 0:
			return task("call_exec_1", "first change")
		case 1:
			return sseTextChunk("first change done")
		case 2:
			return task("call_exec_2", "second change")
		case 3:
			// Executor run fails without visible output, so it is never saved.
			return "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\"}]}\ndata: [DONE]\n"
		case 4:
			return task("call_exec_3", "third change")
		case 5:
			return sseTextChunk("third change done")
		default:
			return sseTextChunk("All done.")
		}
	}}
	err := NewDefaultEngine(sc).RunTurn(context.Background(), TurnRequest{
		SessionID: "ses_exec_stale_resume", WorktreeDir: t.TempDir(), Mode: "build",
		ProviderID: "openai", ModelID: "orch-pro",
		Orchestrator: &OrchestratorConfig{ExecutorProviderID: "google", ExecutorModelID: "exec-flash"},
		UserPrompt:   "Make three changes",
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	sc.mu.Lock()
	calls := append([]capturedOrchestratorCall(nil), sc.calls...)
	sc.mu.Unlock()
	if len(calls) < 7 {
		t.Fatalf("expected 7 calls, got %d", len(calls))
	}
	if calls[3].MessageCount <= calls[1].MessageCount {
		t.Fatalf("second delegation should sticky-resume the saved executor: first=%d second=%d", calls[1].MessageCount, calls[3].MessageCount)
	}
	if calls[5].MessageCount != calls[1].MessageCount {
		t.Fatalf("after a failed unsaved run the next delegation must start fresh: fresh=%d third=%d", calls[1].MessageCount, calls[5].MessageCount)
	}
}

func TestIdenticalRuleCopiesAreRenderedOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := "SHARED_RULE_SENTINEL: run contractcheck before committing.\n"
	for name, body := range map[string]string{
		"AGENTS.md":  shared,
		"CLAUDE.md":  shared,
		"CONTEXT.md": "DISTINCT_CONTEXT_SENTINEL\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res := loadProjectRules(root, nil)
	if n := strings.Count(res.Rendered, "SHARED_RULE_SENTINEL"); n != 1 {
		t.Fatalf("identical rule copies must be rendered once, got %d:\n%s", n, res.Rendered)
	}
	if !strings.Contains(res.Rendered, `file="AGENTS.md"`) || !strings.Contains(res.Rendered, "DISTINCT_CONTEXT_SENTINEL") {
		t.Fatalf("expected first copy and distinct rules kept:\n%s", res.Rendered)
	}
	if len(res.LoadedFiles) != 3 {
		t.Fatalf("duplicate copies must still count as loaded for subdirectory discovery, got %v", res.LoadedFiles)
	}
	if res.TotalBytes != len(strings.TrimSpace(shared))+len("DISTINCT_CONTEXT_SENTINEL") {
		t.Fatalf("duplicate bytes must not count toward the rules budget, got %d", res.TotalBytes)
	}
}
