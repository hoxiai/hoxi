package llm

import (
	"context"
	"encoding/json"
	"time"
)

// ImageTranslateRequest describes one image translation request routed to a
// configured local or remote gateway supporting image translation.
type ImageTranslateRequest struct {
	ProviderID     string
	ModelID        string
	Image          string // data:image/... URL or public https:// URL
	SourceLanguage string // e.g. "zh", "en", "ru"
	TargetLanguage string // e.g. "ru", "en", "zh"
	OutputMode     ImageTranslateOutputMode
	RequestID      string
	Timeout        time.Duration
}

// ImageTranslateResponse carries the translated scene (with remote image layers
// already inlined as data URLs by the host) and optional raster preview URLs.
type ImageTranslateResponse struct {
	ProviderID    string          `json:"providerID,omitempty"`
	ModelID       string          `json:"modelID,omitempty"`
	Resolution    string          `json:"resolution,omitempty"`
	FinalImageURL string          `json:"finalImageUrl,omitempty"`
	InPaintingURL string          `json:"inPaintingUrl,omitempty"`
	Scene         json.RawMessage `json:"scene,omitempty"`
}

// ImageTranslateClient translates product/poster images via configured gateways
// without exposing upstream provider credentials outside internal/provider.
type ImageTranslateClient interface {
	ListImageTranslateModels(mode ImageTranslateOutputMode) []SlotModelOption
	ResolveImageTranslateModel(mode ImageTranslateOutputMode, preferredProviderID, preferredModelID string) (providerID, modelID, resolution string, ok bool)
	TranslateImage(ctx context.Context, req ImageTranslateRequest) (ImageTranslateResponse, error)
}
