package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestOnDemandHostToolsPreserveScopeAndApproval(t *testing.T) {
	ws := t.TempDir()
	step := 0
	browserAsked := false
	stream := &fakeStreamClient{
		target: llm.StreamTarget{ProviderID: "offline", ModelID: "offline", Protocol: llm.ProtocolOpenAI, HasEndpoint: true, ContextLimit: 128000, OutputLimit: 4096},
		openFn: func(_ context.Context, req llm.StreamHTTPRequest) (string, error) {
			step++
			var body struct {
				Tools []struct{ Function struct{ Name string } }
			}
			if err := json.Unmarshal(req.Body, &body); err != nil {
				return "", err
			}
			names := map[string]bool{}
			for _, t := range body.Tools {
				names[t.Function.Name] = true
			}
			if !names["read"] || !names["load_host_tool"] || names["hoxi_video"] || names["hoxi_browser"] != (step > 1) {
				t.Fatalf("step %d catalog: %v", step, names)
			}
			var name, args string
			switch step {
			case 1:
				name, args = "load_host_tool", `{"name":"hoxi_browser"}`
			case 2:
				name, args = "hoxi_browser", `{"action":"snapshot"}`
			case 3:
				if !strings.Contains(string(req.Body), "browser approval declined") {
					t.Fatal("missing approval decision in transcript")
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", nil
			default:
				t.Fatalf("unexpected step %d", step)
			}
			return fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call%d","function":{"name":%q,"arguments":%q}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n", step, name, args), nil
		},
	}
	engine := NewDefaultEngine(stream)
	err := engine.RunTurn(context.Background(), TurnRequest{
		SessionID: "ses_native_tool_load", WorktreeDir: ws, ProviderID: "offline", ModelID: "offline", Mode: "build", UserPrompt: "Inspect the browser",
		PermissionHook: func(_ context.Context, req PermissionRequest) error {
			if req.ToolName == "hoxi_browser" {
				browserAsked = true
				return errors.New("browser approval declined")
			}
			return nil
		},
		HostInvoke: func(context.Context, ToolContext, string, json.RawMessage) (HostToolResult, error) {
			t.Fatal("declined tool reached host")
			return HostToolResult{}, nil
		},
		Sink: EventSinkFunc(func(EngineEvent) {}),
	})
	if err != nil || !browserAsked || step != 3 {
		t.Fatalf("run: err=%v asked=%v steps=%d", err, browserAsked, step)
	}
}

func TestHostToolCatalogRestoresSuccessfulLoadsOnly(t *testing.T) {
	reg := newToolRegistry()
	if isSubtaskToolMutating("load_host_tool", nil) || isWorkspaceMutatingTool(ToolCall{Name: "load_host_tool"}) {
		t.Fatal("schema discovery must preserve read and edit tracking")
	}
	has := func(cat hostToolCatalog, mode, name string) bool {
		for _, spec := range cat.specs(reg, mode, 0) {
			if spec.Name == name {
				return true
			}
		}
		return false
	}
	eager := newHostToolCatalog(map[string]any{"hostToolLoading": "eager"}, nil)
	if !has(eager, "build", "hoxi_browser") || has(eager, "build", "load_host_tool") {
		t.Fatal("unexpected eager catalog")
	}
	history := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentToolUse, ID: "ok", Name: "load_host_tool", Input: json.RawMessage(`{"name":"hoxi_browser"}`)},
			{Type: ContentToolUse, ID: "denied", Name: "load_host_tool", Input: json.RawMessage(`{"name":"hoxi_image"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentToolResult, ToolUseID: "ok"}, {Type: ContentToolResult, ToolUseID: "denied", IsError: true},
		}},
	}
	cat := newHostToolCatalog(map[string]any{"hostToolLoading": "on-demand"}, history)
	if !has(cat, "build", "hoxi_browser") || has(cat, "build", "hoxi_image") || has(cat, "plan", "hoxi_browser") || has(cat, "plan", "load_host_tool") {
		t.Fatal("restored catalog violates history or plan scope")
	}

	// Subagents default to on-demand host tool loading to avoid repeating 5 heavy
	// schemas on every subagent step, unless project config explicitly forces eager.
	subDefault := newHostToolCatalog(nil, nil)
	subHas := func(c hostToolCatalog, name string) bool {
		for _, spec := range c.specs(reg, "build", 1) {
			if spec.Name == name {
				return true
			}
		}
		return false
	}
	if subHas(subDefault, "hoxi_browser") || subHas(subDefault, "hoxi_video") || !subHas(subDefault, "load_host_tool") {
		t.Fatal("expected subagent default catalog to defer heavy host tools behind load_host_tool")
	}
	subEager := eager
	if !subHas(subEager, "hoxi_browser") || subHas(subEager, "load_host_tool") {
		t.Fatal("expected explicit hostToolLoading=eager to expose heavy host tools to subagents")
	}
}

func TestDefaultCatalogWireSizeAndSmallWindowBudget(t *testing.T) {
	reg := newToolRegistry()
	var sizes []int
	for _, mode := range []string{"eager", ""} {
		specs := newHostToolCatalog(map[string]any{"hostToolLoading": mode}, nil).specs(reg, "build", 0)
		req, err := (&openaiDriver{}).BuildRequest(ModelRequest{Target: llm.StreamTarget{ModelID: "offline"}, Tools: specs})
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		window := 8192
		if mode == "eager" {
			window = 16384
		}
		budget := effectiveCompactContextLimit(window, specs)
		if budget != window-estimateToolSpecsTokens(specs) || budget <= 0 || budget >= window {
			t.Fatalf("unexpected schema budget %d for mode=%q window=%d", budget, mode, window)
		}
		sizes = append(sizes, len(body["tools"]))
		t.Logf("mode=%q tools=%d serializedBytes=%d messageBudget=%d", mode, len(specs), len(body["tools"]), budget)
	}
	if sizes[1] >= sizes[0] {
		t.Fatal("default catalog did not reduce the wire payload")
	}
	if budget := effectiveCompactContextLimit(1, reg.SpecsForMode("build")); budget != 1 {
		t.Fatalf("invalid minimum budget %d", budget)
	}
}
