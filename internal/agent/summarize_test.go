package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/llm"
)

type summaryTestClient struct {
	inputs []string
	limit  int
	reply  func(int, string) (string, error)
}

func (c *summaryTestClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	return llm.StreamTarget{ProviderID: p, ModelID: m, Protocol: llm.ProtocolOpenAI,
		HasEndpoint: true, ContextLimit: c.limit, OutputLimit: 4096}
}

func (c *summaryTestClient) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Target.ProviderID != "selected" || req.Target.ModelID != "selected-model" {
		return nil, fmt.Errorf("unexpected provider/model fallback: %+v", req.Target)
	}
	var body struct {
		Messages []struct{ Role, Content string }
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, err
	}
	input := body.Messages[len(body.Messages)-1].Content
	c.inputs = append(c.inputs, input)
	answer, err := c.reply(len(c.inputs), input)
	if err != nil {
		return nil, err
	}
	delta, _ := json.Marshal(map[string]any{"usage": map[string]any{"prompt_tokens": 21, "completion_tokens": 3}, "choices": []any{map[string]any{"delta": map[string]any{"content": answer}}}})
	sse := "data: " + string(delta) + "\n\ndata: [DONE]\n\n"
	return &http.Response{Body: io.NopCloser(strings.NewReader(sse))}, nil
}

func TestOverflowSummaryIncludesMiddleRequirementsAndFullToolEvidence(t *testing.T) {
	markers := []string{"FIRST_REQUIREMENT", "MIDDLE_REQUIREMENT", "LAST_REQUIREMENT", "TOOL_INPUT_DETAIL", "TOOL_ERROR_DETAIL"}
	c := &summaryTestClient{limit: 128000, reply: func(_ int, input string) (string, error) {
		var found []string
		for _, marker := range markers {
			if strings.Contains(input, marker) {
				found = append(found, marker)
			}
		}
		return "Checkpoint: " + strings.Join(found, " "), nil
	}}
	longUser := markers[0] + strings.Repeat("用户约束。", 8000) + markers[1] + strings.Repeat("代码状态。", 8000) + markers[2]
	input, _ := json.Marshal(map[string]string{"content": strings.Repeat("x", 400) + markers[3]})
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: longUser}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "call-1", Name: "edit", Input: input}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "call-1", Text: strings.Repeat("y", 2000) + markers[4], IsError: true}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "continue exactly here"}}},
	}
	before, _ := json.Marshal(msgs)
	got, info, ok := NewDefaultEngine(c).compactContextOverflow(context.Background(), "selected", "selected-model", msgs, 3, 1, nil)
	if !ok || info == nil || len(c.inputs) < 3 {
		t.Fatalf("expected chunked checkpoint: ok=%t info=%+v calls=%d", ok, info, len(c.inputs))
	}
	for _, marker := range markers {
		if !strings.Contains(info.Summary, marker) {
			t.Errorf("summary omitted %s: %s", marker, info.Summary)
		}
	}
	for _, input := range c.inputs {
		if !utf8.ValidString(input) {
			t.Fatal("split damaged UTF-8")
		}
	}
	if !reflect.DeepEqual(got[len(got)-1], msgs[3]) {
		t.Fatal("current user anchor changed")
	}
	after, _ := json.Marshal(msgs)
	if string(before) != string(after) {
		t.Fatal("compaction mutated source history")
	}
}

func TestSummarySplitCoversEveryByteAndHonorsModelBudget(t *testing.T) {
	raw := strings.Repeat("约束🚀\n", 9000)
	chunks := splitSummaryTranscript(raw, 4096)
	if strings.Join(chunks, "") != raw {
		t.Fatal("chunking lost or duplicated transcript bytes")
	}
	for _, chunk := range chunks {
		if len(chunk) > 4096 || !utf8.ValidString(chunk) {
			t.Fatal("invalid chunk boundary")
		}
	}
	c := &summaryTestClient{limit: 8192, reply: func(_ int, s string) (string, error) {
		if len(s) > 4600 { // 4096 input bytes plus request framing
			t.Errorf("summary request exceeds small-model input budget: %d", len(s))
		}
		return "Retain requirements and verification.", nil
	}}
	if _, err := NewDefaultEngine(c).Summarize(context.Background(), "selected", "selected-model", raw); err != nil {
		t.Fatal(err)
	}
}

func TestSummaryFailureNeverCommitsPartialCheckpoint(t *testing.T) {
	boom := errors.New("summary provider failed")
	c := &summaryTestClient{limit: 128000, reply: func(n int, _ string) (string, error) {
		if n == 2 {
			return "", boom
		}
		return "first partial checkpoint", nil
	}}
	eng := NewDefaultEngine(c)
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: strings.Repeat("required detail ", 10000)}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "working"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "continue"}}},
	}
	got, checkpoint, ok := eng.compactContextOverflow(context.Background(), "selected", "selected-model", msgs, 2, 1, nil)
	if ok || got != nil || checkpoint != nil {
		t.Fatal("failed summary must not silently fall back to deleting content")
	}
	if len(c.inputs) != 2 {
		t.Fatalf("must stop on first failed segment, got %d calls", len(c.inputs))
	}
}

func TestSummaryCancellationAndNonConvergence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &summaryTestClient{limit: 8192, reply: func(_ int, _ string) (string, error) {
		cancel()
		return "partial", nil
	}}
	if _, err := NewDefaultEngine(c).Summarize(ctx, "selected", "selected-model", strings.Repeat("x", 20000)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if len(c.inputs) != 1 {
		t.Fatalf("requests continued after Stop: %d", len(c.inputs))
	}
	c = &summaryTestClient{limit: 8192, reply: func(_ int, _ string) (string, error) {
		return strings.Repeat("x", 6000), nil
	}}
	if summary, err := NewDefaultEngine(c).Summarize(context.Background(), "selected", "selected-model", strings.Repeat("x", 20000)); err == nil || summary != "" {
		t.Fatal("non-converging summaries must fail explicitly")
	}
}

func TestSummaryRetainsUsageWhenLaterSegmentFails(t *testing.T) {
	c := &summaryTestClient{limit: 8192, reply: func(n int, _ string) (string, error) {
		if n == 2 {
			return "", errors.New("second segment failed")
		}
		return "checkpoint", nil
	}}
	var usage TokenUsage
	summary, err := NewDefaultEngine(c).SummarizeWithUsage(context.Background(), "selected", "selected-model", strings.Repeat("x", 20000), usage.Add)
	if err == nil || summary != "" || usage.Input != 21 || usage.Output != 3 {
		t.Fatalf("result=%q usage=%+v err=%v", summary, usage, err)
	}
}
