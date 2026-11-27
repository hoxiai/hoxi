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
		{"openai relay miss sibling", llm.ProtocolOpenAI, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_cache_miss_tokens":100}}` + "\n\ndata: [DONE]\n\n", TokenUsage{Input: 100, Output: 50}},
		{"responses", llm.ProtocolOpenAIResponses, `data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":80,"cache_write_tokens":15},"output_tokens_details":{"reasoning_tokens":30}}}}` + "\n\n", TokenUsage{Input: 5, Output: 20, Reasoning: 30, CacheRead: 80, CacheWrite: 15}},
		{"responses cache write omitempty", llm.ProtocolOpenAIResponses, `data: {"type":"response.completed","response":{"usage":{"input_tokens":1000,"output_tokens":40,"input_tokens_details":{"cache_write_tokens":900}}}}` + "\n\n", TokenUsage{Input: 100, Output: 40, CacheWrite: 900}},
		{"gemini", llm.ProtocolGoogle, `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":30,"cachedContentTokenCount":80}}` + "\n\n", TokenUsage{Input: 30, Output: 20, Reasoning: 30, CacheRead: 80}},
		{"anthropic", llm.ProtocolAnthropic, `data: {"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":80,"cache_creation_input_tokens":10}}}` + "\n\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}` + "\n\n", TokenUsage{Input: 20, Output: 50, CacheRead: 80, CacheWrite: 10}},
		{"anthropic cold write omitempty", llm.ProtocolAnthropic, `data: {"type":"message_start","message":{"usage":{"input_tokens":20,"cache_creation_input_tokens":980}}}` + "\n\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}` + "\n\n", TokenUsage{Input: 20, Output: 50, CacheWrite: 980}},
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
			tc.want.ReportedCacheInput = tc.want.Input + tc.want.CacheRead + tc.want.CacheWrite
			if got != tc.want {
				t.Fatalf("usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOmittedAndMalformedCacheTelemetryDropDetection(t *testing.T) {
	tracker := newContextTokenTracker()
	target := llm.StreamTarget{ProviderID: "subapi", ModelID: "gpt-6.2"}

	// Providers that never report cache reads must never trigger false drops.
	coldOmitted := TokenUsage{Input: 4200}
	if drop := tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, coldOmitted); drop != nil {
		t.Fatalf("unexpected drop on first cold omitted step: %+v", drop)
	}
	if drop := tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, TokenUsage{Input: 5200}); drop != nil {
		t.Fatalf("unexpected drop on repeated cold omitted step: %+v", drop)
	}

	// Malformed/missing values must never populate ReportedCacheInput or allow sibling override.
	u := TokenUsage{Input: 4200}
	for _, raw := range []any{nil, "0", -1.0, 0.5, 5000.0} {
		recordCacheCoverage(&u, raw)
		if u.ReportedCacheInput != 0 {
			t.Fatalf("invalid cache telemetry %v counted as reported", raw)
		}
	}
	malformedWithSibling := TokenUsage{Input: 100, CacheWrite: 900}
	recordCacheCoverageFromMap(&malformedWithSibling, map[string]any{
		"cached_tokens":      "bad",
		"cache_write_tokens": float64(900),
	}, "cached_tokens", "cache_write_tokens")
	if malformedWithSibling.ReportedCacheInput != 0 {
		t.Fatalf("malformed primary field must not be overridden by sibling: %+v", malformedWithSibling)
	}

	// Empty usage after a warm step must not report a drop, while preserving the fingerprint.
	warm := TokenUsage{CacheRead: 4000, ReportedCacheInput: 4000}
	tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, warm)
	if drop := tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, TokenUsage{}); drop != nil {
		t.Fatalf("unexpected drop on empty usage: %+v", drop)
	}
	if !tracker.lastFingerprint.valid || tracker.lastFingerprint.cacheRead != 0 {
		t.Fatal("missing telemetry must preserve the history fingerprint for the next turn")
	}

	// Warm cache followed by a nonzero-input step with omitted cache telemetry
	// (e.g. Gemini proto3 / omitempty relays) must surface the drop with
	// cache_telemetry_omitted and reset baseline so subsequent misses do not spam.
	tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, warm)
	recordCacheCoverage(&u, nil)
	drop := tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, u)
	if drop == nil || drop.Cause != "local_prefix_unchanged+cache_telemetry_omitted" || drop.Before != 4000 || drop.After != 0 {
		t.Fatalf("expected omitted-telemetry drop after warm cache, got %+v", drop)
	}
	if !tracker.lastFingerprint.valid || tracker.lastFingerprint.cacheRead != 0 {
		t.Fatal("omitted-telemetry drop must preserve history fingerprint and reset cacheRead baseline")
	}
	if second := tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, TokenUsage{Input: 4800}); second != nil {
		t.Fatalf("subsequent cold omitted step must not duplicate drop alert: %+v", second)
	}

	// Explicit zero cache read preserves the clean local_prefix_unchanged cause.
	tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, warm)
	recordCacheCoverage(&u, float64(0))
	if drop := tracker.ObservePrefixCacheStability("s", target, "s", "system", nil, nil, u); drop == nil || drop.Cause != "local_prefix_unchanged" || drop.Before != 4000 || drop.After != 0 {
		t.Fatalf("expected explicit-zero drop, got %+v", drop)
	}
}
