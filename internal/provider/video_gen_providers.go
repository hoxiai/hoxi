package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func isDashScopeVideoEndpoint(providerID, baseURL string) bool {
	if strings.EqualFold(strings.TrimSpace(providerID), "alibaba") || strings.EqualFold(strings.TrimSpace(providerID), "dashscope") {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.Contains(strings.ToLower(u.Hostname()), "dashscope.aliyuncs.com")
}

func isVolcArkVideoEndpoint(providerID, baseURL, modelID string) bool {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	if pid == "volcengine" || pid == "ark" || pid == "bytedance" {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, "volces.com") || strings.Contains(host, "volcengineapi.com") {
		return true
	}
	return strings.Contains(strings.ToLower(u.Path), "/api/v3") && strings.Contains(strings.ToLower(modelID), "seedance")
}

func isBigModelVideoEndpoint(providerID, baseURL string) bool {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	if pid == "zai" || pid == "zhipu" || pid == "bigmodel" {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.Contains(strings.ToLower(u.Hostname()), "bigmodel.cn")
}

func originSchemeHost(rawBaseURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	}
	return u.Scheme + "://" + u.Host
}

func googleOperationBaseURL(rawBaseURL string) string {
	modelsBase := googleModelsBaseURL(rawBaseURL)
	return strings.TrimSuffix(modelsBase, "/models")
}

func (s *Service) googleVeoGenerateVideo(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts videoGenExecOptions,
) (llm.VideoResponse, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(conn.ModelID), "models/")
	targetURL := googleModelsBaseURL(conn.BaseURL) + "/" + url.PathEscape(cleanModel) + ":predictLongRunning"

	inst := map[string]any{"prompt": prompt}
	for _, img := range inputImages {
		if len(img.Data) == 0 {
			continue
		}
		inst["image"] = map[string]any{
			"bytesBase64Encoded": base64.StdEncoding.EncodeToString(img.Data),
			"mimeType":           normalizeGeneratedImageMime(img.Mime, img.Data),
		}
		break
	}
	params := map[string]any{}
	if opts.aspectRatio != "" {
		params["aspectRatio"] = opts.aspectRatio
	}
	if opts.durationSeconds > 0 {
		params["durationSeconds"] = opts.durationSeconds
	}
	if opts.resolution != "" {
		params["resolution"] = opts.resolution
	}
	payload := map[string]any{"instances": []map[string]any{inst}}
	if len(params) > 0 {
		payload["parameters"] = params
	}

	status, _, body, err := s.doVideoHTTP(ctx, http.MethodPost, targetURL, conn, true, nil, payload, 60*time.Second)
	if err != nil {
		return llm.VideoResponse{}, err
	}
	if status == http.StatusBadRequest && len(params) > 0 {
		delete(payload, "parameters")
		status, _, body, err = s.doVideoHTTP(ctx, http.MethodPost, targetURL, conn, true, nil, payload, 60*time.Second)
		if err != nil {
			return llm.VideoResponse{}, err
		}
	}
	if status < 200 || status >= 300 {
		return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}

	opRoot := googleOperationBaseURL(conn.BaseURL)
	curBody := body
	for {
		var op struct {
			Name     string                    `json:"name"`
			Done     bool                      `json:"done"`
			Error    *struct{ Message string } `json:"error"`
			Response json.RawMessage           `json:"response"`
		}
		if err := json.Unmarshal(curBody, &op); err != nil {
			return llm.VideoResponse{}, fmt.Errorf("decode Veo operation: %w", err)
		}
		if op.Error != nil && strings.TrimSpace(op.Error.Message) != "" {
			return llm.VideoResponse{}, errors.New(redactSecretInText(op.Error.Message, conn.APIKey))
		}
		if vidResp, ok, vErr := parseGoogleVeoPayload(ctx, curBody, conn); vErr != nil {
			return llm.VideoResponse{}, vErr
		} else if ok {
			return vidResp, nil
		}
		if op.Done {
			if len(op.Response) > 0 {
				if vidResp, ok, vErr := parseGoogleVeoPayload(ctx, op.Response, conn); vErr != nil {
					return llm.VideoResponse{}, vErr
				} else if ok {
					return vidResp, nil
				}
			}
			return llm.VideoResponse{}, errors.New("Veo operation completed with no video output")
		}
		opName := strings.TrimLeft(strings.TrimSpace(op.Name), "/")
		if opName == "" {
			return llm.VideoResponse{}, errors.New("Veo response contained neither video nor operation name")
		}
		if err := waitVideoPoll(ctx); err != nil {
			return llm.VideoResponse{}, err
		}
		pollURL := opRoot + "/" + opName
		if strings.HasPrefix(opName, "http://") || strings.HasPrefix(opName, "https://") {
			if err := isBlockedMediaTargetURL(opName, conn.BaseURL); err != nil {
				return llm.VideoResponse{}, err
			}
			if !sameHostIgnoreCase(opName, conn.BaseURL) {
				return llm.VideoResponse{}, errors.New("refusing cross-origin Veo operation poll URL")
			}
			pollURL = opName
		}
		pStatus, _, pBody, pErr := s.doVideoHTTP(ctx, http.MethodGet, pollURL, conn, true, nil, nil, 45*time.Second)
		if pErr != nil {
			return llm.VideoResponse{}, pErr
		}
		if pStatus < 200 || pStatus >= 300 {
			return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(pStatus, pBody), conn.APIKey))
		}
		curBody = pBody
	}
}

