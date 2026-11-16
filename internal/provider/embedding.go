package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

var _ llm.EmbeddingClient = (*Service)(nil)

const (
	defaultEmbeddingTimeout = 15 * time.Second
	maxEmbeddingBatchInputs = 32
)

type resolvedEmbeddingTarget struct {
	ProviderID string
	ModelID    string
	BaseURL    string
	APIKey     string
	NPM        string
	Headers    map[string]string
	IsGoogle   bool
}

func isEmbeddingModelCandidate(modelID, explicitCategory string) bool {
	if normalizeModelCategory(explicitCategory) == "embedding" {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(lower, "embedding") ||
		strings.Contains(lower, "embed") ||
		strings.Contains(lower, "bge-") ||
		strings.Contains(lower, "jina-embeddings")
}

func (s *Service) resolveEmbeddingTarget(preferredProviderID, preferredModelID string) (resolvedEmbeddingTarget, bool) {
	if s == nil {
		return resolvedEmbeddingTarget{}, false
	}
	merged := s.loadOpenCodeImportedProviderMap()
	for k, v := range s.loadHoxiProvidersMap() {
		merged[k] = v
	}

	build := func(pid, mid string) (resolvedEmbeddingTarget, bool) {
		baseURL, apiKey, npm, headers := s.resolveProviderConnection(pid, "", "")
		if !isHTTPEndpoint(baseURL) {
			return resolvedEmbeddingTarget{}, false
		}
		if apiKey == "" && len(headers) == 0 && !isLoopbackURL(baseURL) {
			return resolvedEmbeddingTarget{}, false
		}
		proto := effectiveProviderProtocol(pid, npm, baseURL)
		if proto == "anthropic" {
			return resolvedEmbeddingTarget{}, false
		}
		return resolvedEmbeddingTarget{
			ProviderID: pid,
			ModelID:    mid,
			BaseURL:    baseURL,
			APIKey:     apiKey,
			NPM:        npm,
			Headers:    headers,
			IsGoogle:   proto == "google",
		}, true
	}

	prefPID := strings.TrimSpace(preferredProviderID)
	prefMID := strings.TrimSpace(preferredModelID)
	if prefPID != "" && prefMID != "" && s.isProviderUsableForLocalMedia(prefPID) {
		if t, ok := build(prefPID, prefMID); ok {
			return t, true
		}
	}

	for _, pid := range sortedKeys(merged) {
		if !s.isProviderUsableForLocalMedia(pid) {
			continue
		}
		pObj, _ := merged[pid].(map[string]interface{})
		mMap, _ := pObj["models"].(map[string]interface{})
		for _, mid := range sortedKeys(mMap) {
			mObj, _ := mMap[mid].(map[string]interface{})
			cat := ""
			if mObj != nil {
				cat, _ = mObj["category"].(string)
			}
			if isEmbeddingModelCandidate(mid, cat) {
				if t, ok := build(pid, mid); ok {
					return t, true
				}
			}
		}
	}
	return resolvedEmbeddingTarget{}, false
}

// HasEmbeddingModel reports whether a user-configured embedding model is ready.
func (s *Service) HasEmbeddingModel() bool {
	_, ok := s.resolveEmbeddingTarget("", "")
	return ok
}

// EmbedTexts computes normalized float32 vectors for up to 32 text inputs using
// OpenAI /embeddings or Google Gemini :batchEmbedContents.
func (s *Service) EmbedTexts(ctx context.Context, req llm.EmbeddingRequest) (llm.EmbeddingResponse, error) {
	if len(req.Inputs) == 0 {
		return llm.EmbeddingResponse{}, nil
	}
	inputs := req.Inputs
	if len(inputs) > maxEmbeddingBatchInputs {
		inputs = inputs[:maxEmbeddingBatchInputs]
	}
	target, ok := s.resolveEmbeddingTarget(req.ProviderID, req.ModelID)
	if !ok {
		return llm.EmbeddingResponse{}, llm.ErrUnavailable
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultEmbeddingTimeout
	}

	var vecs [][]float32
	var err error
	if target.IsGoogle {
		vecs, err = s.embedViaGoogleGemini(ctx, target, inputs, timeout)
	} else {
		vecs, err = s.embedViaOpenAI(ctx, target, inputs, timeout)
	}
	if err != nil {
		return llm.EmbeddingResponse{}, err
	}
	for i := range vecs {
		normalizeL2(vecs[i])
	}
	return llm.EmbeddingResponse{
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
		Vectors:    vecs,
	}, nil
}

func (s *Service) embedViaOpenAI(
	ctx context.Context,
	target resolvedEmbeddingTarget,
	inputs []string,
	timeout time.Duration,
) ([][]float32, error) {
	targetURL := strings.TrimRight(strings.TrimSpace(target.BaseURL), "/") + "/embeddings"
	payload := map[string]any{
		"model": target.ModelID,
		"input": inputs,
	}
	conn := imageGenConn{
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
		BaseURL:    target.BaseURL,
		APIKey:     target.APIKey,
		NPM:        target.NPM,
		Headers:    target.Headers,
	}
	status, body, err := s.doJSONImageRequest(ctx, conn, false, targetURL, payload, timeout)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), target.APIKey))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) != len(inputs) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([][]float32, len(inputs))
	for i, item := range parsed.Data {
		idx := item.Index
		if idx < 0 || idx >= len(out) || out[idx] != nil {
			idx = i
		}
		out[idx] = item.Embedding
	}
	return out, nil
}

func (s *Service) embedViaGoogleGemini(
	ctx context.Context,
	target resolvedEmbeddingTarget,
	inputs []string,
	timeout time.Duration,
) ([][]float32, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(target.ModelID), "models/")
	resourceModel := "models/" + cleanModel
	targetURL := googleModelsBaseURL(target.BaseURL) + "/" + url.PathEscape(cleanModel) + ":batchEmbedContents"
	reqs := make([]map[string]any, len(inputs))
	for i, text := range inputs {
		reqs[i] = map[string]any{
			"model": resourceModel,
			"content": map[string]any{
				"parts": []map[string]string{{"text": text}},
			},
		}
	}
	conn := imageGenConn{
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
		BaseURL:    target.BaseURL,
		APIKey:     target.APIKey,
		NPM:        target.NPM,
		Headers:    target.Headers,
	}
	status, body, err := s.doJSONImageRequest(ctx, conn, true, targetURL, map[string]any{"requests": reqs}, timeout)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, errors.New(redactSecretInText(extractAPIErrorMessage(status, body), target.APIKey))
	}
	var parsed struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Embeddings) != len(inputs) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([][]float32, len(inputs))
	for i, e := range parsed.Embeddings {
		out[i] = e.Values
	}
	return out, nil
}

func normalizeL2(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum <= 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= norm
	}
}
