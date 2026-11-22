package provider

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	defaultSpeechTimeout  = 90 * time.Second
	maxSpeechResponseSize = 32 << 20
)

func scoreSpeechModelPreference(modelID string) int {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.Contains(lower, "gpt-4o-mini-tts") || strings.Contains(lower, "gemini-2.5") && strings.Contains(lower, "tts"):
		return 100
	case strings.Contains(lower, "eleven") || strings.Contains(lower, "fish"):
		return 95
	case strings.Contains(lower, "cosyvoice") || strings.Contains(lower, "qwen-tts") || strings.Contains(lower, "tts-1-hd"):
		return 90
	case strings.Contains(lower, "tts"):
		return 85
	default:
		return 65
	}
}

func hasAudioOutputModality(mObj map[string]interface{}) bool {
	return hasModalityEntry(mObj, "output", "audio")
}

// IsSpeechModel reports whether providerID/modelID is a dedicated TTS / speech
// synthesis model.
func (s *Service) IsSpeechModel(providerID, modelID string) bool {
	mid := strings.TrimSpace(modelID)
	if mid == "" {
		return false
	}
	if mObj := s.lookupConfiguredModelObject(providerID, mid); mObj != nil {
		cat, _ := mObj["category"].(string)
		normCat := normalizeModelCategory(cat)
		if normCat == "chat" || normCat == "coding" {
			return false
		}
		if inferModelCategory(mid, cat, "") == "tts" || hasAudioOutputModality(mObj) {
			return true
		}
	}
	return inferModelCategory(mid, "", "") == "tts"
}

// CanModelSynthesizeSpeech reports whether providerID/modelID itself can natively
// synthesize speech (either as a dedicated TTS model or via explicit output.audio
// modality metadata).
func (s *Service) CanModelSynthesizeSpeech(providerID, modelID string) bool {
	mid := strings.TrimSpace(modelID)
	if mid == "" {
		return false
	}
	if s.IsSpeechModel(providerID, mid) {
		return true
	}
	if mObj := s.lookupConfiguredModelObject(providerID, mid); mObj != nil {
		return hasAudioOutputModality(mObj)
	}
	return false
}

// ListSpeechModels returns all TTS / speech synthesis models configured on
// active, credentialed providers in Hoxi / OpenCode config.
func (s *Service) ListSpeechModels() []llm.AudioModelOption {
	if s == nil {
		return nil
	}
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}
	type scoredOpt struct {
		opt   llm.AudioModelOption
		score int
	}
	var found []scoredOpt
	seen := map[string]bool{}

	for _, pid := range sortedKeys(merged) {
		if !s.isProviderUsableForLocalMedia(pid) {
			continue
		}
		pObj, ok := merged[pid].(map[string]interface{})
		if !ok {
			continue
		}
		for _, mapKey := range []string{"models", "mediaModels"} {
			mMap, ok := pObj[mapKey].(map[string]interface{})
			if !ok {
				continue
			}
			for _, mid := range sortedKeys(mMap) {
				mObj, _ := mMap[mid].(map[string]interface{})
				cat := ""
				name := mid
				if mObj != nil {
					cat, _ = mObj["category"].(string)
					if n, _ := mObj["name"].(string); strings.TrimSpace(n) != "" {
						name = strings.TrimSpace(n)
					}
				}
				isTTS := inferModelCategory(mid, cat, "") == "tts" || (mObj != nil && hasAudioOutputModality(mObj))
				if !isTTS {
					continue
				}
				key := pid + "/" + mid
				if seen[key] {
					continue
				}
				seen[key] = true
				found = append(found, scoredOpt{
					opt: llm.AudioModelOption{
						ProviderID: pid,
						ModelID:    mid,
						Name:       name,
					},
					score: scoreSpeechModelPreference(mid),
				})
			}
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		if found[i].opt.ProviderID != found[j].opt.ProviderID {
			return found[i].opt.ProviderID < found[j].opt.ProviderID
		}
		return found[i].opt.ModelID < found[j].opt.ModelID
	})
	out := make([]llm.AudioModelOption, len(found))
	for i, item := range found {
		out[i] = item.opt
	}
	return out
}

