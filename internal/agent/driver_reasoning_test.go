package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestNativeReasoningRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		proto   llm.StreamProtocol
		effort  string
		limit   int
		variant map[string]any
		want    string
		wantErr bool
	}{
		{"claude legacy high", llm.ProtocolAnthropic, "high", 64000, map[string]any{"thinking": map[string]any{"type": "enabled", "budgetTokens": 16000}}, `"budget_tokens":16000`, false},
		{"claude constrained budget", llm.ProtocolAnthropic, "high", 4096, map[string]any{"thinking": map[string]any{"type": "enabled", "budgetTokens": 16000}}, `"budget_tokens":4095`, false},
		{"claude insufficient budget", llm.ProtocolAnthropic, "high", 1024, map[string]any{"thinking": map[string]any{"type": "enabled", "budgetTokens": 16000}}, "", true},
		{"claude adaptive", llm.ProtocolAnthropic, "high", 64000, map[string]any{"thinking": map[string]any{"type": "adaptive"}, "effort": "high"}, `"output_config":{"effort":"high"}`, false},
		{"gemini legacy", llm.ProtocolGoogle, "high", 65536, map[string]any{"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": 16000}}, `"thinkingBudget":16000`, false},
		{"gemini modern", llm.ProtocolGoogle, "high", 65536, map[string]any{"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingLevel": "high"}}, `"thinkingLevel":"high"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drv, err := newDriverFactory().Create(tc.proto)
			if err != nil {
				t.Fatal(err)
			}
			req, err := drv.BuildRequest(ModelRequest{
				Target:    llm.StreamTarget{Variants: map[string]any{tc.effort: tc.variant}},
				MaxTokens: tc.limit, Effort: tc.effort,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an explicit error for an impossible thinking budget")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(req.Body), tc.want) {
				t.Fatalf("missing reasoning configuration %s: %s", tc.want, req.Body)
			}
		})
	}
}

func TestNativeReasoningDisabledAndClamped(t *testing.T) {
	for _, proto := range []llm.StreamProtocol{llm.ProtocolAnthropic, llm.ProtocolGoogle} {
		drv, _ := newDriverFactory().Create(proto)
		for _, effort := range []string{"", "none", "high"} {
			var variants map[string]any
			if effort != "high" {
				variants = map[string]any{"high": map[string]any{
					"thinking": map[string]any{"type": "adaptive"}, "effort": "high",
					"thinkingConfig": map[string]any{"thinkingLevel": "high"},
				}}
			}
			req, err := drv.BuildRequest(ModelRequest{Target: llm.StreamTarget{Variants: variants}, Effort: effort, MaxTokens: 8192})
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(req.Body, &body); err != nil {
				t.Fatal(err)
			}
			if body["thinking"] != nil || strings.Contains(string(req.Body), `"thinkingConfig"`) {
				t.Fatalf("must not inject reasoning into an unconfigured/non-reasoning model: %s", req.Body)
			}
		}
	}
	drv := newGeminiDriver()
	req, err := drv.BuildRequest(ModelRequest{
		Target: llm.StreamTarget{Variants: map[string]any{
			"high": map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": "high"}},
		}}, Effort: "xhigh", MaxTokens: 8192,
	})
	if err != nil || !strings.Contains(string(req.Body), `"thinkingLevel":"high"`) {
		t.Fatalf("unsupported effort should clamp to a configured tier: %s, %v", req.Body, err)
	}
}

type outputBudgetClient struct {
	target llm.StreamTarget
	body   []byte
}

func (c *outputBudgetClient) ResolveStreamTarget(_, _ string) llm.StreamTarget { return c.target }
func (c *outputBudgetClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.body = req.Body
	return &http.Response{Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
}

func TestEngineHonorsModelOutputLimit(t *testing.T) {
	for _, limit := range []int{4096, 16384, 64000, 128000, 0} {
		client := &outputBudgetClient{target: llm.StreamTarget{
			Protocol: llm.ProtocolOpenAI, HasEndpoint: true, ContextLimit: 200000, OutputLimit: limit,
		}}
		if err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
			SessionID: "ses_budget", WorktreeDir: t.TempDir(), UserPrompt: "Write a patch",
		}); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Max int `json:"max_completion_tokens"`
		}
		if err := json.Unmarshal(client.body, &body); err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = 8192
		}
		if body.Max != want {
			t.Errorf("model output limit %d was replaced with %d; want %d", limit, body.Max, want)
		}
	}
}
