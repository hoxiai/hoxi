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
	"regexp"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

var (
	mdDataImageRe = regexp.MustCompile(`!\[[^\]]*\]\((data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=\s]+)\)`)
	rawDataImgRe  = regexp.MustCompile(`data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]{32,}`)
	mdHTTPImageRe = regexp.MustCompile(`!\[[^\]]*\]\((https?://[^\s)]+)\)`)
)

func (s *Service) openAIChatGenerateImage(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	allowTextOnly bool,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	targetURL := trimmedBase + "/chat/completions"

	var userContent any = prompt
	if len(inputImages) > 0 {
		blocks := make([]map[string]any, 0, len(inputImages)+1)
		for _, img := range inputImages {
			if len(img.Data) == 0 {
				continue
			}
			mime := normalizeGeneratedImageMime(img.Mime, img.Data)
			dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]string{"url": dataURI},
			})
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": prompt})
		userContent = blocks
	}

	buildBody := func(withModalities bool) map[string]any {
		m := map[string]any{
			"model": conn.ModelID,
			"messages": []map[string]any{
				{"role": "user", "content": userContent},
			},
		}
		if withModalities {
			m["modalities"] = []string{"text", "image"}
		}
		return m
	}

	status, body, err := s.doJSONImageRequest(ctx, conn, false, targetURL, buildBody(true), timeout)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	if (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity) && isModalitiesParamRejection(body) {
		status, body, err = s.doJSONImageRequest(ctx, conn, false, targetURL, buildBody(false), timeout)
		if err != nil {
			return llm.ImageResponse{}, err
		}
	}
	if status < 200 || status >= 300 {
		return llm.ImageResponse{}, &imageHTTPStatusError{
			status: status,
			msg:    redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey),
		}
	}

	resp, parseErr := s.parseOpenAIChatImageResponse(ctx, body, timeout)
	if parseErr != nil {
		return llm.ImageResponse{}, parseErr
	}
	if len(resp.Images) > 0 || (allowTextOnly && strings.TrimSpace(resp.Text) != "") {
		return resp, nil
	}
	if strings.TrimSpace(resp.Text) != "" {
		return llm.ImageResponse{}, fmt.Errorf("model returned text without generated image: %s", resp.Text)
	}
	return llm.ImageResponse{}, errors.New("chat completion response contained no generated image")
}