func parseGoogleVeoPayload(ctx context.Context, raw []byte, conn imageGenConn) (llm.VideoResponse, bool, error) {
	type veoVideoBlob struct {
		URI                string `json:"uri"`
		BytesBase64Encoded string `json:"bytesBase64Encoded"`
		MimeType           string `json:"mimeType"`
	}
	var parsed struct {
		GenerateVideoResponse *struct {
			GeneratedSamples []struct {
				Video *veoVideoBlob `json:"video"`
			} `json:"generatedSamples"`
			RaiMediaFilteredReasons []string `json:"raiMediaFilteredReasons"`
		} `json:"generateVideoResponse"`
		Predictions []struct {
			BytesBase64Encoded string        `json:"bytesBase64Encoded"`
			MimeType           string        `json:"mimeType"`
			Video              *veoVideoBlob `json:"video"`
		} `json:"predictions"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return llm.VideoResponse{}, false, nil
	}
	var out llm.VideoResponse
	resolveBlob := func(b *veoVideoBlob) error {
		if b == nil {
			return nil
		}
		if strings.TrimSpace(b.BytesBase64Encoded) != "" {
			data, err := decodeBase64Flexible(b.BytesBase64Encoded)
			if err != nil {
				return err
			}
			out.Videos = append(out.Videos, llm.GeneratedVideo{
				Mime: normalizeGeneratedVideoMime(b.MimeType, data),
				Data: data,
			})
			return nil
		}
		if strings.TrimSpace(b.URI) != "" {
			vid, err := fetchGeneratedVideoURL(ctx, b.URI, conn, true)
			if err != nil {
				return err
			}
			out.Videos = append(out.Videos, vid)
		}
		return nil
	}
	if parsed.GenerateVideoResponse != nil {
		for _, sample := range parsed.GenerateVideoResponse.GeneratedSamples {
			if err := resolveBlob(sample.Video); err != nil {
				return llm.VideoResponse{}, false, err
			}
		}
		if len(out.Videos) == 0 && len(parsed.GenerateVideoResponse.RaiMediaFilteredReasons) > 0 {
			return llm.VideoResponse{}, false, fmt.Errorf("video blocked by safety filter: %s", strings.Join(parsed.GenerateVideoResponse.RaiMediaFilteredReasons, "; "))
		}
	}
	for _, pred := range parsed.Predictions {
		if pred.Video != nil {
			if err := resolveBlob(pred.Video); err != nil {
				return llm.VideoResponse{}, false, err
			}
		} else if strings.TrimSpace(pred.BytesBase64Encoded) != "" {
			if data, err := decodeBase64Flexible(pred.BytesBase64Encoded); err == nil && len(data) > 0 {
				out.Videos = append(out.Videos, llm.GeneratedVideo{
					Mime: normalizeGeneratedVideoMime(pred.MimeType, data),
					Data: data,
				})
			}
		}
	}
	return out, len(out.Videos) > 0, nil
}

func (s *Service) dashScopeGenerateVideo(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts videoGenExecOptions,
) (llm.VideoResponse, error) {
	origin := originSchemeHost(conn.BaseURL)
	submitURL := origin + "/api/v1/services/aigc/video-generation/video-synthesis"
	inputObj := map[string]any{"prompt": prompt}
	if imgURI := encodeFirstFrameDataURI(inputImages); imgURI != "" {
		inputObj["img_url"] = imgURI
	}
	params := map[string]any{}
	if opts.aspectRatio == "9:16" {
		params["size"] = "720*1280"
	} else if opts.aspectRatio == "1:1" {
		params["size"] = "960*960"
	} else {
		params["size"] = "1280*720"
	}
	if opts.durationSeconds > 0 {
		params["duration"] = opts.durationSeconds
	}
	payload := map[string]any{
		"model":      conn.ModelID,
		"input":      inputObj,
		"parameters": params,
	}
	status, _, body, err := s.doVideoHTTP(ctx, http.MethodPost, submitURL, conn, false, map[string]string{"X-DashScope-Async": "enable"}, payload, 60*time.Second)
	if err != nil {
		return llm.VideoResponse{}, err
	}
	if status < 200 || status >= 300 {
		return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var submitRes struct {
		Output struct {
			TaskID     string `json:"task_id"`
			TaskStatus string `json:"task_status"`
			VideoURL   string `json:"video_url"`
			Message    string `json:"message"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &submitRes); err != nil {
		return llm.VideoResponse{}, fmt.Errorf("decode DashScope video task: %w", err)
	}
	if strings.TrimSpace(submitRes.Output.VideoURL) != "" {
		vid, err := fetchGeneratedVideoURL(ctx, submitRes.Output.VideoURL, conn, false)
		if err != nil {
			return llm.VideoResponse{}, err
		}
		return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
	}
	taskID := strings.TrimSpace(submitRes.Output.TaskID)
	if taskID == "" {
		return llm.VideoResponse{}, errors.New("DashScope returned empty task_id")
	}
	pollURL := origin + "/api/v1/tasks/" + url.PathEscape(taskID)
	for {
		if err := waitVideoPoll(ctx); err != nil {
			return llm.VideoResponse{}, err
		}
		pStatus, _, pBody, pErr := s.doVideoHTTP(ctx, http.MethodGet, pollURL, conn, false, nil, nil, 45*time.Second)
		if pErr != nil {
			return llm.VideoResponse{}, pErr
		}
		if pStatus < 200 || pStatus >= 300 {
			return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(pStatus, pBody), conn.APIKey))
		}
		var taskRes struct {
			Output struct {
				TaskStatus string `json:"task_status"`
				VideoURL   string `json:"video_url"`
				Message    string `json:"message"`
			} `json:"output"`
		}
		if err := json.Unmarshal(pBody, &taskRes); err != nil {
			return llm.VideoResponse{}, fmt.Errorf("decode DashScope task status: %w", err)
		}
		st := strings.ToUpper(strings.TrimSpace(taskRes.Output.TaskStatus))
		switch st {
		case "SUCCEEDED", "SUCCESS", "COMPLETED":
			if strings.TrimSpace(taskRes.Output.VideoURL) == "" {
				return llm.VideoResponse{}, errors.New("DashScope task succeeded without video_url")
			}
			vid, err := fetchGeneratedVideoURL(ctx, taskRes.Output.VideoURL, conn, false)
			if err != nil {
				return llm.VideoResponse{}, err
			}
			return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
		case "FAILED", "CANCELED", "CANCELLED":
			msg := strings.TrimSpace(taskRes.Output.Message)
			if msg == "" {
				msg = "DashScope video generation task failed"
			}
			return llm.VideoResponse{}, errors.New(redactSecretInText(msg, conn.APIKey))
		}
	}
}

