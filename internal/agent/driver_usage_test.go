package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestDriverUsageUsesDisjointTokenBuckets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proto llm.StreamProtocol
		sse   string
		want  TokenUsage
	}{
		{"openai", llm.ProtocolOpenAI, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":30}}}` + "\n\ndata: [DONE]\n\n", TokenUsage{Input: 20, Output: 20, Reasoning: 30, CacheRead: 80}},
		{"openai openrouter cache write", llm.ProtocolOpenAI, `data: {"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":200,"cache_write_tokens":750}}}` + "\n\ndata: [DONE]\n\n", TokenUsage{Input: 50, Output: 40, CacheRead: 200, CacheWrite: 750}},
		{"openai cache creation details", llm.ProtocolOpenAI, `data: {"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":0,"cache_creation_input_tokens":900}}}` + "\n\ndata: [DONE]\n\n", TokenUsage{Input: 100, Output: 40, CacheWrite: 900}},
		{"openai relay", llm.ProtocolOpenAI, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_cache_hit_tokens":80,"completion_tokens_details":{"reasoning_tokens":30}}}` + "\n\ndata: [DONE]\n\n", TokenUsage{Input: 20, Output: 20, Reasoning: 30, CacheRead: 80}},
		{"responses", llm.ProtocolOpenAIResponses, `data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":80,"cache_write_tokens":15},"output_tokens_details":{"reasoning_tokens":30}}}}` + "\n\n", TokenUsage{Input: 5, Output: 20, Reasoning: 30, CacheRead: 80, CacheWrite: 15}},
		{"gemini", llm.ProtocolGoogle, `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":30,"cachedContentTokenCount":80}}` + "\n\n", TokenUsage{Input: 30, Output: 20, Reasoning: 30, CacheRead: 80}},
		{"anthropic", llm.ProtocolAnthropic, `data: {"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":80,"cache_creation_input_tokens":10}}}` + "\n\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}` + "\n\n", TokenUsage{Input: 20, Output: 50, CacheRead: 80, CacheWrite: 10}},
		{"malformed subtotals", llm.ProtocolOpenAI, `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":8,"cache_write_tokens":50},"completion_tokens_details":{"reasoning_tokens":20}}}` + "\n\ndata: [DONE]\n\n", TokenUsage{CacheRead: 8, CacheWrite: 2, Reasoning: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drv, _ := newDriverFactory().Create(tc.proto)
			var got TokenUsage
			if err := drv.ParseSSE(context.Background(), strings.NewReader(tc.sse), func(e StreamEvent) {
				if e.Usage != nil {
					got = *e.Usage
				}
			}); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}