// ResolveSpeechModel selects a concrete TTS provider/model.
func (s *Service) ResolveSpeechModel(preferredProviderID, preferredModelID string) (string, string, bool) {
	pid := strings.TrimSpace(preferredProviderID)
	mid := strings.TrimSpace(preferredModelID)
	if pid == "" && strings.Contains(mid, "/") {
		parts := strings.SplitN(mid, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" && s.isProviderUsableForLocalMedia(parts[0]) {
			pid = strings.TrimSpace(parts[0])
			mid = strings.TrimSpace(parts[1])
		}
	}
	if pid != "" && mid != "" {
		if conn, err := s.resolveImageConnection(pid, mid); err == nil && isHTTPEndpoint(conn.BaseURL) {
			return pid, mid, true
		}
	}
	list := s.ListSpeechModels()
	if mid != "" {
		for _, item := range list {
			if strings.EqualFold(item.ModelID, mid) {
				return item.ProviderID, item.ModelID, true
			}
		}
	}
	if pid != "" {
		for _, item := range list {
			if item.ProviderID == pid {
				return item.ProviderID, item.ModelID, true
			}
		}
	}
	if len(list) > 0 {
		return list[0].ProviderID, list[0].ModelID, true
	}
	return "", "", false
}

// ResolveSpeechModelForSession performs the capability self-check:
//  1. Explicit caller override ("explicit")
//  2. Active session model's own native speech output capability ("session_self")
//  3. User-configured default speech model in media-roles.json ("role_default")
//  4. Best configured local speech model ("configured_model")
func (s *Service) ResolveSpeechModelForSession(
	preferredProviderID, preferredModelID, sessionProviderID, sessionModelID string,
) (string, string, string, bool) {
	if strings.TrimSpace(preferredProviderID) != "" || strings.TrimSpace(preferredModelID) != "" {
		pid, mid, ok := s.ResolveSpeechModel(preferredProviderID, preferredModelID)
		if !ok {
			return "", "", "", false
		}
		return pid, mid, "explicit", true
	}
	sessPID := strings.TrimSpace(sessionProviderID)
	sessMID := strings.TrimSpace(sessionModelID)
	if sessPID != "" && sessMID != "" && s.CanModelSynthesizeSpeech(sessPID, sessMID) {
		if conn, err := s.resolveImageConnection(sessPID, sessMID); err == nil && isHTTPEndpoint(conn.BaseURL) && (conn.APIKey != "" || len(conn.Headers) > 0 || isLoopbackURL(conn.BaseURL)) {
			return sessPID, sessMID, "session_self", true
		}
	}
	if defPID, defMID := splitMediaRoleSpec(s.LoadMediaRolePrefs().SpeechModel); defPID != "" && defMID != "" {
		if s.isProviderUsableForLocalMedia(defPID) && s.CanModelSynthesizeSpeech(defPID, defMID) {
			return defPID, defMID, "role_default", true
		}
	}
	list := s.ListSpeechModels()
	if len(list) == 0 {
		return "", "", "", false
	}
	best := list[0]
	if sessPID != "" && best.ProviderID != sessPID {
		bestScore := scoreSpeechModelPreference(best.ModelID)
		for _, item := range list[1:] {
			if scoreSpeechModelPreference(item.ModelID) < bestScore {
				break
			}
			if item.ProviderID == sessPID {
				best = item
				break
			}
		}
	}
	return best.ProviderID, best.ModelID, "configured_model", true
}

