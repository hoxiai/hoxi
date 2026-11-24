package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestTranslateImageV1LegacyFallbackSceneInlineAndSSRFGuard(t *testing.T) {
	var seenV1Calls int32
	var seenLegacyCalls int32
	var seenBgFetch int32
	var fallbackMode atomic.Bool

	var baseOrigin string
	baseOrigin = newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/assets/bg.png":
			n := atomic.AddInt32(&seenBgFetch, 1)
			if n == 1 && r.Header.Get("Referer") == "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tinyTestPNG)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/assets/layer.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tinyTestPNG)
			return
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/translations":
			atomic.AddInt32(&seenV1Calls, 1)
			if r.Header.Get("Authorization") != "Bearer sk-gw-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if fallbackMode.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			var reqBody map[string]any
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if reqBody["source_language"] != "zh" || reqBody["target_language"] != "ru" {
				t.Errorf("unexpected translate languages: %+v", reqBody)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"finalImageUrl": baseOrigin + "/assets/bg.png",
					"inPaintingUrl": baseOrigin + "/assets/bg.png",
					"scene": map[string]any{
						"width":         800,
						"height":        800,
						"backgroundImg": baseOrigin + "/assets/bg.png",
						"children": []map[string]any{
							{"type": "image", "src": baseOrigin + "/assets/layer.png"},
							{"type": "text", "value": "Привет"},
						},
					},
				},
			})
			return
		case r.Method == http.MethodPost && r.URL.Path == "/ai/translate/image":
			atomic.AddInt32(&seenLegacyCalls, 1)
			var legReq map[string]any
			_ = json.NewDecoder(r.Body).Decode(&legReq)
			if legReq["image_url"] == "" || legReq["image"] == "" {
				t.Errorf("expected both image and image_url in legacy payload, got %+v", legReq)
			}
			tplObj := map[string]any{
				"width":           800,
				"height":          800,
				"backgroundImg":   baseOrigin + "/assets/bg.png",
				"backgroundImage": baseOrigin + "/assets/bg.png",
				"children": []map[string]any{
					{"type": "image", "src": baseOrigin + "/assets/layer.png"},
				},
			}
			tplBytes, _ := json.Marshal(tplObj)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"final_image_url": baseOrigin + "/assets/bg.png",
					"template_json":   string(tplBytes),
				},
			})
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	hoxiHome := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	wsDir := t.TempDir()
	doc := map[string]any{
		"provider": map[string]any{
			"ainode-local": map[string]any{
				"name": "Local AINode",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": baseOrigin + "/v1",
					"apiKey":  "sk-gw-secret",
				},
				"models": map[string]any{
					"alimt-image-translate": map[string]any{
						"name":           "AliMT Image Translate",
						"capabilityTags": []string{"image_translate"},
						"imageTranslate": map[string]any{"outputModes": []string{"raster", "editable_scene"}},
					},
				},
			},
		},
	}
	rawDoc, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(hoxiHome, "models.json"), rawDoc, 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", wsDir, nil)
	srcDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyTestPNG)

	// 1. Primary /v1/images/translations + remote scene image inlining (including 403 Referer retry)
	res, err := svc.TranslateImage(context.Background(), llm.ImageTranslateRequest{
		Image:          srcDataURL,
		SourceLanguage: "zh",
		TargetLanguage: "ru",
		OutputMode:     llm.ImageTranslateModeEditableScene,
		RequestID:      "req_trans_1",
	})
	if err != nil {
		t.Fatalf("TranslateImage v1 failed: %v", err)
	}
	if !strings.HasPrefix(res.FinalImageURL, "data:image/png;base64,") {
		t.Fatalf("expected inlined finalImageUrl dataUrl, got %q", res.FinalImageURL)
	}
	var sceneMap map[string]any
	if err := json.Unmarshal(res.Scene, &sceneMap); err != nil {
		t.Fatalf("unmarshal scene: %v", err)
	}
	if bg, _ := sceneMap["backgroundImg"].(string); !strings.HasPrefix(bg, "data:image/png;base64,") {
		t.Fatalf("expected inlined backgroundImg, got %q", bg)
	}
	children, _ := sceneMap["children"].([]any)
	firstChild, _ := children[0].(map[string]any)
	if layerSrc, _ := firstChild["src"].(string); !strings.HasPrefix(layerSrc, "data:image/png;base64,") {
		t.Fatalf("expected inlined image layer src, got %q", layerSrc)
	}
	if atomic.LoadInt32(&seenBgFetch) != 2 {
		t.Fatalf("expected 403 hotlink retry (2 fetches, then cached), got %d", atomic.LoadInt32(&seenBgFetch))
	}

	// 2. Fallback to legacy /ai/translate/image + JSON.parse(data.template_json)
	fallbackMode.Store(true)
	res2, err := svc.TranslateImage(context.Background(), llm.ImageTranslateRequest{
		Image:          srcDataURL,
		SourceLanguage: "zh",
		TargetLanguage: "en",
		OutputMode:     llm.ImageTranslateModeEditableScene,
	})
	if err != nil {
		t.Fatalf("TranslateImage legacy fallback failed: %v", err)
	}
	if atomic.LoadInt32(&seenLegacyCalls) != 1 || len(res2.Scene) == 0 {
		t.Fatalf("expected legacy /ai/translate/image fallback with parsed template_json, calls=%d scene=%s", atomic.LoadInt32(&seenLegacyCalls), string(res2.Scene))
	}
	var scene2 map[string]any
	_ = json.Unmarshal(res2.Scene, &scene2)
	if bg, _ := scene2["backgroundImg"].(string); !strings.HasPrefix(bg, "data:image/png;base64,") {
		t.Fatalf("expected inlined backgroundImg from template_json, got %q", bg)
	}
	if bg2, _ := scene2["backgroundImage"].(string); !strings.HasPrefix(bg2, "data:image/png;base64,") {
		t.Fatalf("expected inlined backgroundImage from template_json, got %q", bg2)
	}

	// 3. Reject cross-port loopback SSRF URL inside scene and finalImageUrl
	ssrfScene := map[string]any{
		"backgroundImg": "http://127.0.0.1:5400/api/hoxi/provider/config",
	}
	if err := inlineSceneRemoteImages(context.Background(), ssrfScene, imageGenConn{BaseURL: baseOrigin + "/v1"}, 5*time.Second, map[string]string{}, 0); err == nil {
		t.Fatalf("expected cross-port loopback scene URL to be blocked")
	}
	if _, _, err := maybeInlineOutputImageURL(context.Background(), "http://127.0.0.1:5400/api/hoxi/provider/config", imageGenConn{BaseURL: baseOrigin + "/v1"}, 5*time.Second, map[string]string{}); err == nil {
		t.Fatalf("expected cross-port loopback finalImageUrl to be blocked")
	}
	// 4. Protocol-relative //host/path URLs normalize to https:// and get SSRF-checked
	if got, ok := normalizeRemoteHTTPURL("//ae01.alicdn.com/kf/img.jpg"); !ok || got != "https://ae01.alicdn.com/kf/img.jpg" {
		t.Fatalf("expected protocol-relative URL to normalize to https://, got %q (ok=%v)", got, ok)
	}
	if _, ok := normalizeRemoteHTTPURL("data:image/png;base64,AAA"); ok {
		t.Fatalf("data URL must not be treated as remote HTTP URL")
	}
	// 5. Raw Aliyun PascalCase Data / FinalImageUrl / Url / TemplateJson payload
	parsedPascal, hasPascal, err := parseImageTranslatePayload([]byte(`{"Data":{"Url":"https://cdn.example.com/out.png","InPaintingUrl":"https://cdn.example.com/inpaint.png","TemplateJson":"{\"width\":800,\"children\":[]}"}}`))
	if err != nil || !hasPascal || parsedPascal.finalImageURL != "https://cdn.example.com/out.png" || parsedPascal.inPaintingURL != "https://cdn.example.com/inpaint.png" || parsedPascal.scene == nil {
		t.Fatalf("expected PascalCase Data payload to parse, got parsed=%+v has=%v err=%v", parsedPascal, hasPascal, err)
	}
}

