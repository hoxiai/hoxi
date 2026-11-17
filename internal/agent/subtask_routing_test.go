package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

type economyAwareMockStream struct {
	mu           sync.Mutex
	calls        []recordedLLMCall
	economyByPID map[string]string
	responder    func(idx int, call recordedLLMCall) string
}

func (m *economyAwareMockStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	if providerID == "unconfigured" {
		return llm.StreamTarget{ProviderID: providerID, ModelID: modelID, HasEndpoint: false}
	}
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com/" + providerID + "/v1",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (m *economyAwareMockStream) ResolveEconomyModel(providerID, currentModelID string) (string, bool) {
	if eco, ok := m.economyByPID[providerID]; ok && eco != "" {
		return eco, true
	}
	return "", false
}

func (m *economyAwareMockStream) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(req.Body, &body)

	rec := recordedLLMCall{
		URL:   req.Target.BaseURL,
		Model: body.Model,
	}
	for _, msg := range body.Messages {
		s, _ := msg.Content.(string)
		if msg.Role == "system" && rec.System == "" {
			rec.System = s
		}
		if msg.Role == "user" {
			rec.UserPrompt = s
		}
	}
	for _, tl := range body.Tools {
		rec.ToolNames = append(rec.ToolNames, tl.Function.Name)
	}

	m.mu.Lock()
	idx := len(m.calls)
	m.calls = append(m.calls, rec)
	m.mu.Unlock()

	sse := m.responder(idx, rec)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     make(http.Header),
	}, nil
}

func TestSubtaskAutoEconomyRoutingCallOverrideAndLegacyGeneral(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	quoteJSON := func(raw string) string {
		b, _ := json.Marshal(raw)
		return string(b)
	}

	sc := &economyAwareMockStream{
		economyByPID: map[string]string{
			"anthropic": "claude-haiku-4-5",
		},
		responder: func(idx int, call recordedLLMCall) string {
			if idx == 0 {
				expArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "explore",
					"description":   "auto economy explore",
					"prompt":        "Inspect auth routes",
				})
				overrideArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "scout",
					"description":   "explicit override scout",
					"prompt":        "Locate token parser",
					"model":         "google/gemini-2.5-flash",
				})
				genArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "legacy general model",
					"prompt":        "Summarize module layout",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_exp","function":{"name":"task","arguments":` + quoteJSON(string(expArgs)) + `}},{"index":1,"id":"c_ovr","function":{"name":"task","arguments":` + quoteJSON(string(overrideArgs)) + `}},{"index":2,"id":"c_gen","function":{"name":"task","arguments":` + quoteJSON(string(genArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			if strings.Contains(call.System, "Subtask: auto economy explore") ||
				strings.Contains(call.System, "Subtask: explicit override scout") ||
				strings.Contains(call.System, "Subtask: legacy general model") {
				return "data: {\"choices\":[{\"delta\":{\"content\":\"subagent done\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"parent synthesis ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	var startMetaMu sync.Mutex
	startMetaByCall := map[string]map[string]any{}
	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_eco_routing",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "anthropic",
		ModelID:     "claude-opus-4-6",
		UserPrompt:  "Run heterogeneous subagents",
		MCPConfig: func() map[string]any {
			return map[string]any{
				"subagent_model": "openai/gpt-5-codex",
			}
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineSubtaskStart && evt.ParentCallID != "" {
				startMetaMu.Lock()
				startMetaByCall[evt.ParentCallID] = evt.Metadata
				startMetaMu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if got := startMetaByCall["c_exp"]; got["providerID"] != "anthropic" || got["modelID"] != "claude-haiku-4-5" || got["routing"] != "economy_auto" {
		t.Fatalf("expected explore auto-economy routing anthropic/claude-haiku-4-5, got %+v", got)
	}
	if got := startMetaByCall["c_ovr"]; got["providerID"] != "google" || got["modelID"] != "gemini-2.5-flash" || got["routing"] != "call_override" {
		t.Fatalf("expected scout per-call override google/gemini-2.5-flash, got %+v", got)
	}
	if got := startMetaByCall["c_gen"]; got["providerID"] != "openai" || got["modelID"] != "gpt-5-codex" || got["routing"] != "legacy_config" {
		t.Fatalf("expected general legacy_config openai/gpt-5-codex, got %+v", got)
	}
}

func TestSubtaskAskPolicyTriggersPermissionHook(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	quoteJSON := func(raw string) string {
		b, _ := json.Marshal(raw)
		return string(b)
	}

	sc := &economyAwareMockStream{
		responder: func(idx int, _ recordedLLMCall) string {
			if idx == 0 {
				args, _ := json.Marshal(map[string]any{
					"subagent_type": "explore",
					"description":   "gated explore",
					"prompt":        "Check sensitive module",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_ask","function":{"name":"task","arguments":` + quoteJSON(string(args)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"handled user rejection\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	askedExplicitApproval := false
	var toolErrs []string
	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_ask_policy",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-6",
		UserPrompt:  "Delegate with ask policy",
		MCPConfig: func() map[string]any {
			return map[string]any{
				"permission": map[string]any{
					"task": "ask",
				},
			}
		},
		PermissionHook: func(_ context.Context, req agent.PermissionRequest) error {
			if req.ToolName == "task" && !req.IsRead {
				askedExplicitApproval = true
				return fmt.Errorf("user declined subagent delegation")
			}
			return nil
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				toolErrs = append(toolErrs, evt.ErrorText)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !askedExplicitApproval {
		t.Fatal("expected permission.task=ask to invoke PermissionHook with IsRead=false")
	}
	if len(toolErrs) != 1 || !strings.Contains(toolErrs[0], "user declined subagent delegation") {
		t.Fatalf("expected user rejection error on task tool, got %v", toolErrs)
	}
}
