package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestEstimateTokensDistinguishesASCIIAndCJKAccurately(t *testing.T) {
	// 37,500 bytes of ASCII code -> ~10,000 tokens (3.75 bytes/token), NOT 12,500 (len/3).
	asciiCode := strings.Repeat("func compute(x int) int { return x + 1 }\n", 914) // 37,474 bytes
	gotASCII := estimateTokens(nil, asciiCode)
	if gotASCII < 9800 || gotASCII > 10300 {
		t.Fatalf("expected ~10,000 tokens for 37.5KB ASCII code, got %d", gotASCII)
	}

	// 10,000 CJK runes = 30,000 UTF-8 bytes -> ~10,000 tokens (1 token/CJK rune).
	cjk := strings.Repeat("中", 10000)
	gotCJK := estimateTokens(nil, cjk)
	if gotCJK < 9950 || gotCJK > 10200 {
		t.Fatalf("expected ~10,000 tokens for 10K CJK runes, got %d", gotCJK)
	}
}

func TestResolveTurnContextThresholdsScalesByWindowAndPricingCliff(t *testing.T) {
	// 1. Standard 200K model -> 75% (150K) trigger, 90% (180K) hard cap (clamped by output reserve).
	t200 := resolveTurnContextThresholds(llm.StreamTarget{
		ModelID: "gpt-4o", ContextLimit: 200_000, OutputLimit: 16_384,
	})
	if t200.triggerTokens != 150_000 || t200.hardCapTokens != 180_000 {
		t.Fatalf("200K standard thresholds = %+v, want trigger=150000 hardCap=180000", t200)
	}

	// 2. 1M Claude / Gemini (200K 2x pricing cliff) -> stays safely under 200K.
	tClaude1M := resolveTurnContextThresholds(llm.StreamTarget{
		ModelID: "claude-opus-4-6", ContextLimit: 1_000_000, OutputLimit: 64_000,
	})
	if tClaude1M.triggerTokens != cliffRegularCompactTriggerTokens || tClaude1M.hardCapTokens != cliffRegularCompactHardCapTokens {
		t.Fatalf("1M Claude thresholds = %+v, want cliff caps (%d/%d)", tClaude1M, cliffRegularCompactTriggerTokens, cliffRegularCompactHardCapTokens)
	}
	tGem1M := resolveTurnContextThresholds(llm.StreamTarget{
		ModelID: "gemini-2.5-pro", ContextLimit: 1_000_000, OutputLimit: 65_536,
	})
	if tGem1M.triggerTokens != cliffRegularCompactTriggerTokens || tGem1M.hardCapTokens != cliffRegularCompactHardCapTokens {
		t.Fatalf("1M Gemini thresholds = %+v, want cliff caps (%d/%d)", tGem1M, cliffRegularCompactTriggerTokens, cliffRegularCompactHardCapTokens)
	}

	// 3. 400K flat-pricing OpenAI model -> medium large window caps (220K / 280K).
	tGPT400K := resolveTurnContextThresholds(llm.StreamTarget{
		ModelID: "gpt-5.4-codex", ContextLimit: 400_000, OutputLimit: 64_000,
	})
	if tGPT400K.triggerTokens != mediumWindowCompactTriggerTokens || tGPT400K.hardCapTokens != mediumWindowCompactHardCapTokens {
		t.Fatalf("400K Codex thresholds = %+v, want (%d/%d)", tGPT400K, mediumWindowCompactTriggerTokens, mediumWindowCompactHardCapTokens)
	}

	// 4. 1.05M flat-pricing Codex model -> generous large-window caps (280K / 360K).
	tCodex1M := resolveTurnContextThresholds(llm.StreamTarget{
		ModelID: "openai-codex-gpt-6.2-sol", ContextLimit: 1_050_000, OutputLimit: 128_000,
	})
	if tCodex1M.triggerTokens != largeWindowCompactTriggerTokens || tCodex1M.hardCapTokens != largeWindowCompactHardCapTokens {
		t.Fatalf("1.05M Codex thresholds = %+v, want (%d/%d)", tCodex1M, largeWindowCompactTriggerTokens, largeWindowCompactHardCapTokens)
	}

	// 5. Small window with large declared output limit preserves safe positive trigger < hardCap < contextLimit.
	tSmall := resolveTurnContextThresholds(llm.StreamTarget{
		ModelID: "small-local", ContextLimit: 32_000, OutputLimit: 16_384,
	})
	if tSmall.triggerTokens <= 0 || tSmall.triggerTokens >= tSmall.hardCapTokens || tSmall.hardCapTokens >= 32_000 {
		t.Fatalf("32K thresholds invalid: %+v", tSmall)
	}
}

