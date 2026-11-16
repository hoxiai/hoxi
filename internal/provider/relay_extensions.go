package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	relayResponsesPath      = "/v1/responses"
	relayEmbeddingsPath     = "/v1/embeddings"
	relayImagesGenPath      = "/v1/images/generations"
	relayAudioTransPath     = "/v1/audio/transcriptions"
	maxRelayStickySessions  = 256
	relayStickySessionTTLMs = 30 * 60 * 1000
)

type relayStickyEntry struct {
	providerID string
	modelID    string
	expiresAt  int64
}

var relayStickyPool struct {
	mu      sync.Mutex
	order   []string
	entries map[string]relayStickyEntry
}

func deriveRelaySessionKey(r *http.Request, payload map[string]any) string {
	for _, hdr := range []string{"X-Session-ID", "X-Conversation-ID", "Openai-Conversation-Id"} {
		if v := strings.TrimSpace(r.Header.Get(hdr)); v != "" {
			return "h:" + v
		}
	}
	if payload != nil {
		if u, ok := payload["user"].(string); ok && strings.TrimSpace(u) != "" {
			return "u:" + strings.TrimSpace(u)
		}
		if msgs, ok := payload["messages"].([]any); ok && len(msgs) > 0 {
			var seed strings.Builder
			limit := len(msgs)
			if limit > 2 {
				limit = 2
			}
			for i := 0; i < limit; i++ {
				if m, ok := msgs[i].(map[string]any); ok {
					role, _ := m["role"].(string)
					seed.WriteString(role)
					seed.WriteByte(':')
					seed.WriteString(extractRelayMessageText(m["content"]))
					seed.WriteByte('\n')
				}
			}
			if seed.Len() > 0 {
				sum := sha256.Sum256([]byte(seed.String()))
				return "m:" + hex.EncodeToString(sum[:12])
			}
		}
	}
	return ""
}

func (s *Service) lookupRelayStickyTarget(poolID, sessionKey string) (string, string, bool) {
	if poolID == "" || sessionKey == "" {
		return "", "", false
	}
	key := poolID + "\x00" + sessionKey
	now := time.Now().UnixMilli()

	relayStickyPool.mu.Lock()
	entry, ok := relayStickyPool.entries[key]
	if ok && now > entry.expiresAt {
		delete(relayStickyPool.entries, key)
		ok = false
	}
	relayStickyPool.mu.Unlock()
	if !ok {
		return "", "", false
	}
	if !s.relayCandidateCompatible(entry.providerID) {
		return "", "", false
	}
	if s.breaker != nil {
		if allowed, _ := s.breaker.Evaluate(entry.providerID, entry.modelID); !allowed {
			return "", "", false
		}
	}
	return entry.providerID, entry.modelID, true
}

func rememberRelayStickyTarget(poolID, sessionKey, providerID, modelID string) {
	if poolID == "" || sessionKey == "" || providerID == "" || modelID == "" {
		return
	}
	key := poolID + "\x00" + sessionKey
	now := time.Now().UnixMilli()

	relayStickyPool.mu.Lock()
	defer relayStickyPool.mu.Unlock()
	if relayStickyPool.entries == nil {
		relayStickyPool.entries = make(map[string]relayStickyEntry)
	}
	if _, exists := relayStickyPool.entries[key]; !exists {
		relayStickyPool.order = append(relayStickyPool.order, key)
	}
	relayStickyPool.entries[key] = relayStickyEntry{
		providerID: providerID,
		modelID:    modelID,
		expiresAt:  now + relayStickySessionTTLMs,
	}
	for len(relayStickyPool.order) > maxRelayStickySessions {
		evict := relayStickyPool.order[0]
		relayStickyPool.order = relayStickyPool.order[1:]
		delete(relayStickyPool.entries, evict)
	}
}

