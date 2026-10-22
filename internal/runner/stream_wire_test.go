package runner

import (
	"encoding/json"
	"testing"
)

func TestStreamedPartsKeepEmptyTextOnWire(t *testing.T) {
	t.Parallel()
	for _, runtime := range []string{string(RuntimeClaudeCode), string(RuntimeAntigravity)} {
		for _, partType := range []string{"text", "reasoning"} {
			t.Run(runtime+"/"+partType, func(t *testing.T) {
				tr := NewTurnTranslator("session", runtime, "message", "user", runtime, "model", "plan", "")
				line := `{"event":"step_update","step_update":{"state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"hello"}}`
				if partType == "reasoning" {
					line = `{"event":"step_update","step_update":{"state":"ACTIVE","step_index":1,"step_type":"agent_response","thinking_delta":"hello"}}`
				}
				if runtime == string(RuntimeClaudeCode) {
					delta := `{"type":"text_delta","text":"hello"}`
					if partType == "reasoning" {
						delta = `{"type":"thinking_delta","thinking":"hello"}`
					}
					line = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":` + delta + `}}`
				}
				events := tr.FeedLine(line)
				if len(events) != 2 || events[0].Type != "message.part.updated" || events[1].Type != "message.part.delta" {
					t.Fatalf("expected initial part followed by a delta, got %+v", events)
				}
				raw, err := json.Marshal(events[0])
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Properties struct {
						Part map[string]interface{} `json:"part"`
					} `json:"properties"`
				}
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				text, exists := wire.Properties.Part["text"]
				if !exists || text != "" {
					t.Fatalf("empty %s text must survive SSE JSON serialization: %s", partType, raw)
				}
				if tr.IsTurnFinished() {
					t.Fatal("initial text must be delivered before completion")
				}
			})
		}
	}
}

func TestNonTextPartsStillOmitEmptyText(t *testing.T) {
	t.Parallel()
	for _, partType := range []string{"tool", "file"} {
		raw, err := json.Marshal(MessagePart{ID: "part", Type: partType})
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]interface{}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if _, exists := wire["text"]; exists {
			t.Fatalf("non-text part acquired a text field: %s", raw)
		}
	}
}