func TestContextTokenTrackerAnchorsToProviderUsageAndDrivesPruning(t *testing.T) {
	// Build a 14-message history whose ASCII heuristic is ~9,600 tokens (~36KB)
	// on a 12,000-token window (trigger = 9,000).
	bigOut := strings.Repeat("1: func sampleLine() int { return 42 }\n", 150) // ~5850 bytes (~1560 est tok)
	var msgs []Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: fmt.Sprintf("c%d", i), Name: "read", Input: json.RawMessage(`{"filePath":"a.go"}`)}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: fmt.Sprintf("c%d", i), Text: bigOut}}},
		)
	}
	msgs = append(msgs,
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "c6", Name: "read", Input: json.RawMessage(`{"filePath":"b.go"}`)}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "c6", Text: "short"}}},
	)

	thresholds := resolveTurnContextThresholds(llm.StreamTarget{ModelID: "gpt-5", ContextLimit: 12_000, OutputLimit: 2048})
	tracker := newContextTokenTracker()

	// Case 1: Provider reports real prompt was only 7,000 tokens (well below 9,000 trigger),
	// even though heuristic estimateTokens is >9,400! Pruning must NOT fire prematurely.
	tracker.ObserveStep(msgs, "sys", nil, TokenUsage{Input: 1000, CacheRead: 6000, Output: 200})
	if got := tracker.EstimateTotalTokens(msgs, "sys", nil); got != 7000 {
		t.Fatalf("expected anchored exact token count 7000, got %d", got)
	}
	if !tracker.IsPrefixCacheWarm() {
		t.Fatal("expected CacheRead>0 to mark prefix cache warm")
	}
	pruned, changed := pruneOldToolResultsWithBudget(msgs, "sys", "", nil, thresholds, tracker)
	if changed || pruned[1].Content[0].Text != bigOut {
		t.Fatalf("real provider usage 7000 <= 9000 trigger must not prematurely prune history")
	}

	// Case 2: Provider reports real prompt was 9,500 tokens (above 9,000 trigger),
	// even if messages were dense high-token text below heuristic threshold -> pruning MUST fire!
	trackerHigh := newContextTokenTracker()
	trackerHigh.ObserveStep(msgs, "sys", nil, TokenUsage{Input: 2500, CacheRead: 7000, Output: 200})
	prunedHigh, changedHigh := pruneOldToolResultsWithBudget(msgs, "sys", "", nil, thresholds, trackerHigh)
	if !changedHigh || !strings.HasPrefix(prunedHigh[1].Content[0].Text, "[tool output pruned:") {
		t.Fatalf("expected real provider usage 9500 > 9000 trigger to prune older tool results")
	}

	// Case 3: InvalidateAnchor clears anchor and cache-warm flag while retaining calibrated scale.
	learnedScale := tracker.Scale()
	tracker.InvalidateAnchor(true)
	if tracker.IsPrefixCacheWarm() {
		t.Fatal("expected InvalidateAnchor(true) to clear prefixCacheWarm")
	}
	if tracker.Scale() != learnedScale || learnedScale >= 0.9 {
		t.Fatalf("expected learned tokenizer scale (~0.73) to survive anchor invalidation, got %.3f", tracker.Scale())
	}
}

type recordedCompactionReq struct {
	Body      []byte
	SessionID string
	ModelReq  map[string]any
}

