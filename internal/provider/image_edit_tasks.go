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

	"github.com/hoxiai/hoxi/internal/llm"
)

const defaultImageEditTaskPollInterval = 1200 * time.Millisecond

// imageEditTaskPollIntervalOverride lets unit tests shorten polling without sleeps.
var imageEditTaskPollIntervalOverride time.Duration

func normalizeGatewayV1AndRootBase(rawBaseURL string) (v1Base, rootBase string) {
	trimmed := strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	if trimmed == "" {
		return "", ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "/v1") {
		return trimmed, strings.TrimRight(trimmed[:len(trimmed)-3], "/")
	}
	if strings.HasSuffix(lower, "/v2") || strings.HasSuffix(lower, "/v3") ||
		strings.HasSuffix(lower, "/v4") || strings.Contains(lower, localBridgePathPrefix) {
		return trimmed, trimmed
	}
	return trimmed + "/v1", trimmed
}

func waitImageEditTaskPoll(ctx context.Context) error {
	d := defaultImageEditTaskPollInterval
	if imageEditTaskPollIntervalOverride > 0 {
		d = imageEditTaskPollIntervalOverride
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func encodeImageInputDataURL(img llm.ImageInput) string {
	if len(img.Data) == 0 {
		return ""
	}
	mime := normalizeGeneratedImageMime(img.Mime, img.Data)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

func isJSONContentTypeFallbackError(status int, body []byte) bool {
	if status == http.StatusUnsupportedMediaType {
		return true
	}
	if status != http.StatusBadRequest {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "json") ||
		strings.Contains(lower, "invalid character") ||
		strings.Contains(lower, "content-type") ||
		strings.Contains(lower, "application/json")
}

func (s *Service) openAIImagesEditTask(
	parentCtx context.Context,
	conn imageGenConn,
	prompt string,
	input llm.ImageInput,
	mask *llm.ImageInput,
	opts imageGenExecOptions,
	idempotencyKey string,
	promptKind string,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	if len(input.Data) == 0 {
		return llm.ImageResponse{}, errors.New("empty edit image")
	}
	v1Base, _ := normalizeGatewayV1AndRootBase(conn.BaseURL)
	targetURL := v1Base + "/images/edits/tasks"
	if err := isBlockedMediaTargetURL(targetURL, conn.BaseURL); err != nil {
		return llm.ImageResponse{}, err
	}

	extraHeaders := map[string]string{}
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		extraHeaders["Idempotency-Key"] = key
	}
	if kind := strings.TrimSpace(promptKind); kind != "" {
		extraHeaders["X-Prompt-Kind"] = kind
	}

	status, body, err := s.postImageEditMultipart(parentCtx, targetURL, conn, prompt, input, mask, opts, extraHeaders, timeout)
	if err != nil {
		return llm.ImageResponse{}, err
	}
	if isJSONContentTypeFallbackError(status, body) {
		payload := map[string]any{
			"model":  conn.ModelID,
			"prompt": prompt,
			"image":  encodeImageInputDataURL(input),
			"n":      clampImageCount(opts.count),
		}
		if mask != nil && len(mask.Data) > 0 {
			payload["mask"] = encodeImageInputDataURL(*mask)
		}
		if opts.aspectRatio != "" {
			payload["size"] = openAIImageSizeForAspectRatio(conn.ModelID, opts.aspectRatio)
		}
		jStatus, jBody, jErr := s.postJSONWithHeaders(parentCtx, targetURL, conn, payload, extraHeaders, timeout)
		if jErr != nil {
			return llm.ImageResponse{}, jErr
		}
		status, body = jStatus, jBody
	}

	if status < 200 || status >= 300 {
		return llm.ImageResponse{}, &imageHTTPStatusError{
			status: status,
			msg:    redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey),
		}
	}

	// Support immediate synchronous result if the gateway completed inline.
	if immResp, ok := s.extractImageResponseFromTaskBody(parentCtx, body, conn, timeout); ok {
		return immResp, nil
	}

	taskID, taskStatus, taskErrMsg := parseGatewayTaskEnvelope(body)
	if isTerminalFailedTaskStatus(taskStatus) {
		if taskErrMsg == "" {
			taskErrMsg = "image edit task failed"
		}
		return llm.ImageResponse{}, errors.New(redactSecretInText(taskErrMsg, conn.APIKey))
	}
	if taskID == "" {
		return llm.ImageResponse{}, errors.New("image edit task response missing task id")
	}

	return s.pollGatewayImageEditTask(parentCtx, v1Base, taskID, conn, timeout)
}

func (s *Service) postImageEditMultipart(
	parentCtx context.Context,
	targetURL string,
	conn imageGenConn,
	prompt string,
	input llm.ImageInput,
	mask *llm.ImageInput,
	opts imageGenExecOptions,
	extraHeaders map[string]string,
	timeout time.Duration,
) (int, []byte, error) {
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
		return 0, nil, err
	}
	_, _ = fw.Write(input.Data)
	if mask != nil && len(mask.Data) > 0 {
		maskMime := normalizeGeneratedImageMime(mask.Mime, mask.Data)
		mHdr := make(textproto.MIMEHeader)
		mHdr.Set("Content-Disposition", `form-data; name="mask"; filename="mask.png"`)
		mHdr.Set("Content-Type", maskMime)
		mfw, mErr := mw.CreatePart(mHdr)
		if mErr != nil {
			return 0, nil, mErr
		}
		_, _ = mfw.Write(mask.Data)
	}
	_ = mw.Close()

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, &buf)
	if err != nil {
		return 0, nil, err
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", mw.FormDataContentType())
	applyImageAuthHeaders(outReq, conn, false, false)
	for k, v := range extraHeaders {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			outReq.Header.Set(k, v)
		}
	}
	client := newSafeVideoHTTPClient(timeout, targetURL, conn)
	resp, err := client.Do(outReq)
	if err != nil {
		return 0, nil, errors.New(redactSecretInText(err.Error(), conn.APIKey))
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("read image task response: %w", readErr)
	}
	return resp.StatusCode, body, nil
}

