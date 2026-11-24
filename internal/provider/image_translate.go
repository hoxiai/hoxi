package provider

import (
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

var _ llm.ImageTranslateClient = (*Service)(nil)

const (
	defaultImageTranslateTimeout = 120 * time.Second
	maxSceneInlineImages         = 48
	maxSceneWalkDepth            = 32
)

func slotForImageTranslateMode(mode llm.ImageTranslateOutputMode) llm.ModelSlot {
	if mode == llm.ImageTranslateModeRaster {
		return llm.ModelSlotImageTranslateRaster
	}
	return llm.ModelSlotImageTranslateEditor
}

// ListImageTranslateModels returns configured models capable of image
// translation in the requested output mode ("editable_scene" or "raster").
func (s *Service) ListImageTranslateModels(mode llm.ImageTranslateOutputMode) []llm.SlotModelOption {
	return s.ListModelsBySlot(slotForImageTranslateMode(mode))
}

// ResolveImageTranslateModel selects a concrete provider/model for the
// requested image translation mode.
func (s *Service) ResolveImageTranslateModel(
	mode llm.ImageTranslateOutputMode,
	preferredProviderID, preferredModelID string,
) (string, string, string, bool) {
	return s.ResolveModelForSlot(slotForImageTranslateMode(mode), preferredProviderID, preferredModelID)
}

func normalizeTranslateLang(raw, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "zh", "zh-cn", "zh-hans", "cn":
		return "zh"
	case "en", "en-us", "en-gb":
		return "en"
	case "ru", "ru-ru":
		return "ru"
	case "":
		return fallback
	default:
		clean := strings.ToLower(strings.TrimSpace(raw))
		if len(clean) <= 16 {
			return clean
		}
		return fallback
	}
}

