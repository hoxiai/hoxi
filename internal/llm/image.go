package llm

import (
	"context"
	"time"
)

// ImageInput carries one reference image supplied by the user (or prior turn)
// for multimodal image generation and editing.
type ImageInput struct {
	Mime string
	Data []byte
}

// GeneratedImage holds one decoded image returned by an image model.
type GeneratedImage struct {
	Mime          string
	Data          []byte
	RevisedPrompt string
}

// ImageModelOption describes one configured local/custom image generation model.
type ImageModelOption struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Name       string `json:"name,omitempty"`
	SupportsIn bool   `json:"supportsImageInput"`
}

// ImageRequest describes one image generation or image-editing turn.
type ImageRequest struct {
	ProviderID  string
	ModelID     string
	Prompt      string
	AspectRatio string
	ImageSize   string
	Count       int
	InputImages []ImageInput
	Timeout     time.Duration
}

// ImageResponse carries the text commentary and generated image blobs.
type ImageResponse struct {
	ProviderID   string
	ModelID      string
	Text         string
	Images       []GeneratedImage
	InputTokens  int
	OutputTokens int
}

// ImageClient executes native image generation turns across Google Gemini,
// Google Imagen, and OpenAI-compatible image endpoints without exposing
// provider credentials or HTTP wire details to runner/gateway packages.
type ImageClient interface {
	IsImageModel(providerID, modelID string) bool
	ListImageModels() []ImageModelOption
	ResolveImageModel(preferredProviderID, preferredModelID string) (providerID, modelID string, ok bool)
	GenerateImage(ctx context.Context, req ImageRequest) (ImageResponse, error)
}