// SynthesizeSpeech generates spoken audio from text using the resolved Google
// Gemini Audio/TTS or OpenAI-compatible /audio/speech endpoint.
func (s *Service) SynthesizeSpeech(ctx context.Context, req llm.SpeechRequest) (resp llm.SpeechResponse, resultErr error) {
	text := strings.TrimSpace(req.Input)
	if text == "" {
		return llm.SpeechResponse{}, errors.New("speech input text is required")
	}
	pid := strings.TrimSpace(req.ProviderID)
	mid := strings.TrimSpace(req.ModelID)
	if pid == "" || mid == "" {
		resolvedPID, resolvedMID, ok := s.ResolveSpeechModel(pid, mid)
		if !ok {
			return llm.SpeechResponse{}, llm.ErrUnavailable
		}
		pid, mid = resolvedPID, resolvedMID
	}
	conn, err := s.resolveImageConnection(pid, mid)
	if err != nil {
		return llm.SpeechResponse{}, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultSpeechTimeout
	}

	started := time.Now()
	defer func() {
		level, msg := "info", "speech synthesis completed"
		if resultErr != nil {
			level, msg = "error", "speech synthesis failed"
		}
		diagnostics.Record(diagnostics.Entry{
			Kind:       "request",
			Level:      level,
			Source:     "provider",
			Message:    msg,
			ProviderID: conn.ProviderID,
			ModelID:    conn.ModelID,
			DurationMs: time.Since(started).Milliseconds(),
		})
		if s.stats != nil {
			s.stats.recordLLMRequest(conn.ProviderID, conn.ModelID, 1, resultErr)
		}
	}()

	proto := effectiveProviderProtocol(conn.ProviderID, conn.NPM, conn.BaseURL)
	if proto == "google" {
		resp, resultErr = s.googleSynthesizeSpeech(ctx, conn, text, req.Voice, timeout)
	} else {
		resp, resultErr = s.openAISynthesizeSpeech(ctx, conn, text, req.Voice, req.Format, req.Speed, timeout)
	}
	if resultErr == nil {
		resp.ProviderID = conn.ProviderID
		resp.ModelID = conn.ModelID
	}
	return resp, resultErr
}

func (s *Service) googleSynthesizeSpeech(
	parentCtx context.Context,
	conn imageGenConn,
	text, voice string,
	timeout time.Duration,
) (llm.SpeechResponse, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(conn.ModelID), "models/")
	targetURL := googleModelsBaseURL(conn.BaseURL) + "/" + url.PathEscape(cleanModel) + ":generateContent"
	voiceName := strings.TrimSpace(voice)
	if voiceName == "" {
		voiceName = "Kore"
	}
	payload := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": text}}},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
			"speechConfig": map[string]any{
				"voiceConfig": map[string]any{
					"prebuiltVoiceConfig": map[string]any{"voiceName": voiceName},
				},
			},
		},
	}
	status, _, body, err := s.doVideoHTTP(parentCtx, http.MethodPost, targetURL, conn, true, nil, payload, timeout)
	if err != nil {
		return llm.SpeechResponse{}, err
	}
	if status < 200 || status >= 300 {
		return llm.SpeechResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return llm.SpeechResponse{}, fmt.Errorf("decode Gemini TTS response: %w", err)
	}
	for _, c := range parsed.Candidates {
		for _, p := range c.Content.Parts {
			if p.InlineData == nil || strings.TrimSpace(p.InlineData.Data) == "" {
				continue
			}
			rawAudio, err := decodeBase64Flexible(p.InlineData.Data)
			if err != nil || len(rawAudio) == 0 {
				continue
			}
			mime, data := normalizeSynthesizedAudio(p.InlineData.MimeType, rawAudio)
			return llm.SpeechResponse{Mime: mime, Data: data}, nil
		}
	}
	return llm.SpeechResponse{}, errors.New("Gemini response contained no audio inlineData")
}

