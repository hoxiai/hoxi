package llm

import (
	"context"
	"time"
)

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

// AudioClient executes speech-to-text transcription on user-configured endpoints.
type AudioClient interface {
	HasAudioTranscriber() bool
	TranscribeAudio(ctx context.Context, req AudioTranscribeRequest) (AudioTranscribeResponse, error)
}
