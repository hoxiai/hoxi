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
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestScopedToolsAndDefaultReadWindow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte(strings.Repeat("line\n", 750)), 0600); err != nil {
		t.Fatal(err)
	}
	reg := newToolRegistry()
	out, err := reg.Execute(context.Background(), ToolContext{WorktreeDir: dir}, ToolCall{Name: "read", Input: json.RawMessage(`{"filePath":"file.go"}`)}, nil)
	if err != nil || !strings.Contains(out.Output, "offset=481") || strings.Contains(out.Output, "481: line") {
		t.Fatalf("default page: %v, %d bytes", err, len(out.Output))
	}
	for _, active := range []bool{false, true} {
		for _, skills := range []bool{false, true} {
			var system string
			if skills {
				system = "<available_skills><skill><name>test</name></skill></available_skills>"
			}
			stream := &fakeStreamClient{target: llm.StreamTarget{ProviderID: "p", ModelID: "m", Protocol: llm.ProtocolOpenAI, HasEndpoint: true}, openFn: func(_ context.Context, req llm.StreamHTTPRequest) (string, error) {
				var wire struct {
					Tools []struct{ Function struct{ Name string } }
				}
				if err := json.Unmarshal(req.Body, &wire); err != nil {
					t.Fatal(err)
				}
				names := map[string]bool{}
				for _, tool := range wire.Tools {
					names[tool.Function.Name] = true
				}
				if names["goal_report"] != active || names["skill"] != skills || names["multiedit"] || !names["edit"] || !names["apply_patch"] {
					t.Fatal(names)
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", nil
			}}
			if err := NewDefaultEngine(stream).RunTurn(context.Background(), TurnRequest{SessionID: "s", WorktreeDir: dir, GoalActive: active, System: system, UserPrompt: "go"}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHostProxyScopeAndCacheOptions(t *testing.T) {
	for _, input := range []string{`{"name":"bash","args":{"command":"pwd"}}`, `{"name":"task","args":{}}`, `{"name":"hoxi_browser","args":null}`} {
		if _, ok := resolveHostProxy(ToolCall{Name: "call_host_tool", Input: json.RawMessage(input)}); ok {
			t.Fatal("accepted invalid host proxy", input)
		}
	}
	if resolved, ok := resolveHostProxy(ToolCall{Name: "call_host_tool", Input: json.RawMessage("{\"name\":\"hoxi_browser\",\"args\": \n  {\"action\":\"snapshot\"}}")}); !ok || resolved.Name != "hoxi_browser" || string(resolved.Input) != `{"action":"snapshot"}` {
		t.Fatalf("whitespace args not resolved: ok=%v call=%+v", ok, resolved)
	}
	reg := newToolRegistry()
	if _, err := reg.Execute(context.Background(), ToolContext{Mode: "plan"}, ToolCall{Name: "call_host_tool", Input: json.RawMessage(`{"name":"hoxi_browser","args":{"action":"snapshot"}}`)}, nil); err == nil || !strings.Contains(err.Error(), "disabled in plan") {
		t.Fatalf("plan mode error = %v", err)
	}
	if _, err := reg.Execute(context.Background(), ToolContext{Mode: "build"}, ToolCall{Name: "call_host_tool", Input: json.RawMessage(`{"name":"bash","args":{}}`)}, nil); err == nil || !strings.Contains(err.Error(), "invalid call_host_tool arguments") {
		t.Fatalf("invalid proxy error = %v", err)
	}
	for _, tc := range []struct {
		host, model             string
		responses, compat, want bool
	}{
		{"https://api.openai.com/v1", "gpt-5.2", true, false, true}, {"https://api.openai.com/v1", "GPT-5.3", true, false, true},
		{"https://api.openai.com/v1", "gpt-5.4-mini", true, false, true}, {"https://api.openai.com/v1", "gpt-5.4-pro", true, false, true},
		{"https://gateway.example/v1", "gpt-5.2", true, false, false}, {"https://api.openai.com/v1", "gpt-5.6", true, false, false},
		{"https://api.openai.com/v1", "gpt-4o", true, false, false},
		{"https://gateway.example", "claude-sonnet-4", false, true, true}, {"https://gateway.example", "claude-sonnet-4", false, false, false},
		{"https://gateway.example", "gpt-5", false, true, false},
	} {
		target := llm.StreamTarget{ModelID: tc.model, BaseURL: tc.host, PromptCacheRetention: "24h", OpenAICompatCacheControl: tc.compat}
		req, err := (&openaiDriver{responsesAPI: tc.responses}).BuildRequest(ModelRequest{Target: target, SessionID: "s"})
		var fields map[string]json.RawMessage
		if err != nil || json.Unmarshal(req.Body, &fields) != nil {
			t.Fatal(err)
		}
		key := "cache_control"
		if tc.responses {
			key = "prompt_cache_retention"
		}
		if (fields[key] != nil) != tc.want {
			t.Errorf("%+v: %s=%s", tc, key, fields[key])
		}
	}
}

type fallbackSummaryClient struct {
	models      []string
	rejectSmall bool
	configured  string
	bodyBytes   int
}

func (c *fallbackSummaryClient) ConfiguredCompactionModel() string { return c.configured }

func (*fallbackSummaryClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	return llm.StreamTarget{ProviderID: p, ModelID: m, Protocol: llm.ProtocolOpenAI, HasEndpoint: true, ContextLimit: 128000, OutputLimit: 4096}
}
func (*fallbackSummaryClient) ResolveEconomyModel(p, m string) (string, bool) {
	return "economy-mini", true
}
func (c *fallbackSummaryClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.bodyBytes = len(req.Body)
	c.models = append(c.models, req.Target.ProviderID+"/"+req.Target.ModelID)
	if !strings.Contains(string(req.Body), "code-head") || !strings.Contains(string(req.Body), "code-tail") || len(req.Body) > 12000 || !strings.Contains(string(req.Body), "critical user constraint") || !strings.Contains(string(req.Body), "unique failure detail") || !strings.Contains(string(req.Body), "main.ts") {
		return nil, errors.New("incorrect precompaction")
	}
	if c.rejectSmall && req.Target.ModelID != "main" {
		return nil, errors.New("model unavailable")
	}
	return &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"checkpoint\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
}

func TestOverflowSummaryUsesConfiguredRouteAndPreservesEvidence(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"filePath": "main.ts", "content": "code-head\n" + strings.Repeat("large patch\n", 14000) + "code-tail"})
	msgs := []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "critical user constraint"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "w", Name: "write", Input: payload}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "w", Text: "written"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "b", Name: "bash", Input: json.RawMessage(`{"command":"go test"}`)}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "b", IsError: true, Text: "unique failure detail"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "continue"}}}}
	for _, tc := range []struct {
		cfg  string
		fail bool
		want string
	}{
		{"", false, "p/economy-mini"},
		{"q/mini", true, "q/mini,p/main"},
		{"q/summarizer", false, "q/summarizer"},
	} {
		client := &fallbackSummaryClient{rejectSmall: tc.fail, configured: tc.cfg}
		_, cp, ok := NewDefaultEngine(client).compactContextOverflowWithInPlace(context.Background(), "p", "main", msgs, len(msgs)-1, 1, nil, nil, "")
		t.Logf("%d bytes of write arguments, %d bytes in the complete summary request, route %s", len(payload), client.bodyBytes, strings.Join(client.models, ","))
		if !ok || cp.Summary != "checkpoint" || strings.Join(client.models, ",") != tc.want || len(msgs[1].Content[0].Input) != len(payload) {
			t.Fatalf("models %v, checkpoint %+v", client.models, cp)
		}
	}
}