type inPlaceCompactionClient struct {
	target llm.StreamTarget
	reqs   []recordedCompactionReq
	reply  func(call int, body map[string]any) (string, error)
}

func (c *inPlaceCompactionClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	t := c.target
	t.ProviderID = p
	t.ModelID = m
	return t
}

func (c *inPlaceCompactionClient) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var parsed map[string]any
	_ = json.Unmarshal(req.Body, &parsed)
	c.reqs = append(c.reqs, recordedCompactionReq{
		Body:      append([]byte(nil), req.Body...),
		SessionID: req.SessionID,
		ModelReq:  parsed,
	})
	sse, err := c.reply(len(c.reqs), parsed)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     make(http.Header),
	}, nil
}

func TestProactiveCompactionReusesWarmPromptCacheInPlace(t *testing.T) {
	ws := t.TempDir()
	// History with 2 prior user/assistant exchanges + Step 1 calls a tool so Step 2
	// triggers proactive compaction with a warm provider cache (CacheRead > 0).
	bigHistory := strings.Repeat("existing architecture context line for checkpoint\n", 180)
	history := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Initial spec: " + bigHistory}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Acknowledged spec."}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "More constraints: " + bigHistory}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Ready to implement."}}},
	}

	var summaryUsages []TokenUsage
	var compactedEvents []EngineEvent
	client := &inPlaceCompactionClient{
		target: llm.StreamTarget{
			BaseURL:      "https://api.openai.com/v1",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 24_000, // hardCap ~ 19,200 (after 4,800 output reserve)
			OutputLimit:  4096,
		},
		reply: func(call int, body map[string]any) (string, error) {
			switch call {
			case 1:
				// Step 1: report warm cache with 20,000 prompt tokens so Step 2 crosses hardCap (19,904)
				// while still leaving >2K headroom below ContextLimit (24,000) for in-place compaction.
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"glob","arguments":"{\"pattern\":\"*.go\"}"}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20000,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":18500}}}`,
					`data: [DONE]`,
				}, "\n") + "\n", nil
			case 2:
				// In-place compaction call MUST keep the exact same prompt_cache_key, tools, and system prompt!
				if body["prompt_cache_key"] != "ses_inplace_cache" {
					t.Errorf("in-place compaction dropped prompt_cache_key: %v", body["prompt_cache_key"])
				}
				if tools, _ := body["tools"].([]any); len(tools) == 0 {
					t.Errorf("in-place compaction must keep identical tools list to hit prompt cache")
				}
				msgs, _ := body["messages"].([]any)
				if len(msgs) < 2 {
					t.Fatalf("expected full conversation prefix in in-place compaction, got %d msgs", len(msgs))
				}
				sysMsg, _ := msgs[0].(map[string]any)
				if sysContent, _ := sysMsg["content"].(string); strings.Contains(sysContent, "context compaction engine") {
					t.Errorf("in-place compaction replaced main system prompt instead of reusing warm prefix")
				}
				lastMsg, _ := msgs[len(msgs)-1].(map[string]any)
				if txt, _ := lastMsg["content"].(string); !strings.Contains(txt, "<context_compaction_request>") {
					t.Errorf("expected trailing in-place compaction instruction, got: %q", txt)
				}
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"content":"## Checkpoint\n- Goal preserved\n- Next: finish"},"finish_reason":"stop"}],"usage":{"prompt_tokens":19500,"completion_tokens":60,"prompt_tokens_details":{"cached_tokens":19200}}}`,
					`data: [DONE]`,
				}, "\n") + "\n", nil
			case 3:
				// Post-compaction main Step 2 sees the <context_checkpoint> message and completes.
				msgs, _ := body["messages"].([]any)
				foundCheckpoint := false
				for _, rawMsg := range msgs {
					if m, ok := rawMsg.(map[string]any); ok {
						if content, _ := m["content"].(string); strings.Contains(content, "<context_checkpoint>") && strings.Contains(content, "Goal preserved") {
							foundCheckpoint = true
							break
						}
					}
				}
				if !foundCheckpoint {
					t.Errorf("expected post-compaction step to receive checkpoint, got: %#v", msgs)
				}
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"content":"All done after in-place compaction."},"finish_reason":"stop"}],"usage":{"prompt_tokens":2000,"completion_tokens":20}}`,
					`data: [DONE]`,
				}, "\n") + "\n", nil
			default:
				return "", fmt.Errorf("unexpected extra call %d (should not have fallen back to cold summarizer)", call)
			}
		},
	}

	eng := NewDefaultEngine(client)
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_inplace_cache",
		WorktreeDir: ws,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		History:     history,
		UserPrompt:  "Continue working",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			switch evt.Kind {
			case EngineSummaryUsage:
				if evt.Usage != nil {
					summaryUsages = append(summaryUsages, *evt.Usage)
				}
			case EngineContextCompacted:
				compactedEvents = append(compactedEvents, evt)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(client.reqs) != 3 {
		t.Fatalf("expected exactly 3 calls (step1, in-place compact, step2), got %d", len(client.reqs))
	}
	if len(summaryUsages) != 1 || summaryUsages[0].CacheRead != 19200 {
		t.Fatalf("expected 1 summary usage with 19200 cached tokens, got %+v", summaryUsages)
	}
	if len(compactedEvents) != 1 || compactedEvents[0].Metadata["proactive"] != true {
		t.Fatalf("expected proactive compaction event, got %+v", compactedEvents)
	}
}