func extractRelayMessageText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["text"].(string); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func extractRelayInputImagesAndText(content any) (string, []llm.ImageInput) {
	switch c := content.(type) {
	case string:
		return c, nil
	case []any:
		var texts []string
		var imgs []llm.ImageInput
		for _, item := range c {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			switch typ {
			case "text", "input_text":
				if txt, _ := m["text"].(string); txt != "" {
					texts = append(texts, txt)
				}
			case "image_url", "input_image":
				rawURL := ""
				if s, ok := m["image_url"].(string); ok {
					rawURL = s
				} else if uMap, ok := m["image_url"].(map[string]any); ok {
					rawURL, _ = uMap["url"].(string)
				}
				if strings.HasPrefix(strings.TrimSpace(rawURL), "data:") {
					if img, ok := decodeDataURIImage(rawURL); ok && len(img.Data) > 0 {
						imgs = append(imgs, llm.ImageInput{Mime: img.Mime, Data: img.Data})
					}
				}
			}
		}
		return strings.Join(texts, "\n"), imgs
	default:
		return "", nil
	}
}

func (s *Service) serveTranslatedRelayChat(
	w http.ResponseWriter,
	r *http.Request,
	providerID, modelID, baseURL, authSecret string,
	headers map[string]string,
	proto string,
	payload map[string]any,
) {
	stream, _ := payload["stream"].(bool)
	started := time.Now()
	elapsedMs := func() int64 {
		ms := time.Since(started).Milliseconds()
		if ms <= 0 {
			return 1
		}
		return ms
	}

	var replyText string
	var promptToks, completionToks int
	var status int
	var rawErr []byte
	var err error

	if proto == "google" {
		replyText, promptToks, completionToks, status, rawErr, err = s.callRelayGoogleChat(
			r.Context(), baseURL, authSecret, headers, modelID, payload,
		)
	} else {
		replyText, promptToks, completionToks, status, rawErr, err = s.callRelayAnthropicChat(
			r.Context(), baseURL, authSecret, headers, modelID, payload,
		)
	}

	if err != nil {
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(0, err, nil), 0, elapsedMs(), err.Error())
		recordRelayFailure(providerID, modelID, 0, started)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "upstream request failed: " + err.Error(), "type": "upstream_error"},
		})
		return
	}
	if status < 200 || status >= 300 {
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(status, nil, rawErr), status, elapsedMs(), string(rawErr))
		recordRelayFailure(providerID, modelID, status, started)
		writeRelayUpstreamError(w, status, rawErr)
		return
	}

	s.recordBreakerSuccess(providerID, modelID, elapsedMs())
	nowSec := time.Now().Unix()
	completionID := fmt.Sprintf("chatcmpl-hoxi-%d", time.Now().UnixNano())

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		chunk := map[string]any{
			"id":      completionID,
			"object":  "chat.completion.chunk",
			"created": nowSec,
			"model":   providerID + "/" + modelID,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{"role": "assistant", "content": replyText},
				"finish_reason": nil,
			}},
		}
		stopChunk := map[string]any{
			"id":      completionID,
			"object":  "chat.completion.chunk",
			"created": nowSec,
			"model":   providerID + "/" + modelID,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{
				"prompt_tokens":     promptToks,
				"completion_tokens": completionToks,
				"total_tokens":      promptToks + completionToks,
			},
		}
		b1, _ := json.Marshal(chunk)
		b2, _ := json.Marshal(stopChunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", b1, b2)
		_ = http.NewResponseController(w).Flush()
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": nowSec,
		"model":   providerID + "/" + modelID,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": replyText,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     promptToks,
			"completion_tokens": completionToks,
			"total_tokens":      promptToks + completionToks,
		},
	})
}

func extractRelayMaxTokens(payload map[string]any) int {
	for _, k := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		if v, ok := payload[k].(float64); ok && int(v) > 0 {
			return int(v)
		}
	}
	return 0
}

