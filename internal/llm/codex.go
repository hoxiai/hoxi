package llm

import (
	"encoding/json"
	"net/url"
	"strings"
)

// DefaultCodexInstructions is the fallback system prompt required by ChatGPT's
// /backend-api/codex Responses endpoint when no explicit instructions are set.
const DefaultCodexInstructions = "You are a helpful coding assistant."

var codexForbiddenPayloadFields = []string{
	"max_output_tokens",
	"max_tokens",
	"max_completion_tokens",
	"temperature",
	"top_p",
	"user",
	"metadata",
	"logprobs",
	"top_logprobs",
}

// IsCodexBackendURL reports whether rawURL targets a ChatGPT /backend-api/codex endpoint.
func IsCodexBackendURL(rawURL string) bool {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	p := strings.TrimRight(strings.ToLower(u.Path), "/")
	return p == "/backend-api/codex" ||
		p == "/backend-api/codex/responses" ||
		p == "/backend-api/codex/models"
}

// BuildCodexTextMessage formats a single plain-text message item for /backend-api/codex,
// using "output_text" for assistant turns and "input_text" for user/developer turns.
func BuildCodexTextMessage(role, text string) map[string]any {
	blockType := "input_text"
	if role == "assistant" {
		blockType = "output_text"
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": blockType,
			"text": text,
		}},
	}
}

func normalizeCodexMessageItemMap(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	role, _ := item["role"].(string)
	if role == "" {
		return item
	}
	if textContent, isStr := item["content"].(string); isStr {
		return BuildCodexTextMessage(role, textContent)
	}
	if _, hasType := item["type"]; !hasType {
		item["type"] = "message"
	}
	return item
}

// NormalizeCodexResponsesMap mutates an OpenAI Responses payload map in place
// so it conforms to ChatGPT's /backend-api/codex schema constraints.
func NormalizeCodexResponsesMap(payload map[string]any) {
	if payload == nil {
		return
	}
	payload["store"] = false
	payload["stream"] = true

	instr, _ := payload["instructions"].(string)
	if strings.TrimSpace(instr) == "" {
		payload["instructions"] = DefaultCodexInstructions
	}

	switch inp := payload["input"].(type) {
	case string:
		payload["input"] = []map[string]any{BuildCodexTextMessage("user", inp)}
	case []map[string]any:
		for i, item := range inp {
			inp[i] = normalizeCodexMessageItemMap(item)
		}
	case []any:
		for i, rawItem := range inp {
			if item, ok := rawItem.(map[string]any); ok {
				inp[i] = normalizeCodexMessageItemMap(item)
			}
		}
	}

	for _, forbidden := range codexForbiddenPayloadFields {
		delete(payload, forbidden)
	}
}

// NormalizeCodexResponsesPayload normalizes a serialized Responses JSON body
// for /backend-api/codex, returning raw unchanged if it is not a JSON object.
func NormalizeCodexResponsesPayload(raw []byte) []byte {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return raw
	}
	NormalizeCodexResponsesMap(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return encoded
}
