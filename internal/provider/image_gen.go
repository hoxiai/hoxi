package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

var _ llm.ImageClient = (*Service)(nil)

type imageGenConn struct {
	ProviderID string
	ModelID    string
	BaseURL    string
	APIKey     string
	NPM        string
	Headers    map[string]string
}

type imageGenExecOptions struct {
	aspectRatio string
	imageSize   string
	count       int
}

const (
	defaultImageGenTimeout   = 120 * time.Second
	defaultImageProbeTimeout = 45 * time.Second
	maxImageResponseBytes    = 32 << 20
)

// IsImageModel reports whether providerID/modelID is an image generation model,
// checking both canonical ID patterns and explicit model config modalities.
func (s *Service) IsImageModel(providerID, modelID string) bool {
	mid := strings.TrimSpace(modelID)
	if mid == "" {
		return false
	}
	if IsImageGenerationModel(mid, "", "") {
		return true
	}
	pid := strings.TrimSpace(providerID)
	if pid == "" || s == nil {
		return false
	}
	for _, src := range []map[string]interface{}{s.loadHoxiProvidersMap(), s.loadOpenCodeImportedProviderMap()} {
		pObj, ok := src[pid].(map[string]interface{})
		if !ok {
			continue
		}
		mMap, ok := pObj["models"].(map[string]interface{})
		if !ok {
			continue
		}
		mObj, ok := mMap[mid].(map[string]interface{})
		if !ok {
			for k, v := range mMap {
				if strings.EqualFold(k, mid) {
					mObj, _ = v.(map[string]interface{})
					break
				}
			}
		}
		if mObj == nil {
			continue
		}
		cat, _ := mObj["category"].(string)
		if IsImageGenerationModel(mid, cat, "") || hasImageOutputModality(mObj) {
			return true
		}
	}
	return false
}

func isGoogleImagenModelID(modelID string) bool {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelID)), "models/")
	return strings.HasPrefix(clean, "imagen") || strings.Contains(clean, "/imagen")
}

func (s *Service) resolveImageConnection(providerID, modelID string) (imageGenConn, error) {
	pid := strings.TrimSpace(providerID)
	mid := strings.TrimSpace(modelID)
	if pid == "" || mid == "" {
		return imageGenConn{}, llm.ErrUnavailable
	}
	baseURL, apiKey, npm, headers := s.resolveProviderConnection(pid, "", "")
	if modelURL, modelKey, modelNPM, modelHeaders := s.resolveOpenCodeLiveModelConfig(pid, mid); modelURL != "" || modelNPM != "" || len(modelHeaders) > 0 {
		if baseURL == "" && modelURL != "" {
			baseURL = s.localizeBridgeURL(modelURL)
		}
		if npm == "" && modelNPM != "" {
			npm = modelNPM
		}
		if apiKey == "" && modelKey != "" && s.isAuthorizedProviderBaseURL(pid, baseURL, modelURL) {
			apiKey = modelKey
		}
		for hk, hv := range modelHeaders {
			if _, exists := headers[hk]; !exists {
				headers[hk] = hv
			}
		}
	}
	if !isHTTPEndpoint(baseURL) {
		return imageGenConn{}, llm.ErrUnavailable
	}
	return imageGenConn{
		ProviderID: pid,
		ModelID:    mid,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		NPM:        npm,
		Headers:    headers,
	}, nil
}

