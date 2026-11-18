package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

type recordingAnthropicClient struct {
	mu          sync.Mutex
	target      llm.StreamTarget
	requests    [][]byte
	outcomes    []error
	latenciesMs []int64
	onOpen      func(call int, body []byte) (*http.Response, error)
}

func (c *recordingAnthropicClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	t := c.target
	if t.ProviderID == "" {
		t.ProviderID = providerID
	}
	if t.ModelID == "" {
		t.ModelID = modelID
	}
	return t
}

func (c *recordingAnthropicClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.mu.Lock()
	c.requests = append(c.requests, append([]byte(nil), req.Body...))
	call := len(c.requests)
	c.mu.Unlock()
	return c.onOpen(call, req.Body)
}

func (c *recordingAnthropicClient) RecordStreamOutcome(_ string, _ string, latencyMs int64, streamErr error) {
	c.mu.Lock()
	c.outcomes = append(c.outcomes, streamErr)
	c.latenciesMs = append(c.latenciesMs, latencyMs)
	c.mu.Unlock()
}

func TestAnthropicMultiBlockRedactedThinkingAndNoDuplicateThinkSig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hoxi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	step1SSE := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"Prefix ","signature":""}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"block zero"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_zero"}}`,
		"",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		"",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Checking repo..."}}`,
		"",
		`data: {"type":"content_block_stop","index":1}`,
		"",
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"Interleaved second thought"}}`,
		"",
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"signature_delta","signature":"sig_two"}}`,
		"",
		`data: {"type":"content_block_stop","index":2}`,
		"",
		`data: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"enc_redacted_99"}}`,
		"",
		`data: {"type":"content_block_stop","index":3}`,
		"",
		`data: {"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"toolu_1","name":"glob","input":{}}}`,
		"",
		`data: {"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":\"*.md\"}"}}`,
		"",
		`data: {"type":"content_block_stop","index":4}`,
		"",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	step2SSE := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Final check"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_final"}}`,
		"",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Done analyzing."}}`,
		"",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	sc := &recordingAnthropicClient{
		target: llm.StreamTarget{
			Protocol:     llm.ProtocolAnthropic,
			HasEndpoint:  true,
			ContextLimit: 200000,
			OutputLimit:  16384,
			Variants: map[string]any{
				"high": map[string]any{"thinking": map[string]any{"type": "adaptive"}, "effort": "high"},
			},
		},
		onOpen: func(call int, _ []byte) (*http.Response, error) {
			payload := step1SSE
			if call == 2 {
				payload = step2SSE
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		},
	}

	var thinkEnds []agent.EngineEvent
	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_multi_think",
		WorktreeDir: dir,
		Mode:        "plan",
		ProviderID:  "subapi",
		ModelID:     "claude-opus-4-6",
		Effort:      "high",
		UserPrompt:  "analyze project",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineThinkingEnd {
				thinkEnds = append(thinkEnds, evt)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	// Step 1 had 2 thinking blocks + 1 redacted_thinking block; Step 2 had 1 thinking block (4 total, zero duplicate think_sig_*).
	if len(thinkEnds) != 4 {
		t.Fatalf("expected exactly 4 EngineThinkingEnd events without duplicate think_sig parts, got %d: %+v", len(thinkEnds), thinkEnds)
	}
	for _, ev := range thinkEnds {
		if strings.HasPrefix(ev.PartID, "think_sig") {
			t.Fatalf("unexpected duplicate think_sig part emitted: %+v", ev)
		}
	}
	if thinkEnds[0].Text != "Prefix block zero" || thinkEnds[0].Signature != "sig_zero" {
		t.Fatalf("unexpected block 0 thinking/signature: %+v", thinkEnds[0])
	}
	if thinkEnds[1].Text != "Interleaved second thought" || thinkEnds[1].Signature != "sig_two" {
		t.Fatalf("unexpected block 2 thinking/signature: %+v", thinkEnds[1])
	}

	// Verify Step 2 outgoing request kept each block's signature separate and replayed redacted_thinking.
	var step2Payload struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(sc.requests[1], &step2Payload); err != nil {
		t.Fatal(err)
	}
	asstContent := step2Payload.Messages[1].Content
	if len(asstContent) != 5 {
		t.Fatalf("expected 5 preserved assistant blocks in Step 2 request, got %d: %+v", len(asstContent), asstContent)
	}
	if asstContent[0]["signature"] != "sig_zero" || asstContent[2]["signature"] != "sig_two" {
		t.Fatalf("expected separate signatures sig_zero and sig_two, got %+v and %+v", asstContent[0], asstContent[2])
	}
	if asstContent[3]["type"] != "redacted_thinking" || asstContent[3]["data"] != "enc_redacted_99" {
		t.Fatalf("expected redacted_thinking block to replay intact, got %+v", asstContent[3])
	}
	if len(sc.outcomes) != 2 || sc.outcomes[0] != nil || sc.outcomes[1] != nil {
		t.Fatalf("expected 2 clean stream outcomes recorded, got %+v", sc.outcomes)
	}
}

func TestEngineSelfHealsInvalidThinkingSignature400AndRefusesOther400s(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	step1SSE := strings.Join([]string{
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Reading main.go"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"proxy_translated_bad_sig"}}`,
		"",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_read","name":"read","input":{}}}`,
		"",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"filePath\":\"main.go\"}"}}`,
		"",
		`data: {"type":"content_block_stop","index":1}`,
		"",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	step2RecoveredSSE := strings.Join([]string{
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Project analysis complete."}}`,
		"",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	var sigResetCount atomic.Int32
	sc := &recordingAnthropicClient{
		target: llm.StreamTarget{
			Protocol:     llm.ProtocolAnthropic,
			HasEndpoint:  true,
			ContextLimit: 200000,
			OutputLimit:  16384,
			Variants: map[string]any{
				"high": map[string]any{"thinking": map[string]any{"type": "adaptive"}, "effort": "high"},
			},
		},
		onOpen: func(call int, body []byte) (*http.Response, error) {
			switch call {
			case 1:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(step1SSE)),
					Header:     make(http.Header),
				}, nil
			case 2:
				// Simulate subapi routing Step 2 to Vertex (`req_vrtx_...`) which rejects Step 1's translated signature.
				return nil, &llm.StreamHTTPError{
					StatusCode: http.StatusBadRequest,
					Message:    `HTTP 400: {"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid 'signature' in 'thinking' block"},"request_id":"req_vrtx_011Ch8g7JuKgRv4u3r3Pysw"}`,
				}
			case 3:
				if strings.Contains(string(body), `"role":"assistant"`) || strings.Contains(string(body), `"type":"thinking"`) {
					t.Fatalf("expected Step 2 recovery request to fold unsigned assistant tool loop so forced-thinking proxies cannot reject messages.1, got: %s", body)
				}
				if !strings.Contains(string(body), `completed_tool_steps`) || !strings.Contains(string(body), `package main`) {
					t.Fatalf("expected Step 1 completed read output to remain intact in <completed_tool_steps>: %s", body)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(step2RecoveredSSE)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, errors.New("unexpected extra upstream call")
			}
		},
	}

	var finalAnswer string
	eng := agent.NewDefaultEngine(sc)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_sig_recover",
		WorktreeDir: dir,
		Mode:        "plan",
		ProviderID:  "subapi",
		ModelID:     "antigravity-claude-opus-5-5-high",
		Effort:      "high",
		UserPrompt:  "analyze project",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineThinkingSignatureReset {
				sigResetCount.Add(1)
			}
			if evt.Kind == agent.EngineTextEnd {
				finalAnswer = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected Invalid signature in thinking block 400 to self-heal, got: %v", err)
	}
	if sigResetCount.Load() != 1 {
		t.Fatalf("expected 1 EngineThinkingSignatureReset event, got %d", sigResetCount.Load())
	}
	if finalAnswer != "Project analysis complete." {
		t.Fatalf("unexpected final answer after recovery: %q", finalAnswer)
	}

	// Refusal path: non-thinking 400 (e.g. invalid tool schema) must still fail immediately without retrying.
	badSchemaClient := &recordingAnthropicClient{
		target: sc.target,
		onOpen: func(_ int, _ []byte) (*http.Response, error) {
			return nil, &llm.StreamHTTPError{
				StatusCode: http.StatusBadRequest,
				Message:    `HTTP 400: {"type":"error","error":{"type":"invalid_request_error","message":"tools.0.input_schema: invalid schema"}}`,
			}
		},
	}
	badErr := agent.NewDefaultEngine(badSchemaClient).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bad_schema",
		WorktreeDir: dir,
		Mode:        "plan",
		ProviderID:  "subapi",
		ModelID:     "antigravity-claude-opus-5-5-high",
		Effort:      "high",
		UserPrompt:  "analyze",
	})
	if badErr == nil || len(badSchemaClient.requests) != 1 {
		t.Fatalf("expected non-thinking 400 to fail on attempt 1 without retry, err=%v calls=%d", badErr, len(badSchemaClient.requests))
	}
}