func TestOpenAIImagesEditTaskPollingAndFallback(t *testing.T) {
	origPoll := imageEditTaskPollIntervalOverride
	imageEditTaskPollIntervalOverride = 5 * time.Millisecond
	t.Cleanup(func() { imageEditTaskPollIntervalOverride = origPoll })

	var seenIdem, seenPromptKind string
	var seenSyncIdem, seenSyncPromptKind string
	var pollCount int32
	var tasksUnsupported atomic.Bool
	var syncEditCalled int32

	baseOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/edits/tasks":
			if tasksUnsupported.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
				return
			}
			seenIdem = r.Header.Get("Idempotency-Key")
			seenPromptKind = r.Header.Get("X-Prompt-Kind")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "task_repaint_123",
				"status": "pending",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task_repaint_123":
			n := atomic.AddInt32(&pollCount, 1)
			if n < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "task_repaint_123", "status": "running"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "task_repaint_123",
				"status": "succeeded",
				"result": map[string]any{
					"data": []map[string]any{
						{"b64_json": base64.StdEncoding.EncodeToString(tinyTestPNG)},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/edits":
			atomic.AddInt32(&syncEditCalled, 1)
			seenSyncIdem = r.Header.Get("Idempotency-Key")
			seenSyncPromptKind = r.Header.Get("X-Prompt-Kind")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"b64_json": base64.StdEncoding.EncodeToString(tinyTestPNG)},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	hoxiHome := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	wsDir := t.TempDir()
	doc := map[string]any{
		"provider": map[string]any{
			"ainode-local": map[string]any{
				"name": "Local AINode",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": baseOrigin + "/v1",
					"apiKey":  "sk-edit-key",
				},
				"models": map[string]any{
					"gpt-image-1": map[string]any{
						"name":           "GPT Image 1",
						"capabilityTags": []string{"image_generate", "image_edit"},
					},
				},
			},
		},
	}
	rawDoc, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(hoxiHome, "models.json"), rawDoc, 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", wsDir, nil)
	resp, err := svc.GenerateImage(context.Background(), llm.ImageRequest{
		ProviderID:     "ainode-local",
		ModelID:        "gpt-image-1",
		Prompt:         "remove artifact",
		AspectRatio:    "1:1",
		Count:          1,
		InputImages:    []llm.ImageInput{{Mime: "image/png", Data: tinyTestPNG}},
		MaskImage:      &llm.ImageInput{Mime: "image/png", Data: tinyTestPNG},
		IdempotencyKey: "req_repaint_42",
		PromptKind:     "image_inpaint",
	})
	if err != nil {
		t.Fatalf("GenerateImage async task failed: %v", err)
	}
	if seenIdem != "req_repaint_42" || seenPromptKind != "image_inpaint" {
		t.Fatalf("expected Idempotency-Key & X-Prompt-Kind headers, got idem=%q kind=%q", seenIdem, seenPromptKind)
	}
	if len(resp.Images) != 1 || atomic.LoadInt32(&pollCount) != 2 {
		t.Fatalf("expected 1 image after 2 polls, got imgs=%d polls=%d", len(resp.Images), atomic.LoadInt32(&pollCount))
	}

	// Fallback cleanly to synchronous /v1/images/edits when /v1/images/edits/tasks returns 404
	tasksUnsupported.Store(true)
	resp2, err := svc.GenerateImage(context.Background(), llm.ImageRequest{
		ProviderID:     "ainode-local",
		ModelID:        "gpt-image-1",
		Prompt:         "remove artifact",
		AspectRatio:    "1:1",
		Count:          1,
		InputImages:    []llm.ImageInput{{Mime: "image/png", Data: tinyTestPNG}},
		MaskImage:      &llm.ImageInput{Mime: "image/png", Data: tinyTestPNG},
		IdempotencyKey: "req_repaint_43",
		PromptKind:     "image_inpaint",
	})
	if err != nil || len(resp2.Images) != 1 || atomic.LoadInt32(&syncEditCalled) != 1 {
		t.Fatalf("expected fallback to /v1/images/edits, got err=%v imgs=%d syncCalls=%d", err, len(resp2.Images), atomic.LoadInt32(&syncEditCalled))
	}
	if seenSyncIdem != "req_repaint_43" || seenSyncPromptKind != "image_inpaint" {
		t.Fatalf("expected sync /v1/images/edits to forward Idempotency-Key & X-Prompt-Kind, got idem=%q kind=%q", seenSyncIdem, seenSyncPromptKind)
	}

	// Bare baseURL without /v1 also normalizes to /v1/images/edits on sync fallback
	docBare := map[string]any{
		"provider": map[string]any{
			"ainode-bare": map[string]any{
				"name": "Bare AINode",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": baseOrigin,
					"apiKey":  "sk-edit-key",
				},
				"mediaModels": map[string]any{
					"gpt-image-1": map[string]any{
						"name":           "GPT Image 1",
						"capabilityTags": []string{"image_generate", "image_edit"},
					},
				},
			},
		},
	}
	rawBare, _ := json.Marshal(docBare)
	if err := os.WriteFile(filepath.Join(hoxiHome, "models.json"), rawBare, 0o600); err != nil {
		t.Fatal(err)
	}
	resp3, err := svc.GenerateImage(context.Background(), llm.ImageRequest{
		ProviderID:     "ainode-bare",
		ModelID:        "gpt-image-1",
		Prompt:         "remove artifact",
		InputImages:    []llm.ImageInput{{Mime: "image/png", Data: tinyTestPNG}},
		MaskImage:      &llm.ImageInput{Mime: "image/png", Data: tinyTestPNG},
		IdempotencyKey: "req_repaint_44",
		PromptKind:     "image_inpaint",
	})
	if err != nil || len(resp3.Images) != 1 || atomic.LoadInt32(&syncEditCalled) != 2 {
		t.Fatalf("expected bare baseURL sync /v1/images/edits fallback to succeed, err=%v imgs=%d syncCalls=%d", err, len(resp3.Images), atomic.LoadInt32(&syncEditCalled))
	}
}

