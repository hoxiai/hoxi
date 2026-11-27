package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

type stubProtocolStreamClient struct {
	protocols map[string]llm.StreamProtocol
}

func (s *stubProtocolStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	proto := s.protocols[providerID]
	if proto == "" {
		proto = llm.ProtocolOpenAI
	}
	return llm.StreamTarget{ProviderID: providerID, ModelID: modelID, Protocol: proto, HasEndpoint: true}
}

func (s *stubProtocolStreamClient) OpenStream(_ context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	return nil, llm.ErrUnavailable
}

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

func TestNativeAudioAttachmentTightensToGoogleProtocolAndASRTranscriptReplay(t *testing.T) {
	attDir := t.TempDir()
	workDir := t.TempDir()
	store := attachment.NewStore(attDir)

	audioMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_nat_audio",
		Directory: workDir,
		Filename:  "voice.wav",
		Mime:      "audio/wav",
		Data:      []byte("RIFF-fake-wav-payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	asrStub := &stubTurnAudioClient{transcript: "Fix the audio drop bug on non-Google drivers."}
	svc := &Service{attachments: store}
	svc.SetAudioClient(asrStub)
	svc.SetStreamClient(&stubProtocolStreamClient{
		protocols: map[string]llm.StreamProtocol{
			"google":     llm.ProtocolGoogle,
			"openai":     llm.ProtocolOpenAI,
			"openrouter": llm.ProtocolOpenAI,
			"anthropic":  llm.ProtocolAnthropic,
		},
	})

	natRec := SessionRecord{
		ID:           "ses_nat_audio",
		Runtime:      string(RuntimeNative),
		WorktreePath: workDir,
	}
	audioPart := []MessagePart{{Type: "file", Filename: audioMeta.Filename, Mime: audioMeta.Mime, URL: audioMeta.URL}}

	// 1. Native Google Gemini protocol passes raw audio inline without calling ASR.
	_, googleDelivery, _ := svc.prepareTurnAttachmentsForModel(
		natRec, "google", "gemini-2.5-pro", false, 2000, 1, "msg_u_g", "Listen", audioPart,
	)
	if asrStub.calls != 0 {
		t.Fatalf("expected 0 ASR calls for native google protocol, got %d", asrStub.calls)
	}
	hasRawAudio := false
	for _, dp := range googleDelivery {
		if dp.Type == "file" && dp.Mime == "audio/wav" && strings.HasPrefix(dp.URL, "data:") {
			hasRawAudio = true
		}
	}
	if !hasRawAudio {
		t.Fatalf("expected native google protocol to receive raw audio data URL, got %+v", googleDelivery)
	}

	// 2. Non-Google protocols (even for model IDs containing "audio", "omni", or "gemini" on an OpenAI proxy)
	// MUST transcribe via ASR and NOT pass raw audio binary into deliveryParts.
	for idx, tc := range []struct {
		prov  string
		model string
	}{
		{"openai", "gpt-4o-audio-preview"},
		{"openrouter", "google/gemini-2.5-pro"},
		{"anthropic", "claude-sonnet-4-5"},
	} {
		prevCalls := asrStub.calls
		stored, delivery, cliPrompt := svc.prepareTurnAttachmentsForModel(
			natRec, tc.prov, tc.model, false, int64(2010+idx), int64(idx+2), "msg_u_ng", "Transcribe this", audioPart,
		)
		if asrStub.calls != prevCalls+1 {
			t.Fatalf("%s/%s: expected ASR call, got %d -> %d", tc.prov, tc.model, prevCalls, asrStub.calls)
		}
		for _, dp := range delivery {
			if dp.Type == "file" {
				t.Fatalf("%s/%s: unexpected raw binary audio file in deliveryParts: %+v", tc.prov, tc.model, dp)
			}
		}
		if !strings.Contains(cliPrompt, "[Audio Transcript]") || !strings.Contains(cliPrompt, asrStub.transcript) {
			t.Fatalf("%s/%s: missing [Audio Transcript] in cliPrompt: %q", tc.prov, tc.model, cliPrompt)
		}

		attachNativeModelText(stored, cliPrompt)

		// 3. Multi-turn canonical history replay keeps the transcript for OpenAI/Anthropic,
		// and replays raw media if the user later switches to native Google protocol.
		turns := []MessageTurn{{
			Info:  MessageInfo{ID: "msg_u_ng", SessionID: natRec.ID, Role: "user"},
			Parts: stored,
		}}
		openAIHist := svc.projectCanonicalNativeHistory(svc.rootDir, turns, tc.prov, tc.model)
		foundHistTranscript := false
		for _, m := range openAIHist {
			for _, b := range m.Content {
				if b.Type == "media" {
					t.Fatalf("%s/%s: unexpected raw audio media block in canonical history replay: %+v", tc.prov, tc.model, b)
				}
				if b.Type == "text" && strings.Contains(b.Text, asrStub.transcript) {
					foundHistTranscript = true
				}
			}
		}
		if !foundHistTranscript {
			t.Fatalf("%s/%s: expected persisted ASR transcript replayed in canonical history, got %+v", tc.prov, tc.model, openAIHist)
		}
		googleHist := svc.projectCanonicalNativeHistory(svc.rootDir, turns, "google", "gemini-2.5-pro")
		foundGoogleMedia := false
		for _, m := range googleHist {
			for _, b := range m.Content {
				if b.Type == "media" && b.MimeType == "audio/wav" {
					foundGoogleMedia = true
				}
				if b.Type == "text" && strings.Contains(b.Text, asrStub.transcript) {
					t.Fatal("native audio replay included both the recording and its transcript")
				}
			}
		}
		if !foundGoogleMedia {
			t.Fatalf("expected raw audio media replayed when switching history to native google protocol, got %+v", googleHist)
		}
	}
}

func TestNativeHistorySkipsUnsupportedBinaryMediaWhenNativeModelTextPresent(t *testing.T) {
	attDir := t.TempDir()
	workDir := t.TempDir()
	store := attachment.NewStore(attDir)

	videoMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_nat_cap", Directory: workDir, Filename: "clip.mp4", Mime: "video/mp4",
		Data: []byte("ftypisom-video-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	audioMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_nat_cap", Directory: workDir, Filename: "note.wav", Mime: "audio/wav",
		Data: []byte("RIFF-no-asr-wav-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	pdfMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_nat_cap", Directory: workDir, Filename: "spec.pdf", Mime: "application/pdf",
		Data: []byte("%PDF-1.4 spec"),
	})
	if err != nil {
		t.Fatal(err)
	}
	imgMeta, err := store.Save(attachment.SaveRequest{
		SessionID: "ses_nat_cap", Directory: workDir, Filename: "shot.png", Mime: "image/png",
		Data: []byte("\x89PNG\r\n\x1a\nfake"),
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := &Service{attachments: store, rootDir: workDir}
	svc.SetStreamClient(&stubProtocolStreamClient{
		protocols: map[string]llm.StreamProtocol{
			"google":   llm.ProtocolGoogle,
			"openai":   llm.ProtocolOpenAIResponses,
			"deepseek": llm.ProtocolOpenAI,
		},
	})
	rec := SessionRecord{ID: "ses_nat_cap", Runtime: string(RuntimeNative), WorktreePath: workDir}
	parts := []MessagePart{
		{Type: "file", Filename: videoMeta.Filename, Mime: videoMeta.Mime, URL: videoMeta.URL},
		{Type: "file", Filename: audioMeta.Filename, Mime: audioMeta.Mime, URL: audioMeta.URL},
		{Type: "file", Filename: pdfMeta.Filename, Mime: pdfMeta.Mime, URL: pdfMeta.URL},
		{Type: "file", Filename: imgMeta.Filename, Mime: imgMeta.Mime, URL: imgMeta.URL},
	}

	// Simulate live Turn 1 on DeepSeek (non-PDF, non-audio/video, image-only media).
	stored, _, cliPrompt := svc.prepareTurnAttachmentsForModel(rec, "deepseek", "deepseek-chat", false, 3000, 1, "msg_u_cap", "Review attachments", parts)
	attachNativeModelText(stored, cliPrompt)
	turns := []MessageTurn{{Info: MessageInfo{ID: "msg_u_cap", SessionID: rec.ID, Role: "user"}, Parts: stored}}

	// Replay on DeepSeek must keep ONLY image/png media (no unsupported video/audio/pdf binary blocks).
	dsHist := svc.projectCanonicalNativeHistory(svc.rootDir, turns, "deepseek", "deepseek-chat")
	var dsMimes []string
	for _, b := range dsHist[0].Content {
		if b.Type == "media" {
			dsMimes = append(dsMimes, b.MimeType)
		}
	}
	if len(dsMimes) != 1 || dsMimes[0] != "image/png" {
		t.Fatalf("expected DeepSeek replay to keep only image/png media, got %v", dsMimes)
	}

	// Replay on OpenAI GPT-5 keeps image/png + application/pdf, still skipping unsupported video/audio.
	oaHist := svc.projectCanonicalNativeHistory(svc.rootDir, turns, "openai", "gpt-5")
	var oaMimes []string
	for _, b := range oaHist[0].Content {
		if b.Type == "media" {
			oaMimes = append(oaMimes, b.MimeType)
		}
	}
	if len(oaMimes) != 2 || oaMimes[0] != "application/pdf" || oaMimes[1] != "image/png" {
		t.Fatalf("expected GPT-5 replay to keep [application/pdf, image/png], got %v", oaMimes)
	}

	// Switching to Google Gemini unlocks raw video/audio/pdf/image media replay.
	gemHist := svc.projectCanonicalNativeHistory(svc.rootDir, turns, "google", "gemini-2.5-pro")
	var gemMimes []string
	for _, b := range gemHist[0].Content {
		if b.Type == "media" {
			gemMimes = append(gemMimes, b.MimeType)
		}
	}
	if len(gemMimes) != 4 {
		t.Fatalf("expected Gemini replay to keep all 4 media blocks, got %v", gemMimes)
	}
}

func TestNativeStagedImageReplaysOriginalBytes(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{attachments: attachment.NewStore(t.TempDir())}
	rec := SessionRecord{ID: "ses_staged", Runtime: "native", WorktreePath: dir}
	path := filepath.Join(dir, "screen.png")
	if err := os.WriteFile(path, []byte("original screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	part := MessagePart{Type: "file", Mime: "image/png", Filename: "screen.png", URL: "/api/hoxi/file/content?raw=1&path=screen.png"}
	stored, delivery, prompt := svc.prepareTurnAttachmentsForModel(rec, "openai", "gpt-5", false, 1, 1, "msg_u", "Review", []MessagePart{part})
	attachNativeModelText(stored, prompt)
	live := buildNativeUserBlocks(prompt, delivery)
	if len(live) != 2 || live[1].Type != "media" || attachment.ResolveRefID(stored[1].URL) == "" {
		t.Fatal("expected an inlined image and an immutable stored reference")
	}
	if err := os.WriteFile(path, []byte("updated screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	history := svc.projectCanonicalNativeHistory(rec.WorktreePath, []MessageTurn{{Info: MessageInfo{Role: "user"}, Parts: stored}}, "openai", "gpt-5")
	if len(history) != 1 || !reflect.DeepEqual(history[0].Content, live) {
		t.Fatal("replay changed the original model input after a workspace edit")
	}
}

func TestStagedMediaOutsideWorkspaceDoesNotInlineOrTranscribe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "outside.wav")
	if err := os.WriteFile(path, []byte("RIFF-audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(dir, "alias.wav")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	transcriber := &stubTurnAudioClient{}
	svc := &Service{attachments: attachment.NewStore(t.TempDir()), audioClient: transcriber}
	rec := SessionRecord{ID: "ses_staged", Runtime: "native", WorktreePath: dir}
	part := MessagePart{Type: "file", Mime: "audio/wav", Filename: "alias.wav", URL: "/api/hoxi/file/content?path=alias.wav"}
	_, delivery, prompt := svc.prepareTurnAttachmentsForModel(rec, "google", "gemini-2.5-pro", false, 1, 1, "msg_u", "", []MessagePart{part})
	if transcriber.calls != 0 || strings.Contains(prompt, "native_multimodal") || len(buildNativeUserBlocks(prompt, delivery)) != 1 {
		t.Fatal("out-of-workspace attachment must remain a path reference")
	}
}

func TestNativePDFReplayReplacesOnlyExtractsOnModelSwitch(t *testing.T) {
	dir := t.TempDir()
	store := attachment.NewStore(t.TempDir())
	svc := &Service{attachments: store}
	rec := SessionRecord{ID: "ses_pdf", Runtime: "native", WorktreePath: dir}
	const user = "Keep my words: unique PDF phrase"
	files := []MessagePart{}
	for _, name := range []string{"one.pdf", "two.pdf"} {
		path := filepath.Join(dir, name)
		if err := attachment.WritePDFFile(path, name, "unique PDF phrase"); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		meta, err := store.Save(attachment.SaveRequest{SessionID: rec.ID, Filename: name, Mime: "application/pdf", Data: raw})
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, MessagePart{Type: "file", Mime: meta.Mime, Filename: name, URL: meta.URL})
	}
	stored, _, prompt := svc.prepareTurnAttachmentsForModel(rec, "deepseek", "deepseek-chat", false, 1, 1, "msg_u", user, files)
	prompt = withTurnDirective("keep this directive", prompt)
	attachNativeModelText(stored, prompt)
	turns := []MessageTurn{{Info: MessageInfo{Role: "user"}, Parts: stored}}
	for _, legacy := range []bool{false, true} {
		if legacy {
			for i := range stored {
				if stored[i].Type == "file" {
					stored[i].Metadata = nil
				}
			}
		}
		textOnly := svc.projectCanonicalNativeHistory(rec.WorktreePath, turns, "deepseek", "deepseek-chat")[0].Content
		if len(textOnly) != 1 || textOnly[0].Text != prompt || strings.Count(prompt, "unique PDF phrase") != 3 {
			t.Fatalf("legacy=%v: text-only history lost an extract", legacy)
		}
		pdf := svc.projectCanonicalNativeHistory(rec.WorktreePath, turns, "openai", "gpt-5")[0].Content
		if len(pdf) != 3 || pdf[1].MimeType != "application/pdf" || pdf[2].MimeType != "application/pdf" ||
			strings.Count(pdf[0].Text, "unique PDF phrase") != 1 || !strings.Contains(pdf[0].Text, user) || !strings.Contains(pdf[0].Text, "keep this directive") {
			t.Fatalf("legacy=%v: expected both PDFs, the directive and just the original user text", legacy)
		}
	}

	// Reverse the switch: the native-file turn contained only path annotations.
	nativeParts, delivery, nativePrompt := svc.prepareTurnAttachmentsForModel(rec, "openai", "gpt-5", false, 2, 2, "msg_native", user, files)
	attachNativeModelText(nativeParts, nativePrompt)
	nativeTurns := []MessageTurn{{Info: MessageInfo{Role: "user"}, Parts: nativeParts}}
	nativeReplay := svc.projectCanonicalNativeHistory(dir, nativeTurns, "openai", "gpt-5")[0].Content
	if !reflect.DeepEqual(nativeReplay, buildNativeUserBlocks(nativePrompt, delivery)) {
		t.Fatal("native PDF history diverged before switching models")
	}
	textReplay := svc.projectCanonicalNativeHistory(dir, nativeTurns, "deepseek", "deepseek-chat")[0].Content
	var body strings.Builder
	for _, b := range textReplay {
		if b.Type != "text" {
			t.Fatalf("unsupported binary block sent to a text model: %s", b.Type)
		}
		body.WriteString(b.Text)
	}
	if strings.Count(body.String(), "unique PDF phrase") != 3 {
		t.Fatal("switching to a text model lost the PDF evidence")
	}

}

func TestHistoryRespectsTextOnlyModelOnGoogleProtocol(t *testing.T) {
	svc := &Service{}
	svc.SetStreamClient(&stubProtocolStreamClient{protocols: map[string]llm.StreamProtocol{"custom": llm.ProtocolGoogle}})
	turns := []MessageTurn{{Info: MessageInfo{Role: "user"}, Parts: []MessagePart{
		{Type: "text", Text: "review"},
		{Type: "file", Mime: "audio/wav", URL: "data:audio/wav;base64,YWJj", Metadata: map[string]interface{}{"transcript": "spoken requirement"}},
		{Type: "file", Mime: "video/mp4", URL: "data:video/mp4;base64,YWJj"},
	}}}
	blocks := svc.projectCanonicalNativeHistory("", turns, "custom", "text-only")[0].Content
	if len(blocks) != 2 || blocks[1].Type != "text" || !strings.Contains(blocks[1].Text, "spoken requirement") {
		t.Fatalf("text-only model received unexpected attachment blocks: %+v", blocks)
	}
}

func TestHistoryResolvesPDFCapabilityOnceInSessionWorktree(t *testing.T) {
	dir := t.TempDir()
	var requests atomic.Int32
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/providers" || r.URL.Query().Get("directory") != dir {
			t.Errorf("unexpected capability request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		_, _ = io.WriteString(w, `{"providers":[{"id":"custom","models":{"reader":{"modalities":{"input":["pdf"]}}}}]}`)
	}))
	defer catalog.Close()
	svc := &Service{rootDir: t.TempDir()}
	svc.openCode = ocapi.New(catalog.URL, "test")
	turns := []MessageTurn{{Info: MessageInfo{Role: "user"}, Parts: []MessagePart{
		{Type: "text", Text: "Review", Metadata: map[string]interface{}{nativeModelTextMetaKey: "Review PDFs"}},
		{Type: "file", Mime: "application/pdf", URL: "data:application/pdf;base64,JVBERg=="},
		{Type: "file", Mime: "application/pdf", URL: "data:application/pdf;base64,JVBERg=="},
	}}}
	history := svc.projectCanonicalNativeHistory(dir, turns, "custom", "reader")
	if len(history) != 1 || len(history[0].Content) != 3 || requests.Load() != 1 {
		t.Fatal("expected both PDFs to use a single session-scoped capability lookup")
	}
}