func (s *Service) parseOpenAIChatImageResponse(ctx context.Context, raw []byte, timeout time.Duration) (llm.ImageResponse, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage   `json:"content"`
				Images  []json.RawMessage `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.ImageResponse{}, fmt.Errorf("decode chat image response: %w", err)
	}
	var out llm.ImageResponse
	if parsed.Usage != nil {
		out.InputTokens = parsed.Usage.PromptTokens
		out.OutputTokens = parsed.Usage.CompletionTokens
	}
	if len(parsed.Choices) == 0 {
		return out, nil
	}
	msg := parsed.Choices[0].Message

	for _, rawImg := range msg.Images {
		if img, ok := s.resolveOpenAIImageItem(ctx, rawImg, timeout); ok {
			out.Images = append(out.Images, img)
		}
	}

	trimmedContent := bytes.TrimSpace(msg.Content)
	if len(trimmedContent) > 0 && trimmedContent[0] == '[' {
		var parts []json.RawMessage
		if json.Unmarshal(trimmedContent, &parts) == nil {
			var texts []string
			for _, partRaw := range parts {
				var meta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				_ = json.Unmarshal(partRaw, &meta)
				if (meta.Type == "" || meta.Type == "text") && strings.TrimSpace(meta.Text) != "" {
					cleaned := s.extractEmbeddedImagesFromText(ctx, meta.Text, &out.Images, timeout)
					if strings.TrimSpace(cleaned) != "" {
						texts = append(texts, strings.TrimSpace(cleaned))
					}
					continue
				}
				if img, ok := s.resolveOpenAIImageItem(ctx, partRaw, timeout); ok {
					out.Images = append(out.Images, img)
				}
			}
			out.Text = strings.Join(texts, "\n\n")
		}
	} else if len(trimmedContent) > 0 && trimmedContent[0] == '"' {
		var textContent string
		if json.Unmarshal(trimmedContent, &textContent) == nil {
			out.Text = strings.TrimSpace(s.extractEmbeddedImagesFromText(ctx, textContent, &out.Images, timeout))
		}
	}
	return out, nil
}

func (s *Service) extractEmbeddedImagesFromText(ctx context.Context, text string, dst *[]llm.GeneratedImage, timeout time.Duration) string {
	cleaned := mdDataImageRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := mdDataImageRe.FindStringSubmatch(match)
		if len(sub) >= 2 {
			if img, ok := decodeDataURIImage(sub[1]); ok {
				*dst = append(*dst, img)
				return ""
			}
		}
		return match
	})
	cleaned = rawDataImgRe.ReplaceAllStringFunc(cleaned, func(match string) string {
		if img, ok := decodeDataURIImage(match); ok {
			*dst = append(*dst, img)
			return ""
		}
		return match
	})
	cleaned = mdHTTPImageRe.ReplaceAllStringFunc(cleaned, func(match string) string {
		sub := mdHTTPImageRe.FindStringSubmatch(match)
		if len(sub) >= 2 {
			if img, err := fetchRemoteImage(ctx, sub[1], timeout); err == nil && len(img.Data) > 0 {
				*dst = append(*dst, img)
				return ""
			}
		}
		return match
	})
	return strings.TrimSpace(cleaned)
}

func (s *Service) resolveOpenAIImageItem(ctx context.Context, raw json.RawMessage, timeout time.Duration) (llm.GeneratedImage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return llm.GeneratedImage{}, false
	}
	if trimmed[0] == '"' {
		var str string
		if json.Unmarshal(trimmed, &str) == nil {
			return resolveImageStringValue(ctx, str, "", timeout)
		}
		return llm.GeneratedImage{}, false
	}
	var obj struct {
		URL           string `json:"url"`
		B64JSON       string `json:"b64_json"`
		Data          string `json:"data"`
		MimeType      string `json:"mime_type"`
		AltMimeType   string `json:"mimeType"`
		RevisedPrompt string `json:"revised_prompt"`
		ImageURL      any    `json:"image_url"`
		InlineData    *struct {
			MimeType    string `json:"mimeType"`
			AltMimeType string `json:"mime_type"`
			Data        string `json:"data"`
		} `json:"inline_data"`
		AltInlineData *struct {
			MimeType    string `json:"mimeType"`
			AltMimeType string `json:"mime_type"`
			Data        string `json:"data"`
		} `json:"inlineData"`
	}
	if json.Unmarshal(trimmed, &obj) != nil {
		return llm.GeneratedImage{}, false
	}
	mime := firstNonEmptyImageStr(obj.MimeType, obj.AltMimeType)
	for _, inline := range []*struct {
		MimeType    string `json:"mimeType"`
		AltMimeType string `json:"mime_type"`
		Data        string `json:"data"`
	}{obj.InlineData, obj.AltInlineData} {
		if inline != nil && strings.TrimSpace(inline.Data) != "" {
			if decoded, err := decodeBase64Flexible(inline.Data); err == nil && len(decoded) > 0 {
				m := firstNonEmptyImageStr(inline.MimeType, inline.AltMimeType, mime)
				return llm.GeneratedImage{Mime: normalizeGeneratedImageMime(m, decoded), Data: decoded, RevisedPrompt: strings.TrimSpace(obj.RevisedPrompt)}, true
			}
		}
	}
	for _, b64Candidate := range []string{obj.B64JSON, obj.Data} {
		if strings.TrimSpace(b64Candidate) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(b64Candidate), "data:") {
			if img, ok := decodeDataURIImage(b64Candidate); ok {
				img.RevisedPrompt = strings.TrimSpace(obj.RevisedPrompt)
				return img, true
			}
		} else if decoded, err := decodeBase64Flexible(b64Candidate); err == nil && len(decoded) > 0 {
			return llm.GeneratedImage{
				Mime:          normalizeGeneratedImageMime(mime, decoded),
				Data:          decoded,
				RevisedPrompt: strings.TrimSpace(obj.RevisedPrompt),
			}, true
		}
	}
	urlCandidate := strings.TrimSpace(obj.URL)
	if urlCandidate == "" && obj.ImageURL != nil {
		switch v := obj.ImageURL.(type) {
		case string:
			urlCandidate = strings.TrimSpace(v)
		case map[string]any:
			if u, ok := v["url"].(string); ok {
				urlCandidate = strings.TrimSpace(u)
			}
		}
	}
	if urlCandidate != "" {
		if img, ok := resolveImageStringValue(ctx, urlCandidate, mime, timeout); ok {
			img.RevisedPrompt = strings.TrimSpace(obj.RevisedPrompt)
			return img, true
		}
	}
	return llm.GeneratedImage{}, false
}

func (s *Service) openAIImagesGenerate(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts imageGenExecOptions,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	if len(inputImages) > 0 {
		if editResp, editErr := s.openAIImagesEditMultipart(ctx, conn, prompt, inputImages[0], opts, timeout); editErr == nil {
			return editResp, nil
		}
	}
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	targetURL := trimmedBase + "/images/generations"
	count := clampImageCount(opts.count)
	if strings.Contains(strings.ToLower(conn.ModelID), "dall-e-3") {
		count = 1
	}
	payload := map[string]any{
		"model":  conn.ModelID,
		"prompt": prompt,
		"n":      count,
	}
	if opts.aspectRatio != "" {
		payload["size"] = openAIImageSizeForAspectRatio(conn.ModelID, opts.aspectRatio)
	}
	status, body, err := s.doJSONImageRequest(ctx, conn, false, targetURL, payload, timeout)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	if status == http.StatusBadRequest && opts.aspectRatio != "" {
		delete(payload, "size")
		status, body, err = s.doJSONImageRequest(ctx, conn, false, targetURL, payload, timeout)
		if err != nil {
			return llm.ImageResponse{}, err
		}
	}
	if status < 200 || status >= 300 {
		return llm.ImageResponse{}, &imageHTTPStatusError{
			status: status,
			msg:    redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey),
		}
	}
	return s.parseOpenAIImagesEndpointResponse(ctx, body, timeout)
}

func (s *Service) openAIImagesEditMultipart(
	parentCtx context.Context,
	conn imageGenConn,
	prompt string,
	input llm.ImageInput,
	opts imageGenExecOptions,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	if len(input.Data) == 0 {
		return llm.ImageResponse{}, errors.New("empty edit image")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", conn.ModelID)
	_ = mw.WriteField("prompt", prompt)
	_ = mw.WriteField("n", fmt.Sprintf("%d", clampImageCount(opts.count)))
	if opts.aspectRatio != "" {
		_ = mw.WriteField("size", openAIImageSizeForAspectRatio(conn.ModelID, opts.aspectRatio))
	}
	mime := normalizeGeneratedImageMime(input.Mime, input.Data)
	ext := ".png"
	if mime == "image/jpeg" {
		ext = ".jpg"
	} else if mime == "image/webp" {
		ext = ".webp"
	}
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="reference%s"`, ext))
	hdr.Set("Content-Type", mime)
	fw, err := mw.CreatePart(hdr)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	_, _ = fw.Write(input.Data)
	_ = mw.Close()

	targetURL := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/") + "/images/edits"
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, &buf)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", mw.FormDataContentType())
	applyImageAuthHeaders(outReq, conn, false, false)

	client := &http.Client{Timeout: timeout, Transport: selectImageTransport(targetURL)}
	resp, err := client.Do(outReq)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.ImageResponse{}, &imageHTTPStatusError{
			status: resp.StatusCode,
			msg:    redactSecretInText(extractAPIErrorMessage(resp.StatusCode, body), conn.APIKey),
		}
	}
	return s.parseOpenAIImagesEndpointResponse(parentCtx, body, timeout)
}