// GenerateImage executes one image generation or image-editing turn against the
// configured provider (Google Gemini :generateContent, Google Imagen :predict,
// or OpenAI-compatible /chat/completions and /images/generations).
func (s *Service) GenerateImage(ctx context.Context, req llm.ImageRequest) (resp llm.ImageResponse, resultErr error) {
	pid := strings.TrimSpace(req.ProviderID)
	mid := strings.TrimSpace(req.ModelID)
	if pid == "" || mid == "" {
		resolvedPID, resolvedMID, ok := s.ResolveImageModel(pid, mid)
		if !ok {
			return llm.ImageResponse{}, llm.ErrUnavailable
		}
		pid, mid = resolvedPID, resolvedMID
	}
	conn, err := s.resolveImageConnection(pid, mid)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultImageGenTimeout
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		if len(req.InputImages) > 0 {
			prompt = "Generate an enhanced variation of the attached image."
		} else {
			return llm.ImageResponse{}, errors.New("prompt text is required for image generation")
		}
	}

	opts := imageGenExecOptions{
		aspectRatio: normalizeAspectRatio(req.AspectRatio),
		imageSize:   normalizeImageSizeTier(req.ImageSize),
		count:       clampImageCount(req.Count),
	}

	started := time.Now()
	defer func() {
		level, msg := "info", "image generation completed"
		if resultErr != nil {
			level, msg = "error", "image generation failed"
			if errors.Is(resultErr, context.Canceled) {
				level, msg = "info", "image generation canceled"
			}
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

	resp, resultErr = s.executeImageRequest(ctx, conn, prompt, req.InputImages, opts, false, timeout)
	if resultErr == nil {
		resp.ProviderID = conn.ProviderID
		resp.ModelID = conn.ModelID
	}
	return resp, resultErr
}

func (s *Service) probeImageModel(ctx context.Context, conn imageGenConn) error {
	_, err := s.executeImageRequest(ctx, conn, "Simple minimal blue circle icon on white background", nil, imageGenExecOptions{count: 1}, true, defaultImageProbeTimeout)
	return err
}

func (s *Service) executeImageRequest(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts imageGenExecOptions,
	probeOnly bool,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	proto := effectiveProviderProtocol(conn.ProviderID, conn.NPM, conn.BaseURL)
	if proto == "google" {
		if isGoogleImagenModelID(conn.ModelID) {
			return s.googleImagenPredict(ctx, conn, prompt, opts, timeout)
		}
		probePrompt := prompt
		if probeOnly {
			probePrompt = "Reply with OK only, no image needed."
		}
		return s.googleGeminiGenerateImage(ctx, conn, probePrompt, inputImages, opts, probeOnly, timeout)
	}

	if isConversationalImageModelID(conn.ModelID) {
		chatPrompt := prompt
		if probeOnly {
			chatPrompt = "Reply with OK only, no image needed."
		} else if opts.aspectRatio != "" {
			chatPrompt = prompt + "\n(Preferred aspect ratio: " + opts.aspectRatio + ")"
		}
		resp, err := s.openAIChatGenerateImage(ctx, conn, chatPrompt, inputImages, probeOnly, timeout)
		if err == nil {
			return resp, nil
		}
		if len(inputImages) == 0 && shouldFallbackImageEndpoint(err) {
			if imgResp, imgErr := s.openAIImagesGenerate(ctx, conn, prompt, nil, opts, timeout); imgErr == nil {
				return imgResp, nil
			}
		}
		return llm.ImageResponse{}, err
	}

	resp, err := s.openAIImagesGenerate(ctx, conn, prompt, inputImages, opts, timeout)
	if err == nil {
		return resp, nil
	}
	if shouldFallbackImageEndpoint(err) {
		chatPrompt := prompt
		if !probeOnly && opts.aspectRatio != "" {
			chatPrompt = prompt + "\n(Preferred aspect ratio: " + opts.aspectRatio + ")"
		}
		if chatResp, chatErr := s.openAIChatGenerateImage(ctx, conn, chatPrompt, inputImages, probeOnly, timeout); chatErr == nil {
			return chatResp, nil
		}
	}
	return llm.ImageResponse{}, err
}

func (s *Service) doJSONImageRequest(
	parentCtx context.Context,
	conn imageGenConn,
	isGoogle bool,
	targetURL string,
	payload any,
	timeout time.Duration,
) (int, []byte, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	send := func(reqURL string, includeBearer bool) (int, []byte, error) {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(rawPayload))
		if err != nil {
			return 0, nil, err
		}
		outReq.Header.Set("Accept", "application/json")
		outReq.Header.Set("Content-Type", "application/json")
		applyImageAuthHeaders(outReq, conn, isGoogle, includeBearer)
		client := &http.Client{Timeout: timeout, Transport: selectImageTransport(reqURL)}
		resp, err := client.Do(outReq)
		if err != nil {
			return 0, nil, errors.New(redactSecretInText(err.Error(), conn.APIKey))
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
		if readErr != nil {
			return resp.StatusCode, nil, fmt.Errorf("read image response: %w", readErr)
		}
		return resp.StatusCode, body, nil
	}

	status, body, err := send(targetURL, false)
	if err != nil {
		return status, nil, err
	}
	if isGoogle && conn.APIKey != "" && !isOfficialGoogleHost(targetURL) &&
		(status == http.StatusUnauthorized || status == http.StatusForbidden) {
		if rStatus, rBody, rErr := send(appendGoogleQueryKey(targetURL, conn.APIKey), shouldSendBearerOnGoogleRetry(conn.APIKey)); rErr == nil {
			status, body = rStatus, rBody
		}
	}
	return status, body, nil
}

func applyImageAuthHeaders(outReq *http.Request, conn imageGenConn, isGoogle, includeBearer bool) {
	hasCustomAuth := false
	for k, v := range conn.Headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "x-goog-api-key") || strings.EqualFold(k, "x-api-key") {
				hasCustomAuth = true
			}
			outReq.Header.Set(k, v)
		}
	}
	if conn.APIKey == "" || hasCustomAuth {
		return
	}
	if isGoogle {
		outReq.Header.Set("x-goog-api-key", conn.APIKey)
		if includeBearer {
			outReq.Header.Set("Authorization", "Bearer "+conn.APIKey)
		}
		return
	}
	outReq.Header.Set("Authorization", "Bearer "+conn.APIKey)
}