func TestReadOnlyRulesAndScreenshotBudget(t *testing.T) {
	rules := "<project_instructions file=\"AGENTS.md\" scope=\"project\">\n" + strings.Repeat("编码约束", 4500) + "TAIL\n</project_instructions>"
	readonly := extractInheritedSubtaskConstraints(rules, true)
	if len(readonly) > readOnlyRulesBudget+100 || !utf8.ValidString(readonly) || !strings.Contains(readonly, "TAIL") || !strings.Contains(extractInheritedSubtaskConstraints(rules, false), rules) {
		t.Fatal("incorrect rule budget")
	}
	msgs := []Message{}
	for i := range 7 {
		id := fmt.Sprint(i)
		msgs = append(msgs, Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: id, Name: "hoxi_browser"}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: id}, {Type: ContentMedia, ScreenshotTrigger: relaxedLiveToolScreenshots, MimeType: "image/png", Filename: "browser.jpg", Data: "AQ=="}}})
	}
	before := msgs[:12]
	six := pruneStaleToolScreenshots(before)
	seven := pruneStaleToolScreenshots(msgs)
	if six[1].Content[1].Type != ContentMedia || seven[9].Content[1].Type != ContentText || seven[11].Content[1].Type != ContentMedia {
		t.Fatal("wrong relaxed media window")
	}
	cause, _ := classifyPrefixCacheChange(computePrefixFingerprint("s", "", nil, six, 5000), computePrefixFingerprint("s", "", nil, seven, 0))
	if !strings.Contains(cause, "media_pruned") {
		t.Fatal(cause)
	}
}

func TestSummaryReadFoldingUsesDeliveredHistoricalPages(t *testing.T) {
	makeRead := func(lines int) []Message {
		var body strings.Builder
		for n := 1; n <= lines; n++ {
			fmt.Fprintf(&body, "%d: source\n", n)
		}
		return []Message{
			{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "read", Name: "read", Input: json.RawMessage(`{"filePath":"main.go"}`)}}},
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "read", Text: body.String()}}},
		}
	}
	for _, tc := range []struct {
		old, recent int
		fold        bool
	}{{600, 480, false}, {480, 600, true}} {
		msgs := makeRead(tc.old)
		for range compactChunkStride {
			msgs = append(msgs, Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "next task"}}})
		}
		msgs = append(msgs, makeRead(tc.recent)...)
		projected := prepareSummaryMessages(msgs, t.TempDir())
		if folded := strings.HasPrefix(projected[1].Content[0].Text, "[superseded read"); folded != tc.fold {
			t.Fatalf("old page %d and new page %d: folded = %v", tc.old, tc.recent, folded)
		}
	}
}