func TestInPlaceCompactionFallsBackOnToolCallTruncationAndColdCache(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Spec requirement A"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Working on A"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Continue B"}}},
	}
	drv := newOpenAIDriver(false)

	for _, tc := range []struct {
		name         string
		cacheWarm    bool
		firstReply   string
		wantCalls    int
		wantFallback bool
	}{
		{
			name:         "tool call rejected and falls back to standalone summarizer",
			cacheWarm:    true,
			firstReply:   "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"bad_tc\",\"function\":{\"name\":\"glob\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
			wantCalls:    2,
			wantFallback: true,
		},
		{
			name:         "truncated length response rejected and falls back",
			cacheWarm:    true,
			firstReply:   "data: {\"choices\":[{\"delta\":{\"content\":\"partial summary\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
			wantCalls:    2,
			wantFallback: true,
		},
		{
			name:         "cold cache skips in-place and goes straight to standalone summarizer",
			cacheWarm:    false,
			firstReply:   "",
			wantCalls:    1,
			wantFallback: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &inPlaceCompactionClient{
				target: llm.StreamTarget{
					BaseURL:      "https://api.openai.com/v1",
					Protocol:     llm.ProtocolOpenAI,
					HasEndpoint:  true,
					ContextLimit: 128_000,
					OutputLimit:  4096,
				},
				reply: func(call int, body map[string]any) (string, error) {
					if tc.cacheWarm && call == 1 {
						return tc.firstReply, nil
					}
					// Standalone fallback uses compactionSystemPrompt and no tools.
					if tools, _ := body["tools"].([]any); len(tools) != 0 {
						t.Errorf("standalone fallback must not send tools, got %d", len(tools))
					}
					return "data: {\"choices\":[{\"delta\":{\"content\":\"Standalone checkpoint summary\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", nil
				},
			}
			eng := NewDefaultEngine(client)
			inPlace := &inPlaceCompactionContext{
				Driver:         drv,
				Target:         client.ResolveStreamTarget("openai", "gpt-5"),
				SessionID:      "ses_fallback",
				System:         "main system",
				Tools:          []ToolSpec{{Name: "glob", Description: "find files", Parameters: map[string]any{"type": "object"}}},
				MaxTokens:      4096,
				CacheWarm:      tc.cacheWarm,
				MeasuredTokens: 40_000,
			}
			out, info, ok := eng.compactContextOverflowWithInPlace(context.Background(), "openai", "gpt-5", msgs, 2, 2, inPlace, nil)
			if !ok || info == nil || len(out) == 0 {
				t.Fatalf("expected successful compaction via fallback, ok=%v info=%+v", ok, info)
			}
			if info.Summary != "Standalone checkpoint summary" {
				t.Fatalf("expected fallback summary, got %q", info.Summary)
			}
			if len(client.reqs) != tc.wantCalls {
				t.Fatalf("expected %d requests, got %d", tc.wantCalls, len(client.reqs))
			}
		})
	}
}

