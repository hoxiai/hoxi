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

type mockFastStreamClient struct {
	target   llm.StreamTarget
	calls    []llm.StreamHTTPRequest
	handlers []func(req llm.StreamHTTPRequest) (*http.Response, error)
}

func (m *mockFastStreamClient) ResolveStreamTarget(_, _ string) llm.StreamTarget {
	return m.target
}

func (m *mockFastStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	idx := len(m.calls)
	m.calls = append(m.calls, req)
	if idx < len(m.handlers) {
		return m.handlers[idx](req)
	}
	return m.handlers[len(m.handlers)-1](req)
}

func sseHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestAnthropicFastModeRequestAnd2xPricing(t *testing.T) {
	drv := newAnthropicDriver()
	opusTarget := llm.StreamTarget{
		ProviderID:  "anthropic",
		ModelID:     "claude-opus-5-5",
		BaseURL:     "https://api.anthropic.com",
		Protocol:    llm.ProtocolAnthropic,
		HasEndpoint: true,
		Cost:        llm.ModelPricing{InputPerM: 4, OutputPerM: 20},
	}

	httpReq, err := drv.BuildRequest(ModelRequest{
		Target:   opusTarget,
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "ping"}}}},
		FastMode: true,
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(httpReq.Body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["speed"] != "fast" {
		t.Fatalf("expected payload speed=fast, got %v", payload["speed"])
	}
	if httpReq.ExtraHeaders["anthropic-beta"] != anthropicFastModeBetaHeader {
		t.Fatalf("expected anthropic-beta=%q, got %+v", anthropicFastModeBetaHeader, httpReq.ExtraHeaders)
	}

	// Sonnet and third-party relays must not receive speed="fast".
	for _, unsupported := range []llm.StreamTarget{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", BaseURL: "https://api.anthropic.com", Protocol: llm.ProtocolAnthropic},
		{ProviderID: "custom-relay", ModelID: "claude-opus-5-5", BaseURL: "https://openrouter.ai/api/v1", Protocol: llm.ProtocolAnthropic},
	} {
		r, err := drv.BuildRequest(ModelRequest{
			Target:   unsupported,
			Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "ping"}}}},
			FastMode: true,
		})
		if err != nil {
			t.Fatalf("BuildRequest(%s/%s): %v", unsupported.ProviderID, unsupported.ModelID, err)
		}
		var p map[string]any
		_ = json.Unmarshal(r.Body, &p)
		if _, exists := p["speed"]; exists {
			t.Fatalf("unexpected speed field for %s/%s: %+v", unsupported.ProviderID, unsupported.ModelID, p)
		}
	}

	// Verify 2x cost multiplier when SSE reports speed="fast".
	sseBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1000,"output_tokens":0,"speed":"fast"}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ok"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":500,"speed":"fast"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	client := &mockFastStreamClient{
		target: opusTarget,
		handlers: []func(llm.StreamHTTPRequest) (*http.Response, error){
			func(llm.StreamHTTPRequest) (*http.Response, error) { return sseHTTPResponse(sseBody), nil },
		},
	}
	eng := NewDefaultEngine(client)
	var gotUsage *TokenUsage
	err = eng.RunTurn(context.Background(), TurnRequest{
		ProviderID: "anthropic",
		ModelID:    "claude-opus-5-5",
		UserPrompt: "hello",
		FastMode:   true,
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineTurnUsage && evt.Usage != nil {
				cp := *evt.Usage
				gotUsage = &cp
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if gotUsage == nil || gotUsage.ServiceTier != "fast" {
		t.Fatalf("expected ServiceTier=fast, got %+v", gotUsage)
	}
	// Standard cost = (1000*$4 + 500*$20)/1e6 = 0.014; 2x fast = 0.028.
	if diff := gotUsage.CostUSD - 0.028; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("expected 2x fast cost 0.028, got %f", gotUsage.CostUSD)
	}
}

func TestOpenAIAndCodexFastModeFallbackAndSubtaskSuppression(t *testing.T) {
	codexTarget := llm.StreamTarget{
		ProviderID:  "openai",
		ModelID:     "gpt-5.4-codex",
		BaseURL:     "https://chatgpt.com/backend-api/codex",
		Protocol:    llm.ProtocolOpenAIResponses,
		HasEndpoint: true,
		Cost:        llm.ModelPricing{InputPerM: 2, OutputPerM: 10},
	}
	okSSE := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		``,
		`data: {"type":"response.completed","response":{"service_tier":"priority","usage":{"input_tokens":1000,"output_tokens":200}}}`,
		``,
	}, "\n")

	client := &mockFastStreamClient{
		target: codexTarget,
		handlers: []func(llm.StreamHTTPRequest) (*http.Response, error){
			func(req llm.StreamHTTPRequest) (*http.Response, error) {
				var p map[string]any
				_ = json.Unmarshal(req.Body, &p)
				if p["service_tier"] == "priority" {
					return nil, &llm.StreamHTTPError{
						StatusCode: http.StatusBadRequest,
						Message:    "HTTP 400: Unknown parameter: 'service_tier'",
					}
				}
				return sseHTTPResponse(okSSE), nil
			},
			func(req llm.StreamHTTPRequest) (*http.Response, error) {
				return sseHTTPResponse(okSSE), nil
			},
		},
	}
	eng := NewDefaultEngine(client)

	// First turn: sends service_tier="priority", gets 400, automatically falls back without failing, and memoizes noFastMode.
	if err := eng.RunTurn(context.Background(), TurnRequest{
		ProviderID: "openai",
		ModelID:    "gpt-5.4-codex",
		UserPrompt: "task 1",
		FastMode:   true,
	}); err != nil {
		t.Fatalf("expected automatic fallback from 400 service_tier error, got: %v", err)
	}
	if len(client.calls) != 2 {
		t.Fatalf("expected 2 OpenStream calls (initial + stripped retry), got %d", len(client.calls))
	}
	var firstBody, secondBody map[string]any
	_ = json.Unmarshal(client.calls[0].Body, &firstBody)
	_ = json.Unmarshal(client.calls[1].Body, &secondBody)
	if firstBody["service_tier"] != "priority" {
		t.Fatalf("expected first request to carry service_tier=priority, got %v", firstBody["service_tier"])
	}
	if _, exists := secondBody["service_tier"]; exists {
		t.Fatalf("expected fallback retry to strip service_tier, got %v", secondBody["service_tier"])
	}

	// Second turn on the same engine: skips service_tier immediately due to noFastMode memoization.
	if err := eng.RunTurn(context.Background(), TurnRequest{
		ProviderID: "openai",
		ModelID:    "gpt-5.4-codex",
		UserPrompt: "task 2",
		FastMode:   true,
	}); err != nil {
		t.Fatalf("second RunTurn failed: %v", err)
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected memoized third call without retry, got %d total calls", len(client.calls))
	}

	// Subtask (SubtaskDepth > 0) on a fresh engine must never send service_tier even when FastMode=true.
	freshClient := &mockFastStreamClient{
		target: codexTarget,
		handlers: []func(llm.StreamHTTPRequest) (*http.Response, error){
			func(llm.StreamHTTPRequest) (*http.Response, error) { return sseHTTPResponse(okSSE), nil },
		},
	}
	freshEng := NewDefaultEngine(freshClient)
	var subUsage *TokenUsage
	if err := freshEng.RunTurn(context.Background(), TurnRequest{
		ProviderID:   "openai",
		ModelID:      "gpt-5.4-codex",
		UserPrompt:   "subtask",
		SubtaskDepth: 1,
		FastMode:     true,
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineTurnUsage && evt.Usage != nil {
				cp := *evt.Usage
				subUsage = &cp
			}
		}),
	}); err != nil {
		t.Fatalf("subtask RunTurn failed: %v", err)
	}
	var subBody map[string]any
	_ = json.Unmarshal(freshClient.calls[0].Body, &subBody)
	if _, exists := subBody["service_tier"]; exists {
		t.Fatalf("subtask must never inherit service_tier, got %v", subBody["service_tier"])
	}
	if subUsage == nil || subUsage.ServiceTier != "fast" {
		t.Fatalf("expected response service_tier=priority to still parse as ServiceTier=fast, got %+v", subUsage)
	}
}
