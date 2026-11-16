package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

var _ llm.AudioClient = (*Service)(nil)

const (
	defaultAudioTranscribeTimeout = 60 * time.Second
	maxAudioTranscribeBytes       = 25 << 20
)

type resolvedAudioTarget struct {
	ProviderID string
	ModelID    string
	BaseURL    string
	APIKey     string
	NPM        string
	Headers    map[string]string
	Kind       string // "openai_asr" | "google_gemini"
}

func (s *Service) resolveAudioTranscriber(preferredProviderID, preferredModelID string) (resolvedAudioTarget, bool) {
	if s == nil {
		return resolvedAudioTarget{}, false
	}
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}

	buildTarget := func(pid, mid, kind string) (resolvedAudioTarget, bool) {
		baseURL, apiKey, npm, headers := s.resolveProviderConnection(pid, "", "")
		if !isHTTPEndpoint(baseURL) {
			return resolvedAudioTarget{}, false
		}
		if apiKey == "" && len(headers) == 0 && !isLoopbackURL(baseURL) {
			return resolvedAudioTarget{}, false
		}
		return resolvedAudioTarget{
			ProviderID: pid,
			ModelID:    mid,
			BaseURL:    baseURL,
			APIKey:     apiKey,
			NPM:        npm,
			Headers:    headers,
			Kind:       kind,
		}, true
	}

	prefPID := strings.TrimSpace(preferredProviderID)
	prefMID := strings.TrimSpace(preferredModelID)
	if prefPID != "" && prefMID != "" && s.isProviderUsableForLocalMedia(prefPID) {
		baseURL, _, npm, _ := s.resolveProviderConnection(prefPID, "", "")
		kind := "openai_asr"
		if effectiveProviderProtocol(prefPID, npm, baseURL) == "google" {
			kind = "google_gemini"
		}
		if t, ok := buildTarget(prefPID, prefMID, kind); ok {
			return t, true
		}
	}

	// Pass 1: Dedicated ASR models (Whisper / SenseVoice / Paraformer) on OpenAI-compatible providers.
	for _, pid := range sortedKeys(merged) {
		if !s.isProviderUsableForLocalMedia(pid) {
			continue
		}
		pObj, _ := merged[pid].(map[string]interface{})
		mMap, _ := pObj["models"].(map[string]interface{})
		baseURL, _, npm, _ := s.resolveProviderConnection(pid, "", "")
		if effectiveProviderProtocol(pid, npm, baseURL) != "openai" {
			continue
		}
		for _, mid := range sortedKeys(mMap) {
			mObj, _ := mMap[mid].(map[string]interface{})
			cat := ""
			if mObj != nil {
				cat, _ = mObj["category"].(string)
			}
			if inferModelCategory(mid, cat, "") == "asr" {
				if t, ok := buildTarget(pid, mid, "openai_asr"); ok {
					return t, true
				}
			}
		}
	}

	// Pass 2: Multimodal Google Gemini chat models (preferring fast Flash models).
	var geminiFallback *resolvedAudioTarget
	for _, pid := range sortedKeys(merged) {
		if !s.isProviderUsableForLocalMedia(pid) {
			continue
		}
		pObj, _ := merged[pid].(map[string]interface{})
		mMap, _ := pObj["models"].(map[string]interface{})
		baseURL, _, npm, _ := s.resolveProviderConnection(pid, "", "")
		if effectiveProviderProtocol(pid, npm, baseURL) != "google" {
			continue
		}
		for _, mid := range sortedKeys(mMap) {
			mObj, _ := mMap[mid].(map[string]interface{})
			cat := ""
			if mObj != nil {
				cat, _ = mObj["category"].(string)
			}
			if !isConversationalCategory(inferModelCategory(mid, cat, "")) {
				continue
			}
			t, ok := buildTarget(pid, mid, "google_gemini")
			if !ok {
				continue
			}
			if strings.Contains(strings.ToLower(mid), "flash") {
				return t, true
			}
			if geminiFallback == nil {
				cp := t
				geminiFallback = &cp
			}
		}
	}
	if geminiFallback != nil {
		return *geminiFallback, true
	}
	return resolvedAudioTarget{}, false
}

// HasAudioTranscriber reports whether at least one user-configured ASR or
// multimodal Gemini provider is ready for local voice transcription.
func (s *Service) HasAudioTranscriber() bool {
	_, ok := s.resolveAudioTranscriber("", "")
	return ok
}

// TranscribeAudio converts spoken audio bytes into text using the user's
// configured ASR or Gemini multimodal endpoint.
func (s *Service) TranscribeAudio(ctx context.Context, req llm.AudioTranscribeRequest) (resp llm.AudioTranscribeResponse, resultErr error) {
	if len(req.Data) == 0 {
		return llm.AudioTranscribeResponse{}, errors.New("audio data is empty")
	}
	if len(req.Data) > maxAudioTranscribeBytes {
		return llm.AudioTranscribeResponse{}, errors.New("audio exceeds 25 MB limit")
	}
	target, ok := s.resolveAudioTranscriber(req.ProviderID, req.ModelID)
	if !ok {
		return llm.AudioTranscribeResponse{}, llm.ErrUnavailable
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultAudioTranscribeTimeout
	}
	started := time.Now()
	defer func() {
		level, msg := "info", "audio transcription completed"
		if resultErr != nil {
			level, msg = "error", "audio transcription failed"
		}
		diagnostics.Record(diagnostics.Entry{
			Kind:       "request",
			Level:      level,
			Source:     "provider",
			Message:    msg,
			ProviderID: target.ProviderID,
			ModelID:    target.ModelID,
			DurationMs: time.Since(started).Milliseconds(),
		})
	}()

	var text string
	var err error
	if target.Kind == "google_gemini" {
		text, err = s.transcribeViaGoogleGemini(ctx, target, req, timeout)
	} else {
		text, err = s.transcribeViaOpenAIWhisper(ctx, target, req, timeout)
	}
	if err != nil {
		return llm.AudioTranscribeResponse{}, err
	}
	return llm.AudioTranscribeResponse{
		Text:       strings.TrimSpace(text),
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
	}, nil
}