func selectImageTransport(targetURL string) *http.Transport {
	if isLoopbackURL(targetURL) {
		return localTransport
	}
	return providerTransport
}

func (s *Service) googleImagenPredict(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	opts imageGenExecOptions,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(conn.ModelID), "models/")
	targetURL := googleModelsBaseURL(conn.BaseURL) + "/" + url.PathEscape(cleanModel) + ":predict"
	params := map[string]any{"sampleCount": clampImageCount(opts.count)}
	if opts.aspectRatio != "" {
		params["aspectRatio"] = opts.aspectRatio
	}
	payload := map[string]any{
		"instances":  []map[string]any{{"prompt": prompt}},
		"parameters": params,
	}
	status, body, err := s.doJSONImageRequest(ctx, conn, true, targetURL, payload, timeout)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	if status == http.StatusBadRequest && opts.aspectRatio != "" {
		payload["parameters"] = map[string]any{"sampleCount": clampImageCount(opts.count)}
		status, body, err = s.doJSONImageRequest(ctx, conn, true, targetURL, payload, timeout)
		if err != nil {
			return llm.ImageResponse{}, err
		}
	}
	if status < 200 || status >= 300 {
		return llm.ImageResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var parsed struct {
		Predictions []struct {
			BytesBase64Encoded string `json:"bytesBase64Encoded"`
			MimeType           string `json:"mimeType"`
			Image              *struct {
				BytesBase64Encoded string `json:"bytesBase64Encoded"`
				MimeType           string `json:"mimeType"`
			} `json:"image"`
		} `json:"predictions"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return llm.ImageResponse{}, fmt.Errorf("decode imagen response: %w", err)
	}
	var out llm.ImageResponse
	for _, p := range parsed.Predictions {
		b64 := strings.TrimSpace(p.BytesBase64Encoded)
		mime := strings.TrimSpace(p.MimeType)
		if b64 == "" && p.Image != nil {
			b64 = strings.TrimSpace(p.Image.BytesBase64Encoded)
			if mime == "" {
				mime = strings.TrimSpace(p.Image.MimeType)
			}
		}
		if raw, decErr := decodeBase64Flexible(b64); decErr == nil && len(raw) > 0 {
			out.Images = append(out.Images, llm.GeneratedImage{
				Mime: normalizeGeneratedImageMime(mime, raw),
				Data: raw,
			})
		}
	}
	if len(out.Images) == 0 {
		return llm.ImageResponse{}, errors.New("Imagen response contained no generated images")
	}
	return out, nil
}

func (s *Service) googleGeminiGenerateImage(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts imageGenExecOptions,
	allowTextOnly bool,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(conn.ModelID), "models/")
	targetURL := googleModelsBaseURL(conn.BaseURL) + "/" + url.PathEscape(cleanModel) + ":generateContent"

	buildPayload := func(modalities []string, textPrompt string, includeImageCfg bool) map[string]any {
		parts := make([]map[string]any, 0, len(inputImages)+1)
		for _, img := range inputImages {
			if len(img.Data) == 0 {
				continue
			}
			mime := normalizeGeneratedImageMime(img.Mime, img.Data)
			parts = append(parts, map[string]any{
				"inlineData": map[string]string{
					"mimeType": mime,
					"data":     base64.StdEncoding.EncodeToString(img.Data),
				},
			})
		}
		parts = append(parts, map[string]any{"text": textPrompt})
		genCfg := map[string]any{
			"responseModalities": modalities,
		}
		if includeImageCfg && !allowTextOnly && (opts.aspectRatio != "" || opts.imageSize != "") {
			imgCfg := map[string]any{}
			if opts.aspectRatio != "" {
				imgCfg["aspectRatio"] = opts.aspectRatio
			}
			if opts.imageSize != "" {
				imgCfg["imageSize"] = opts.imageSize
			}
			genCfg["imageConfig"] = imgCfg
		}
		return map[string]any{
			"contents": []map[string]any{
				{"role": "user", "parts": parts},
			},
			"generationConfig": genCfg,
		}
	}

	hasImgCfg := !allowTextOnly && (opts.aspectRatio != "" || opts.imageSize != "")
	status, body, err := s.doJSONImageRequest(ctx, conn, true, targetURL, buildPayload([]string{"TEXT", "IMAGE"}, prompt, true), timeout)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	if status == http.StatusBadRequest && hasImgCfg && isGoogleImageConfigRejection(body) {
		hasImgCfg = false
		status, body, err = s.doJSONImageRequest(ctx, conn, true, targetURL, buildPayload([]string{"TEXT", "IMAGE"}, prompt, false), timeout)
		if err != nil {
			return llm.ImageResponse{}, err
		}
	}
	if status == http.StatusBadRequest && isGoogleModalityRejection(body) {
		retryPrompt := prompt
		if allowTextOnly {
			retryPrompt = "Simple minimal blue circle icon on white background"
		}
		status, body, err = s.doJSONImageRequest(ctx, conn, true, targetURL, buildPayload([]string{"IMAGE"}, retryPrompt, hasImgCfg), timeout)
		if err != nil {
			return llm.ImageResponse{}, err
		}
	}
	if status < 200 || status >= 300 {
		return llm.ImageResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}

	resp, finishReason, parseErr := parseGoogleGenerateContentImageResponse(body)
	if parseErr != nil {
		return llm.ImageResponse{}, parseErr
	}
	if len(resp.Images) > 0 || (allowTextOnly && strings.TrimSpace(resp.Text) != "") {
		return resp, nil
	}
	if !allowTextOnly && !isSafetyFinishReason(finishReason) {
		rStatus, rBody, rErr := s.doJSONImageRequest(ctx, conn, true, targetURL, buildPayload([]string{"IMAGE"}, prompt, hasImgCfg), timeout)
		if rErr == nil && rStatus >= 200 && rStatus < 300 {
			if rResp, _, rParseErr := parseGoogleGenerateContentImageResponse(rBody); rParseErr == nil && len(rResp.Images) > 0 {
				if strings.TrimSpace(rResp.Text) == "" {
					rResp.Text = resp.Text
				}
				return rResp, nil
			}
		}
	}
	if finishReason != "" && finishReason != "STOP" {
		if strings.TrimSpace(resp.Text) != "" {
			return llm.ImageResponse{}, fmt.Errorf("image generation stopped (%s): %s", finishReason, resp.Text)
		}
		return llm.ImageResponse{}, fmt.Errorf("image generation stopped (%s)", finishReason)
	}
	if strings.TrimSpace(resp.Text) != "" {
		return llm.ImageResponse{}, fmt.Errorf("model returned text without generated image: %s", resp.Text)
	}
	return llm.ImageResponse{}, errors.New("Gemini response contained no generated image")
}

func parseGoogleGenerateContentImageResponse(raw []byte) (llm.ImageResponse, string, error) {
	type inlineBlob struct {
		MimeType    string `json:"mimeType"`
		AltMimeType string `json:"mime_type"`
		Data        string `json:"data"`
	}
	var parsed struct {
		Candidates []struct {
			FinishReason  string `json:"finishReason"`
			FinishMessage string `json:"finishMessage"`
			Content       struct {
				Parts []struct {
					Text          string      `json:"text"`
					Thought       bool        `json:"thought"`
					InlineData    *inlineBlob `json:"inlineData"`
					AltInlineData *inlineBlob `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback *struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.ImageResponse{}, "", fmt.Errorf("decode Gemini response: %w", err)
	}
	var out llm.ImageResponse
	if parsed.UsageMetadata != nil {
		out.InputTokens = parsed.UsageMetadata.PromptTokenCount
		out.OutputTokens = parsed.UsageMetadata.CandidatesTokenCount
	}
	if len(parsed.Candidates) == 0 {
		if parsed.PromptFeedback != nil && strings.TrimSpace(parsed.PromptFeedback.BlockReason) != "" {
			return out, parsed.PromptFeedback.BlockReason, nil
		}
		return out, "", nil
	}
	cand := parsed.Candidates[0]
	var texts []string
	for _, p := range cand.Content.Parts {
		blob := p.InlineData
		if blob == nil {
			blob = p.AltInlineData
		}
		if blob != nil && strings.TrimSpace(blob.Data) != "" {
			if decoded, err := decodeBase64Flexible(blob.Data); err == nil && len(decoded) > 0 {
				mime := firstNonEmptyImageStr(blob.MimeType, blob.AltMimeType)
				out.Images = append(out.Images, llm.GeneratedImage{
					Mime: normalizeGeneratedImageMime(mime, decoded),
					Data: decoded,
				})
			}
			continue
		}
		if !p.Thought && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	out.Text = strings.Join(texts, "\n\n")
	if out.Text == "" && strings.TrimSpace(cand.FinishMessage) != "" {
		out.Text = strings.TrimSpace(cand.FinishMessage)
	}
	return out, strings.TrimSpace(cand.FinishReason), nil
}

func isGoogleModalityRejection(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "responsemodalities") ||
		strings.Contains(lower, "response_modalities") ||
		strings.Contains(lower, "modality") ||
		strings.Contains(lower, "modalities")
}

func isGoogleImageConfigRejection(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "imageconfig") ||
		strings.Contains(lower, "image_config") ||
		strings.Contains(lower, "aspectratio") ||
		strings.Contains(lower, "aspect_ratio") ||
		strings.Contains(lower, "imagesize") ||
		strings.Contains(lower, "image_size")
}

func isSafetyFinishReason(reason string) bool {
	upper := strings.ToUpper(strings.TrimSpace(reason))
	return strings.Contains(upper, "SAFETY") || strings.Contains(upper, "BLOCK") || strings.Contains(upper, "PROHIBITED")
}