func TestProbeNonChatTranslateAndEmbeddingModelsWithoutCallingChat(t *testing.T) {
	var chatCalls int32
	var v1ModelsCalls int32
	baseOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			atomic.AddInt32(&v1ModelsCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "alimt-image-translate", "capabilities": []string{"image_translate"}},
				},
			})
		case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/chat/completions":
			atomic.AddInt32(&chatCalls, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unsupported model for chat"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	svc := NewService(0, "", t.TempDir(), nil)
	for _, mid := range []string{"alimt-image-translate", "qwen-mt-image", "text-embedding-3-small", "bge-reranker-v2-m3"} {
		err := svc.probeVideoModel(context.Background(), imageGenConn{
			ProviderID: "ainode",
			ModelID:    mid,
			BaseURL:    baseOrigin, // bare root without /v1
			APIKey:     "sk-probe",
			NPM:        "@ai-sdk/openai-compatible",
		})
		if err != nil {
			t.Fatalf("probeVideoModel(%s) on bare baseURL failed: %v", mid, err)
		}
		cat := inferModelCategory(mid, "", "")
		if cat != "image_translate" && cat != "embedding" && cat != "rerank" {
			t.Fatalf("unexpected category %q for %s", cat, mid)
		}
	}
	if atomic.LoadInt32(&chatCalls) != 0 {
		t.Fatalf("non-chat probes must never hit /chat/completions, got %d calls", atomic.LoadInt32(&chatCalls))
	}
	if atomic.LoadInt32(&v1ModelsCalls) < 4 {
		t.Fatalf("expected /v1/models retry for bare baseURL, got %d calls", atomic.LoadInt32(&v1ModelsCalls))
	}
}