func (s *Service) volcArkGenerateVideo(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts videoGenExecOptions,
) (llm.VideoResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	if !strings.Contains(strings.ToLower(trimmedBase), "/api/v") {
		trimmedBase = originSchemeHost(conn.BaseURL) + "/api/v3"
	}
	submitURL := trimmedBase + "/contents/generations/tasks"
	textWithFlags := prompt
	var flags []string
	if opts.aspectRatio != "" {
		flags = append(flags, "--ratio "+opts.aspectRatio)
	}
	if opts.durationSeconds > 0 {
		flags = append(flags, "--dur "+strconv.Itoa(opts.durationSeconds))
	}
	if opts.resolution != "" {
		flags = append(flags, "--rs "+opts.resolution)
	}
	if len(flags) > 0 && !strings.Contains(prompt, "--ratio") && !strings.Contains(prompt, "--dur") {
		textWithFlags = prompt + " " + strings.Join(flags, " ")
	}
	content := []map[string]any{{"type": "text", "text": textWithFlags}}
	if imgURI := encodeFirstFrameDataURI(inputImages); imgURI != "" {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": imgURI},
		})
	}
	payload := map[string]any{"model": conn.ModelID, "content": content}
	status, _, body, err := s.doVideoHTTP(ctx, http.MethodPost, submitURL, conn, false, nil, payload, 60*time.Second)
	if err != nil {
		return llm.VideoResponse{}, err
	}
	if status < 200 || status >= 300 {
		return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || strings.TrimSpace(created.ID) == "" {
		return llm.VideoResponse{}, errors.New("Ark video endpoint returned no task id")
	}
	pollURL := submitURL + "/" + url.PathEscape(strings.TrimSpace(created.ID))
	for {
		if err := waitVideoPoll(ctx); err != nil {
			return llm.VideoResponse{}, err
		}
		pStatus, _, pBody, pErr := s.doVideoHTTP(ctx, http.MethodGet, pollURL, conn, false, nil, nil, 45*time.Second)
		if pErr != nil {
			return llm.VideoResponse{}, pErr
		}
		if pStatus < 200 || pStatus >= 300 {
			return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(pStatus, pBody), conn.APIKey))
		}
		var task struct {
			Status  string                    `json:"status"`
			Error   *struct{ Message string } `json:"error"`
			Content struct {
				VideoURL string `json:"video_url"`
			} `json:"content"`
		}
		if err := json.Unmarshal(pBody, &task); err != nil {
			return llm.VideoResponse{}, fmt.Errorf("decode Ark video status: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(task.Status)) {
		case "succeeded", "completed", "success":
			vid, err := fetchGeneratedVideoURL(ctx, task.Content.VideoURL, conn, false)
			if err != nil {
				return llm.VideoResponse{}, err
			}
			return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
		case "failed", "cancelled", "canceled":
			msg := "Ark video generation failed"
			if task.Error != nil && strings.TrimSpace(task.Error.Message) != "" {
				msg = task.Error.Message
			}
			return llm.VideoResponse{}, errors.New(redactSecretInText(msg, conn.APIKey))
		}
	}
}