func TestOpenAIChatForcedThinkingProxyFoldsMultiStepLoopAndFallsBackOn404Messages(t *testing.T) {
	dir := t.TempDir()
	// 1. Verify FallbackOpenAI transparently switches from Anthropic /v1/messages (404) to OpenAI /v1/chat/completions
	// AND proactively folds multi-step unsigned tool loops when the proxy forces thinking upstream.
	step1ChatSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"Checking git status","content":"Running step 1","tool_calls":[{"index":0,"id":"c1","function":{"name":"bash","arguments":"{\"command\":\"git status\"}"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	step2ChatSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"Running go test","tool_calls":[{"index":0,"id":"c2","function":{"name":"bash","arguments":"{\"command\":\"go test\"}"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	step3ChatSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"Summarizing audit","content":"All audit checks green."},"finish_reason":"stop"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	sc := &recordingAnthropicClient{
		target: llm.StreamTarget{
			Protocol:       llm.ProtocolAnthropic,
			FallbackOpenAI: true,
			HasEndpoint:    true,
			ContextLimit:   200000,
			OutputLimit:    16384,
		},
		onOpen: func(call int, body []byte) (*http.Response, error) {
			if call == 1 {
				// Simulate OpenAI-only proxy returning 404 on /v1/messages.
				return nil, &llm.StreamHTTPError{StatusCode: http.StatusNotFound, Message: "404 page not found"}
			}
			// Every subsequent call must be OpenAI chat/completions; reject ANY assistant message in `messages`
			// (simulating Sub2API/Vertex `-high` model rejecting unsigned assistant messages!).
			if strings.Contains(string(body), `"role":"assistant"`) {
				return nil, &llm.StreamHTTPError{
					StatusCode: http.StatusBadRequest,
					Message:    `HTTP 400: {"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid 'signature' in 'thinking' block"},"request_id":"req_vrtx_test"}`,
				}
			}
			switch call {
			case 2:
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(step1ChatSSE)), Header: make(http.Header)}, nil
			case 4:
				// Call 3 was Step 2's initial unsigned attempt (rejected above -> folded); Call 4 is Step 2's folded retry!
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(step2ChatSSE)), Header: make(http.Header)}, nil
			case 5:
				// Step 3 proactively folds without even needing a 400 retry!
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(step3ChatSSE)), Header: make(http.Header)}, nil
			default:
				return nil, errors.New("unexpected call")
			}
		},
	}

	var ranCmds []string
	var finalReply string
	err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_forced_high",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "subapi",
		ModelID:     "antigravity-claude-opus-5-5-high",
		UserPrompt:  "Hoxi full audit",
		CommandRunner: func(_ context.Context, _, cmd string) (string, int, error) {
			ranCmds = append(ranCmds, cmd)
			return "ok: " + cmd, 0, nil
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineTextEnd {
				finalReply = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected 3-step bash audit on forced-thinking OpenAI proxy to succeed, got: %v", err)
	}
	if len(ranCmds) != 2 || finalReply != "All audit checks green." {
		t.Fatalf("unexpected execution: cmds=%v reply=%q", ranCmds, finalReply)
	}
}
