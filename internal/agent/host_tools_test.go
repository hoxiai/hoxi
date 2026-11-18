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

type fakeStreamClient struct {
	target llm.StreamTarget
	openFn func(ctx context.Context, req llm.StreamHTTPRequest) (string, error)
}

func (f *fakeStreamClient) ResolveStreamTarget(_, _ string) llm.StreamTarget {
	return f.target
}

func (f *fakeStreamClient) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	body, err := f.openFn(ctx, req)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestHostToolsRegistrationAndMediaPropagation(t *testing.T) {
	reg := newToolRegistry()
	for _, name := range []string{"hoxi_browser", "hoxi_computer", "hoxi_image", "hoxi_tools", "skill"} {
		tool, ok := reg.Get(name)
		if !ok || tool == nil {
			t.Fatalf("expected host tool %q to be registered", name)
		}
	}

	planSpecs := reg.SpecsForMode("plan")
	foundSkillInPlan := false
	for _, s := range planSpecs {
		if s.Name == "skill" {
			foundSkillInPlan = true
		}
		if s.Name == "hoxi_browser" || s.Name == "hoxi_computer" || s.Name == "hoxi_image" || s.Name == "hoxi_tools" {
			t.Fatalf("mutating host tool %s should not be exposed in plan mode", s.Name)
		}
	}
	if !foundSkillInPlan {
		t.Fatal("expected read-only skill loader tool to be available in plan mode")
	}

	var capturedMsgs [][]Message
	step := 0
	mockStream := &fakeStreamClient{
		target: llm.StreamTarget{
			ProviderID:   "openai",
			ModelID:      "gpt-5",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 128000,
			OutputLimit:  8192,
		},
		openFn: func(_ context.Context, req llm.StreamHTTPRequest) (string, error) {
			step++
			var parsed map[string]any
			if err := json.Unmarshal(req.Body, &parsed); err != nil {
				t.Fatalf("unmarshal openai request: %v", err)
			}
			if step == 2 {
				rawMsgs, _ := parsed["messages"].([]any)
				toolIdx, userMediaIdx := -1, -1
				for idx, rm := range rawMsgs {
					m, _ := rm.(map[string]any)
					if m["role"] == "tool" {
						toolIdx = idx
					}
					if m["role"] == "user" {
						if arr, ok := m["content"].([]any); ok && len(arr) > 0 {
							userMediaIdx = idx
						}
					}
				}
				if toolIdx < 0 || userMediaIdx < 0 || toolIdx >= userMediaIdx {
					t.Fatalf("expected role:tool (%d) before role:user screenshot media (%d) in OpenAI chat request: %s", toolIdx, userMediaIdx, string(req.Body))
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"Screenshot inspected.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", nil
			}
			return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_br_1\",\"type\":\"function\",\"function\":{\"name\":\"hoxi_browser\",\"arguments\":\"{\\\"action\\\":\\\"screenshot\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", nil
		},
	}
	_ = capturedMsgs

	eng := NewDefaultEngine(mockStream)
	var finalText strings.Builder
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_nat_host_1",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Take a browser screenshot",
		HostInvoke: func(_ context.Context, _ ToolContext, toolName string, _ json.RawMessage) (HostToolResult, error) {
			if toolName != "hoxi_browser" {
				t.Fatalf("unexpected toolName %s", toolName)
			}
			return HostToolResult{
				Title:  "hoxi_browser",
				Output: `{"url":"https://example.com"}`,
				Media: []ContentBlock{{
					Type:     ContentMedia,
					MimeType: "image/jpeg",
					Data:     "ZmFrZS1qcGVn",
					Filename: "browser.jpg",
				}},
			}, nil
		},
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineTextDelta {
				finalText.WriteString(evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(finalText.String(), "Screenshot inspected.") {
		t.Fatalf("unexpected final text: %q", finalText.String())
	}
}

func TestComputerAndBrowserSchemaAlignmentAndAnthropicMultiToolMediaOrder(t *testing.T) {
	reg := newToolRegistry()
	compTool, ok := reg.Get("hoxi_computer")
	if !ok {
		t.Fatal("missing hoxi_computer")
	}
	compProps, _ := compTool.Parameters()["properties"].(map[string]any)
	winProp, _ := compProps["windowID"].(map[string]any)
	elemProp, _ := compProps["elementID"].(map[string]any)
	dxProp, _ := compProps["deltaX"].(map[string]any)
	ccProp, _ := compProps["clickCount"].(map[string]any)
	if winProp["type"] != "string" {
		t.Fatalf("hoxi_computer.windowID must be string to match gateway/computer.go, got %v", winProp["type"])
	}
	if elemProp["type"] != "integer" {
		t.Fatalf("hoxi_computer.elementID must be integer to match gateway/computer.go, got %v", elemProp["type"])
	}
	if dxProp["type"] != "integer" {
		t.Fatalf("hoxi_computer.deltaX must be integer, got %v", dxProp["type"])
	}
	if ccProp["maximum"] != 3 {
		t.Fatalf("hoxi_computer.clickCount maximum must be 3, got %v", ccProp["maximum"])
	}

	brTool, ok := reg.Get("hoxi_browser")
	if !ok {
		t.Fatal("missing hoxi_browser")
	}
	brProps, _ := brTool.Parameters()["properties"].(map[string]any)
	actEnum, _ := brProps["action"].(map[string]any)["enum"].([]string)
	enumSet := make(map[string]bool, len(actEnum))
	for _, a := range actEnum {
		enumSet[a] = true
	}
	for _, want := range []string{"hover", "select", "wait", "diagnostics", "snapshot", "screenshot", "evaluate"} {
		if !enumSet[want] {
			t.Fatalf("hoxi_browser missing action %q", want)
		}
	}
	if _, hasClear := brProps["clear"]; !hasClear {
		t.Fatal("hoxi_browser missing clear parameter")
	}
	if _, hasTimeout := brProps["timeoutMs"]; !hasTimeout {
		t.Fatal("hoxi_browser missing timeoutMs parameter")
	}

	ciTool, ok := reg.Get("hoxi_code_index")
	if !ok || !ciTool.IsReadOnly() {
		t.Fatal("missing read-only hoxi_code_index tool")
	}
	ciProps, _ := ciTool.Parameters()["properties"].(map[string]any)
	ciEnum, _ := ciProps["action"].(map[string]any)["enum"].([]string)
	if len(ciEnum) != 7 {
		t.Fatalf("expected 7 hoxi_code_index actions, got %v", ciEnum)
	}

	// Verify Anthropic Messages API receives all tool_result blocks BEFORE image media blocks
	// when a model invokes multiple tools in a single step where an earlier call returns Media.
	drv := newAnthropicDriver()
	httpReq, err := drv.BuildRequest(ModelRequest{
		Target: llm.StreamTarget{ModelID: "claude-sonnet-4-5", Protocol: llm.ProtocolAnthropic},
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Inspect browser and skill"}}},
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: ContentToolUse, ID: "tu_1", Name: "hoxi_browser", Input: json.RawMessage(`{"action":"screenshot"}`)},
				{Type: ContentToolUse, ID: "tu_2", Name: "skill", Input: json.RawMessage(`{}`)},
			}},
			{Role: RoleUser, Content: []ContentBlock{
				{Type: ContentToolResult, ToolUseID: "tu_1", Text: `{"url":"https://example.com"}`},
				{Type: ContentMedia, MimeType: "image/jpeg", Data: "ZmFrZQ=="},
				{Type: ContentToolResult, ToolUseID: "tu_2", Text: "No skills"},
			}},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	var payload struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(httpReq.Body, &payload); err != nil {
		t.Fatalf("unmarshal anthropic payload: %v", err)
	}
	lastContent := payload.Messages[len(payload.Messages)-1].Content
	if len(lastContent) != 3 || lastContent[0]["type"] != "tool_result" || lastContent[1]["type"] != "tool_result" || lastContent[2]["type"] != "image" {
		t.Fatalf("expected [tool_result, tool_result, image] in Anthropic user turn, got: %+v", lastContent)
	}
}
