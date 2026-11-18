package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestTruncatedReadBlocksFullWriteUntilAllSpansReadWithoutTruncation(t *testing.T) {
	wsDir := t.TempDir()
	largePath := filepath.Join(wsDir, "large_module.go")

	var src strings.Builder
	src.WriteString("package core\n\n")
	for i := 1; i <= 900; i++ {
		if i == 450 {
			src.WriteString("const MiddleCriticalInvariant = 777 // MUST NOT BE LOST\n")
		} else {
			fmt.Fprintf(&src, "var generatedSymbolLine%04d = %q\n", i, strings.Repeat("x", 36))
		}
	}
	origContent := src.String()
	if len(origContent) <= 40*1024 {
		t.Fatalf("expected test fixture > 40 KiB so single read truncates at 32 KiB, got %d bytes", len(origContent))
	}
	if err := os.WriteFile(largePath, []byte(origContent), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := agent.NewFileRevisionTracker()
	sseStop := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	// 1. Single read covers all 902 lines on disk, but registry truncates output to 32 KiB (hiding line 450).
	// Subsequent full-file `write` and `apply_patch` Add File MUST both be rejected.
	fullLineReadArgs, _ := json.Marshal(map[string]any{"filePath": "large_module.go", "offset": 1, "limit": 2000})
	blindWriteArgs, _ := json.Marshal(map[string]any{"filePath": "large_module.go", "content": "package core\n// clobbered\n"})
	blindPatchAddArgs, _ := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Add File: large_module.go\n+package core\n*** End Patch",
	})

	sseReadAllTruncated := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"r_trunc","function":{"name":"read","arguments":` + mustJSONString(string(fullLineReadArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseAttemptOverwrites := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"w_after_trunc","function":{"name":"write","arguments":` + mustJSONString(string(blindWriteArgs)) + `}},` +
			`{"index":1,"id":"p_after_trunc","function":{"name":"apply_patch","arguments":` + mustJSONString(string(blindPatchAddArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	var readDelivered string
	errs := map[string]string{}
	sc1 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseReadAllTruncated, sseAttemptOverwrites, sseStop}}
	if err := agent.NewDefaultEngine(sc1).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_trunc_guard",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Read and overwrite large_module.go",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted && evt.CallID == "r_trunc" {
				readDelivered = evt.Text
			}
			if evt.Kind == agent.EngineToolError {
				errs[evt.CallID] = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(readDelivered, "bytes truncated") || strings.Contains(readDelivered, "MiddleCriticalInvariant") {
		t.Fatalf("expected single read output to truncate middle invariant, got len=%d", len(readDelivered))
	}
	if !strings.Contains(errs["w_after_trunc"], "partial range") {
		t.Fatalf("expected write after truncated read to be blocked, got: %q", errs["w_after_trunc"])
	}
	if !strings.Contains(errs["p_after_trunc"], "partial range") {
		t.Fatalf("expected apply_patch Add File after truncated read to be blocked, got: %q", errs["p_after_trunc"])
	}

	// 2. Targeted `edit` succeeds after truncated read, and still does NOT promote `fullRead` to allow blind `write`.
	editLine1Args, _ := json.Marshal(map[string]any{
		"filePath":  "large_module.go",
		"oldString": `var generatedSymbolLine0001 = "` + strings.Repeat("x", 36) + `"`,
		"newString": `var generatedSymbolLine0001 = "edited_safely"`,
	})
	sseTargetedEdit := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"e_safe","function":{"name":"edit","arguments":` + mustJSONString(string(editLine1Args)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseWriteAfterEdit := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w_after_edit","function":{"name":"write","arguments":` + mustJSONString(string(blindWriteArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	errs = map[string]string{}
	sc2 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseTargetedEdit, sseWriteAfterEdit, sseStop}}
	if err := agent.NewDefaultEngine(sc2).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_trunc_guard",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Targeted edit then write",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				errs[evt.CallID] = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn targeted edit failed: %v", err)
	}
	if errs["e_safe"] != "" {
		t.Fatalf("expected targeted edit to succeed, got err=%q", errs["e_safe"])
	}
	if !strings.Contains(errs["w_after_edit"], "partial range") {
		t.Fatalf("expected targeted edit on partially-seen file not to unlock full write, got: %q", errs["w_after_edit"])
	}
	afterEditDisk, _ := os.ReadFile(largePath)
	if !strings.Contains(string(afterEditDisk), "MiddleCriticalInvariant = 777") {
		t.Fatal("MiddleCriticalInvariant was lost!")
	}

	// 3. Reading all lines across three un-truncated pages ([1..320] + [321..640] + [641..905]) unlocks full-file `write`.
	page1Args, _ := json.Marshal(map[string]any{"filePath": "large_module.go", "offset": 1, "limit": 320})
	page2Args, _ := json.Marshal(map[string]any{"filePath": "large_module.go", "offset": 321, "limit": 320})
	page3Args, _ := json.Marshal(map[string]any{"filePath": "large_module.go", "offset": 641, "limit": 320})
	fullReplacement := "package core\n\nconst MiddleCriticalInvariant = 777\nconst RewrittenCleanly = true\n"
	validWriteArgs, _ := json.Marshal(map[string]any{"filePath": "large_module.go", "content": fullReplacement})

	ssePaginatedReads := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"r_p1","function":{"name":"read","arguments":` + mustJSONString(string(page1Args)) + `}},` +
			`{"index":1,"id":"r_p2","function":{"name":"read","arguments":` + mustJSONString(string(page2Args)) + `}},` +
			`{"index":2,"id":"r_p3","function":{"name":"read","arguments":` + mustJSONString(string(page3Args)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseValidWrite := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w_ok","function":{"name":"write","arguments":` + mustJSONString(string(validWriteArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	errs = map[string]string{}
	sc3 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{ssePaginatedReads, sseValidWrite, sseStop}}
	if err := agent.NewDefaultEngine(sc3).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_trunc_guard",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Paginated full read then write",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				errs[evt.CallID] = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn paginated read+write failed: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("expected paginated un-truncated full read to authorize write, got errors: %+v", errs)
	}
	finalDisk, _ := os.ReadFile(largePath)
	if string(finalDisk) != fullReplacement {
		t.Fatalf("unexpected final disk content: %q", string(finalDisk))
	}
}

func TestSubagentFileRevisionIsolationPreventsCrossAgentBlindOverwrite(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	childReadFile := filepath.Join(wsDir, "child_only.go")
	parentReadFile := filepath.Join(wsDir, "parent_only.go")
	if err := os.WriteFile(childReadFile, []byte("package demo\nconst ChildReadOnly = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parentReadFile, []byte("package demo\nconst ParentReadV1 = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	toolErrs := map[string]string{}

	sc := &economyAwareMockStream{
		responder: func(idx int, call recordedLLMCall) string {
			// Child explore subagent reads child_only.go in full.
			if strings.Contains(call.System, "Subagent role: explore") {
				if !strings.Contains(call.UserPrompt, "done") && idx == 1 {
					rArgs, _ := json.Marshal(map[string]any{"filePath": "child_only.go"})
					return strings.Join([]string{
						`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_read_1","function":{"name":"read","arguments":` + mustJSONString(string(rArgs)) + `}}]}}]}`,
						`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
						`data: [DONE]`,
					}, "\n") + "\n"
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"child_only.go defines ChildReadOnly = 1\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			// Child general subagent tries blind write on parent_only.go (which ONLY parent read!).
			if strings.Contains(call.System, "Subagent role: general") {
				if idx == 4 {
					wArgs, _ := json.Marshal(map[string]any{"filePath": "parent_only.go", "content": "package demo\n// child blind wipe\n"})
					return strings.Join([]string{
						`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"g_blind_w","function":{"name":"write","arguments":` + mustJSONString(string(wArgs)) + `}}]}}]}`,
						`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
						`data: [DONE]`,
					}, "\n") + "\n"
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"general finished\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}

			switch idx {
			case 0:
				// Parent reads parent_only.go and spawns explore subagent to read child_only.go.
				rParent, _ := json.Marshal(map[string]any{"filePath": "parent_only.go"})
				tExplore, _ := json.Marshal(map[string]any{
					"subagent_type": "explore",
					"description":   "inspect child_only",
					"prompt":        "Read child_only.go",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[` +
						`{"index":0,"id":"p_read","function":{"name":"read","arguments":` + mustJSONString(string(rParent)) + `}},` +
						`{"index":1,"id":"p_exp","function":{"name":"task","arguments":` + mustJSONString(string(tExplore)) + `}}` +
						`]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			case 3:
				// Parent tries blind write on child_only.go (only read by child!), and spawns general subagent.
				wParentBlind, _ := json.Marshal(map[string]any{"filePath": "child_only.go", "content": "package demo\n// parent blind wipe\n"})
				tGeneral, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "try blind write",
					"prompt":        "Attempt overwrite",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[` +
						`{"index":0,"id":"p_blind_w","function":{"name":"write","arguments":` + mustJSONString(string(wParentBlind)) + `}},` +
						`{"index":1,"id":"p_gen","function":{"name":"task","arguments":` + mustJSONString(string(tGeneral)) + `}}` +
						`]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			default:
				return "data: {\"choices\":[{\"delta\":{\"content\":\"all checks complete\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
		},
	}

	tracker := agent.NewFileRevisionTracker()
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_sub_iso",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Delegate and test write isolation",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				mu.Lock()
				toolErrs[evt.CallID] = evt.ErrorText
				mu.Unlock()
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if !strings.Contains(toolErrs["p_blind_w"], "without reading it first") {
		t.Fatalf("expected parent blind write after child read to be rejected, got: %q", toolErrs["p_blind_w"])
	}
	if !strings.Contains(toolErrs["g_blind_w"], "without reading it first") {
		t.Fatalf("expected child blind write after parent read to be rejected, got: %q", toolErrs["g_blind_w"])
	}
}

type reasoningCaptureStream struct {
	target    llm.StreamTarget
	responses []string
	bodies    [][]byte
}

func (s *reasoningCaptureStream) ResolveStreamTarget(_, _ string) llm.StreamTarget { return s.target }
func (s *reasoningCaptureStream) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	idx := len(s.bodies)
	s.bodies = append(s.bodies, append([]byte(nil), req.Body...))
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
	if idx < len(s.responses) {
		body = s.responses[idx]
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestDeepSeekThinkingToolCallLoopEchoesReasoningContentAndSkipsStrictOpenAI(t *testing.T) {
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "calc.go"), []byte("package calc\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readArgs, _ := json.Marshal(map[string]any{"filePath": "calc.go"})
	globArgs, _ := json.Marshal(map[string]any{"pattern": "*.go"})

	// Step 1: emits reasoning_content + tool_call (read).
	step1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"Need to inspect calc.go before answering."}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"ds_call_1","function":{"name":"read","arguments":` + mustJSONString(string(readArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	// Step 2: emits tool_call with empty reasoning_content (must still echo "reasoning_content": "" on Step 3!).
	step2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"ds_call_2","function":{"name":"glob","arguments":` + mustJSONString(string(globArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	step3 := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"All clear.\",\"content\":\"Add is verified.\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	dsClient := &reasoningCaptureStream{
		target: llm.StreamTarget{
			ProviderID:  "deepseek",
			ModelID:     "deepseek-reasoner",
			BaseURL:     "https://api.deepseek.com/v1",
			Protocol:    llm.ProtocolOpenAI,
			HasEndpoint: true,
		},
		responses: []string{step1, step2, step3},
	}
	if err := agent.NewDefaultEngine(dsClient).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_ds_think",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "deepseek",
		ModelID:     "deepseek-reasoner",
		UserPrompt:  "Verify calc.go",
	}); err != nil {
		t.Fatalf("DeepSeek RunTurn failed: %v", err)
	}
	if len(dsClient.bodies) != 3 {
		t.Fatalf("expected 3 upstream requests, got %d", len(dsClient.bodies))
	}

	var req3 struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(dsClient.bodies[2], &req3); err != nil {
		t.Fatal(err)
	}
	var asstMsgs []map[string]any
	for _, m := range req3.Messages {
		if m["role"] == "assistant" {
			asstMsgs = append(asstMsgs, m)
		}
	}
	if len(asstMsgs) != 2 {
		t.Fatalf("expected 2 assistant tool-call messages in step 3 payload, got %d: %s", len(asstMsgs), dsClient.bodies[2])
	}
	if got, ok := asstMsgs[0]["reasoning_content"].(string); !ok || got != "Need to inspect calc.go before answering." {
		t.Fatalf("step 1 assistant message missing reasoning_content: %+v", asstMsgs[0])
	}
	if got, exists := asstMsgs[1]["reasoning_content"]; !exists || got != "" {
		t.Fatalf("step 2 assistant tool-call message must include empty reasoning_content field in active thinking session, got: %+v", asstMsgs[1])
	}

	// Rejection path: official api.openai.com Chat Completions must NOT receive unknown reasoning_content field.
	oaClient := &reasoningCaptureStream{
		target: llm.StreamTarget{
			ProviderID:  "openai",
			ModelID:     "gpt-5",
			BaseURL:     "https://api.openai.com/v1",
			Protocol:    llm.ProtocolOpenAI,
			HasEndpoint: true,
		},
		responses: []string{step3},
	}
	if err := agent.NewDefaultEngine(oaClient).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_oa_strict",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		History: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "prior"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
				{Type: agent.ContentThinking, Text: "private thought"},
				{Type: agent.ContentText, Text: "prior answer"},
			}},
		},
		UserPrompt: "follow up",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(oaClient.bodies[0]), `"reasoning_content"`) {
		t.Fatalf("official api.openai.com chat completions must not include reasoning_content: %s", oaClient.bodies[0])
	}
}

