package llm

import (
	"context"
	"net/http"
	"time"
)

// StreamHTTPError carries an upstream non-2xx HTTP status code, parsed
// Retry-After hint, redacted error message, and sanitized upstream diagnostic
// metadata from StreamClient.OpenStream.
type StreamHTTPError struct {
	StatusCode int
	RetryAfter time.Duration
	Message    string
	Upstream   *UpstreamResponseDetails
}

func (e *StreamHTTPError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// StreamProtocol identifies the wire format used by a model endpoint.
type StreamProtocol string

const (
	ProtocolAnthropic       StreamProtocol = "anthropic"
	ProtocolOpenAI          StreamProtocol = "openai"
	ProtocolOpenAIResponses StreamProtocol = "openai-responses"
	ProtocolGoogle          StreamProtocol = "google"
)

// ModelPricing holds USD rates per 1,000,000 tokens for a resolved model.
type ModelPricing struct {
	InputPerM      float64
	OutputPerM     float64
	ReasoningPerM  float64
	CacheReadPerM  float64
	CacheWritePerM float64
}

// ComputeUSD calculates total step cost in USD from disjoint token buckets
// (where inTokens excludes cache hits/writes and outTokens excludes reasoning).
func (p ModelPricing) ComputeUSD(inTokens, outTokens, reasoningTokens, cacheReadTokens, cacheWriteTokens int) float64 {
	if p.InputPerM <= 0 && p.OutputPerM <= 0 && p.ReasoningPerM <= 0 && p.CacheReadPerM <= 0 && p.CacheWritePerM <= 0 {
		return 0
	}
	var total float64
	if inTokens > 0 && p.InputPerM > 0 {
		total += float64(inTokens) * p.InputPerM / 1_000_000
	}
	if outTokens > 0 && p.OutputPerM > 0 {
		total += float64(outTokens) * p.OutputPerM / 1_000_000
	}
	if reasoningTokens > 0 {
		rRate := p.ReasoningPerM
		if rRate <= 0 {
			rRate = p.OutputPerM
		}
		if rRate > 0 {
			total += float64(reasoningTokens) * rRate / 1_000_000
		}
	}
	if cacheReadTokens > 0 && p.CacheReadPerM > 0 {
		total += float64(cacheReadTokens) * p.CacheReadPerM / 1_000_000
	}
	if cacheWriteTokens > 0 && p.CacheWritePerM > 0 {
		total += float64(cacheWriteTokens) * p.CacheWritePerM / 1_000_000
	}
	return total
}

// TokenUsage contains disjoint billing buckets for one request or a sum of requests.
type TokenUsage struct {
	Input       int     `json:"input"`
	Output      int     `json:"output"`
	Reasoning   int     `json:"reasoning"`
	CacheRead   int     `json:"cacheRead"`
	CacheWrite  int     `json:"cacheWrite"`
	CostUSD     float64 `json:"costUSD"`
	ServiceTier string  `json:"serviceTier,omitempty"`
}

func (u *TokenUsage) Add(v TokenUsage) {
	u.Input += v.Input
	u.Output += v.Output
	u.Reasoning += v.Reasoning
	u.CacheRead += v.CacheRead
	u.CacheWrite += v.CacheWrite
	u.CostUSD += v.CostUSD
}

// StreamTarget describes a resolved model endpoint for streaming agent turns.
// Credentials remain encapsulated inside the provider implementation.
type StreamTarget struct {
	ProviderID   string
	ModelID      string
	BaseURL      string
	NPM          string
	Protocol     StreamProtocol
	HasEndpoint  bool
	ContextLimit int
	OutputLimit  int
	Cost         ModelPricing
	// Variants contains credential-free model options shared with the OpenCode
	// configuration (thinking budgets, adaptive effort, and Gemini thinking levels).
	Variants map[string]any
	// FallbackOpenAI marks a Claude target on an OpenAI-compatible gateway that
	// was promoted to native Anthropic /v1/messages so 404/405 falls back cleanly
	// to /v1/chat/completions.
	FallbackOpenAI bool
}

// StreamHTTPRequest carries a protocol-formatted JSON payload to OpenStream.
// Endpoint path resolution stays inside internal/provider.
type StreamHTTPRequest struct {
	Target       StreamTarget
	Body         []byte
	ExtraHeaders map[string]string
	// SessionID is an optional conversation identifier used to populate default
	// gateway prompt-cache affinity headers (x-session-affinity / X-Session-Id)
	// when not overridden by user-configured provider headers.
	SessionID string
}

// StreamClient opens authenticated streaming HTTP responses against user-configured
// providers without exposing API keys outside internal/provider.
type StreamClient interface {
	ResolveStreamTarget(providerID, modelID string) StreamTarget
	OpenStream(ctx context.Context, req StreamHTTPRequest) (*http.Response, error)
}

// StreamOutcomeReporter optionally records the terminal outcome of an opened
// 2xx SSE response stream so circuit breakers credit success only after a clean
// terminal frame and penalize mid-stream SSE, EOF, or stall failures.
type StreamOutcomeReporter interface {
	RecordStreamOutcome(providerID, modelID string, latencyMs int64, streamErr error)
}

// SubagentEconomyResolver optionally resolves a fast, low-cost tool-capable
// text model within the SAME configured provider for read-only exploration
// subagents (strictly honoring the no cross-provider implicit fallback rule).
type SubagentEconomyResolver interface {
	ResolveEconomyModel(providerID, currentModelID string) (modelID string, ok bool)
}
