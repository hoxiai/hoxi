package llm

import (
	"context"
	"time"
)

// EmbeddingRequest describes a batch text embedding call routed strictly to a
// user-configured embedding provider (OpenAI /embeddings or Google :batchEmbedContents).
type EmbeddingRequest struct {
	ProviderID string
	ModelID    string
	Inputs     []string
	Timeout    time.Duration
}

// EmbeddingResponse carries normalized float32 vectors aligned 1:1 with Inputs.
type EmbeddingResponse struct {
	ProviderID string
	ModelID    string
	Vectors    [][]float32
}

// EmbeddingClient generates vector embeddings for semantic code and session search.
type EmbeddingClient interface {
	HasEmbeddingModel() bool
	EmbedTexts(ctx context.Context, req EmbeddingRequest) (EmbeddingResponse, error)
}
