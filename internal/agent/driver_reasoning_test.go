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

func TestOpenAIChatPromptCacheKeyAndReasoningForProxyCodexModels(t *testing.T) {
	drv := newOpenAIDriver(false)
	activeMsgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Audit recent commits"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentThinking, Text: "Inspecting diff"},
			{Type: ContentToolUse, ID: "c1", Name: "read", Input: json.RawMessage(`{"filePath":"a.go"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "c1", Text: "1: ok"}}},
	}

	// Positive cases: OpenAI / Codex models on unified gateways must send prompt_cache_key
	// and omit non-standard reasoning_content so cross-turn prefixes stay byte-identical.
	for _, tc := range []struct {
		name   string
		target llm.StreamTarget
	}{
		{
			name:   "subapi codex astra",
			target: llm.StreamTarget{ProviderID: "subapi", ModelID: "openai-codex-gpt-6.2-astra", BaseURL: "http://127.0.0.1:5431/v1", Protocol: llm.ProtocolOpenAI},
		},
		{
			name:   "subapi codex sol",
			target: llm.StreamTarget{ProviderID: "subapi", ModelID: "openai-codex-gpt-6.2-sol", BaseURL: "http://127.0.0.1:5431/v1", Protocol: llm.ProtocolOpenAI},
		},
		{
			name:   "openrouter gpt-5",
			target: llm.StreamTarget{ProviderID: "openrouter", ModelID: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1", Protocol: llm.ProtocolOpenAI},
		},
		{
			name:   "official openai chat",
			target: llm.StreamTarget{ProviderID: "openai", ModelID: "gpt-4o", BaseURL: "https://api.openai.com/v1", Protocol: llm.ProtocolOpenAI},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := drv.BuildRequest(ModelRequest{
				SessionID: "ses_codex_cache_test",
				Target:    tc.target,
				Messages:  activeMsgs,
			})
			if err != nil {
				t.Fatalf("BuildRequest failed: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(req.Body, &body); err != nil {
				t.Fatal(err)
			}
			if got, _ := body["prompt_cache_key"].(string); got != "ses_codex_cache_test" {
				t.Fatalf("expected prompt_cache_key=ses_codex_cache_test, got %q (body=%s)", got, req.Body)
			}
			if strings.Contains(string(req.Body), `"reasoning_content"`) {
				t.Fatalf("OpenAI/Codex chat payload must not inject reasoning_content: %s", req.Body)
			}
		})
	}

	// Rejection cases: non-OpenAI models and strict third-party hosts must NOT receive prompt_cache_key;
	// non-OpenAI reasoning relays must retain reasoning_content echo in the active turn.
	for _, tc := range []struct {
		name               string
		target             llm.StreamTarget
		wantReasoningField bool
	}{
		{
			name:               "subapi deepseek",
			target:             llm.StreamTarget{ProviderID: "subapi", ModelID: "Loomy-DeepSeek-V4.1-Flash", BaseURL: "http://127.0.0.1:5431/v1", Protocol: llm.ProtocolOpenAI},
			wantReasoningField: true,
		},
		{
			name:               "subapi gemini",
			target:             llm.StreamTarget{ProviderID: "subapi", ModelID: "gemini-web-gemini-4-carbon", BaseURL: "http://127.0.0.1:5431/v1", Protocol: llm.ProtocolOpenAI},
			wantReasoningField: true,
		},
		{
			name:               "deepseek official",
			target:             llm.StreamTarget{ProviderID: "deepseek", ModelID: "deepseek-reasoner", BaseURL: "https://api.deepseek.com/v1", Protocol: llm.ProtocolOpenAI},
			wantReasoningField: true,
		},
		{
			name:               "groq gpt-oss",
			target:             llm.StreamTarget{ProviderID: "groq", ModelID: "openai/gpt-oss-120b", BaseURL: "https://api.groq.com/openai/v1", Protocol: llm.ProtocolOpenAI},
			wantReasoningField: true,
		},
		{
			name:               "strict third-party host with gpt-5 alias",
			target:             llm.StreamTarget{ProviderID: "groq", ModelID: "gpt-5", BaseURL: "https://api.groq.com/openai/v1", Protocol: llm.ProtocolOpenAI},
			wantReasoningField: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := drv.BuildRequest(ModelRequest{
				SessionID: "ses_non_oai_test",
				Target:    tc.target,
				Messages:  activeMsgs,
			})
			if err != nil {
				t.Fatalf("BuildRequest failed: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(req.Body, &body); err != nil {
				t.Fatal(err)
			}
			if _, hasKey := body["prompt_cache_key"]; hasKey {
				t.Fatalf("must not send prompt_cache_key to %s: %s", tc.name, req.Body)
			}
			hasReasoning := strings.Contains(string(req.Body), `"reasoning_content"`)
			if hasReasoning != tc.wantReasoningField {
				t.Fatalf("reasoning_content mismatch for %s: got %v want %v", tc.name, hasReasoning, tc.wantReasoningField)
			}
		})
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