func (s *Service) bigModelGenerateVideo(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts videoGenExecOptions,
) (llm.VideoResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	payload := map[string]any{"model": conn.ModelID, "prompt": prompt}
	if imgURI := encodeFirstFrameDataURI(inputImages); imgURI != "" {
		payload["image_url"] = imgURI
	}
	if opts.durationSeconds > 0 {
		payload["duration"] = opts.durationSeconds
	}
	status, _, body, err := s.doVideoHTTP(ctx, http.MethodPost, trimmedBase+"/videos/generations", conn, false, nil, payload, 60*time.Second)
	if err != nil {
		return llm.VideoResponse{}, err
	}
	if status < 200 || status >= 300 {
		return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var created struct {
		ID          string `json:"id"`
		TaskStatus  string `json:"task_status"`
		VideoResult []struct {
			URL string `json:"url"`
		} `json:"video_result"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return llm.VideoResponse{}, fmt.Errorf("decode BigModel video response: %w", err)
	}
	if len(created.VideoResult) > 0 && strings.TrimSpace(created.VideoResult[0].URL) != "" {
		vid, err := fetchGeneratedVideoURL(ctx, created.VideoResult[0].URL, conn, false)
		if err != nil {
			return llm.VideoResponse{}, err
		}
		return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
	}
	taskID := strings.TrimSpace(created.ID)
	if taskID == "" {
		return llm.VideoResponse{}, errors.New("BigModel video endpoint returned no task id")
	}
	pollURL := trimmedBase + "/async-result/" + url.PathEscape(taskID)
	for {
		if err := waitVideoPoll(ctx); err != nil {
			return llm.VideoResponse{}, err
		}
		pStatus, _, pBody, pErr := s.doVideoHTTP(ctx, http.MethodGet, pollURL, conn, false, nil, nil, 45*time.Second)
		if pErr != nil {
			return llm.VideoResponse{}, pErr
		}
		if pStatus < 200 || pStatus >= 300 {
			return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(pStatus, pBody), conn.APIKey))
		}
		var res struct {
			TaskStatus  string `json:"task_status"`
			VideoResult []struct {
				URL string `json:"url"`
			} `json:"video_result"`
		}
		if err := json.Unmarshal(pBody, &res); err != nil {
			return llm.VideoResponse{}, fmt.Errorf("decode BigModel async-result: %w", err)
		}
		switch strings.ToUpper(strings.TrimSpace(res.TaskStatus)) {
		case "SUCCESS", "SUCCEEDED", "COMPLETED":
			if len(res.VideoResult) == 0 || strings.TrimSpace(res.VideoResult[0].URL) == "" {
				return llm.VideoResponse{}, errors.New("BigModel task succeeded without video URL")
			}
			vid, err := fetchGeneratedVideoURL(ctx, res.VideoResult[0].URL, conn, false)
			if err != nil {
				return llm.VideoResponse{}, err
			}
			return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
		case "FAIL", "FAILED":
			return llm.VideoResponse{}, errors.New("BigModel video generation task failed")
		}
	}
}

func openAIVideoSizeForAspect(aspectRatio, resolution string) string {
	high := resolution == "1080p"
	switch aspectRatio {
	case "9:16":
		if high {
			return "1080x1920"
		}
		return "720x1280"
	case "1:1":
		if high {
			return "1080x1080"
		}
		return "720x720"
	default:
		if high {
			return "1920x1080"
		}
		return "1280x720"
	}
}

func (s *Service) openAIVideosGenerate(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
	opts videoGenExecOptions,
) (llm.VideoResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	payload := map[string]any{
		"model":  conn.ModelID,
		"prompt": prompt,
	}
	if opts.aspectRatio != "" || opts.resolution != "" {
		payload["size"] = openAIVideoSizeForAspect(opts.aspectRatio, opts.resolution)
	}
	if opts.durationSeconds > 0 {
		payload["seconds"] = strconv.Itoa(opts.durationSeconds)
	}
	if imgURI := encodeFirstFrameDataURI(inputImages); imgURI != "" {
		payload["image_url"] = imgURI
	}

	endpoints := []string{trimmedBase + "/videos", trimmedBase + "/videos/generations"}
	var lastStatus int
	var lastBody []byte
	var activeEndpoint string

	for idx, ep := range endpoints {
		status, ct, body, err := s.doVideoHTTP(ctx, http.MethodPost, ep, conn, false, nil, payload, 90*time.Second)
		if err != nil {
			return llm.VideoResponse{}, err
		}
		if status == http.StatusBadRequest && (payload["size"] != nil || payload["seconds"] != nil) {
			minimal := map[string]any{"model": conn.ModelID, "prompt": prompt}
			if imgURI := encodeFirstFrameDataURI(inputImages); imgURI != "" {
				minimal["image_url"] = imgURI
			}
			status, ct, body, err = s.doVideoHTTP(ctx, http.MethodPost, ep, conn, false, nil, minimal, 90*time.Second)
			if err != nil {
				return llm.VideoResponse{}, err
			}
		}
		lastStatus, lastBody, activeEndpoint = status, body, ep
		if status >= 200 && status < 300 {
			if strings.HasPrefix(strings.ToLower(ct), "video/") && len(body) > 0 {
				return llm.VideoResponse{
					Videos: []llm.GeneratedVideo{{Mime: normalizeGeneratedVideoMime(ct, body), Data: body}},
				}, nil
			}
			return s.resolveOpenAIVideoPayloadOrPoll(ctx, conn, activeEndpoint, body)
		}
		if idx == 0 && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) {
			continue
		}
		break
	}
	if lastStatus == http.StatusNotFound || lastStatus == http.StatusMethodNotAllowed {
		if chatResp, chatErr := s.openAIChatGenerateVideo(ctx, conn, prompt, inputImages); chatErr == nil {
			return chatResp, nil
		}
	}
	return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(lastStatus, lastBody), conn.APIKey))
}

func (s *Service) openAIChatGenerateVideo(
	ctx context.Context,
	conn imageGenConn,
	prompt string,
	inputImages []llm.ImageInput,
) (llm.VideoResponse, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/")
	var userContent any = prompt
	if imgURI := encodeFirstFrameDataURI(inputImages); imgURI != "" {
		userContent = []map[string]any{
			{"type": "image_url", "image_url": map[string]string{"url": imgURI}},
			{"type": "text", "text": prompt},
		}
	}
	payload := map[string]any{
		"model":      conn.ModelID,
		"modalities": []string{"text", "video"},
		"messages":   []map[string]any{{"role": "user", "content": userContent}},
	}
	status, _, body, err := s.doVideoHTTP(ctx, http.MethodPost, trimmedBase+"/chat/completions", conn, false, nil, payload, 2*time.Minute)
	if err != nil {
		return llm.VideoResponse{}, err
	}
	if status < 200 || status >= 300 {
		return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), conn.APIKey))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Video   *struct {
					Data string `json:"data"`
					URL  string `json:"url"`
				} `json:"video"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Choices) == 0 {
		return llm.VideoResponse{}, errors.New("chat video fallback returned no choices")
	}
	msg := parsed.Choices[0].Message
	if msg.Video != nil {
		if vid, ok, vErr := extractImmediateOpenAIVideo(ctx, conn, msg.Video.Data, msg.Video.URL); vErr == nil && ok {
			return llm.VideoResponse{Text: strings.TrimSpace(msg.Content), Videos: []llm.GeneratedVideo{vid}}, nil
		}
	}
	if idx := strings.Index(msg.Content, "data:video/"); idx >= 0 {
		sub := msg.Content[idx:]
		if end := strings.IndexAny(sub, " \t\r\n)\"'"); end > 0 {
			sub = sub[:end]
		}
		if vid, ok := decodeDataURIVideo(sub); ok {
			return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
		}
	}
	return llm.VideoResponse{}, errors.New("chat response contained no video payload")
}

func (s *Service) resolveOpenAIVideoPayloadOrPoll(
	ctx context.Context,
	conn imageGenConn,
	submitEndpoint string,
	initialBody []byte,
) (llm.VideoResponse, error) {
	curBody := initialBody
	for {
		var parsed struct {
			ID       string `json:"id"`
			TaskID   string `json:"task_id"`
			Status   string `json:"status"`
			URL      string `json:"url"`
			VideoURL string `json:"video_url"`
			B64JSON  string `json:"b64_json"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
			Data []struct {
				URL           string `json:"url"`
				VideoURL      string `json:"video_url"`
				B64JSON       string `json:"b64_json"`
				RevisedPrompt string `json:"revised_prompt"`
			} `json:"data"`
		}
		if err := json.Unmarshal(curBody, &parsed); err != nil {
			return llm.VideoResponse{}, fmt.Errorf("decode video response: %w", err)
		}
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return llm.VideoResponse{}, errors.New(redactSecretInText(parsed.Error.Message, conn.APIKey))
		}
		if vid, ok, err := extractImmediateOpenAIVideo(ctx, conn, parsed.B64JSON, firstNonEmptyImageStr(parsed.VideoURL, parsed.URL)); err != nil {
			return llm.VideoResponse{}, err
		} else if ok {
			return llm.VideoResponse{Videos: []llm.GeneratedVideo{vid}}, nil
		}
		var listVids []llm.GeneratedVideo
		var note string
		for _, item := range parsed.Data {
			if vid, ok, err := extractImmediateOpenAIVideo(ctx, conn, item.B64JSON, firstNonEmptyImageStr(item.VideoURL, item.URL)); err != nil {
				return llm.VideoResponse{}, err
			} else if ok {
				if note == "" && strings.TrimSpace(item.RevisedPrompt) != "" {
					note = strings.TrimSpace(item.RevisedPrompt)
				}
				listVids = append(listVids, vid)
			}
		}
		if len(listVids) > 0 {
			return llm.VideoResponse{Text: note, Videos: listVids}, nil
		}

		jobID := firstNonEmptyImageStr(parsed.ID, parsed.TaskID)
		st := strings.ToLower(strings.TrimSpace(parsed.Status))
		if st == "failed" || st == "canceled" || st == "cancelled" || st == "error" {
			return llm.VideoResponse{}, fmt.Errorf("video generation job %s ended with status %s", jobID, st)
		}
		if st == "completed" || st == "succeeded" || st == "success" {
			if jobID != "" {
				contentURL := strings.TrimRight(submitEndpoint, "/") + "/" + url.PathEscape(jobID) + "/content"
				cStatus, cType, cBody, cErr := s.doVideoHTTP(ctx, http.MethodGet, contentURL, conn, false, nil, nil, 2*time.Minute)
				if cErr == nil && cStatus >= 200 && cStatus < 300 && len(cBody) > 0 {
					return llm.VideoResponse{
						Videos: []llm.GeneratedVideo{{Mime: normalizeGeneratedVideoMime(cType, cBody), Data: cBody}},
					}, nil
				}
			}
			return llm.VideoResponse{}, errors.New("video job completed without downloadable video content")
		}
		if jobID == "" {
			return llm.VideoResponse{}, errors.New("video endpoint returned neither video data nor async job id")
		}
		if err := waitVideoPoll(ctx); err != nil {
			return llm.VideoResponse{}, err
		}
		pollURL := strings.TrimRight(submitEndpoint, "/") + "/" + url.PathEscape(jobID)
		pStatus, _, pBody, pErr := s.doVideoHTTP(ctx, http.MethodGet, pollURL, conn, false, nil, nil, 45*time.Second)
		if pErr != nil {
			return llm.VideoResponse{}, pErr
		}
		if pStatus < 200 || pStatus >= 300 {
			return llm.VideoResponse{}, errors.New(redactSecretInText(extractAPIErrorMessage(pStatus, pBody), conn.APIKey))
		}
		curBody = pBody
	}
}

func extractImmediateOpenAIVideo(
	ctx context.Context,
	conn imageGenConn,
	b64, rawURL string,
) (llm.GeneratedVideo, bool, error) {
	if strings.TrimSpace(b64) != "" {
		data, err := decodeBase64Flexible(b64)
		if err != nil {
			return llm.GeneratedVideo{}, false, err
		}
		return llm.GeneratedVideo{Mime: normalizeGeneratedVideoMime("", data), Data: data}, true, nil
	}
	if strings.TrimSpace(rawURL) != "" {
		vid, err := fetchGeneratedVideoURL(ctx, rawURL, conn, false)
		if err != nil {
			return llm.GeneratedVideo{}, false, err
		}
		return vid, true, nil
	}
	return llm.GeneratedVideo{}, false, nil
}