// TranslateImage sends an image translation request to the resolved gateway
// (trying /v1/images/translations first and falling back to /ai/translate/image),
// parses scene or template_json, and inlines every remote http(s):// image layer
// into a data URL inside the local Go process.
func (s *Service) TranslateImage(ctx context.Context, req llm.ImageTranslateRequest) (resp llm.ImageTranslateResponse, resultErr error) {
	imageInput := strings.TrimSpace(req.Image)
	if imageInput == "" {
		return llm.ImageTranslateResponse{}, errors.New("image is required for image translation")
	}
	if !strings.HasPrefix(strings.ToLower(imageInput), "data:image/") {
		if err := isBlockedMediaTargetURL(imageInput, ""); err != nil {
			return llm.ImageTranslateResponse{}, fmt.Errorf("unsafe image URL: %w", err)
		}
	}

	mode := req.OutputMode
	if mode == "" {
		mode = llm.ImageTranslateModeEditableScene
	}
	pid, mid, resolution, ok := s.ResolveImageTranslateModel(mode, req.ProviderID, req.ModelID)
	if !ok {
		return llm.ImageTranslateResponse{}, llm.ErrUnavailable
	}
	conn, err := s.resolveImageConnection(pid, mid)
	if err != nil {
		return llm.ImageTranslateResponse{}, err
	}

	srcLang := normalizeTranslateLang(req.SourceLanguage, "zh")
	defaultDst := "ru"
	if srcLang == "ru" {
		defaultDst = "en"
	}
	dstLang := normalizeTranslateLang(req.TargetLanguage, defaultDst)
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultImageTranslateTimeout
	}

	started := time.Now()
	defer func() {
		level, msg := "info", "image translation completed"
		if resultErr != nil {
			level, msg = "error", "image translation failed"
			if errors.Is(resultErr, context.Canceled) {
				level, msg = "info", "image translation canceled"
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

	v1Base, rootBase := normalizeGatewayV1AndRootBase(conn.BaseURL)
	extraHeaders := map[string]string{}
	if reqID := strings.TrimSpace(req.RequestID); reqID != "" {
		extraHeaders["Idempotency-Key"] = reqID
	}

	v1Payload := map[string]any{
		"model":           conn.ModelID,
		"image":           imageInput,
		"source_language": srcLang,
		"target_language": dstLang,
		"output_mode":     string(mode),
		"with_template":   mode == llm.ImageTranslateModeEditableScene,
	}
	v1URL := v1Base + "/images/translations"
	status, body, err := s.postJSONWithHeaders(ctx, v1URL, conn, v1Payload, extraHeaders, timeout)
	if err != nil {
		return llm.ImageTranslateResponse{}, err
	}

	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented {
		legacyPayload := map[string]any{
			"model":            conn.ModelID,
			"image":            imageInput,
			"image_url":        imageInput,
			"source_language":  srcLang,
			"target_language":  dstLang,
			"with_template":    mode == llm.ImageTranslateModeEditableScene,
			"need_editor_data": mode == llm.ImageTranslateModeEditableScene,
		}
		legacyURL := rootBase + "/ai/translate/image"
		status, body, err = s.postJSONWithHeaders(ctx, legacyURL, conn, legacyPayload, extraHeaders, timeout)
		if err != nil {
			return llm.ImageTranslateResponse{}, err
		}
	}

	if status < 200 || status >= 300 {
		return llm.ImageTranslateResponse{}, &imageHTTPStatusError{
			status: status,
			msg:    redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey),
		}
	}

	parsed, hasPayload, parseErr := parseImageTranslatePayload(body)
	if parseErr != nil {
		return llm.ImageTranslateResponse{}, parseErr
	}
	if !hasPayload {
		taskID, taskStatus, taskErr := parseGatewayTaskEnvelope(body)
		if isTerminalFailedTaskStatus(taskStatus) {
			if taskErr == "" {
				taskErr = "image translation task failed"
			}
			return llm.ImageTranslateResponse{}, errors.New(redactSecretInText(taskErr, conn.APIKey))
		}
		if taskID != "" {
			parsed, parseErr = s.pollGatewayImageTranslateTask(ctx, v1Base, taskID, conn, timeout)
			if parseErr != nil {
				return llm.ImageTranslateResponse{}, parseErr
			}
		} else {
			return llm.ImageTranslateResponse{}, errors.New("image translation response contained neither scene nor image URL")
		}
	}

	cache := map[string]string{}
	if parsed.scene != nil {
		if err := inlineSceneRemoteImages(ctx, parsed.scene, conn, timeout, cache, 0); err != nil {
			return llm.ImageTranslateResponse{}, err
		}
		encodedScene, err := json.Marshal(parsed.scene)
		if err != nil {
			return llm.ImageTranslateResponse{}, fmt.Errorf("encode inlined scene: %w", err)
		}
		resp.Scene = encodedScene
	}

	if parsed.finalImageURL != "" {
		inlined, ok, err := maybeInlineOutputImageURL(ctx, parsed.finalImageURL, conn, timeout, cache)
		if err != nil {
			return llm.ImageTranslateResponse{}, err
		}
		if ok {
			resp.FinalImageURL = inlined
		} else {
			resp.FinalImageURL = inlined
		}
	}
	if parsed.inPaintingURL != "" {
		inlined, ok, err := maybeInlineOutputImageURL(ctx, parsed.inPaintingURL, conn, timeout, cache)
		if err != nil {
			return llm.ImageTranslateResponse{}, err
		}
		if ok {
			resp.InPaintingURL = inlined
		} else {
			resp.InPaintingURL = inlined
		}
	}

	resp.ProviderID = conn.ProviderID
	resp.ModelID = conn.ModelID
	resp.Resolution = resolution
	return resp, nil
}

type parsedImageTranslateData struct {
	scene         any
	finalImageURL string
	inPaintingURL string
}

func parseImageTranslatePayload(raw []byte) (parsedImageTranslateData, bool, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return parsedImageTranslateData{}, false, fmt.Errorf("decode image translation response: %w", err)
	}
	if top == nil {
		return parsedImageTranslateData{}, false, nil
	}
	if okVal, hasOK := top["success"].(bool); hasOK && !okVal {
		msg := firstNonEmptyString(top["message"], top["error"], "image translation failed")
		return parsedImageTranslateData{}, false, errors.New(msg)
	}

	sources := []map[string]any{}
	if dataMap, ok := top["data"].(map[string]any); ok && dataMap != nil {
		sources = append(sources, dataMap)
		if resMap, ok := dataMap["result"].(map[string]any); ok && resMap != nil {
			sources = append(sources, resMap)
		}
	}
	if pascalData, ok := top["Data"].(map[string]any); ok && pascalData != nil {
		sources = append(sources, pascalData)
	}
	if resMap, ok := top["result"].(map[string]any); ok && resMap != nil {
		sources = append(sources, resMap)
	}
	if outMap, ok := top["output"].(map[string]any); ok && outMap != nil {
		sources = append(sources, outMap)
	}
	sources = append(sources, top)

	var out parsedImageTranslateData
	for _, src := range sources {
		if out.finalImageURL == "" {
			out.finalImageURL = firstNonEmptyString(
				src["finalImageUrl"],
				src["final_image_url"],
				src["FinalImageUrl"],
				src["translated_image_url"],
				src["url"],
				src["Url"],
			)
		}
		if out.inPaintingURL == "" {
			out.inPaintingURL = firstNonEmptyString(
				src["inPaintingUrl"],
				src["in_painting_url"],
				src["InPaintingUrl"],
				src["inpainting_url"],
			)
		}
		if out.scene == nil {
			if sc, ok := src["scene"]; ok && sc != nil {
				if str, isStr := sc.(string); isStr {
					trimmed := strings.TrimSpace(str)
					if trimmed != "" {
						var decoded any
						if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
							return parsedImageTranslateData{}, false, fmt.Errorf("invalid scene JSON string: %w", err)
						}
						out.scene = decoded
					}
				} else {
					out.scene = sc
				}
			}
		}
		if out.scene == nil {
			for _, tplKey := range []string{"template_json", "templateJson", "TemplateJson"} {
				if rawTpl, ok := src[tplKey]; ok && rawTpl != nil {
					if str, isStr := rawTpl.(string); isStr {
						trimmed := strings.TrimSpace(str)
						if trimmed == "" {
							continue
						}
						var decoded any
						if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
							return parsedImageTranslateData{}, false, fmt.Errorf("invalid %s payload: %w", tplKey, err)
						}
						out.scene = decoded
						break
					}
					out.scene = rawTpl
					break
				}
			}
		}
	}

	hasAny := out.scene != nil || out.finalImageURL != "" || out.inPaintingURL != ""
	return out, hasAny, nil
}

