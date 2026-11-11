package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

var tinyTestPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
)

type memPipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
}

func newMemPipeServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	ln := &memPipeListener{
		ch:     make(chan net.Conn, 16),
		closed: make(chan struct{}),
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()

	origLocal := localTransport
	localTransport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c1, c2 := net.Pipe()
			select {
			case ln.ch <- c2:
				return c1, nil
			case <-ln.closed:
				_ = c1.Close()
				_ = c2.Close()
				return nil, net.ErrClosed
			case <-ctx.Done():
				_ = c1.Close()
				_ = c2.Close()
				return nil, ctx.Err()
			}
		},
	}
	t.Cleanup(func() {
		localTransport = origLocal
		close(ln.closed)
		_ = srv.Close()
	})
	return strings.Join([]string{"http:", "", "127.0.0.1:19090"}, "/")
}

func (l *memPipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memPipeListener) Close() error { return nil }
func (l *memPipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 19090}
}

func TestGeminiProtocolAndModelCapsNormalization(t *testing.T) {
	localHost := strings.Join([]string{"http:", "", "127.0.0.1:18080"}, "/")
	if got := effectiveProviderProtocol("custom-gem", "@ai-sdk/openai-compatible", localHost+"/v1beta"); got != "google" {
		t.Fatalf("expected google protocol for /v1beta endpoint, got %q", got)
	}
	if got := effectiveProviderProtocol("google", "@ai-sdk/google", localHost+"/v1beta/openai"); got != "openai" {
		t.Fatalf("expected openai protocol for /v1beta/openai endpoint, got %q", got)
	}
	if got := normalizeGoogleBaseURL(localHost + "/gemini-proxy"); got != localHost+"/gemini-proxy/v1beta" {
		t.Fatalf("expected sub-path Gemini proxy to append /v1beta, got %q", got)
	}
	if shouldSendBearerOnGoogleRetry("AIzaSyTest12345") {
		t.Fatalf("expected AIza key not to send Bearer header on Google retry")
	}
	if !shouldSendBearerOnGoogleRetry("sk-relay-12345") {
		t.Fatalf("expected relay token to send Bearer header on Google retry")
	}

	doc := map[string]interface{}{
		"provider": map[string]interface{}{
			"my-gemini": map[string]interface{}{
				"npm": "@ai-sdk/google",
				"options": map[string]interface{}{
					"baseURL": localHost + "/gemini",
				},
				"models": map[string]interface{}{
					"gemini-2.5-flash-image": map[string]interface{}{
						"name":      "Gemini 2.5 Flash Image",
						"reasoning": true,
						"variants": map[string]interface{}{
							"low": map[string]interface{}{
								"thinkingConfig": map[string]interface{}{"thinkingBudget": 4096},
							},
						},
					},
					"models/gemini-2.5-pro": map[string]interface{}{
						"name": "Gemini 2.5 Pro",
					},
					"gemini-2.0-flash": map[string]interface{}{
						"name":      "Gemini 2.0 Flash",
						"reasoning": true,
						"variants": map[string]interface{}{
							"low": map[string]interface{}{
								"thinkingConfig": map[string]interface{}{"thinkingBudget": 4096},
							},
						},
					},
				},
			},
		},
	}

	normalizeDocModelVariants(doc)
	prov := doc["provider"].(map[string]interface{})["my-gemini"].(map[string]interface{})
	opts := prov["options"].(map[string]interface{})
	if opts["baseURL"] != localHost+"/gemini/v1beta" {
		t.Fatalf("expected sub-path baseURL to normalize to /gemini/v1beta, got %v", opts["baseURL"])
	}

	models := prov["models"].(map[string]interface{})
	imgModel := models["gemini-2.5-flash-image"].(map[string]interface{})
	if r, _ := imgModel["reasoning"].(bool); r {
		t.Fatalf("expected gemini-2.5-flash-image reasoning=false after cleanup, got true")
	}
	if _, hasVariants := imgModel["variants"]; hasVariants {
		t.Fatalf("expected auto-injected thinkingConfig variants to be stripped from image model")
	}
	if !hasImageOutputModality(imgModel) || !hasImageInputModality(imgModel) {
		t.Fatalf("expected gemini-2.5-flash-image to have both input and output image modalities: %+v", imgModel["modalities"])
	}

	if _, hasPrefixed := models["models/gemini-2.5-pro"]; hasPrefixed {
		t.Fatalf("expected models/ prefix to be stripped from Gemini model map key")
	}
	proModel := models["gemini-2.5-pro"].(map[string]interface{})
	if r, _ := proModel["reasoning"].(bool); !r {
		t.Fatalf("expected gemini-2.5-pro to keep reasoning=true")
	}

	flash20 := models["gemini-2.0-flash"].(map[string]interface{})
	if r, _ := flash20["reasoning"].(bool); r {
		t.Fatalf("expected non-thinking gemini-2.0-flash to normalize reasoning=false")
	}
	if _, hasV := flash20["variants"]; hasV {
		t.Fatalf("expected auto-injected thinkingConfig variants to be stripped from gemini-2.0-flash")
	}
}