func (s *Service) callRelayGoogleChat(
	ctx context.Context,
	baseURL, authSecret string,
	headers map[string]string,
	modelID string,
	payload map[string]any,
) (string, int, int, int, []byte, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	targetURL := googleModelsBaseURL(baseURL) + "/" + url.PathEscape(cleanModel) + ":generateContent"

	var sysTexts []string
	var contents []map[string]any
	if msgs, ok := payload["messages"].([]any); ok {
		for _, item := range msgs {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role, _ := m["role"].(string)
			txt, imgs := extractRelayInputImagesAndText(m["content"])
			if role == "system" || role == "developer" {
				if strings.TrimSpace(txt) != "" {
					sysTexts = append(sysTexts, txt)
				}
				continue
			}
			gRole := "user"
			if role == "assistant" {
				gRole = "model"
			}
			var parts []map[string]any
			for _, img := range imgs {
				parts = append(parts, map[string]any{
					"inlineData": map[string]string{
						"mimeType": img.Mime,
						"data":     base64.StdEncoding.EncodeToString(img.Data),
					},
				})
			}
			if txt != "" || len(parts) == 0 {
				parts = append(parts, map[string]any{"text": txt})
			}
			contents = append(contents, map[string]any{"role": gRole, "parts": parts})
		}
	}
	if len(contents) == 0 {
		contents = []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "Hello"}}}}
	}

	gBody := map[string]any{"contents": contents}
	if len(sysTexts) > 0 {
		gBody["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": strings.Join(sysTexts, "\n\n")}},
		}
	}
	genCfg := map[string]any{}
	if maxTok := extractRelayMaxTokens(payload); maxTok > 0 {
		genCfg["maxOutputTokens"] = maxTok
	}
	if temp, ok := payload["temperature"].(float64); ok {
		genCfg["temperature"] = temp
	}
	if len(genCfg) > 0 {
		gBody["generationConfig"] = genCfg
	}

	conn := imageGenConn{BaseURL: baseURL, APIKey: authSecret, Headers: headers}
	status, respBytes, err := s.doJSONImageRequest(ctx, conn, true, targetURL, gBody, 120*time.Second)
	if err != nil {
		return "", 0, 0, 0, nil, err
	}
	if status < 200 || status >= 300 {
		return "", 0, 0, status, respBytes, nil
	}

	var parsed struct {
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	_ = json.Unmarshal(respBytes, &parsed)
	var pTok, cTok int
	if parsed.UsageMetadata != nil {
		pTok = parsed.UsageMetadata.PromptTokenCount
		cTok = parsed.UsageMetadata.CandidatesTokenCount
	}
	return extractLLMTextContent(respBytes), pTok, cTok, status, nil, nil
}