func (s *Service) parseOpenAIImagesEndpointResponse(ctx context.Context, raw []byte, timeout time.Duration) (llm.ImageResponse, error) {
	var parsed struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return llm.ImageResponse{}, fmt.Errorf("decode images response: %w", err)
	}
	var out llm.ImageResponse
	for _, item := range parsed.Data {
		if img, ok := s.resolveOpenAIImageItem(ctx, item, timeout); ok {
			out.Images = append(out.Images, img)
		}
	}
	if len(out.Images) == 0 {
		return llm.ImageResponse{}, errors.New("images endpoint returned no image data")
	}
	return out, nil
}

type imageHTTPStatusError struct {
	status int
	msg    string
}

func (e *imageHTTPStatusError) Error() string { return e.msg }

func shouldFallbackImageEndpoint(err error) bool {
	var stErr *imageHTTPStatusError
	if errors.As(err, &stErr) {
		switch stErr.status {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUnprocessableEntity, http.StatusNotImplemented:
			return true
		}
	}
	return false
}

func isModalitiesParamRejection(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "modalities") ||
		strings.Contains(lower, "extra inputs are not permitted") ||
		strings.Contains(lower, "unknown field") ||
		strings.Contains(lower, "unknown parameter") ||
		strings.Contains(lower, "unrecognized request argument")
}

