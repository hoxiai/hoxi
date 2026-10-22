package provider

import "testing"

func TestCanonicalModelIDStripsVariantAndChannelLabels(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"deepseek-v4.1-flash-solo", "deepseek-v4-1-flash"},
		{"DeepSeek-V4.1-Flash", "deepseek-v4-1-flash"},
		{"deepseek-v4.1-flash", "deepseek-v4-1-flash"},
		{"deepseek-v4-1-flash-free", "deepseek-v4-1-flash"},
		{"grok-4.7-build-fast", "grok-4-7"},
		{"gpt-6.1-sol", "gpt-6-1-sol"},
		{"claude-opus-5-5", "claude-opus-5-5"},
		{"anthropic/claude-sonnet-5-5", "claude-sonnet-5-5"},
		{"models/deepseek-v4.1-flash-1017", "deepseek-v4-1-flash"},
		{"deepseek-v4.1-flash-1017-free", "deepseek-v4-1-flash"},
		{"claude-sonnet-4-5-20250929-thinking", "claude-sonnet-4-5"},
		{"deepseek-v4.1-flash:thinking", "deepseek-v4-1-flash"},
		{"kimi-k3 (2026-10-22)", "kimi-k3"},
		{"", ""},
	} {
		if got := canonicalModelID(tc.in); got != tc.want {
			t.Fatalf("canonicalModelID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKnownModelTokenLimitFamilies(t *testing.T) {
	for _, tc := range []struct {
		model   string
		context int
	}{
		{"deepseek-v4.1-flash-solo", 1_048_576},
		{"deepseek-v4-pro", 1_048_576},
		{"deepseek-v3.2", 163_840},
		{"claude-opus-5-5", 1_000_000},
		{"claude-sonnet-4-6", 200_000},
		{"claude-sonnet-4-5-20250929-thinking", 200_000},
		{"claude-haiku-4-5", 200_000},
		{"gpt-6.1-sol", 1_050_000},
		{"gpt-6-astra", 1_050_000},
		{"gpt-5.6", 1_050_000},
		{"gpt-5.5", 1_000_000},
		{"gpt-4.6", 400_000},
		{"gemini-3.7-flash", 1_048_576},
		{"glm-5.3-flash-solo", 1_000_000},
		{"glm-4.6", 200_000},
		{"kimi-k3", 1_048_576},
		{"kimi-k2", 131_072},
		{"mimo-v2.6-pro-solo", 1_048_576},
		{"step-5-preview-solo", 1_000_000},
		{"minimax-m3", 1_000_000},
		{"grok-4.7", 500_000},
	} {
		limit, ok := knownModelTokenLimit(canonicalModelID(tc.model))
		if !ok {
			t.Fatalf("%s: no known window", tc.model)
		}
		if limit.Context != tc.context {
			t.Fatalf("%s: context = %d, want %d", tc.model, limit.Context, tc.context)
		}
		if limit.Output <= 0 || limit.Output >= limit.Context {
			t.Fatalf("%s: implausible output window %d", tc.model, limit.Output)
		}
	}
}

func TestKnownModelTokenLimitLeavesUnknownFamiliesAlone(t *testing.T) {
	for _, model := range []string{"custom-local", "internal-thing", ""} {
		if _, ok := knownModelTokenLimit(canonicalModelID(model)); ok {
			t.Fatalf("%s should not have an inferred window", model)
		}
	}
}

func TestEnsureModelTokenLimitFillsMissingWindow(t *testing.T) {
	model := map[string]interface{}{"name": "DeepSeek-V4.1-Flash"}
	ensureModelTokenLimit(model, "deepseek-v4.1-flash-solo")
	limit, ok := model["limit"].(map[string]interface{})
	if !ok {
		t.Fatalf("limit not written: %+v", model)
	}
	if positiveNumber(limit["context"]) != 1_048_576 {
		t.Fatalf("context = %v", limit["context"])
	}
	if positiveNumber(limit["output"]) != 384_000 {
		t.Fatalf("output = %v", limit["output"])
	}
}

func TestEnsureModelTokenLimitKeepsDeclaredWindow(t *testing.T) {
	model := map[string]interface{}{
		"limit": map[string]interface{}{"context": 32_768, "output": 4_096},
	}
	ensureModelTokenLimit(model, "deepseek-v4.1-flash-solo")
	limit := model["limit"].(map[string]interface{})
	if positiveNumber(limit["context"]) != 32_768 {
		t.Fatalf("declared context overwritten: %v", limit["context"])
	}
	if positiveNumber(limit["output"]) != 4_096 {
		t.Fatalf("declared output overwritten: %v", limit["output"])
	}
}

func TestEnsureModelTokenLimitLeavesRoomForConversation(t *testing.T) {
	// A catalog entry whose output equals its context would make OpenCode
	// compact on every turn (threshold = context - output = 0).
	model := map[string]interface{}{
		"limit": map[string]interface{}{"context": 128_000, "output": 128_000},
	}
	ensureModelTokenLimit(model, "deepseek-v4.1-flash-solo")
	limit := model["limit"].(map[string]interface{})
	context := positiveNumber(limit["context"])
	output := positiveNumber(limit["output"])
	if output >= context {
		t.Fatalf("output %d must stay below context %d", output, context)
	}
	if output < 8192 {
		t.Fatalf("output %d must keep at least a minimal generation budget", output)
	}

	// Small context models (e.g. context: 4096) must clamp output below context
	// even when knownModelTokenLimit returns false or when 8192 >= context.
	for _, id := range []string{"deepseek-v4.1-flash-solo", "custom-small-local"} {
		small := map[string]interface{}{
			"limit": map[string]interface{}{"context": 4096, "output": 4096},
		}
		ensureModelTokenLimit(small, id)
		sLim := small["limit"].(map[string]interface{})
		sCtx := positiveNumber(sLim["context"])
		sOut := positiveNumber(sLim["output"])
		if sCtx != 4096 || sOut <= 0 || sOut >= sCtx {
			t.Fatalf("%s: small context %d output %d must stay below context", id, sCtx, sOut)
		}
	}
}

func TestNormalizeDocModelVariantsFillsTokenWindows(t *testing.T) {
	doc := map[string]interface{}{
		"provider": map[string]interface{}{
			"subapi": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"models": map[string]interface{}{
					"deepseek-v4.1-flash-solo": map[string]interface{}{"name": "DeepSeek-V4.1-Flash"},
					"mystery-model-9":          map[string]interface{}{"name": "Mystery"},
					"embed-3":                  map[string]interface{}{"name": "Embed"},
				},
			},
		},
	}
	normalizeDocModelVariants(doc)
	models := doc["provider"].(map[string]interface{})["subapi"].(map[string]interface{})["models"].(map[string]interface{})

	limit := models["deepseek-v4.1-flash-solo"].(map[string]interface{})["limit"].(map[string]interface{})
	if positiveNumber(limit["context"]) != 1_048_576 {
		t.Fatalf("deepseek window not filled: %v", limit)
	}
	if _, has := models["mystery-model-9"].(map[string]interface{})["limit"]; has {
		t.Fatal("unknown family must not receive an invented window")
	}
	if _, has := models["embed-3"].(map[string]interface{})["limit"]; has {
		t.Fatal("utility models must not receive a chat window")
	}
}

func TestEnsureCompactionDefaultsKeepsUserChoice(t *testing.T) {
	doc := map[string]interface{}{}
	ensureCompactionDefaults(doc)
	compaction, _ := doc["compaction"].(map[string]interface{})
	if compaction["auto"] != true {
		t.Fatalf("auto compaction not enabled by default: %+v", doc["compaction"])
	}

	disabled := map[string]interface{}{"compaction": map[string]interface{}{"auto": false}}
	ensureCompactionDefaults(disabled)
	if disabled["compaction"].(map[string]interface{})["auto"] != false {
		t.Fatal("user-disabled compaction must stay disabled")
	}

	custom := map[string]interface{}{"compaction": map[string]interface{}{"tail_turns": 3}}
	ensureCompactionDefaults(custom)
	compaction = custom["compaction"].(map[string]interface{})
	if compaction["auto"] != true {
		t.Fatal("auto should be filled in when only tail_turns is set")
	}
	if compaction["tail_turns"] != 3 {
		t.Fatal("existing compaction settings must survive")
	}
}

func TestKnownModelTokenLimitHandlesVendorPrefixedIDs(t *testing.T) {
	for _, tc := range []struct {
		model   string
		context int
	}{
		{"OfficeAce-deepseek-v4.1-flash", 1_048_576},
		{"OfficeAce-gpt-6-astra", 1_050_000},
		{"OfficeAce-claude-opus-5-5", 1_000_000},
		{"OfficeAce-claude-sonnet-4-6", 200_000},
		{"Loomy-DeepSeek-V4.1-Flash", 1_048_576},
		{"QoderCN-DeepSeek-Flash", 1_048_576},
		{"kukuAI-deepseek-v4.1-flash-", 1_048_576},
		{"Trae-deepseek-v4.1-flash", 1_048_576},
	} {
		limit, ok := knownModelTokenLimit(canonicalModelID(tc.model))
		if !ok {
			t.Fatalf("%s: no known window", tc.model)
		}
		if limit.Context != tc.context {
			t.Fatalf("%s: context = %d, want %d", tc.model, limit.Context, tc.context)
		}
	}
}

func TestKnownModelTokenLimitDoesNotMatchUnrelatedSubstrings(t *testing.T) {
	// `xstep` and `glmmodel` only look like known families; a substring match
	// would have invented a window for an unrelated deployment.
	for _, model := range []string{"xstep-9", "internal-glmmodel"} {
		if limit, ok := knownModelTokenLimit(canonicalModelID(model)); ok {
			t.Fatalf("%s matched a family by substring: %+v", model, limit)
		}
	}
	// A bare token still matches its family.
	if limit, ok := knownModelTokenLimit(canonicalModelID("step-5-preview")); !ok || limit.Context != 1_000_000 {
		t.Fatalf("step-5-preview: %+v ok=%v", limit, ok)
	}
}

func TestEnsureModelTokenLimitUsesDisplayNameForOpaqueIDs(t *testing.T) {
	model := map[string]interface{}{"name": "DeepSeek-V4.1-Flash"}
	ensureModelTokenLimit(model, "Qoder-DeepSeek-Flash")
	limit, _ := model["limit"].(map[string]interface{})
	if positiveNumber(limit["context"]) != 1_048_576 {
		t.Fatalf("limit not filled from id: %+v", model)
	}

	opaque := map[string]interface{}{"name": "Claude Opus 5.5"}
	ensureModelTokenLimit(opaque, "internal-deployment-a")
	limit, _ = opaque["limit"].(map[string]interface{})
	if positiveNumber(limit["context"]) != 1_000_000 {
		t.Fatalf("limit not filled from display name: %+v", opaque)
	}
}