func (s *Service) callRelayAnthropicChat(
	ctx context.Context,
	baseURL, authSecret string,
	headers map[string]string,
	modelID string,
	payload map[string]any,
) (string, int, int, int, []byte, error) {
	targetURL := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/messages"
	var sysTexts []string
	var messages []map[string]any
	if msgs, ok := payload["messages"].([]any); ok {
		for _, item := range msgs {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role, _ := m["role"].(string)
			txt, imgs := extractRelayInputImagesAndText(m["content"])
			if role == "system" || role == "developer" {
				if strings.TrimSpace(txt) != "" {
					sysTexts = append(sysTexts, txt)
				}
				continue
			}
			aRole := "user"
			if role == "assistant" {
				aRole = "assistant"
			}
			var blocks []map[string]any
			for _, img := range imgs {
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]string{
						"type":       "base64",
						"media_type": img.Mime,
						"data":       base64.StdEncoding.EncodeToString(img.Data),
					},
				})
			}
			if txt != "" || len(blocks) == 0 {
				blocks = append(blocks, map[string]any{"type": "text", "text": txt})
			}
			messages = append(messages, map[string]any{"role": aRole, "content": blocks})
		}
	}
	if len(messages) == 0 {
		messages = []map[string]any{{"role": "user", "content": "Hello"}}
	}
	maxTok := extractRelayMaxTokens(payload)
	if maxTok <= 0 {
		maxTok = 4096
	}
	aBody := map[string]any{
		"model":      modelID,
		"max_tokens": maxTok,
		"messages":   messages,
	}
	if len(sysTexts) > 0 {
		aBody["system"] = strings.Join(sysTexts, "\n\n")
	}
	if temp, ok := payload["temperature"].(float64); ok && !isClaudeSamplingForbiddenModel(modelID) {
		aBody["temperature"] = temp
	}

	rawReq, _ := json.Marshal(aBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(rawReq))
	if err != nil {
		return "", 0, 0, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}
	if authSecret != "" && req.Header.Get("x-api-key") == "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("x-api-key", authSecret)
	}

	transport := providerTransport
	if isLoopbackURL(baseURL) {
		transport = localTransport
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second, Transport: transport}).Do(req)
	if err != nil {
		return "", 0, 0, 0, nil, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxRelayBody))
	if err != nil {
		return "", 0, 0, resp.StatusCode, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, 0, resp.StatusCode, respBytes, nil
	}
	var parsed struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(respBytes, &parsed)
	var pTok, cTok int
	if parsed.Usage != nil {
		pTok = parsed.Usage.InputTokens
		cTok = parsed.Usage.OutputTokens
	}
	return extractLLMTextContent(respBytes), pTok, cTok, resp.StatusCode, nil, nil
}

func (s *Service) serveRelayEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var payload struct {
		Model string `json:"model"`
		Input any    `json:"input"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, maxRelayBody)).Decode(&payload) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var inputs []string
	switch v := payload.Input.(type) {
	case string:
		if v != "" {
			inputs = []string{v}
		}
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				inputs = append(inputs, str)
			}
		}
	}
	if len(inputs) == 0 {
		writeJSONError(w, http.StatusBadRequest, "input is required")
		return
	}
	var pid, mid string
	if raw := strings.TrimSpace(payload.Model); raw != "" {
		if p, m, ok := strings.Cut(raw, "/"); ok {
			pid, mid = strings.TrimSpace(p), strings.TrimSpace(m)
		} else {
			mid = raw
		}
	}
	res, err := s.EmbedTexts(r.Context(), llm.EmbeddingRequest{
		ProviderID: pid,
		ModelID:    mid,
		Inputs:     inputs,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"},
		})
		return
	}
	data := make([]map[string]any, len(res.Vectors))
	for i, vec := range res.Vectors {
		data[i] = map[string]any{
			"object":    "embedding",
			"index":     i,
			"embedding": vec,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"model":  res.ProviderID + "/" + res.ModelID,
		"data":   data,
	})
}

func (s *Service) serveRelayImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var payload struct {
		Model       string `json:"model"`
		Prompt      string `json:"prompt"`
		N           int    `json:"n"`
		Size        string `json:"size"`
		AspectRatio string `json:"aspect_ratio"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, maxRelayBody)).Decode(&payload) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var pid, mid string
	if raw := strings.TrimSpace(payload.Model); raw != "" {
		if p, m, ok := strings.Cut(raw, "/"); ok {
			pid, mid = strings.TrimSpace(p), strings.TrimSpace(m)
		} else {
			mid = raw
		}
	}
	aspect := strings.TrimSpace(payload.AspectRatio)
	if aspect == "" {
		switch strings.TrimSpace(payload.Size) {
		case "1792x1024", "1536x1024":
			aspect = "16:9"
		case "1024x1792", "1024x1536":
			aspect = "9:16"
		}
	}
	res, err := s.GenerateImage(r.Context(), llm.ImageRequest{
		ProviderID:  pid,
		ModelID:     mid,
		Prompt:      payload.Prompt,
		Count:       payload.N,
		AspectRatio: aspect,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"},
		})
		return
	}
	data := make([]map[string]any, len(res.Images))
	for i, img := range res.Images {
		data[i] = map[string]any{
			"b64_json":       base64.StdEncoding.EncodeToString(img.Data),
			"revised_prompt": res.Text,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	})
}

