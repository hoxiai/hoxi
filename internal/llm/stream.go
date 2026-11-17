package llm

import (
	"context"
	"net/http"
)

// StreamProtocol identifies the wire format used by a model endpoint.
type StreamProtocol string

const (
	ProtocolAnthropic       StreamProtocol = "anthropic"
	ProtocolOpenAI          StreamProtocol = "openai"
	ProtocolOpenAIResponses StreamProtocol = "openai-responses"
	ProtocolGoogle          StreamProtocol = "google"
)

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
	// Variants contains credential-free model options shared with the OpenCode
	// configuration (thinking budgets, adaptive effort, and Gemini thinking levels).
	Variants map[string]any
}

// StreamHTTPRequest carries a protocol-formatted JSON payload to OpenStream.
// Endpoint path resolution stays inside internal/provider.
type StreamHTTPRequest struct {
	Target       StreamTarget
	Body         []byte
	ExtraHeaders map[string]string
}

// StreamClient opens authenticated streaming HTTP responses against user-configured
// providers without exposing API keys outside internal/provider.
type StreamClient interface {
	ResolveStreamTarget(providerID, modelID string) StreamTarget
	OpenStream(ctx context.Context, req StreamHTTPRequest) (*http.Response, error)
}

// SubagentEconomyResolver optionally resolves a fast, low-cost tool-capable
// text model within the SAME configured provider for read-only exploration
// subagents (strictly honoring the no cross-provider implicit fallback rule).
type SubagentEconomyResolver interface {
	ResolveEconomyModel(providerID, currentModelID string) (modelID string, ok bool)
}
