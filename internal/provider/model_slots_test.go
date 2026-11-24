package provider

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestParseOpenAIModelsPayloadCapabilitiesAndLegacyModality(t *testing.T) {
	raw := []byte(`{
		"object": "list",
		"data": [
			{
				"id": "alimt-image-translate",
				"name": "AliMT Image Translate",
				"capabilities": ["image_translate"],
				"image_translate": { "output_modes": ["raster", "editable_scene"] }
			},
			{
				"id": "qwen-mt-image",
				"capabilities": ["image_translate"],
				"image_translate": { "output_modes": ["raster"] }
			},
			{
				"id": "legacy-editor-trans",
				"modality": "translate_image"
			},
			{
				"id": "legacy-batch-trans",
				"modality": "translate_image_task"
			},
			{
				"id": "deepseek-chat",
				"modality": "text"
			},
			{
				"id": "qwen-vl-max",
				"modality": "vision"
			},
			{
				"id": "gpt-image-1",
				"modality": "image"
			},
			{
				"id": "flux-schnell",
				"modality": "image"
			},
			{
				"id": "collector-web",
				"modality": "crawl"
			},
			{
				"id": "codex-auto-review"
			}
		]
	}`)

	items := parseOpenAIModelsPayload(raw)
	byID := map[string]fetchedModelItem{}
	for _, it := range items {
		byID[it.ID] = it
	}

	hasSlot := func(id string, target llm.ModelSlot) bool {
		for _, s := range byID[id].Slots {
			if s == target {
				return true
			}
		}
		return false
	}

	if !hasSlot("alimt-image-translate", llm.ModelSlotImageTranslateEditor) || !hasSlot("alimt-image-translate", llm.ModelSlotImageTranslateRaster) {
		t.Fatalf("expected alimt-image-translate in editor & raster slots, got %+v", byID["alimt-image-translate"])
	}
	if !hasSlot("qwen-mt-image", llm.ModelSlotImageTranslateRaster) || hasSlot("qwen-mt-image", llm.ModelSlotImageTranslateEditor) {
		t.Fatalf("qwen-mt-image must match raster only, got %+v", byID["qwen-mt-image"])
	}
	if !hasSlot("legacy-editor-trans", llm.ModelSlotImageTranslateEditor) {
		t.Fatalf("legacy translate_image must match image_translate_editor, got %+v", byID["legacy-editor-trans"])
	}
	if !hasSlot("legacy-batch-trans", llm.ModelSlotImageTranslateRaster) || hasSlot("legacy-batch-trans", llm.ModelSlotImageTranslateEditor) {
		t.Fatalf("legacy translate_image_task must match raster only, got %+v", byID["legacy-batch-trans"])
	}
	if hasSlot("deepseek-chat", llm.ModelSlotVision) || !hasSlot("deepseek-chat", llm.ModelSlotChat) {
		t.Fatalf("pure-text deepseek-chat must not enter vision slot: %+v", byID["deepseek-chat"])
	}
	if !hasSlot("qwen-vl-max", llm.ModelSlotVision) || !hasSlot("qwen-vl-max", llm.ModelSlotChat) {
		t.Fatalf("vision model must match vision & chat: %+v", byID["qwen-vl-max"])
	}
	if !hasSlot("gpt-image-1", llm.ModelSlotImageEdit) || hasSlot("gpt-image-1", llm.ModelSlotImageTranslateEditor) {
		t.Fatalf("gpt-image-1 must match image_edit and NOT image_translate_editor: %+v", byID["gpt-image-1"])
	}
	if !hasSlot("flux-schnell", llm.ModelSlotImageGenerate) || hasSlot("flux-schnell", llm.ModelSlotImageEdit) {
		t.Fatalf("flux-schnell must match image_generate only: %+v", byID["flux-schnell"])
	}
	if len(byID["collector-web"].Slots) != 0 || len(byID["collector-web"].Capabilities) != 0 {
		t.Fatalf("crawl/collector model must have empty slots & capabilities: %+v", byID["collector-web"])
	}
	if len(byID["codex-auto-review"].Slots) != 0 || len(byID["codex-auto-review"].Capabilities) != 0 {
		t.Fatalf("codex-auto-review must have empty slots & capabilities: %+v", byID["codex-auto-review"])
	}
}