func TestOpenAIResponsesIncludesEncryptedReasoningForReasoningModelsWithoutEffort(t *testing.T) {
	drv := newOpenAIDriver(true)
	msgs := []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "hi"}}}}

	for _, modelID := range []string{"openai-codex-gpt-6.2-sol", "gpt-5", "o3", "o4-mini"} {
		req, err := drv.BuildRequest(ModelRequest{
			SessionID: "ses_resp_enc",
			Target:    llm.StreamTarget{ModelID: modelID, BaseURL: "http://127.0.0.1:5431/v1", Protocol: llm.ProtocolOpenAIResponses},
			Messages:  msgs,
		})
		if err != nil {
			t.Fatalf("%s: %v", modelID, err)
		}
		if !strings.Contains(string(req.Body), `"include":["reasoning.encrypted_content"]`) {
			t.Fatalf("expected %s Responses request to include reasoning.encrypted_content, got: %s", modelID, req.Body)
		}
	}

	for _, nonReasoning := range []string{"gpt-4o", "openai/gpt-oss-120b"} {
		req, err := drv.BuildRequest(ModelRequest{
			SessionID: "ses_resp_nonr",
			Target:    llm.StreamTarget{ModelID: nonReasoning, BaseURL: "http://127.0.0.1:5431/v1", Protocol: llm.ProtocolOpenAIResponses},
			Messages:  msgs,
		})
		if err != nil {
			t.Fatalf("%s: %v", nonReasoning, err)
		}
		if strings.Contains(string(req.Body), `"include"`) {
			t.Fatalf("non-reasoning model %s must not force reasoning.encrypted_content include, got: %s", nonReasoning, req.Body)
		}
	}
}

func TestEnginePromotedResponsesTransparentlyFallsBackToChatCompletionsOnUnsupportedEndpoint(t *testing.T) {
	var seenProtocols []llm.StreamProtocol
	target := llm.StreamTarget{
		BaseURL:        "http://127.0.0.1:5431/v1",
		Protocol:       llm.ProtocolOpenAIResponses,
		FallbackOpenAI: true,
		HasEndpoint:    true,
		ContextLimit:   400_000,
		OutputLimit:    16_384,
	}
	wrapped := &protocolCaptureClient{
		target: target,
		openFn: func(call int, req llm.StreamHTTPRequest) (*http.Response, error) {
			seenProtocols = append(seenProtocols, req.Target.Protocol)
			if call == 1 {
				return nil, &llm.StreamHTTPError{
					StatusCode: http.StatusBadRequest,
					Message:    "unsupported endpoint /v1/responses",
				}
			}
			// Fallback attempt must be ProtocolOpenAI with prompt_cache_key preserved.
			if !strings.Contains(string(req.Body), `"prompt_cache_key":"ses_resp_fallback"`) {
				t.Errorf("fallback chat request missing prompt_cache_key: %s", req.Body)
			}
			sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Fallback succeeded\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
		},
	}

	var reply string
	err := NewDefaultEngine(wrapped).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_resp_fallback",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "subapi",
		ModelID:     "openai-codex-gpt-6.2-sol",
		UserPrompt:  "hello",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineTextEnd {
				reply = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if reply != "Fallback succeeded" {
		t.Fatalf("unexpected reply %q", reply)
	}
	if len(seenProtocols) != 2 || seenProtocols[0] != llm.ProtocolOpenAIResponses || seenProtocols[1] != llm.ProtocolOpenAI {
		t.Fatalf("expected [openai-responses openai] fallback sequence, got %v", seenProtocols)
	}

	// Rejection case: a regular 400 (unrelated to unsupported endpoint) must NOT fall back.
	var nonFallbackCalls int
	errClient := &protocolCaptureClient{
		target: target,
		openFn: func(_ int, _ llm.StreamHTTPRequest) (*http.Response, error) {
			nonFallbackCalls++
			return nil, &llm.StreamHTTPError{
				StatusCode: http.StatusBadRequest,
				Message:    "invalid model parameter temperature",
			}
		},
	}
	if err := NewDefaultEngine(errClient).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_resp_no_fb",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "subapi",
		ModelID:     "openai-codex-gpt-6.2-sol",
		UserPrompt:  "hello",
	}); err == nil || nonFallbackCalls != 1 {
		t.Fatalf("ordinary 400 must fail immediately without fallback: err=%v calls=%d", err, nonFallbackCalls)
	}
}

