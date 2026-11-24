package llm

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidMediaRole indicates a malformed media role specification or unknown field.
var ErrInvalidMediaRole = errors.New("invalid media role specification")

// MediaRolePrefs stores optional user-selected default models for media
// generation, image translation, vision/utility slots, and voice transcription
// when multiple local providers are configured.
// Empty strings mean "auto-select best configured model".
type MediaRolePrefs struct {
	ImageModel                string `json:"imageModel,omitempty"`
	ImageEditModel            string `json:"imageEditModel,omitempty"`
	ImageTranslateRasterModel string `json:"imageTranslateRasterModel,omitempty"`
	ImageTranslateEditorModel string `json:"imageTranslateEditorModel,omitempty"`
	VideoModel                string `json:"videoModel,omitempty"`
	SpeechModel               string `json:"speechModel,omitempty"`
	TranscribeModel           string `json:"transcribeModel,omitempty"`
	VisionModel               string `json:"visionModel,omitempty"`
	TextTranslateModel        string `json:"textTranslateModel,omitempty"`
	EmbeddingModel            string `json:"embeddingModel,omitempty"`
	RerankModel               string `json:"rerankModel,omitempty"`
}

// AudioModelOption describes one configured speech synthesis (TTS) or ASR model.
type AudioModelOption struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Name       string `json:"name,omitempty"`
}

// AudioTranscribeRequest describes a single speech-to-text transcription call
// routed strictly to a user-configured ASR or multimodal Gemini provider.
type AudioTranscribeRequest struct {
	ProviderID string
	ModelID    string
	Filename   string
	Mime       string
	Data       []byte
	Language   string
	Prompt     string
	Timeout    time.Duration
}

// AudioTranscribeResponse carries the transcribed text and resolved model.
type AudioTranscribeResponse struct {
	Text       string `json:"text"`
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// SpeechRequest describes one text-to-speech synthesis turn.
type SpeechRequest struct {
	ProviderID string
	ModelID    string
	Input      string
	Voice      string
	Format     string
	Speed      float64
	Timeout    time.Duration
}

// SpeechResponse carries synthesized audio bytes and resolved model metadata.
type SpeechResponse struct {
	ProviderID string
	ModelID    string
	Mime       string
	Data       []byte
}

// AudioClient executes speech-to-text transcription and text-to-speech synthesis
// on user-configured endpoints.
type AudioClient interface {
	HasAudioTranscriber() bool
	TranscribeAudio(ctx context.Context, req AudioTranscribeRequest) (AudioTranscribeResponse, error)
}