func TestModelSlotsFilteringAndLegacyImageModelFallback(t *testing.T) {
	hoxiHome := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	wsDir := t.TempDir()

	modelsDoc := map[string]any{
		"provider": map[string]any{
			"gw": map[string]any{
				"name": "Local Gateway",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": "http://127.0.0.1:8787/v1",
					"apiKey":  "sk-test",
				},
				"models": map[string]any{
					"deepseek-chat": map[string]any{
						"name":           "DeepSeek Chat",
						"modality":       "text",
						"capabilityTags": []string{"chat", "text_translate"},
					},
					"gpt-4o": map[string]any{
						"name":           "GPT-4o",
						"modality":       "vision",
						"capabilityTags": []string{"chat", "vision", "text_translate"},
					},
					"flux-schnell": map[string]any{
						"name":           "Flux Schnell",
						"modality":       "image",
						"capabilityTags": []string{"image_generate"},
					},
					"gpt-image-1": map[string]any{
						"name":           "GPT Image 1",
						"modality":       "image",
						"capabilityTags": []string{"image_generate", "image_edit"},
					},
					"qwen-mt-image": map[string]any{
						"name":           "Qwen MT Image",
						"capabilityTags": []string{"image_translate"},
						"imageTranslate": map[string]any{"outputModes": []string{"raster"}},
					},
					"alimt-image-translate": map[string]any{
						"name":           "AliMT Image Translate",
						"capabilityTags": []string{"image_translate"},
						"imageTranslate": map[string]any{"outputModes": []string{"raster", "editable_scene"}},
					},
					"collector-catalog": map[string]any{
						"name":     "Collector",
						"modality": "crawl",
					},
					"edit-only-inpaint": map[string]any{
						"name":           "Edit Only",
						"capabilityTags": []string{"image_edit"},
					},
					"custom-vec-v2": map[string]any{
						"name":           "Custom Vector",
						"capabilityTags": []string{"embedding"},
					},
					"custom-voice-v2": map[string]any{
						"name":           "Custom Voice",
						"capabilityTags": []string{"tts"},
					},
					"custom-clip-v2": map[string]any{
						"name":           "Custom Clip",
						"capabilityTags": []string{"video"},
					},
				},
			},
		},
	}
	rawDoc, _ := json.Marshal(modelsDoc)
	if err := os.WriteFile(filepath.Join(hoxiHome, "models.json"), rawDoc, 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", wsDir, nil)
	slots := svc.ListAllModelSlots()

	// Vision must only contain gpt-4o (never deepseek-chat or collector-catalog)
	if len(slots[llm.ModelSlotVision]) != 1 || slots[llm.ModelSlotVision][0].ModelID != "gpt-4o" {
		t.Fatalf("expected only gpt-4o in vision slot, got %+v", slots[llm.ModelSlotVision])
	}
	// image_translate_editor must ONLY contain alimt-image-translate (not qwen-mt-image, not gpt-image-1)
	if len(slots[llm.ModelSlotImageTranslateEditor]) != 1 || slots[llm.ModelSlotImageTranslateEditor][0].ModelID != "alimt-image-translate" {
		t.Fatalf("expected only alimt-image-translate in image_translate_editor, got %+v", slots[llm.ModelSlotImageTranslateEditor])
	}
	// image_edit must contain gpt-image-1 and edit-only-inpaint (not flux-schnell)
	if len(slots[llm.ModelSlotImageEdit]) != 2 {
		t.Fatalf("expected 2 models in image_edit, got %+v", slots[llm.ModelSlotImageEdit])
	}
	// ListImageModels (image_generate) must NOT leak edit-only-inpaint
	for _, imgOpt := range svc.ListImageModels() {
		if imgOpt.ModelID == "edit-only-inpaint" {
			t.Fatalf("edit-only model leaked into ListImageModels()")
		}
	}
	// Single-source-of-truth capabilityTags recognition across video, tts, embedding
	if !svc.HasEmbeddingModel() {
		t.Fatalf("expected HasEmbeddingModel() to recognize capabilityTags=[embedding]")
	}
	if len(svc.ListSpeechModels()) != 1 || svc.ListSpeechModels()[0].ModelID != "custom-voice-v2" {
		t.Fatalf("expected ListSpeechModels() to recognize capabilityTags=[tts], got %+v", svc.ListSpeechModels())
	}
	if len(svc.ListVideoModels()) != 1 || svc.ListVideoModels()[0].ModelID != "custom-clip-v2" {
		t.Fatalf("expected ListVideoModels() to recognize capabilityTags=[video], got %+v", svc.ListVideoModels())
	}
	// Explicit configured incompatible model must be rejected by ResolveModelForSlot
	if _, _, _, ok := svc.ResolveModelForSlot(llm.ModelSlotImageTranslateEditor, "gw", "deepseek-chat"); ok {
		t.Fatalf("expected explicit incompatible configured model deepseek-chat to be rejected for image_translate_editor")
	}
	// normalizeDocModelVariants must NOT inject attachment=true onto explicit text-only capabilityTags
	normDoc := map[string]any{
		"provider": map[string]any{
			"gw": map[string]any{
				"options": map[string]any{"baseURL": "http://127.0.0.1:9999/v1"},
				"models": map[string]any{
					"plain-chat": map[string]any{
						"name":           "Plain Chat",
						"capabilityTags": []string{"chat", "text_translate"},
					},
				},
			},
		},
	}
	normalizeDocModelVariants(normDoc)
	plainEntry := normDoc["provider"].(map[string]any)["gw"].(map[string]any)["models"].(map[string]any)["plain-chat"].(map[string]any)
	if attach, _ := plainEntry["attachment"].(bool); attach {
		t.Fatalf("expected normalizeDocModelVariants not to inject attachment=true onto text-only capabilityTags model, got %+v", plainEntry)
	}
	// collector-catalog must appear in zero slots
	for slot, opts := range slots {
		for _, opt := range opts {
			if opt.ModelID == "collector-catalog" {
				t.Fatalf("excluded utility model leaked into slot %s", slot)
			}
		}
	}

	// Legacy imageModel pinned to generate-only flux-schnell must NOT hijack image_edit!
	genOnly := "gw/flux-schnell"
	if _, err := svc.UpdateMediaRolePrefs(map[string]*string{"imageModel": &genOnly}); err != nil {
		t.Fatalf("UpdateMediaRolePrefs failed: %v", err)
	}
	pid, mid, res, ok := svc.ResolveModelForSlot(llm.ModelSlotImageEdit, "", "")
	if !ok || pid != "gw" || mid != "gpt-image-1" || res != "configured_model" {
		t.Fatalf("expected image_edit to skip generate-only legacy imageModel and pick gpt-image-1, got %s/%s (%s, ok=%v)", pid, mid, res, ok)
	}

	// Legacy imageModel pinned to edit-capable gpt-image-1 smoothly maps to image_edit!
	editCapable := "gw/gpt-image-1"
	if _, err := svc.UpdateMediaRolePrefs(map[string]*string{"imageModel": &editCapable}); err != nil {
		t.Fatalf("UpdateMediaRolePrefs failed: %v", err)
	}
	pid, mid, res, ok = svc.ResolveModelForSlot(llm.ModelSlotImageEdit, "", "")
	if !ok || pid != "gw" || mid != "gpt-image-1" || res != "role_default_legacy" {
		t.Fatalf("expected capable legacy imageModel to map smoothly to image_edit, got %s/%s (%s, ok=%v)", pid, mid, res, ok)
	}

	// Verify /api/hoxi/provider/fetch-models returns normalized capabilities & slots
	upstreamOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":           "alimt-image-translate",
					"capabilities": []string{"image_translate"},
					"image_translate": map[string]any{
						"output_modes": []string{"raster", "editable_scene"},
					},
				},
			},
		})
	}))
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/fetch-models", map[string]any{
		"baseURL": upstreamOrigin + "/v1",
		"apiKey":  "sk-test",
	})
	var fetchRes fetchProviderModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fetchRes); err != nil {
		t.Fatalf("decode fetch-models: %v", err)
	}
	if len(fetchRes.Models) != 1 || fetchRes.Models[0].ImageTranslate == nil || len(fetchRes.Models[0].Slots) != 2 {
		t.Fatalf("expected enriched fetched model with image_translate slots, got %+v", fetchRes.Models)
	}

	// Verify bare baseURL (without /v1) retries /v1/models when /models returns 404
	bareOrigin := newMemPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "qwen-image-edit", "modality": "image"},
			},
		})
	}))
	bareRec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/fetch-models", map[string]any{
		"baseURL": bareOrigin,
		"apiKey":  "sk-test",
	})
	var bareRes fetchProviderModelsResponse
	if err := json.Unmarshal(bareRec.Body.Bytes(), &bareRes); err != nil {
		t.Fatalf("decode bare fetch-models: %v", err)
	}
	if len(bareRes.Models) != 1 || bareRes.Models[0].ID != "qwen-image-edit" {
		t.Fatalf("expected bare baseURL to retry /v1/models cleanly, got %+v", bareRes)
	}

	// Verify MCP OAuth parseSiteModelsPayload preserves capabilities, image_translate, and modality
	siteItems := parseSiteModelsPayload(map[string]any{
		"data": []any{
			map[string]any{
				"id":           "alimt-image-translate",
				"name":         "AliMT",
				"capabilities": []any{"image_translate"},
				"image_translate": map[string]any{
					"output_modes": []any{"raster", "editable_scene"},
				},
			},
			map[string]any{
				"id":       "deepseek-v3",
				"modality": "text",
			},
		},
	})
	if len(siteItems) != 2 || siteItems[0].ImageTranslate == nil || len(siteItems[0].ImageTranslate.OutputModes) != 2 {
		t.Fatalf("expected parseSiteModelsPayload to preserve image_translate capabilities, got %+v", siteItems)
	}
	if siteItems[1].Modality != "text" || len(siteItems[1].Capabilities) != 2 {
		t.Fatalf("expected parseSiteModelsPayload to preserve text modality without vision, got %+v", siteItems[1])
	}
}