func normalizeAudioUploadMime(filename, mime string) (string, string) {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	name := strings.TrimSpace(filename)
	switch {
	case strings.Contains(m, "webm") || strings.HasSuffix(strings.ToLower(name), ".webm"):
		if name == "" {
			name = "recording.webm"
		}
		return name, "audio/webm"
	case strings.Contains(m, "wav") || strings.HasSuffix(strings.ToLower(name), ".wav"):
		if name == "" {
			name = "recording.wav"
		}
		return name, "audio/wav"
	case strings.Contains(m, "mp4") || strings.Contains(m, "m4a") || strings.HasSuffix(strings.ToLower(name), ".m4a") || strings.HasSuffix(strings.ToLower(name), ".mp4"):
		if name == "" {
			name = "recording.m4a"
		}
		return name, "audio/mp4"
	case strings.Contains(m, "ogg") || strings.HasSuffix(strings.ToLower(name), ".ogg"):
		if name == "" {
			name = "recording.ogg"
		}
		return name, "audio/ogg"
	case strings.Contains(m, "mpeg") || strings.Contains(m, "mp3") || strings.HasSuffix(strings.ToLower(name), ".mp3"):
		if name == "" {
			name = "recording.mp3"
		}
		return name, "audio/mpeg"
	default:
		if name == "" {
			name = "recording.webm"
		}
		if m == "" {
			m = "audio/webm"
		}
		return name, m
	}
}

func (s *Service) transcribeViaOpenAIWhisper(
	parentCtx context.Context,
	target resolvedAudioTarget,
	req llm.AudioTranscribeRequest,
	timeout time.Duration,
) (string, error) {
	fname, mime := normalizeAudioUploadMime(req.Filename, req.Mime)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", target.ModelID)
	if lang := strings.TrimSpace(req.Language); lang != "" {
		if idx := strings.IndexAny(lang, "-_"); idx > 0 {
			lang = lang[:idx]
		}
		_ = mw.WriteField("language", strings.ToLower(lang))
	}
	if p := strings.TrimSpace(req.Prompt); p != "" {
		_ = mw.WriteField("prompt", p)
	}
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, fname))
	hdr.Set("Content-Type", mime)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return "", err
	}
	_, _ = part.Write(req.Data)
	_ = mw.Close()

	targetURL := strings.TrimRight(strings.TrimSpace(target.BaseURL), "/") + "/audio/transcriptions"
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, &buf)
	if err != nil {
		return "", err
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", mw.FormDataContentType())
	conn := imageGenConn{
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
		BaseURL:    target.BaseURL,
		APIKey:     target.APIKey,
		Headers:    target.Headers,
	}
	applyImageAuthHeaders(outReq, conn, false, false)
	client := &http.Client{Timeout: timeout, Transport: selectImageTransport(targetURL)}
	resp, err := client.Do(outReq)
	if err != nil {
		return "", errors.New(redactSecretInText(err.Error(), target.APIKey))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New(redactSecretInText(extractAPIErrorMessage(resp.StatusCode, body), target.APIKey))
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(body, &parsed) == nil && strings.TrimSpace(parsed.Text) != "" {
		return strings.TrimSpace(parsed.Text), nil
	}
	return strings.TrimSpace(string(body)), nil
}

func (s *Service) transcribeViaGoogleGemini(
	ctx context.Context,
	target resolvedAudioTarget,
	req llm.AudioTranscribeRequest,
	timeout time.Duration,
) (string, error) {
	_, mime := normalizeAudioUploadMime(req.Filename, req.Mime)
	cleanModel := strings.TrimPrefix(strings.TrimSpace(target.ModelID), "models/")
	targetURL := googleModelsBaseURL(target.BaseURL) + "/" + url.PathEscape(cleanModel) + ":generateContent"
	instruction := "Transcribe the spoken audio accurately. Output ONLY the transcribed words without extra commentary or markdown fences."
	if strings.TrimSpace(req.Language) != "" {
		instruction += " Preferred language hint: " + strings.TrimSpace(req.Language) + "."
	}
	payload := map[string]any{
		"contents": []map[string]any{{
			"role": "user",
			"parts": []map[string]any{
				{
					"inlineData": map[string]string{
						"mimeType": mime,
						"data":     base64.StdEncoding.EncodeToString(req.Data),
					},
				},
				{"text": instruction},
			},
		}},
		"generationConfig": map[string]any{
			"temperature":     0,
			"maxOutputTokens": 2048,
		},
	}
	conn := imageGenConn{
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
		BaseURL:    target.BaseURL,
		APIKey:     target.APIKey,
		NPM:        target.NPM,
		Headers:    target.Headers,
	}
	status, body, err := s.doJSONImageRequest(ctx, conn, true, targetURL, payload, timeout)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", errors.New(redactSecretInText(extractAPIErrorMessage(status, body), target.APIKey))
	}
	return strings.TrimSpace(extractLLMTextContent(body)), nil
}