func (s *Service) pollGatewayImageTranslateTask(
	parentCtx context.Context,
	v1Base, taskID string,
	conn imageGenConn,
	timeout time.Duration,
) (parsedImageTranslateData, error) {
	pollCtx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	for {
		if err := waitImageEditTaskPoll(pollCtx); err != nil {
			return parsedImageTranslateData{}, err
		}
		status, body, err := s.getGatewayTaskBody(pollCtx, v1Base, taskID, conn, timeout)
		if err != nil {
			return parsedImageTranslateData{}, err
		}
		if status < 200 || status >= 300 {
			return parsedImageTranslateData{}, &imageHTTPStatusError{
				status: status,
				msg:    redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey),
			}
		}
		_, taskStatus, taskErrMsg := parseGatewayTaskEnvelope(body)
		if isTerminalFailedTaskStatus(taskStatus) {
			if taskErrMsg == "" {
				taskErrMsg = "image translation task failed"
			}
			return parsedImageTranslateData{}, errors.New(redactSecretInText(taskErrMsg, conn.APIKey))
		}
		parsed, hasPayload, parseErr := parseImageTranslatePayload(body)
		if parseErr != nil {
			return parsedImageTranslateData{}, parseErr
		}
		if hasPayload {
			return parsed, nil
		}
		if isTerminalSucceededTaskStatus(taskStatus) {
			return parsedImageTranslateData{}, errors.New("image translation task completed without scene or image output")
		}
	}
}

func normalizeRemoteHTTPURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return trimmed, true
	}
	if strings.HasPrefix(trimmed, "//") && len(trimmed) > 2 && trimmed[2] != '/' {
		return "https:" + trimmed, true
	}
	return "", false
}

