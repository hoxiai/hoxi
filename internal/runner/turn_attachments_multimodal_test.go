package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

type stubTurnAudioClient struct {
	calls      int
	transcript string
}

func (s *stubTurnAudioClient) HasAudioTranscriber() bool { return true }

func (s *stubTurnAudioClient) TranscribeAudio(_ context.Context, _ llm.AudioTranscribeRequest) (llm.AudioTranscribeResponse, error) {
	s.calls++
	return llm.AudioTranscribeResponse{
		ProviderID: "whisper-local",
		ModelID:    "whisper-large-v3",
		Text:       s.transcript,
	}, nil
}

func TestNativeMultimodalAttachmentPassthroughVsOCRFallback(t *testing.T) {
	attDir := t.TempDir()
	workDir := t.TempDir()
	store := attachment.NewStore(attDir)

	pdfMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_mm",
		Directory: workDir,
		Filename:  "spec.pdf",
		Mime:      "application/pdf",
		Data:      []byte("%PDF-1.4 fake minimal pdf bytes"),
	})
	if err != nil {
		t.Fatalf("save pdf failed: %v", err)
	}
	audioMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_mm",
		Directory: workDir,
		Filename:  "meeting.mp3",
		Mime:      "audio/mpeg",
		Data:      []byte("ID3-fake-mp3-payload"),
	})
	if err != nil || audioMeta.Kind != attachment.KindAudio {
		t.Fatalf("save mp3 failed: meta=%+v err=%v", audioMeta, err)
	}
	videoMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_mm",
		Directory: workDir,
		Filename:  "demo.mp4",
		Mime:      "video/mp4",
		Data:      []byte("ftypisom-fake-mp4-payload"),
	})
	if err != nil || videoMeta.Kind != attachment.KindVideo {
		t.Fatalf("save mp4 failed: meta=%+v err=%v", videoMeta, err)
	}

	svc := &Service{attachments: store}
	rec := SessionRecord{
		ID:           "ses_mm",
		Runtime:      "opencode",
		WorktreePath: workDir,
	}
	parts := []MessagePart{
		{Type: "file", Filename: pdfMeta.Filename, Mime: pdfMeta.Mime, URL: pdfMeta.URL},
		{Type: "file", Filename: audioMeta.Filename, Mime: audioMeta.Mime, URL: audioMeta.URL},
		{Type: "file", Filename: videoMeta.Filename, Mime: videoMeta.Mime, URL: videoMeta.URL},
	}

	// 1. Gemini 2.5 Pro receives PDF, Audio, and Video natively as binary data: URLs in deliveryParts.
	_, gemDelivery, _ := svc.prepareTurnAttachmentsForModel(
		rec, "google", "gemini-2.5-pro", false, 1000, 1, "msg_u_1", "Summarize all three files", parts,
	)
	var gemFileMimes []string
	for _, dp := range gemDelivery {
		if dp.Type == "file" && strings.HasPrefix(dp.URL, "data:") {
			gemFileMimes = append(gemFileMimes, dp.Mime)
		}
	}
	if len(gemFileMimes) != 3 {
		t.Fatalf("expected Gemini 2.5 Pro to receive 3 native binary file parts (pdf/audio/video), got %v", gemFileMimes)
	}

	// 2. Claude Sonnet 4.5 receives PDF natively as binary file part, while audio/video stay path-referenced.
	_, claudeDelivery, _ := svc.prepareTurnAttachmentsForModel(
		rec, "anthropic", "claude-sonnet-4-5", false, 1001, 2, "msg_u_2", "Review spec", parts,
	)
	var claudeFileMimes []string
	for _, dp := range claudeDelivery {
		if dp.Type == "file" && strings.HasPrefix(dp.URL, "data:") {
			claudeFileMimes = append(claudeFileMimes, dp.Mime)
		}
	}
	if len(claudeFileMimes) != 1 || claudeFileMimes[0] != "application/pdf" {
		t.Fatalf("expected Claude Sonnet 4.5 to receive native application/pdf only, got %v", claudeFileMimes)
	}

	// 3. Text-only DeepSeek model does NOT receive unsupported binary PDF/audio/video file parts.
	_, dsDelivery, _ := svc.prepareTurnAttachmentsForModel(
		rec, "deepseek", "deepseek-chat", false, 1002, 3, "msg_u_3", "Read spec", parts,
	)
	for _, dp := range dsDelivery {
		if dp.Type == "file" {
			t.Fatalf("expected text-only deepseek-chat to use text/path fallback instead of binary file part: %+v", dp)
		}
	}

	// 4. With an AudioClient wired, uploading an audio attachment to a non-audio model
	// automatically transcribes the audio and injects [Audio Transcript] into text context.
	asrStub := &stubTurnAudioClient{transcript: "Action item: ship multi-protocol relay today."}
	svc.SetAudioClient(asrStub)
	_, dsAudioDelivery, _ := svc.prepareTurnAttachmentsForModel(
		rec, "deepseek", "deepseek-chat", false, 1003, 4, "msg_u_4", "Summarize meeting",
		[]MessagePart{{Type: "file", Filename: audioMeta.Filename, Mime: audioMeta.Mime, URL: audioMeta.URL}},
	)
	if asrStub.calls != 1 {
		t.Fatalf("expected 1 automatic TranscribeAudio call for non-audio model, got %d", asrStub.calls)
	}
	joinedText := ""
	for _, dp := range dsAudioDelivery {
		joinedText += dp.Text + "\n"
	}
	if !strings.Contains(joinedText, "[Audio Transcript]") || !strings.Contains(joinedText, asrStub.transcript) {
		t.Fatalf("expected [Audio Transcript] injected into prompt delivery parts, got %q", joinedText)
	}
}