func (s *Service) postJSONWithHeaders(
	parentCtx context.Context,
	targetURL string,
	conn imageGenConn,
	payload any,
	extraHeaders map[string]string,
	timeout time.Duration,
) (int, []byte, error) {
	if err := isBlockedMediaTargetURL(targetURL, conn.BaseURL); err != nil {
		return 0, nil, err
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(rawPayload))
	if err != nil {
		return 0, nil, err
	}
	outReq.Header.Set("Accept", "application/json")
	outReq.Header.Set("Content-Type", "application/json")
	applyImageAuthHeaders(outReq, conn, false, false)
	for k, v := range extraHeaders {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			outReq.Header.Set(k, v)
		}
	}
	client := newSafeVideoHTTPClient(timeout, targetURL, conn)
	resp, err := client.Do(outReq)
	if err != nil {
		return 0, nil, errors.New(redactSecretInText(err.Error(), conn.APIKey))
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("read gateway response: %w", readErr)
	}
	return resp.StatusCode, body, nil
}

func (s *Service) getGatewayTaskBody(
	parentCtx context.Context,
	v1Base, taskID string,
	conn imageGenConn,
	timeout time.Duration,
) (int, []byte, error) {
	pollURL := v1Base + "/tasks/" + url.PathEscape(strings.TrimSpace(taskID))
	if err := isBlockedMediaTargetURL(pollURL, conn.BaseURL); err != nil {
		return 0, nil, err
	}
	reqTimeout := timeout
	if reqTimeout <= 0 || reqTimeout > 30*time.Second {
		reqTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, reqTimeout)
	defer cancel()
	outReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
	if err != nil {
		return 0, nil, err
	}
	outReq.Header.Set("Accept", "application/json")
	applyImageAuthHeaders(outReq, conn, false, false)
	client := newSafeVideoHTTPClient(reqTimeout, pollURL, conn)
	resp, err := client.Do(outReq)
	if err != nil {
		return 0, nil, errors.New(redactSecretInText(err.Error(), conn.APIKey))
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("read task poll response: %w", readErr)
	}
	return resp.StatusCode, body, nil
}