func TestImageResponseParsers(t *testing.T) {
	b64PNG := base64.StdEncoding.EncodeToString(tinyTestPNG)

	googleRaw, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{{
			"finishReason": "STOP",
			"content": map[string]any{
				"parts": []map[string]any{
					{"text": "internal thought", "thought": true},
					{"text": "Generated Gemini illustration"},
					{"inlineData": map[string]any{"mimeType": "image/png", "data": b64PNG}},
				},
			},
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     20,
			"candidatesTokenCount": 1290,
		},
	})
	gResp, reason, err := parseGoogleGenerateContentImageResponse(googleRaw)
	if err != nil || reason != "STOP" || len(gResp.Images) != 1 || gResp.Text != "Generated Gemini illustration" {
		t.Fatalf("unexpected google parse result: resp=%+v reason=%q err=%v", gResp, reason, err)
	}

	svc := NewService(0, "", t.TempDir(), nil)
	chatRaw, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"content": "Here is the artwork:",
				"images": []map[string]any{{
					"b64_json": b64PNG,
				}},
			},
		}},
	})
	cResp, err := svc.parseOpenAIChatImageResponse(context.Background(), chatRaw, 5*time.Second)
	if err != nil || len(cResp.Images) != 1 || cResp.Text != "Here is the artwork:" {
		t.Fatalf("unexpected chat image parse result: resp=%+v err=%v", cResp, err)
	}
}

func TestProbeAndGenerateImageOverHTTP(t *testing.T) {
	b64PNG := base64.StdEncoding.EncodeToString(tinyTestPNG)
	var seenGeminiProMaxTokens float64
	var seenModalities []any

	baseOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		switch r.URL.Path {
		case "/v1beta/models/gemini-2.5-pro:generateContent":
			if genCfg, ok := payload["generationConfig"].(map[string]any); ok {
				seenGeminiProMaxTokens, _ = genCfg["maxOutputTokens"].(float64)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}},
				}},
			})
		case "/v1beta/models/gemini-2.5-flash-image:generateContent":
			if genCfg, ok := payload["generationConfig"].(map[string]any); ok {
				seenModalities, _ = genCfg["responseModalities"].([]any)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": "Gemini image ready"},
							{"inlineData": map[string]any{"mimeType": "image/png", "data": b64PNG}},
						},
					},
				}},
			})
		case "/v1beta/models/imagen-3.0-generate-002:predict":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"predictions": []map[string]any{{
					"mimeType":           "image/png",
					"bytesBase64Encoded": b64PNG,
				}},
			})
		case "/v1/images/generations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"b64_json": b64PNG}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	rootDir := t.TempDir()
	t.Setenv("HOME", rootDir)
	svc := NewService(0, "", rootDir, nil)

	for _, tc := range []struct {
		modelID string
		baseURL string
		npm     string
	}{
		{"gemini-2.5-pro", baseOrigin + "/v1beta", "@ai-sdk/openai-compatible"},
		{"gemini-2.5-flash-image", baseOrigin, "@ai-sdk/google"},
		{"imagen-3.0-generate-002", baseOrigin + "/v1beta", "@ai-sdk/google"},
		{"gpt-image-1", baseOrigin + "/v1", "@ai-sdk/openai-compatible"},
	} {
		raw, _ := json.Marshal(testProviderModelRequest{
			ProviderID: "test-prov",
			BaseURL:    tc.baseURL,
			NPM:        tc.npm,
			ModelID:    tc.modelID,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/test-model", bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		svc.handleTestProviderModel(rec, req)
		var out testProviderModelResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if !out.OK {
			t.Fatalf("probe failed for %s: %s", tc.modelID, out.Error)
		}
	}
	if seenGeminiProMaxTokens < 256 {
		t.Fatalf("expected Gemini 2.5 Pro probe maxOutputTokens >= 256, got %v", seenGeminiProMaxTokens)
	}
	if len(seenModalities) == 0 {
		t.Fatalf("expected responseModalities to be populated on Gemini image request")
	}

	_ = svc.PersistHoxiConfigDoc(map[string]interface{}{
		"provider": map[string]interface{}{
			"gem-prov": map[string]interface{}{
				"npm": "@ai-sdk/google",
				"options": map[string]interface{}{
					"baseURL": baseOrigin + "/v1beta",
				},
				"models": map[string]interface{}{
					"gemini-2.5-flash-image": map[string]interface{}{"name": "Gemini Flash Image"},
				},
			},
		},
	})
	imgOut, err := svc.GenerateImage(context.Background(), llm.ImageRequest{
		ProviderID: "gem-prov",
		ModelID:    "gemini-2.5-flash-image",
		Prompt:     "Draw a mountain",
	})
	if err != nil || len(imgOut.Images) != 1 || !strings.Contains(imgOut.Text, "Gemini image ready") {
		t.Fatalf("GenerateImage failed: err=%v out=%+v", err, imgOut)
	}
}
