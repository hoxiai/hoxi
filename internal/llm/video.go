package llm

import (
	"context"
	"time"
)

// GeneratedVideo holds one decoded video clip returned by a video generation model.
type GeneratedVideo struct {
	Mime            string
	Data            []byte
	DurationSeconds int
	RevisedPrompt   string
}

// VideoModelOption describes one configured local/custom video generation model.
type VideoModelOption struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Name       string `json:"name,omitempty"`
	SupportsIn bool   `json:"supportsImageInput"`
}

// VideoRequest describes one video generation (text-to-video or image-to-video) turn.
type VideoRequest struct {
	ProviderID      string
	ModelID         string
	Prompt          string
	AspectRatio     string
	Resolution      string
	DurationSeconds int
	InputImages     []ImageInput
	Timeout         time.Duration
}

// VideoResponse carries any commentary and generated video blobs.
type VideoResponse struct {
	ProviderID string
	ModelID    string
	Text       string
	Videos     []GeneratedVideo
}

// VideoClient executes native video generation turns across Google Veo,
// Alibaba DashScope Wan, Volcengine Ark Seedance, Z.AI CogVideo, and
// OpenAI-compatible /videos endpoints without exposing provider credentials
// to runner/gateway packages.
type VideoClient interface {
	IsVideoModel(providerID, modelID string) bool
	ListVideoModels() []VideoModelOption
	ResolveVideoModel(preferredProviderID, preferredModelID string) (providerID, modelID string, ok bool)
	GenerateVideo(ctx context.Context, req VideoRequest) (VideoResponse, error)
}