func resolveImageStringValue(ctx context.Context, val, fallbackMime string, timeout time.Duration) (llm.GeneratedImage, bool) {
	trimmed := strings.TrimSpace(val)
	if strings.HasPrefix(trimmed, "data:") {
		return decodeDataURIImage(trimmed)
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if img, err := fetchRemoteImage(ctx, trimmed, timeout); err == nil && len(img.Data) > 0 {
			return img, true
		}
		return llm.GeneratedImage{}, false
	}
	if decoded, err := decodeBase64Flexible(trimmed); err == nil && len(decoded) > 0 {
		return llm.GeneratedImage{Mime: normalizeGeneratedImageMime(fallbackMime, decoded), Data: decoded}, true
	}
	return llm.GeneratedImage{}, false
}

func decodeDataURIImage(dataURI string) (llm.GeneratedImage, bool) {
	trimmed := strings.TrimSpace(dataURI)
	if !strings.HasPrefix(trimmed, "data:") {
		return llm.GeneratedImage{}, false
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma < 0 {
		return llm.GeneratedImage{}, false
	}
	header := trimmed[5:comma]
	mime := strings.TrimSpace(strings.Split(header, ";")[0])
	decoded, err := decodeBase64Flexible(trimmed[comma+1:])
	if err != nil || len(decoded) == 0 {
		return llm.GeneratedImage{}, false
	}
	return llm.GeneratedImage{
		Mime: normalizeGeneratedImageMime(mime, decoded),
		Data: decoded,
	}, true
}

func fetchRemoteImage(parentCtx context.Context, rawURL string, timeout time.Duration) (llm.GeneratedImage, error) {
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return llm.GeneratedImage{}, err
	}
	client := &http.Client{Timeout: timeout, Transport: selectImageTransport(rawURL)}
	resp, err := client.Do(req)
	if err != nil {
		return llm.GeneratedImage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.GeneratedImage{}, fmt.Errorf("download generated image HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if err != nil || len(data) == 0 {
		return llm.GeneratedImage{}, fmt.Errorf("empty generated image body")
	}
	ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	return llm.GeneratedImage{
		Mime: normalizeGeneratedImageMime(ct, data),
		Data: data,
	}, nil
}

func decodeBase64Flexible(s string) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if clean == "" {
		return nil, errors.New("empty base64")
	}
	if out, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return out, nil
	}
	if out, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err == nil {
		return out, nil
	}
	return base64.URLEncoding.DecodeString(clean)
}

func normalizeGeneratedImageMime(mime string, data []byte) string {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if m == "image/jpg" {
		m = "image/jpeg"
	}
	if strings.HasPrefix(m, "image/") {
		return m
	}
	if len(data) > 0 {
		detected := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
		if strings.HasPrefix(detected, "image/") {
			return detected
		}
	}
	return "image/png"
}

func firstNonEmptyImageStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
