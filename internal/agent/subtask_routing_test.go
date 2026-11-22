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
	"time"

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

func (m *economyAwareMockStream) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	var body struct {
		Model    string `json:"model"`
		Effort   string `json:"reasoning_effort"`
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
		URL:    req.Target.BaseURL,
		Model:  body.Model,
		Effort: body.Effort,
	}
	rec.Deadline, _ = ctx.Deadline()
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

func TestSubtaskInheritsQualityAndLifetimeUnlessModelOverridden(t *testing.T) {
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
					"description":   "inherit parent explore",
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
			if strings.Contains(call.System, "Subtask: inherit parent explore") ||
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	err := eng.RunTurn(ctx, agent.TurnRequest{
		SessionID:   "ses_eco_routing",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "anthropic",
		ModelID:     "claude-opus-4-6",
		Effort:      "xhigh",
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

	if got := startMetaByCall["c_exp"]; got["providerID"] != "anthropic" || got["modelID"] != "claude-opus-4-6" || got["routing"] != "inherit_parent" {
		t.Fatalf("expected explore to inherit parent model despite an available economy model, got %+v", got)
	}
	for _, call := range sc.calls {
		wantEffort := "xhigh"
		if strings.Contains(call.System, "Subagent role: explore") || strings.Contains(call.System, "Subagent role: scout") {
			wantEffort = "high"
		}
		if call.Effort != wantEffort || !call.Deadline.Equal(parentDeadline) {
			t.Fatalf("subtasks must inherit effort and deadline: %+v", call)
		}
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

func TestSubtaskIsolatesCacheKeyPreservesStaticPrefixAndInheritsBuildEffort(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	type capturedResponsesReq struct {
		HeaderSessionID string
		PromptCacheKey  string
		Instructions    string
		Effort          string
	}
	var capMu sync.Mutex
	var captured []capturedResponsesReq
	var permSessions []string

	quoteJSON := func(raw string) string {
		b, _ := json.Marshal(raw)
		return string(b)
	}

	sc := &responsesSubtaskCaptureStream{
		onRequest: func(idx int, req llm.StreamHTTPRequest) string {
			var body struct {
				PromptCacheKey string `json:"prompt_cache_key"`
				Instructions   string `json:"instructions"`
				Reasoning      struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
			}
			_ = json.Unmarshal(req.Body, &body)
			capMu.Lock()
			captured = append(captured, capturedResponsesReq{
				HeaderSessionID: req.SessionID,
				PromptCacheKey:  body.PromptCacheKey,
				Instructions:    body.Instructions,
				Effort:          body.Reasoning.Effort,
			})
			capMu.Unlock()

			if idx == 0 {
				a1, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "implement export module",
					"prompt":        "Create export.txt",
				})
				a2, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "implement import module",
					"prompt":        "Create import.txt",
				})
				return strings.Join([]string{
					`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_0d151b446c5e4d7db67a39ed4a1a6e57","name":"task"}}`,
					`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":` + quoteJSON(string(a1)) + `}`,
					`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_0d151b446c5e4d7db67a39ed4a1a6e57","name":"task","arguments":` + quoteJSON(string(a1)) + `}}`,
					`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_5bd6952ad9a941d19ffa6b9e40d3f3d8","name":"task"}}`,
					`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":` + quoteJSON(string(a2)) + `}`,
					`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_5bd6952ad9a941d19ffa6b9e40d3f3d8","name":"task","arguments":` + quoteJSON(string(a2)) + `}}`,
					`data: {"type":"response.completed","response":{"status":"completed"}}`,
				}, "\n") + "\n"
			}
			if strings.Contains(body.Instructions, "Subagent role: general (implement export module).") {
				if idx == 1 {
					wArgs, _ := json.Marshal(map[string]any{"filePath": "export.txt", "content": "ok\n"})
					return strings.Join([]string{
						`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"w_exp","name":"write"}}`,
						`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":` + quoteJSON(string(wArgs)) + `}`,
						`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"w_exp","name":"write","arguments":` + quoteJSON(string(wArgs)) + `}}`,
						`data: {"type":"response.completed","response":{"status":"completed"}}`,
					}, "\n") + "\n"
				}
				return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"export done\"}\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
			}
			if strings.Contains(body.Instructions, "Subagent role: general (implement import module).") {
				return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"import done\"}\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
			}
			return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"all done\"}\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
		},
	}

	const parentSID = "ses_nat_1795066598530_5f57e76b4335"
	var startEfforts []string
	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   parentSID,
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		Effort:      "xhigh",
		UserPrompt:  "Build export and import modules",
		PermissionHook: func(_ context.Context, req agent.PermissionRequest) error {
			capMu.Lock()
			permSessions = append(permSessions, req.SessionID)
			capMu.Unlock()
			return nil
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineSubtaskStart {
				if eff, _ := evt.Metadata["effort"].(string); eff != "" {
					startEfforts = append(startEfforts, eff)
				}
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if len(captured) != 5 {
		t.Fatalf("expected 5 requests (parent step1, sub1 step1+step2, sub2 step1, parent step2), got %d", len(captured))
	}
	parentReq, sub1Step1, sub1Step2, sub2Step1, parentFinal := captured[0], captured[1], captured[2], captured[3], captured[4]

	// 1. Parent retains its SessionID as cache key and keeps xhigh effort.
	if parentReq.PromptCacheKey != parentSID || parentFinal.PromptCacheKey != parentSID {
		t.Fatalf("parent prompt_cache_key mismatch: start=%q final=%q", parentReq.PromptCacheKey, parentFinal.PromptCacheKey)
	}
	if parentReq.Effort != "xhigh" {
		t.Fatalf("expected parent effort=xhigh, got %q", parentReq.Effort)
	}

	// 2. Subagents get deterministic, <=64-char cache keys isolated from the parent,
	// shared across same-type sibling subagents so they reuse the cached [tools + static system] prefix.
	if sub1Step1.PromptCacheKey == "" || sub1Step1.PromptCacheKey == parentSID {
		t.Fatalf("subagent 1 must not reuse parent cache key: %q", sub1Step1.PromptCacheKey)
	}
	if sub1Step1.PromptCacheKey != sub1Step2.PromptCacheKey {
		t.Fatalf("subagent 1 cache key must stay stable across steps: %q vs %q", sub1Step1.PromptCacheKey, sub1Step2.PromptCacheKey)
	}
	if sub2Step1.PromptCacheKey == "" || sub2Step1.PromptCacheKey == parentSID || sub2Step1.PromptCacheKey != sub1Step1.PromptCacheKey {
		t.Fatalf("same-type sibling subagents should share cache key while staying isolated from parent: sub1=%q sub2=%q", sub1Step1.PromptCacheKey, sub2Step1.PromptCacheKey)
	}
	if len(sub1Step1.PromptCacheKey) > 64 || len(sub2Step1.PromptCacheKey) > 64 {
		t.Fatalf("subagent cache keys must stay <= 64 chars: len1=%d len2=%d", len(sub1Step1.PromptCacheKey), len(sub2Step1.PromptCacheKey))
	}
	if sub1Step1.HeaderSessionID != sub1Step1.PromptCacheKey || sub2Step1.HeaderSessionID != sub2Step1.PromptCacheKey {
		t.Fatalf("StreamHTTPRequest.SessionID affinity header must match isolated cache key")
	}

	// 3. Permission hooks still attribute child tool calls to the parent SessionID.
	if len(permSessions) == 0 {
		t.Fatalf("expected child write tool to invoke PermissionHook")
	}
	for _, sid := range permSessions {
		if sid != parentSID {
			t.Fatalf("permission hook must receive parent SessionID %q, got %q", parentSID, sid)
		}
	}

	// Build-capable children inherit the effort chosen for the parent.
	if sub1Step1.Effort != "xhigh" || sub2Step1.Effort != "xhigh" {
		t.Fatalf("expected build subagents to inherit xhigh, got sub1=%q sub2=%q", sub1Step1.Effort, sub2Step1.Effort)
	}
	if len(startEfforts) != 2 || startEfforts[0] != "xhigh" || startEfforts[1] != "xhigh" {
		t.Fatalf("expected EngineSubtaskStart effort metadata=[xhigh xhigh], got %v", startEfforts)
	}

	// 5. Dynamic role description sits at the very end so both general subagents share a 100% byte-identical static prefix.
	const role1 = "Subagent role: general (implement export module)."
	const role2 = "Subagent role: general (implement import module)."
	if !strings.HasSuffix(sub1Step1.Instructions, role1) || !strings.HasSuffix(sub2Step1.Instructions, role2) {
		t.Fatalf("expected dynamic role line at tail of instructions:\nsub1=%q\nsub2=%q", sub1Step1.Instructions, sub2Step1.Instructions)
	}
	prefix1 := strings.TrimSuffix(sub1Step1.Instructions, role1)
	prefix2 := strings.TrimSuffix(sub2Step1.Instructions, role2)
	if prefix1 != prefix2 {
		t.Fatalf("expected identical static instruction prefix across sibling general subagents")
	}
	if !strings.Contains(prefix1, "<native_agent_guidelines>") || !strings.Contains(prefix1, "<environment>") {
		t.Fatalf("expected shared static prefix to contain guidelines and environment blocks")
	}

	// 6. Built-in general subagent step cap raised to 36 to avoid premature 24-step cutoffs.
	_, byKey := llm.LoadAgents(wsDir, nil)
	if got := byKey["general"].Steps; got != 36 {
		t.Fatalf("expected built-in general subagent Steps=36, got %d", got)
	}
}

type responsesSubtaskCaptureStream struct {
	mu        sync.Mutex
	idx       int
	onRequest func(idx int, req llm.StreamHTTPRequest) string
}

func (s *responsesSubtaskCaptureStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com/v1",
		Protocol:     llm.ProtocolOpenAIResponses,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (s *responsesSubtaskCaptureStream) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	s.mu.Lock()
	i := s.idx
	s.idx++
	s.mu.Unlock()
	sse := s.onRequest(i, req)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     make(http.Header),
	}, nil
}
