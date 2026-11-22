package llm

import (
	"encoding/json"
	"testing"
)

func TestCodexBackendURLAndPayloadNormalization(t *testing.T) {
	if !IsCodexBackendURL("https://chatgpt.com/backend-api/codex") ||
		!IsCodexBackendURL("https://chatgpt.com/backend-api/codex/responses/") ||
		!IsCodexBackendURL("https://chatgpt.com/backend-api/codex/models") {
		t.Fatal("expected /backend-api/codex URLs to match")
	}
	if IsCodexBackendURL("https://api.openai.com/v1") || IsCodexBackendURL("") {
		t.Fatal("expected standard /v1 URLs not to match Codex backend")
	}

	raw := []byte(`{"model":"gpt-5.3-codex","input":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}],"max_output_tokens":1024,"temperature":0.2}`)
	out := NormalizeCodexResponsesPayload(raw)
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal normalized payload: %v", err)
	}
	if decoded["instructions"] != DefaultCodexInstructions {
		t.Fatalf("expected default instructions %q, got %#v", DefaultCodexInstructions, decoded["instructions"])
	}
	if _, exists := decoded["max_output_tokens"]; exists {
		t.Fatalf("expected max_output_tokens to be stripped: %#v", decoded)
	}
	if _, exists := decoded["temperature"]; exists {
		t.Fatalf("expected temperature to be stripped: %#v", decoded)
	}
	items, ok := decoded["input"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 normalized input items, got %#v", decoded["input"])
	}
	asst := items[1].(map[string]any)
	blocks := asst["content"].([]any)
	if asst["type"] != "message" || blocks[0].(map[string]any)["type"] != "output_text" {
		t.Fatalf("expected assistant item to use message + output_text, got %#v", asst)
	}
}
