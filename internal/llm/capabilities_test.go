package llm

import (
	"slices"
	"testing"
)

func TestNormalizeModelCapabilitiesExplicitAndLegacy(t *testing.T) {
	// 1. Explicit standard capabilities from /v1/models
	explicitEditor := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:                           "alimt-image-translate",
		HasExplicitCapabilities:           true,
		ExplicitCapabilities:              []string{"image_translate"},
		ExplicitImageTranslateOutputModes: []string{"raster", "editable_scene"},
	})
	if !MatchesModelSlot(explicitEditor, ModelSlotImageTranslateEditor) {
		t.Fatalf("expected alimt-image-translate to match image_translate_editor, got %+v", explicitEditor)
	}
	if !MatchesModelSlot(explicitEditor, ModelSlotImageTranslateRaster) {
		t.Fatalf("expected alimt-image-translate to match image_translate_raster, got %+v", explicitEditor)
	}
	if MatchesModelSlot(explicitEditor, ModelSlotImageGenerate) || MatchesModelSlot(explicitEditor, ModelSlotImageEdit) {
		t.Fatalf("image_translate model must not match image_generate/image_edit: %+v", explicitEditor)
	}

	// 2. Explicit raster-only image_translate model must NOT match image_translate_editor
	rasterOnly := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:                           "qwen-mt-image",
		HasExplicitCapabilities:           true,
		ExplicitCapabilities:              []string{"image_translate"},
		ExplicitImageTranslateOutputModes: []string{"raster"},
	})
	if !MatchesModelSlot(rasterOnly, ModelSlotImageTranslateRaster) {
		t.Fatalf("expected qwen-mt-image to match image_translate_raster, got %+v", rasterOnly)
	}
	if MatchesModelSlot(rasterOnly, ModelSlotImageTranslateEditor) {
		t.Fatalf("raster-only qwen-mt-image must NOT match image_translate_editor: %+v", rasterOnly)
	}

	// 3. Legacy modality: translate_image vs translate_image_task
	legacyEditor := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:  "custom-img-trans",
		Modality: "translate_image",
	})
	if !MatchesModelSlot(legacyEditor, ModelSlotImageTranslateEditor) || !MatchesModelSlot(legacyEditor, ModelSlotImageTranslateRaster) {
		t.Fatalf("legacy modality=translate_image must match both raster & editor, got %+v", legacyEditor)
	}

	legacyRaster := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:  "qwen-mt-image",
		Modality: "translate_image_task",
	})
	if !MatchesModelSlot(legacyRaster, ModelSlotImageTranslateRaster) || MatchesModelSlot(legacyRaster, ModelSlotImageTranslateEditor) {
		t.Fatalf("legacy modality=translate_image_task must match raster only, got %+v", legacyRaster)
	}

	// 4. Pure-text models must NEVER enter vision slot
	legacyText := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:  "deepseek-chat",
		Modality: "text",
	})
	if !MatchesModelSlot(legacyText, ModelSlotChat) || !MatchesModelSlot(legacyText, ModelSlotTextTranslate) {
		t.Fatalf("expected text model in chat & text_translate, got %+v", legacyText)
	}
	if MatchesModelSlot(legacyText, ModelSlotVision) {
		t.Fatalf("pure-text model must NEVER match vision slot: %+v", legacyText)
	}

	pureTextNoModality := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID: "deepseek-reasoner",
	})
	if MatchesModelSlot(pureTextNoModality, ModelSlotVision) {
		t.Fatalf("deepseek-reasoner without vision metadata must NOT match vision slot: %+v", pureTextNoModality)
	}

	// 5. Legacy vision modality gets chat + vision + text_translate
	legacyVision := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:  "my-custom-vlm",
		Modality: "vision",
	})
	if !MatchesModelSlot(legacyVision, ModelSlotChat) ||
		!MatchesModelSlot(legacyVision, ModelSlotVision) ||
		!MatchesModelSlot(legacyVision, ModelSlotTextTranslate) {
		t.Fatalf("modality=vision must match chat/vision/text_translate, got %+v", legacyVision)
	}

	// 6. Image generation models: edit-capable vs generate-only, NEVER image_translate_editor
	imgEdit := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:  "gpt-image-1",
		Modality: "image",
	})
	if !MatchesModelSlot(imgEdit, ModelSlotImageGenerate) || !MatchesModelSlot(imgEdit, ModelSlotImageEdit) {
		t.Fatalf("gpt-image-1 must match image_generate & image_edit, got %+v", imgEdit)
	}
	if MatchesModelSlot(imgEdit, ModelSlotImageTranslateEditor) {
		t.Fatalf("image generation model must NEVER match image_translate_editor: %+v", imgEdit)
	}

	imgGenOnly := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:  "flux-schnell",
		Modality: "image",
	})
	if !MatchesModelSlot(imgGenOnly, ModelSlotImageGenerate) {
		t.Fatalf("flux-schnell must match image_generate, got %+v", imgGenOnly)
	}
	if MatchesModelSlot(imgGenOnly, ModelSlotImageEdit) {
		t.Fatalf("generate-only flux-schnell must NOT match image_edit, got %+v", imgGenOnly)
	}

	for _, editID := range []string{"dall-e-2", "imagen-3.0", "qwen-image-edit", "flux-kontext-pro", "flux-fill-dev", "seedream-3.0", "custom-inpaint-v1", "my-model-edit"} {
		caps := NormalizeModelCapabilities(ModelCapabilityInput{ModelID: editID, Modality: "image"})
		if !MatchesModelSlot(caps, ModelSlotImageEdit) {
			t.Fatalf("expected %q to include image_edit, got %+v", editID, caps)
		}
	}

	// 7. Excluded utility/crawler models get [] capabilities and match ZERO slots
	for _, tc := range []ModelCapabilityInput{
		{ModelID: "web-scraper", Modality: "crawl"},
		{ModelID: "collector-1688"},
		{ModelID: "codex-auto-review"},
		{ModelID: "explicit-empty", HasExplicitCapabilities: true, ExplicitCapabilities: []string{}},
	} {
		caps := NormalizeModelCapabilities(tc)
		if len(caps.Capabilities) != 0 {
			t.Fatalf("expected empty capabilities for %+v, got %+v", tc, caps)
		}
		if slots := SlotsForCapabilities(caps); len(slots) != 0 {
			t.Fatalf("expected zero slots for %+v, got %v", tc, slots)
		}
	}

	// 8. Non-chat dedicated media/retrieval slots
	for _, tc := range []struct {
		id   string
		slot ModelSlot
	}{
		{"veo-3.0-generate", ModelSlotVideo},
		{"gpt-4o-mini-tts", ModelSlotTTS},
		{"whisper-large-v3", ModelSlotASR},
		{"text-embedding-3-large", ModelSlotEmbedding},
		{"bge-reranker-v2-m3", ModelSlotRerank},
	} {
		caps := NormalizeModelCapabilities(ModelCapabilityInput{ModelID: tc.id})
		if !MatchesModelSlot(caps, tc.slot) {
			t.Fatalf("expected %q to match slot %s, got %+v", tc.id, tc.slot, caps)
		}
		if MatchesModelSlot(caps, ModelSlotChat) {
			t.Fatalf("dedicated %q must not match chat slot: %+v", tc.id, caps)
		}
	}

	// 9. Explicit multi-capability model
	multi := NormalizeModelCapabilities(ModelCapabilityInput{
		ModelID:                 "omni-model",
		HasExplicitCapabilities: true,
		ExplicitCapabilities:    []string{"vision", "chat", "tts", "unknown_cap"},
	})
	want := []ModelCapability{CapabilityChat, CapabilityVision, CapabilityTTS}
	if !slices.Equal(multi.Capabilities, want) {
		t.Fatalf("unexpected canonical capabilities: got=%v want=%v", multi.Capabilities, want)
	}
}