func fetchRemoteImageDataURL(
	parentCtx context.Context,
	rawURL string,
	conn imageGenConn,
	timeout time.Duration,
	cache map[string]string,
) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if cached, ok := cache[trimmed]; ok {
		return cached, nil
	}
	if len(cache) >= maxSceneInlineImages {
		return "", errors.New("too many remote images inside translated scene")
	}
	if err := isBlockedMediaTargetURL(trimmed, conn.BaseURL); err != nil {
		return "", err
	}
	if parsed, uErr := url.Parse(trimmed); uErr == nil && isLoopbackHostname(parsed.Hostname()) && !sameHostIgnoreCase(trimmed, conn.BaseURL) {
		return "", errors.New("refusing cross-port loopback scene image URL")
	}
	reqTimeout := timeout
	if reqTimeout <= 0 || reqTimeout > 30*time.Second {
		reqTimeout = 30 * time.Second
	}

	doFetch := func(referer string) (*http.Response, error) {
		ctx, cancel := context.WithTimeout(parentCtx, reqTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmed, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "image/webp,image/png,image/jpeg,image/gif,*/*;q=0.8")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		if sameHostIgnoreCase(trimmed, conn.BaseURL) {
			applyImageAuthHeaders(req, conn, false, false)
		}
		client := newSafeVideoHTTPClient(reqTimeout, trimmed, conn)
		return client.Do(req)
	}

	resp, err := doFetch("")
	if err != nil {
		return "", errors.New(redactSecretInText(err.Error(), conn.APIKey))
	}
	if resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if parsed, uErr := url.Parse(trimmed); uErr == nil && parsed.Host != "" {
			resp, err = doFetch(parsed.Scheme + "://" + parsed.Host + "/")
			if err != nil {
				return "", errors.New(redactSecretInText(err.Error(), conn.APIKey))
			}
		} else {
			return "", fmt.Errorf("download translated image HTTP %d", http.StatusForbidden)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download translated image HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes+1))
	if err != nil || len(data) == 0 {
		return "", errors.New("empty translated image body")
	}
	if len(data) > maxImageResponseBytes {
		return "", fmt.Errorf("translated image exceeds %d MB limit", maxImageResponseBytes>>20)
	}
	verifiedMime, ok := detectVerifiedImageMime(data)
	if !ok {
		return "", errors.New("downloaded translated scene resource is not a valid image")
	}
	dataURL := "data:" + verifiedMime + ";base64," + base64.StdEncoding.EncodeToString(data)
	cache[trimmed] = dataURL
	return dataURL, nil
}

func maybeInlineOutputImageURL(
	ctx context.Context,
	rawURL string,
	conn imageGenConn,
	timeout time.Duration,
	cache map[string]string,
) (string, bool, error) {
	trimmed, ok := normalizeRemoteHTTPURL(rawURL)
	if !ok {
		return strings.TrimSpace(rawURL), false, nil
	}
	if err := isBlockedMediaTargetURL(trimmed, conn.BaseURL); err != nil {
		return "", false, err
	}
	if parsed, uErr := url.Parse(trimmed); uErr == nil && isLoopbackHostname(parsed.Hostname()) && !sameHostIgnoreCase(trimmed, conn.BaseURL) {
		return "", false, errors.New("refusing cross-port loopback image URL")
	}
	dataURL, err := fetchRemoteImageDataURL(ctx, trimmed, conn, timeout, cache)
	if err != nil {
		return trimmed, false, nil
	}
	return dataURL, true, nil
}

func inlineSceneRemoteImages(
	ctx context.Context,
	node any,
	conn imageGenConn,
	timeout time.Duration,
	cache map[string]string,
	depth int,
) error {
	if node == nil || depth > maxSceneWalkDepth {
		return nil
	}
	switch typed := node.(type) {
	case map[string]any:
		typeStr, _ := typed["type"].(string)
		trimmedType := strings.TrimSpace(typeStr)
		isImageNode := strings.EqualFold(trimmedType, "image") ||
			strings.EqualFold(trimmedType, "img") ||
			strings.EqualFold(trimmedType, "background")
		keysToInline := []string{"backgroundImg", "backgroundImage", "backgroundImgUrl", "inPaintingUrl", "inpaintingUrl"}
		if isImageNode {
			keysToInline = append(keysToInline, "src", "url")
		}
		for _, k := range keysToInline {
			if rawVal, ok := typed[k].(string); ok {
				if normURL, isRemote := normalizeRemoteHTTPURL(rawVal); isRemote {
					inlined, err := fetchRemoteImageDataURL(ctx, normURL, conn, timeout, cache)
					if err != nil {
						return fmt.Errorf("inline scene %s failed: %w", k, err)
					}
					typed[k] = inlined
				}
			}
		}
		for _, val := range typed {
			if err := inlineSceneRemoteImages(ctx, val, conn, timeout, cache, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := inlineSceneRemoteImages(ctx, item, conn, timeout, cache, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