type protocolCaptureClient struct {
	target llm.StreamTarget
	calls  int
	openFn func(call int, req llm.StreamHTTPRequest) (*http.Response, error)
}

func (c *protocolCaptureClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	t := c.target
	t.ProviderID = p
	t.ModelID = m
	return t
}

func (c *protocolCaptureClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.calls++
	return c.openFn(c.calls, req)
}

func TestProactiveInPlaceCompactionPreservesWarmPrefixAcrossFutileTightPrune(t *testing.T) {
	ws := t.TempDir()
	// History with large plain text (cannot be pruned under hardCap) PLUS one large
	// recent tool result (>800 bytes) that emergency tight-prune would mutate.
	largeText := strings.Repeat("immutable architectural decision line\n", 320) // ~12KB each
	recentRead := strings.Repeat("1: func recentKeptBeforeTightPrune() {}\n", 40)
	history := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: largeText}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: largeText}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: largeText}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "r_mid", Name: "read", Input: json.RawMessage(`{"filePath":"mid.go"}`)}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "r_mid", Text: recentRead}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Analyzed mid.go."}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Next step"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Working"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "Keep going"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "Still working"}}},
	}

	var inPlaceSawUnprunedRead bool
	client := &inPlaceCompactionClient{
		target: llm.StreamTarget{
			BaseURL:      "https://api.openai.com/v1",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 24_000, // hardCap = 19,904
			OutputLimit:  4096,
		},
		reply: func(call int, body map[string]any) (string, error) {
			switch call {
			case 1:
				// Step 1 succeeds with warm cache at 21,000 tokens (> hardCap 19,904 even
				// after pruning r_mid's ~437 tokens, yet 21,000+2048 < 24,000 for in-place).
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"g1","function":{"name":"glob","arguments":"{\"pattern\":\"*.go\"}"}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":21000,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":18500}}}`,
					`data: [DONE]`,
				}, "\n") + "\n", nil
			case 2:
				// Step 2 tight-prunes r_mid, STILL exceeds hardCap, and must run IN-PLACE compaction
				// using the UNMODIFIED warm pre-prune prefix (containing recentRead verbatim)!
				rawMsgs, _ := json.Marshal(body["messages"])
				if strings.Contains(string(rawMsgs), "recentKeptBeforeTightPrune") && !strings.Contains(string(rawMsgs), "[tool output pruned:") {
					inPlaceSawUnprunedRead = true
				}
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"content":"Warm in-place checkpoint"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20200,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":19800}}}`,
					`data: [DONE]`,
				}, "\n") + "\n", nil
			case 3:
				return "data: {\"choices\":[{\"delta\":{\"content\":\"Completed after warm checkpoint\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", nil
			default:
				return "", fmt.Errorf("unexpected extra call %d (should not fall back to cold summarizer)", call)
			}
		},
	}

	if err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_tight_prune_warm",
		WorktreeDir: ws,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		History:     history,
		UserPrompt:  "Continue",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(client.reqs) != 3 {
		t.Fatalf("expected 3 calls (step1, warm in-place, step2), got %d", len(client.reqs))
	}
	if !inPlaceSawUnprunedRead {
		t.Fatal("expected in-place compaction to reuse unmodified warm pre-prune prefix instead of post-tight-prune prefix")
	}
}