func (s *Service) openAISynthesizeSpeech(
	parentCtx context.Context,
	conn imageGenConn,
	text, voice, format string,
	speed float64,
	timeout time.Duration,
) (llm.SpeechResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	voiceName := strings.TrimSpace(voice)
	if voiceName == "" {
		voiceName = "alloy"
	}
	respFormat := strings.ToLower(strings.TrimSpace(format))
	if respFormat != "wav" && respFormat != "opus" && respFormat != "flac" && respFormat != "aac" {
		respFormat = "mp3"
	}
	payload := map[string]any{
		"model":           conn.ModelID,
		"input":           text,
		"voice":           voiceName,
		"response_format": respFormat,
	}
	if speed >= 0.25 && speed <= 4.0 {
		payload["speed"] = speed
	}
	rawPayload, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, trimmedBase+"/audio/speech", bytes.NewReader(rawPayload))
	if err != nil {
		return llm.SpeechResponse{}, err
	}
	outReq.Header.Set("Content-Type", "application/json")
	outReq.Header.Set("Accept", "audio/*, application/json;q=0.8")
	applyImageAuthHeaders(outReq, conn, false, false)

	resp, err := (&http.Client{Timeout: timeout, Transport: selectImageTransport(trimmedBase)}).Do(outReq)
	if err != nil {
		return llm.SpeechResponse{}, errors.New(redactSecretInText(err.Error(), conn.APIKey))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxSpeechResponseSize+1))
	if len(body) > maxSpeechResponseSize {
		return llm.SpeechResponse{}, errors.New("synthesized audio exceeds 32 MB limit")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && len(body) > 0 {
		ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
		mime, data := normalizeSynthesizedAudio(ct, body)
		if ct == "" || ct == "application/octet-stream" {
			if respFormat == "wav" {
				mime = "audio/wav"
			} else {
				mime = "audio/mpeg"
			}
		}
		return llm.SpeechResponse{Mime: mime, Data: data}, nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		if chatResp, chatErr := s.openAIChatSynthesizeSpeech(parentCtx, conn, text, voiceName, timeout); chatErr == nil {
			return chatResp, nil
		}
	}
	return llm.SpeechResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(resp.StatusCode, body), conn.APIKey))
}

func (s *Service) openAIChatSynthesizeSpeech(
	parentCtx context.Context,
	conn imageGenConn,
	text, voice string,
	timeout time.Duration,
) (llm.SpeechResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	payload := map[string]any{
		"model":      conn.ModelID,
		"modalities": []string{"text", "audio"},
		"audio":      map[string]any{"voice": voice, "format": "mp3"},
		"messages":   []map[string]any{{"role": "user", "content": text}},
	}
	status, _, body, err := s.doVideoHTTP(parentCtx, http.MethodPost, trimmedBase+"/chat/completions", conn, false, nil, payload, timeout)
	if err != nil {
		return llm.SpeechResponse{}, err
	}
	if status < 200 || status >= 300 {
		return llm.SpeechResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Audio *struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Choices) == 0 || parsed.Choices[0].Message.Audio == nil {
		return llm.SpeechResponse{}, errors.New("chat audio fallback returned no audio payload")
	}
	raw, err := decodeBase64Flexible(parsed.Choices[0].Message.Audio.Data)
	if err != nil || len(raw) == 0 {
		return llm.SpeechResponse{}, errors.New("empty chat audio data")
	}
	return llm.SpeechResponse{Mime: "audio/mpeg", Data: raw}, nil
}

func normalizeSynthesizedAudio(rawMime string, data []byte) (string, []byte) {
	lower := strings.ToLower(strings.TrimSpace(rawMime))
	if strings.Contains(lower, "audio/pcm") || strings.Contains(lower, "audio/l16") {
		sampleRate := 24000
		for _, seg := range strings.Split(lower, ";") {
			seg = strings.TrimSpace(seg)
			if strings.HasPrefix(seg, "rate=") {
				if r, err := strconv.Atoi(strings.TrimPrefix(seg, "rate=")); err == nil && r >= 8000 && r <= 96000 {
					sampleRate = r
				}
			}
		}
		return "audio/wav", wrapPCM16LEAsWAV(data, sampleRate, 1)
	}
	baseMime := strings.TrimSpace(strings.Split(lower, ";")[0])
	switch baseMime {
	case "audio/mp3", "audio/mpeg":
		return "audio/mpeg", data
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "audio/wav", data
	case "audio/ogg", "audio/opus", "audio/flac", "audio/aac", "audio/mp4":
		return baseMime, data
	}
	if len(data) >= 4 && string(data[:4]) == "RIFF" {
		return "audio/wav", data
	}
	return "audio/mpeg", data
}

func wrapPCM16LEAsWAV(pcm []byte, sampleRate, channels int) []byte {
	byteRate := uint32(sampleRate * channels * 2)
	blockAlign := uint16(channels * 2)
	dataLen := uint32(len(pcm))
	out := make([]byte, 44+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], 36+dataLen)
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(out[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:32], byteRate)
	binary.LittleEndian.PutUint16(out[32:34], blockAlign)
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], dataLen)
	copy(out[44:], pcm)
	return out
}
