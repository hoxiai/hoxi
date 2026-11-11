package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestOpenAIResponsesCompletionAndFallback(t *testing.T) {
	var responsesCalls int32
	var chatFallbackCalls int32

	baseOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/responses":
			atomic.AddInt32(&responsesCalls, 1)
			if body["model"] == "legacy-chat-only" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"responses endpoint not supported"}}`))
				return
			}
			maxOut, _ := body["max_output_tokens"].(float64)
			if maxOut < 32 {
				t.Errorf("expected max_output_tokens >= 32 floor, got %v", maxOut)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{"type": "output_text", "text": "Title From Responses API"},
						},
					},
				},
			})
		case "/v1/chat/completions":
			atomic.AddInt32(&chatFallbackCalls, 1)
			if body["model"] == "gpt-5-codex" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"This model is only supported in v1/responses and not in v1/chat/completions"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "Fallback Chat Title"}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	rootDir := t.TempDir()
	t.Setenv("HOME", rootDir)
	svc := NewService(0, "", rootDir, nil)
	_ = svc.PersistHoxiConfigDoc(map[string]interface{}{
		"provider": map[string]interface{}{
			"oai-resp": map[string]interface{}{
				"npm": "@ai-sdk/openai",
				"options": map[string]interface{}{
					"baseURL": baseOrigin + "/v1",
				},
				"models": map[string]interface{}{
					"gpt-5":            map[string]interface{}{"name": "GPT-5"},
					"legacy-chat-only": map[string]interface{}{"name": "Legacy Chat"},
				},
			},
			"oai-compat": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"baseURL": baseOrigin + "/v1",
				},
				"models": map[string]interface{}{
					"gpt-5-codex": map[string]interface{}{"name": "GPT-5 Codex"},
				},
			},
		},
	})

	out1, err := svc.Complete(context.Background(), svc.Resolve("oai-resp", "gpt-5", "", ""), llm.Request{
		Prompt:    "Hello world",
		MaxTokens: 8,
	})
	if err != nil || out1 != "Title From Responses API" {
		t.Fatalf("expected /v1/responses output, got %q (err=%v)", out1, err)
	}

	out2, err := svc.Complete(context.Background(), svc.Resolve("oai-resp", "legacy-chat-only", "", ""), llm.Request{
		Prompt:    "Test fallback",
		MaxTokens: 64,
	})
	if err != nil || out2 != "Fallback Chat Title" {
		t.Fatalf("expected /chat/completions fallback output, got %q (err=%v)", out2, err)
	}

	out3, err := svc.Complete(context.Background(), svc.Resolve("oai-compat", "gpt-5-codex", "", ""), llm.Request{
		Prompt:    "Upgrade to responses",
		MaxTokens: 64,
	})
	if err != nil || out3 != "Title From Responses API" {
		t.Fatalf("expected automatic upgrade to /v1/responses, got %q (err=%v)", out3, err)
	}
	if atomic.LoadInt32(&responsesCalls) < 3 || atomic.LoadInt32(&chatFallbackCalls) < 2 {
		t.Fatalf("expected both endpoints to be exercised: responses=%d chat=%d", responsesCalls, chatFallbackCalls)
	}
}

func TestMultimodalCapsSmallFamilyAndCacheEligibleTelemetry(t *testing.T) {
	if fam := inferSmallModelFamily("gemini-2.5-flash"); fam != "gemini-flash" {
		t.Fatalf("expected gemini-flash family, got %q", fam)
	}
	if fam := inferSmallModelFamily("gpt-4.1-mini"); fam != "gpt-nano" {
		t.Fatalf("expected gpt-nano family, got %q", fam)
	}
	if fam := inferSmallModelFamily("claude-haiku-4-5"); fam != "claude-haiku" {
		t.Fatalf("expected claude-haiku family, got %q", fam)
	}
	if fam := inferSmallModelFamily("gemini-2.5-pro"); fam != "" {
		t.Fatalf("expected flagship model not to receive small family tag, got %q", fam)
	}

	mods := inferRichChatInputModalities("google", "gemini-2.5-pro", true)
	wantSet := map[string]bool{"text": true, "image": true, "pdf": true, "audio": true, "video": true}
	for _, m := range mods {
		delete(wantSet, m)
	}
	if len(wantSet) != 0 {
		t.Fatalf("missing Gemini multimodal inputs: remaining=%v got=%v", wantSet, mods)
	}

	claudeMods := inferRichChatInputModalities("anthropic", "claude-sonnet-4-5", true)
	hasPDF := false
	for _, m := range claudeMods {
		if m == "pdf" {
			hasPDF = true
		}
	}
	if !hasPDF {
		t.Fatalf("expected Claude Sonnet 4.5 to advertise native pdf input, got %v", claudeMods)
	}

	t.Setenv("HOXI_HOME", t.TempDir())
	store := newModelStatsStore(t.TempDir())
	mux := http.NewServeMux()
	store.registerRoutes(mux)

	postBody, _ := json.Marshal(map[string]any{
		"providers": map[string]any{
			"google": map[string]any{
				"totalTurns":         26,
				"successTurns":       26,
				"cacheHitTurns":      0,
				"cacheEligibleTurns": 4,
				"models": map[string]any{
					"gemini-2.5-flash": map[string]any{
						"modelID":            "gemini-2.5-flash",
						"totalTurns":         26,
						"successTurns":       26,
						"cacheHitTurns":      0,
						"cacheEligibleTurns": 4,
					},
				},
			},
		},
	})
	wPost := httptest.NewRecorder()
	mux.ServeHTTP(wPost, httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/telemetry", bytes.NewReader(postBody)))
	if wPost.Code != http.StatusOK {
		t.Fatalf("telemetry POST failed: status=%d body=%s", wPost.Code, wPost.Body.String())
	}

	wGet := httptest.NewRecorder()
	mux.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/telemetry", nil))
	var getResp struct {
		Providers map[string]providerTelemetryStats `json:"providers"`
	}
	_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)
	ms := getResp.Providers["google"].Models["gemini-2.5-flash"]
	if ms == nil || ms.TotalTurns != 26 || ms.CacheEligibleTurns != 4 {
		t.Fatalf("expected persisted CacheEligibleTurns=4 roundtrip, got %+v", ms)
	}
}

func TestImageAspectSizeAudioASRAndEmbeddings(t *testing.T) {
	b64PNG := base64.StdEncoding.EncodeToString(tinyTestPNG)
	var seenAspect, seenImageSize string
	var flash20Attempts int32

	baseOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1beta/models/gemini-2.5-flash-image:generateContent":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if genCfg, ok := body["generationConfig"].(map[string]any); ok {
				if imgCfg, ok := genCfg["imageConfig"].(map[string]any); ok {
					seenAspect, _ = imgCfg["aspectRatio"].(string)
					seenImageSize, _ = imgCfg["imageSize"].(string)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{
							{"inlineData": map[string]any{"mimeType": "image/png", "data": b64PNG}},
						},
					},
				}},
			})
		case "/v1beta/models/gemini-2.0-flash-preview-image-generation:generateContent":
			attempt := atomic.AddInt32(&flash20Attempts, 1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if genCfg, ok := body["generationConfig"].(map[string]any); ok {
				if _, hasImgCfg := genCfg["imageConfig"]; hasImgCfg && attempt == 1 {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"message":"Unknown name \"imageConfig\" at 'generation_config'"}}`))
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{
							{"inlineData": map[string]any{"mimeType": "image/png", "data": b64PNG}},
						},
					},
				}},
			})
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "whisper dictated hello"})
		case "/v1beta/models/gemini-2.5-flash:generateContent":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": "gemini dictated audio"}},
					},
				}},
			})
		case "/v1beta/models/gemini-embedding-001:batchEmbedContents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"embeddings": []map[string]any{
					{"values": []float64{3, 4}},
					{"values": []float64{0, 5}},
				},
			})
		case "/v1/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"index": 1, "embedding": []float64{0, 2}},
					{"index": 0, "embedding": []float64{6, 8}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	rootDir := t.TempDir()
	t.Setenv("HOME", rootDir)
	svc := NewService(0, "", rootDir, nil)
	_ = svc.PersistHoxiConfigDoc(map[string]interface{}{
		"provider": map[string]interface{}{
			"gem-local": map[string]interface{}{
				"npm": "@ai-sdk/google",
				"options": map[string]interface{}{
					"baseURL": baseOrigin + "/v1beta",
				},
				"models": map[string]interface{}{
					"gemini-2.5-flash":                          map[string]interface{}{"name": "Gemini 2.5 Flash"},
					"gemini-2.5-flash-image":                    map[string]interface{}{"name": "Gemini 2.5 Flash Image"},
					"gemini-2.0-flash-preview-image-generation": map[string]interface{}{"name": "Gemini 2.0 Image"},
					"gemini-embedding-001":                      map[string]interface{}{"name": "Gemini Embedding"},
				},
			},
			"oai-local": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"baseURL": baseOrigin + "/v1",
				},
				"models": map[string]interface{}{
					"whisper-large-v3":       map[string]interface{}{"name": "Whisper v3"},
					"text-embedding-3-small": map[string]interface{}{"name": "Text Embedding 3 Small"},
				},
			},
		},
	})

	imgRes, err := svc.GenerateImage(context.Background(), llm.ImageRequest{
		Prompt:      "Wide banner",
		AspectRatio: "16:9",
		ImageSize:   "2K",
	})
	if err != nil || len(imgRes.Images) != 1 || seenAspect != "16:9" || seenImageSize != "2K" {
		t.Fatalf("expected 16:9 2K Gemini image, got aspect=%q size=%q err=%v", seenAspect, seenImageSize, err)
	}

	img20, err := svc.GenerateImage(context.Background(), llm.ImageRequest{
		ProviderID:  "gem-local",
		ModelID:     "gemini-2.0-flash-preview-image-generation",
		Prompt:      "Icon",
		AspectRatio: "4:3",
		ImageSize:   "2K",
	})
	if err != nil || len(img20.Images) != 1 || atomic.LoadInt32(&flash20Attempts) != 2 {
		t.Fatalf("expected HTTP 400 imageConfig retry on Gemini 2.0 image model, attempts=%d err=%v", flash20Attempts, err)
	}

	if !svc.HasAudioTranscriber() {
		t.Fatalf("expected HasAudioTranscriber() == true")
	}
	asrWhisper, err := svc.TranscribeAudio(context.Background(), llm.AudioTranscribeRequest{
		Filename: "voice.webm",
		Mime:     "audio/webm",
		Data:     []byte("fake-webm-audio"),
		Language: "zh",
	})
	if err != nil || asrWhisper.Text != "whisper dictated hello" || asrWhisper.ProviderID != "oai-local" {
		t.Fatalf("unexpected Whisper ASR result: %+v err=%v", asrWhisper, err)
	}

	asrGem, err := svc.TranscribeAudio(context.Background(), llm.AudioTranscribeRequest{
		ProviderID: "gem-local",
		ModelID:    "gemini-2.5-flash",
		Filename:   "clip.mp3",
		Mime:       "audio/mpeg",
		Data:       []byte("fake-mp3-audio"),
	})
	if err != nil || asrGem.Text != "gemini dictated audio" {
		t.Fatalf("unexpected Gemini ASR result: %+v err=%v", asrGem, err)
	}

	if !svc.HasEmbeddingModel() {
		t.Fatalf("expected HasEmbeddingModel() == true")
	}
	embOAI, err := svc.EmbedTexts(context.Background(), llm.EmbeddingRequest{
		ProviderID: "oai-local",
		ModelID:    "text-embedding-3-small",
		Inputs:     []string{"first", "second"},
	})
	if err != nil || len(embOAI.Vectors) != 2 {
		t.Fatalf("unexpected OpenAI embeddings: %+v err=%v", embOAI, err)
	}
	if math.Abs(float64(embOAI.Vectors[0][0])-0.6) > 1e-4 || math.Abs(float64(embOAI.Vectors[0][1])-0.8) > 1e-4 {
		t.Fatalf("expected L2-normalized [0.6, 0.8] at index 0, got %v", embOAI.Vectors[0])
	}

	embGem, err := svc.EmbedTexts(context.Background(), llm.EmbeddingRequest{
		ProviderID: "gem-local",
		ModelID:    "gemini-embedding-001",
		Inputs:     []string{"alpha", "beta"},
	})
	if err != nil || len(embGem.Vectors) != 2 || math.Abs(float64(embGem.Vectors[0][0])-0.6) > 1e-4 {
		t.Fatalf("unexpected Gemini batch embeddings: %+v err=%v", embGem, err)
	}
}
