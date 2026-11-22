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

func TestModelDriversPreferExplicitCacheKeyOverSessionID(t *testing.T) {
	factory := newDriverFactory()
	baseMsgs := []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "ping"}}}}

	// 1. OpenAI Chat Completions
	chatDrv, err := factory.Create(llm.ProtocolOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	chatReq, err := chatDrv.BuildRequest(ModelRequest{
		SessionID: "ses_parent",
		CacheKey:  "ses_parent:sub:call_1",
		Target:    llm.StreamTarget{ProviderID: "openai", ModelID: "gpt-5", BaseURL: "https://api.openai.com/v1", Protocol: llm.ProtocolOpenAI},
		Messages:  baseMsgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	var chatBody map[string]any
	if err := json.Unmarshal(chatReq.Body, &chatBody); err != nil {
		t.Fatal(err)
	}
	if chatBody["prompt_cache_key"] != "ses_parent:sub:call_1" || chatReq.SessionID != "ses_parent:sub:call_1" {
		t.Fatalf("openai chat ignored CacheKey: body=%v headerSID=%q", chatBody["prompt_cache_key"], chatReq.SessionID)
	}

	// 2. Anthropic sticky pool user_id and SessionID affinity
	anthDrv, err := factory.Create(llm.ProtocolAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	parentAnth, err := anthDrv.BuildRequest(ModelRequest{
		SessionID: "ses_parent",
		Target:    llm.StreamTarget{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", Protocol: llm.ProtocolAnthropic},
		Messages:  baseMsgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	childAnth, err := anthDrv.BuildRequest(ModelRequest{
		SessionID: "ses_parent",
		CacheKey:  "ses_parent:sub:call_1",
		Target:    llm.StreamTarget{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", Protocol: llm.ProtocolAnthropic},
		Messages:  baseMsgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	var parentAnthBody, childAnthBody struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(parentAnth.Body, &parentAnthBody)
	_ = json.Unmarshal(childAnth.Body, &childAnthBody)
	if childAnth.SessionID != "ses_parent:sub:call_1" || childAnthBody.Metadata.UserID == "" || childAnthBody.Metadata.UserID == parentAnthBody.Metadata.UserID {
		t.Fatalf("anthropic driver did not isolate CacheKey: parent=%q child=%q headerSID=%q",
			parentAnthBody.Metadata.UserID, childAnthBody.Metadata.UserID, childAnth.SessionID)
	}

	// 3. Gemini SessionID affinity
	gemDrv, err := factory.Create(llm.ProtocolGoogle)
	if err != nil {
		t.Fatal(err)
	}
	gemReq, err := gemDrv.BuildRequest(ModelRequest{
		SessionID: "ses_parent",
		CacheKey:  "ses_parent:sub:call_1",
		Target:    llm.StreamTarget{ProviderID: "google", ModelID: "gemini-2.5-pro", Protocol: llm.ProtocolGoogle},
		Messages:  baseMsgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gemReq.SessionID != "ses_parent:sub:call_1" {
		t.Fatalf("gemini driver ignored CacheKey: got %q", gemReq.SessionID)
	}
}

func TestGeminiLocalCallIDAcrossResponses(t *testing.T) {
	driver := newGeminiDriver()
	seen := map[string]bool{}
	for _, label := range []string{"inspect", "implement"} {
		frame := `data: {"candidates":[{"finishReason":"STOP","content":{"parts":[{"functionCall":{"name":"task","args":{}}}]}}]}` + "\n\n"
		var id string
		err := driver.ParseSSE(context.Background(), strings.NewReader(frame), func(e StreamEvent) {
			if e.ToolCall != nil {
				id = e.ToolCall.ID
			}
		})
		if err != nil || id == "" || seen[id] {
			t.Fatalf("%s: call id %q, seen %t, error %v", label, id, seen[id], err)
		}
		seen[id] = true
	}
}