func (s *Service) serveRelayAudioTranscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if err := r.ParseMultipartForm(maxRelayBody); err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart/form-data is required")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	audioBytes, err := io.ReadAll(io.LimitReader(file, maxRelayBody))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var pid, mid string
	if raw := strings.TrimSpace(r.FormValue("model")); raw != "" {
		if p, m, ok := strings.Cut(raw, "/"); ok {
			pid, mid = strings.TrimSpace(p), strings.TrimSpace(m)
		} else if !strings.EqualFold(raw, "whisper-1") {
			mid = raw
		}
	}
	res, err := s.TranscribeAudio(r.Context(), llm.AudioTranscribeRequest{
		ProviderID: pid,
		ModelID:    mid,
		Filename:   hdr.Filename,
		Mime:       hdr.Header.Get("Content-Type"),
		Data:       audioBytes,
		Language:   r.FormValue("language"),
		Prompt:     r.FormValue("prompt"),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": res.Text})
}

func (s *Service) serveRelayResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRelayBody+1))
	if err != nil || len(raw) > maxRelayBody {
		writeJSONError(w, http.StatusBadRequest, "invalid or oversized request body")
		return
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		writeJSONError(w, http.StatusBadRequest, "request body must be a JSON object")
		return
	}
	requested, _ := payload["model"].(string)
	sessionKey := deriveRelaySessionKey(r, payload)
	providerID, modelID, err := s.relayResolveModelWithSession(requested, sessionKey)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"},
		})
		return
	}
	baseURL, authSecret, npm, headers := s.resolveProviderConnection(providerID, "", "")
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	proto := effectiveProviderProtocol(providerID, npm, baseURL)
	if proto != "openai" {
		// Convert simple Responses input to messages and reuse translated chat
		var msgs []any
		if instr, ok := payload["instructions"].(string); ok && strings.TrimSpace(instr) != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": instr})
		}
		switch inp := payload["input"].(type) {
		case string:
			msgs = append(msgs, map[string]any{"role": "user", "content": inp})
		case []any:
			msgs = append(msgs, inp...)
		}
		chatPayload := map[string]any{
			"model":    requested,
			"messages": msgs,
		}
		var reply string
		var status int
		var rawErr []byte
		if proto == "google" {
			reply, _, _, status, rawErr, err = s.callRelayGoogleChat(r.Context(), baseURL, authSecret, headers, modelID, chatPayload)
		} else {
			reply, _, _, status, rawErr, err = s.callRelayAnthropicChat(r.Context(), baseURL, authSecret, headers, modelID, chatPayload)
		}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error()}})
			return
		}
		if status < 200 || status >= 300 {
			writeRelayUpstreamError(w, status, rawErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":          fmt.Sprintf("resp_hoxi_%d", time.Now().UnixNano()),
			"object":      "response",
			"model":       providerID + "/" + modelID,
			"output_text": reply,
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": reply,
				}},
			}},
		})
		return
	}

	payload["model"] = modelID
	body, _ := json.Marshal(payload)
	outReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	outReq.Header.Set("Content-Type", "application/json")
	outReq.Header.Set("Accept", "application/json")
	if stream, _ := payload["stream"].(bool); stream {
		outReq.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range headers {
		if k != "" && v != "" {
			outReq.Header.Set(k, v)
		}
	}
	if authSecret != "" {
		outReq.Header.Set("Authorization", "Bearer "+authSecret)
	}
	transport := providerTransport
	if isLoopbackURL(baseURL) {
		transport = localTransport
	}
	resp, err := (&http.Client{Transport: transport}).Do(outReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		writeRelayUpstreamError(w, resp.StatusCode, rawErr)
		return
	}
	copyRelayHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_ = streamRelayBody(w, resp.Body)
}