func TestCodeIndexSubagentNonMutatingAndWildcardTaskPolicyDeterministic(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	// 1. Deterministic wildcard evaluation across 100 fresh maps (defeating Go's randomized map iteration).
	for i := 0; i < 100; i++ {
		overlapAllowDeny := map[string]any{
			"explore-*": "allow",
			"*-secret":  "deny",
			"*":         "allow",
		}
		if got := llm.EvalWildcardPolicy(overlapAllowDeny, "explore-secret"); got != "deny" {
			t.Fatalf("iter %d: overlapping allow+deny wildcards must deterministically return deny, got %q", i, got)
		}

		overlapAllowAsk := map[string]any{
			"a*": "allow",
			"*b": "ask",
		}
		if got := llm.EvalWildcardPolicy(overlapAllowAsk, "ab"); got != "ask" {
			t.Fatalf("iter %d: overlapping allow+ask wildcards must deterministically return ask, got %q", i, got)
		}

		allowlistWithStarDeny := map[string]any{
			"*":         "deny",
			"explore-*": "allow",
		}
		if got := llm.EvalWildcardPolicy(allowlistWithStarDeny, "explore-go"); got != "allow" {
			t.Fatalf("iter %d: specific wildcard must override catch-all '*', got %q", i, got)
		}
		if got := llm.EvalWildcardPolicy(allowlistWithStarDeny, "general"); got != "deny" {
			t.Fatalf("iter %d: unmatched target must fall back to '*', got %q", i, got)
		}

		exactException := map[string]any{
			"danger-*":    "deny",
			"danger-safe": "allow",
		}
		if got := llm.EvalWildcardPolicy(exactException, "danger-safe"); got != "allow" {
			t.Fatalf("iter %d: exact literal rule must take precedence over wildcard, got %q", i, got)
		}

		caseCollision := map[string]any{
			"Explore": "allow",
			"explore": "deny",
		}
		if got := llm.EvalWildcardPolicy(caseCollision, "Explore"); got != "deny" {
			t.Fatalf("iter %d: case-colliding literal rules must fail closed to deny, got %q", i, got)
		}
	}

	// 2. Subagent calling hoxi_code_index must report mutated=false, and 4 identical hoxi_code_index calls hit loopGuard.
	ciArgs, _ := json.Marshal(map[string]any{"action": "context", "symbol": "RunTurn"})
	sc := &economyAwareMockStream{
		responder: func(idx int, call recordedLLMCall) string {
			if strings.Contains(call.System, "Subagent role: explore") {
				if idx == 1 {
					return strings.Join([]string{
						`data: {"choices":[{"delta":{"tool_calls":[` +
							`{"index":0,"id":"ci_1","function":{"name":"hoxi_code_index","arguments":` + mustJSONString(string(ciArgs)) + `}},` +
							`{"index":1,"id":"ci_2","function":{"name":"hoxi_code_index","arguments":` + mustJSONString(string(ciArgs)) + `}},` +
							`{"index":2,"id":"ci_3","function":{"name":"hoxi_code_index","arguments":` + mustJSONString(string(ciArgs)) + `}},` +
							`{"index":3,"id":"ci_4","function":{"name":"hoxi_code_index","arguments":` + mustJSONString(string(ciArgs)) + `}}` +
							`]}}]}`,
						`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
						`data: [DONE]`,
					}, "\n") + "\n"
				}
				return "data: {\"choices\":[{\"delta\":{\"content\":\"indexed context ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
			if idx == 0 {
				tArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "explore",
					"description":   "index lookup",
					"prompt":        "Inspect RunTurn",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t_ci","function":{"name":"task","arguments":` + mustJSONString(string(tArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	var sawSubtaskEnd bool
	var subtaskMutated bool
	var sawLoopGuard bool
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_ci_mut",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Check hoxi_code_index mutating flag",
		HostInvoke: func(_ context.Context, _ agent.ToolContext, toolName string, _ json.RawMessage) (agent.HostToolResult, error) {
			return agent.HostToolResult{Title: toolName, Output: "symbol RunTurn at engine.go:159"}, nil
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineSubtaskEnd {
				sawSubtaskEnd = true
				subtaskMutated, _ = evt.Metadata["mutated"].(bool)
			}
			if evt.Kind == agent.EngineToolError && evt.ToolName == "hoxi_code_index" && strings.Contains(evt.ErrorText, "[loop guard]") {
				sawLoopGuard = true
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !sawSubtaskEnd || subtaskMutated {
		t.Fatalf("expected explore subagent using hoxi_code_index to end with mutated=false, got sawEnd=%v mutated=%v", sawSubtaskEnd, subtaskMutated)
	}
	if !sawLoopGuard {
		t.Fatal("expected 4th identical hoxi_code_index call without intervening mutation to trigger loop guard")
	}
}
