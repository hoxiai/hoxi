package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

type recordedLLMCall struct {
	URL        string
	Model      string
	System     string
	ToolNames  []string
	UserPrompt string
	Effort     string
	Deadline   time.Time
}

type inspectableStreamClient struct {
	mu        sync.Mutex
	calls     []recordedLLMCall
	responder func(idx int, call recordedLLMCall) string
}

func (c *inspectableStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
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

func (c *inspectableStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
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
	for _, m := range body.Messages {
		s, _ := m.Content.(string)
		if m.Role == "system" && rec.System == "" {
			rec.System = s
		}
		if m.Role == "user" {
			rec.UserPrompt = s
		}
	}
	for _, tl := range body.Tools {
		rec.ToolNames = append(rec.ToolNames, tl.Function.Name)
	}

	c.mu.Lock()
	idx := len(c.calls)
	c.calls = append(c.calls, rec)
	c.mu.Unlock()

	sse := c.responder(idx, rec)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     make(http.Header),
	}, nil
}

func hasToolNamed(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}

func TestSubtaskExploreReadOnlyVsGeneralAndCustomAgent(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	agentsDir := filepath.Join(wsDir, ".hoxi", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secMD := "---\nname: sec-auditor\ndescription: Read-only security auditor\nmode: subagent\nmodel: secprov/audit-v1\npermission:\n  edit: deny\n---\nCUSTOM_SEC_AUDITOR_PROMPT"
	if err := os.WriteFile(filepath.Join(agentsDir, "sec-auditor.md"), []byte(secMD), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := &inspectableStreamClient{
		responder: func(idx int, call recordedLLMCall) string {
			if idx == 0 {
				expArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "explore",
					"description":   "locate handlers",
					"prompt":        "Find HTTP handlers",
				})
				secArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "sec-auditor",
					"description":   "audit auth",
					"prompt":        "Check token validation",
				})
				genArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "create note",
					"prompt":        "Write subagent_done.txt",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t_exp","function":{"name":"task","arguments":` + mustJSONString(string(expArgs)) + `}},{"index":1,"id":"t_sec","function":{"name":"task","arguments":` + mustJSONString(string(secArgs)) + `}},{"index":2,"id":"t_gen","function":{"name":"task","arguments":` + mustJSONString(string(genArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			if strings.Contains(call.System, "Subagent role: explore (locate handlers)") {
				return "data: {\"choices\":[{\"delta\":{\"content\":\"explore report ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			if strings.Contains(call.System, "CUSTOM_SEC_AUDITOR_PROMPT") {
				return "data: {\"choices\":[{\"delta\":{\"content\":\"sec-auditor report ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			if strings.Contains(call.System, "Subagent role: general (create note)") {
				if _, err := os.Stat(filepath.Join(wsDir, "subagent_done.txt")); os.IsNotExist(err) {
					wArgs, _ := json.Marshal(map[string]any{
						"filePath": "subagent_done.txt",
						"content":  "written by general subagent\n",
					})
					return strings.Join([]string{
						`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w_gen","function":{"name":"write","arguments":` + mustJSONString(string(wArgs)) + `}}]}}]}`,
						`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
						`data: [DONE]`,
					}, "\n") + "\n"
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"general write complete\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"all subagents finished\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	eng := agent.NewDefaultEngine(sc)
	var finalOut strings.Builder
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_subroles",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Coordinate subagents",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == agent.EngineTextDelta {
				finalOut.WriteString(evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if finalOut.String() != "all subagents finished" {
		t.Fatalf("unexpected parent output: %q", finalOut.String())
	}

	written, err := os.ReadFile(filepath.Join(wsDir, "subagent_done.txt"))
	if err != nil || !strings.Contains(string(written), "written by general subagent") {
		t.Fatalf("expected general subagent to write file, err=%v content=%q", err, string(written))
	}

	sc.mu.Lock()
	calls := append([]recordedLLMCall(nil), sc.calls...)
	sc.mu.Unlock()

	var sawExplore, sawSec, sawGeneral bool
	for _, c := range calls {
		switch {
		case strings.Contains(c.System, "Subagent role: explore (locate handlers)"):
			sawExplore = true
			if hasToolNamed(c.ToolNames, "write") || hasToolNamed(c.ToolNames, "bash") || hasToolNamed(c.ToolNames, "task") {
				t.Fatalf("explore subagent must be strictly read-only without nested task, got tools: %v", c.ToolNames)
			}
		case strings.Contains(c.System, "CUSTOM_SEC_AUDITOR_PROMPT"):
			sawSec = true
			if c.Model != "audit-v1" || !strings.Contains(c.URL, "/secprov/") {
				t.Fatalf("expected sec-auditor model override secprov/audit-v1, got model=%q url=%q", c.Model, c.URL)
			}
			if hasToolNamed(c.ToolNames, "edit") || hasToolNamed(c.ToolNames, "write") {
				t.Fatalf("sec-auditor with permission.edit=deny must not receive mutating tools: %v", c.ToolNames)
			}
		case strings.Contains(c.System, "Subagent role: general (create note)"):
			sawGeneral = true
			if !hasToolNamed(c.ToolNames, "write") || !hasToolNamed(c.ToolNames, "bash") || hasToolNamed(c.ToolNames, "task") {
				t.Fatalf("general subagent must have write/bash but no nested task, got tools: %v", c.ToolNames)
			}
		}
	}
	if !sawExplore || !sawSec || !sawGeneral {
		t.Fatalf("missing subagent calls: explore=%v sec=%v general=%v", sawExplore, sawSec, sawGeneral)
	}
}

func TestPrimaryCustomAgentReadOnlyAndTaskPolicyDeny(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	var toolErrors []string
	sc := &inspectableStreamClient{
		responder: func(idx int, _ recordedLLMCall) string {
			if idx == 0 {
				genArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "disallowed general",
					"prompt":        "Attempt blocked general subagent",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t_blocked","function":{"name":"task","arguments":` + mustJSONString(string(genArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"handled policy denial\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_policy",
		WorktreeDir: wsDir,
		Mode:        "reviewer",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Review repo",
		MCPConfig: func() map[string]any {
			return map[string]any{
				"agent": map[string]any{
					"reviewer": map[string]any{
						"mode":   "primary",
						"prompt": "PRIMARY_REVIEWER_CUSTOM_PROMPT",
						"permission": map[string]any{
							"edit": "deny",
							"task": map[string]any{
								"*":       "deny",
								"explore": "allow",
							},
						},
					},
				},
			}
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				toolErrors = append(toolErrors, evt.ErrorText)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(toolErrors) != 1 || !strings.Contains(toolErrors[0], "denied by task permission policy") {
		t.Fatalf("expected task permission denial error, got: %v", toolErrors)
	}
	sc.mu.Lock()
	first := sc.calls[0]
	sc.mu.Unlock()
	if !strings.Contains(first.System, "PRIMARY_REVIEWER_CUSTOM_PROMPT") {
		t.Fatalf("expected custom primary agent prompt in system, got:\n%s", first.System)
	}
	if hasToolNamed(first.ToolNames, "write") || hasToolNamed(first.ToolNames, "edit") {
		t.Fatalf("expected read-only primary agent to exclude write/edit tools, got: %v", first.ToolNames)
	}
	if !hasToolNamed(first.ToolNames, "task") {
		t.Fatalf("expected allowlist primary agent (explore=allow) to retain task tool, got: %v", first.ToolNames)
	}
}

func TestTaskToolOmittedWhenPermissionTaskDeniesAllAndUnlockedOnPlanPromote(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	// 1. Global permission.task=deny omits `task` from the tool schema up front.
	globalDenyStream := &inspectableStreamClient{
		responder: func(_ int, _ recordedLLMCall) string {
			return "data: {\"choices\":[{\"delta\":{\"content\":\"done directly\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}
	eng := agent.NewDefaultEngine(globalDenyStream)
	if err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_global_deny_task",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Answer directly",
		MCPConfig: func() map[string]any {
			return map[string]any{"permission": map[string]any{"task": "deny"}}
		},
		Sink: agent.EventSinkFunc(func(agent.EngineEvent) {}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if hasToolNamed(globalDenyStream.calls[0].ToolNames, "task") {
		t.Fatalf("expected global permission.task=deny to omit task schema, got: %v", globalDenyStream.calls[0].ToolNames)
	}

	// 2. Plan mode with task=deny omits `task`, then unlocks `task` when promoted to Build.
	promoteStream := &inspectableStreamClient{
		responder: func(idx int, _ recordedLLMCall) string {
			if idx == 0 {
				consentArgs, _ := json.Marshal(map[string]any{
					"title":   "Small refactor",
					"summary": "Update helper",
					"steps":   []string{"Edit helper", "Run tests"},
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"pc_1","function":{"name":"plan_consent","arguments":` + mustJSONString(string(consentArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"implemented\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}
	eng2 := agent.NewDefaultEngine(promoteStream)
	if err := eng2.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_plan_deny_task",
		WorktreeDir: wsDir,
		Mode:        "plan",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Plan a refactor",
		MCPConfig: func() map[string]any {
			return map[string]any{
				"agent": map[string]any{
					"plan": map[string]any{
						"permission": map[string]any{"task": "deny"},
					},
				},
			}
		},
		AskQuestion: func(_ context.Context, _ string, _ string) (string, map[string]any, error) {
			return agent.PlanConsentApproveAndBuild, map[string]any{
				"answers": [][]string{{agent.PlanConsentApproveAndBuild}},
			}, nil
		},
		Sink: agent.EventSinkFunc(func(agent.EngineEvent) {}),
	}); err != nil {
		t.Fatalf("RunTurn plan promote failed: %v", err)
	}
	if len(promoteStream.calls) < 2 {
		t.Fatalf("expected at least 2 LLM steps, got %d", len(promoteStream.calls))
	}
	if hasToolNamed(promoteStream.calls[0].ToolNames, "task") {
		t.Fatalf("expected plan mode with task=deny to omit task schema, got: %v", promoteStream.calls[0].ToolNames)
	}
	if !hasToolNamed(promoteStream.calls[1].ToolNames, "task") {
		t.Fatalf("expected promoted build mode to restore task schema, got: %v", promoteStream.calls[1].ToolNames)
	}
}