func (s *Service) pollGatewayImageEditTask(
	parentCtx context.Context,
	v1Base, taskID string,
	conn imageGenConn,
	timeout time.Duration,
) (llm.ImageResponse, error) {
	pollCtx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	for {
		if err := waitImageEditTaskPoll(pollCtx); err != nil {
			return llm.ImageResponse{}, err
		}
		status, body, err := s.getGatewayTaskBody(pollCtx, v1Base, taskID, conn, timeout)
		if err != nil {
			return llm.ImageResponse{}, err
		}
		if status < 200 || status >= 300 {
			return llm.ImageResponse{}, &imageHTTPStatusError{
				status: status,
				msg:    redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey),
			}
		}
		_, taskStatus, taskErrMsg := parseGatewayTaskEnvelope(body)
		if isTerminalFailedTaskStatus(taskStatus) {
			if taskErrMsg == "" {
				taskErrMsg = "image edit task failed"
			}
			return llm.ImageResponse{}, errors.New(redactSecretInText(taskErrMsg, conn.APIKey))
		}
		if imgResp, ok := s.extractImageResponseFromTaskBody(pollCtx, body, conn, timeout); ok {
			return imgResp, nil
		}
		if isTerminalSucceededTaskStatus(taskStatus) {
			return llm.ImageResponse{}, errors.New("image edit task completed without image output")
		}
	}
}

func parseGatewayTaskEnvelope(raw []byte) (taskID, status, errMsg string) {
	var top map[string]any
	if json.Unmarshal(raw, &top) != nil || top == nil {
		return "", "", ""
	}
	readMap := func(m map[string]any) {
		if m == nil {
			return
		}
		if taskID == "" {
			taskID = firstNonEmptyString(m["task_id"], m["taskId"], m["id"])
		}
		if status == "" {
			status = strings.ToLower(firstNonEmptyString(m["status"], m["state"]))
		}
		if errMsg == "" {
			if eObj, ok := m["error"].(map[string]any); ok {
				errMsg = firstNonEmptyString(eObj["message"], eObj["error"], eObj["detail"])
			} else {
				errMsg = firstNonEmptyString(m["error"], m["error_message"], m["fail_reason"], m["message"])
			}
		}
	}
	readMap(top)
	if dataMap, ok := top["data"].(map[string]any); ok {
		readMap(dataMap)
	}
	return taskID, status, errMsg
}

func isTerminalFailedTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "canceled", "cancelled", "expired", "timeout", "rejected":
		return true
	default:
		return false
	}
}

func isTerminalSucceededTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "completed", "done", "success", "finished":
		return true
	default:
		return false
	}
}

func (s *Service) extractImageResponseFromTaskBody(
	ctx context.Context,
	raw []byte,
	conn imageGenConn,
	timeout time.Duration,
) (llm.ImageResponse, bool) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || len(top) == 0 {
		return llm.ImageResponse{}, false
	}
	var out llm.ImageResponse
	collectFromContainer := func(container map[string]json.RawMessage) {
		if len(container) == 0 {
			return
		}
		for _, arrKey := range []string{"data", "images", "artifacts"} {
			rawArr := bytes.TrimSpace(container[arrKey])
			if len(rawArr) > 0 && rawArr[0] == '[' {
				var items []json.RawMessage
				if json.Unmarshal(rawArr, &items) == nil {
					for _, item := range items {
						if img, ok := s.resolveOpenAIImageItem(ctx, item, conn, timeout); ok {
							out.Images = append(out.Images, img)
						}
					}
				}
			}
		}
		for _, strKey := range []string{"dataUrl", "data_url", "b64_json", "image_url", "imageUrl", "url"} {
			rawVal := bytes.TrimSpace(container[strKey])
			if len(rawVal) > 0 && rawVal[0] == '"' {
				var str string
				if json.Unmarshal(rawVal, &str) == nil && strings.TrimSpace(str) != "" {
					if img, ok := resolveImageStringValue(ctx, str, "", conn, timeout); ok {
						out.Images = append(out.Images, img)
					}
				}
			}
		}
	}

	collectFromContainer(top)
	for _, nestedKey := range []string{"result", "output", "data"} {
		rawNested := bytes.TrimSpace(top[nestedKey])
		if len(rawNested) > 0 && rawNested[0] == '{' {
			var sub map[string]json.RawMessage
			if json.Unmarshal(rawNested, &sub) == nil {
				collectFromContainer(sub)
				for _, deepKey := range []string{"result", "output"} {
					rawDeep := bytes.TrimSpace(sub[deepKey])
					if len(rawDeep) > 0 && rawDeep[0] == '{' {
						var deep map[string]json.RawMessage
						if json.Unmarshal(rawDeep, &deep) == nil {
							collectFromContainer(deep)
						}
					}
				}
			}
		}
	}
	if len(out.Images) == 0 {
		return llm.ImageResponse{}, false
	}
	return out, true
}
