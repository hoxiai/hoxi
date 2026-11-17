package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

type anthropicDriver struct{}

func newAnthropicDriver() ModelDriver { return &anthropicDriver{} }

func (d *anthropicDriver) Protocol() llm.StreamProtocol { return llm.ProtocolAnthropic }

func (d *anthropicDriver) SystemPromptPatch() string {
	return "Prefer parallel read/glob/grep calls when gathering context. Keep edits minimal and verify with targeted checks."
}

func (d *anthropicDriver) BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	msgs := make([]map[string]any, 0, len(req.Messages))
	for i, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		blocks := make([]map[string]any, 0, len(m.Content))
		for j, b := range m.Content {
			var block map[string]any
			switch b.Type {
			case ContentText:
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				block = map[string]any{"type": "text", "text": b.Text}
			case ContentMedia:
				if b.Data == "" || b.MimeType == "" {
					continue
				}
				if strings.EqualFold(b.MimeType, "application/pdf") {
					block = map[string]any{
						"type": "document",
						"source": map[string]any{
							"type":       "base64",
							"media_type": "application/pdf",
							"data":       b.Data,
						},
					}
				} else if strings.HasPrefix(strings.ToLower(b.MimeType), "image/") {
					block = map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": b.MimeType,
							"data":       b.Data,
						},
					}
				}
			case ContentThinking:
				if b.Text == "" && b.Signature == "" {
					continue
				}
				// Anthropic requires a valid cryptographic signature to replay thinking blocks.
				if b.Signature == "" {
					continue
				}
				block = map[string]any{
					"type":      "thinking",
					"thinking":  b.Text,
					"signature": b.Signature,
				}
			case ContentToolUse:
				var parsedInput any = map[string]any{}
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &parsedInput)
				}
				block = map[string]any{
					"type":  "tool_use",
					"id":    b.ID,
					"name":  b.Name,
					"input": parsedInput,
				}
			case ContentToolResult:
				block = map[string]any{
					"type":        "tool_result",
					"tool_use_id": b.ToolUseID,
					"content":     b.Text,
				}
				if b.IsError {
					block["is_error"] = true
				}
			}
			if block == nil {
				continue
			}
			if i == len(req.Messages)-1 && j == len(m.Content)-1 && m.Role == RoleUser && b.Type != ContentThinking {
				block["cache_control"] = map[string]string{"type": "ephemeral"}
			}
			blocks = append(blocks, block)
		}
		if len(blocks) == 0 {
			continue
		}
		msgs = append(msgs, map[string]any{"role": string(m.Role), "content": blocks})
	}

	payload := map[string]any{
		"model":      req.Target.ModelID,
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   msgs,
	}
	if strings.TrimSpace(req.System) != "" {
		payload["system"] = []map[string]any{
			{"type": "text", "text": req.System, "cache_control": map[string]string{"type": "ephemeral"}},
		}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for i, t := range req.Tools {
			entry := map[string]any{
				"name": t.Name, "description": t.Description, "input_schema": t.Parameters,
			}
			if i == len(req.Tools)-1 {
				entry["cache_control"] = map[string]string{"type": "ephemeral"}
			}
			tools = append(tools, entry)
		}
		payload["tools"] = tools
	}
	thinking, effort, err := anthropicThinking(req, maxTokens)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	if thinking != nil {
		payload["thinking"] = thinking
	}
	if effort != "" {
		payload["output_config"] = map[string]string{"effort": effort}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	return llm.StreamHTTPRequest{Target: req.Target, Body: raw}, nil
}

func (d *anthropicDriver) ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	type pendingTool struct {
		id      string
		name    string
		input   strings.Builder
		stopped bool
	}
	toolsByIdx := make(map[int]*pendingTool)
	var usage TokenUsage
	var stopReason string
	var sawTerminal bool

	err := readSSEFrames(ctx, body, func(frame sseRawFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var raw map[string]any
		if json.Unmarshal([]byte(data), &raw) != nil {
			return nil
		}
		eventName := frame.Event
		if t, _ := raw["type"].(string); t != "" {
			eventName = t
		}
		switch eventName {
		case "error":
			errObj, _ := raw["error"].(map[string]any)
			msg, _ := errObj["message"].(string)
			if msg == "" {
				msg = data
			}
			return fmt.Errorf("anthropic stream error: %s", msg)
		case "message_start":
			if msg, ok := raw["message"].(map[string]any); ok {
				if u, ok := msg["usage"].(map[string]any); ok {
					usage.Input += intFromAny(u["input_tokens"])
					usage.CacheRead += intFromAny(u["cache_read_input_tokens"])
					usage.CacheWrite += intFromAny(u["cache_creation_input_tokens"])
				}
			}
		case "content_block_start":
			idx := intFromAny(raw["index"])
			if cb, ok := raw["content_block"].(map[string]any); ok && cb["type"] == "tool_use" {
				id, _ := cb["id"].(string)
				name, _ := cb["name"].(string)
				pt := &pendingTool{id: id, name: name}
				if inputMap, ok := cb["input"].(map[string]any); ok && len(inputMap) > 0 {
					if b, err := json.Marshal(inputMap); err == nil {
						pt.input.Write(b)
					}
				}
				toolsByIdx[idx] = pt
			}
		case "content_block_delta":
			idx := intFromAny(raw["index"])
			delta, ok := raw["delta"].(map[string]any)
			if !ok {
				return nil
			}
			switch delta["type"] {
			case "text_delta":
				if txt, _ := delta["text"].(string); txt != "" {
					emit(StreamEvent{Kind: EventTextDelta, Delta: txt})
				}
			case "thinking_delta":
				if txt, _ := delta["thinking"].(string); txt != "" {
					emit(StreamEvent{Kind: EventThinkingDelta, Delta: txt})
				}
			case "signature_delta":
				if sig, _ := delta["signature"].(string); sig != "" {
					emit(StreamEvent{Kind: EventSignatureDelta, Delta: sig})
				}
			case "input_json_delta":
				if pt := toolsByIdx[idx]; pt != nil {
					if pj, _ := delta["partial_json"].(string); pj != "" {
						pt.input.WriteString(pj)
					}
				}
			}
		case "content_block_stop":
			idx := intFromAny(raw["index"])
			if pt := toolsByIdx[idx]; pt != nil {
				pt.stopped = true
			}
		case "message_delta":
			if u, ok := raw["usage"].(map[string]any); ok {
				usage.Output += intFromAny(u["output_tokens"])
			}
			if dMap, ok := raw["delta"].(map[string]any); ok {
				if stop, _ := dMap["stop_reason"].(string); stop != "" {
					stopReason = stop
					sawTerminal = true
				}
			}
		case "message_stop":
			sawTerminal = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !sawTerminal {
		return ErrIncompleteStream
	}

	truncatedByMaxTokens := stopReason == "max_tokens"
	if len(toolsByIdx) > 0 {
		indices := make([]int, 0, len(toolsByIdx))
		for idx := range toolsByIdx {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			pt := toolsByIdx[idx]
			if !pt.stopped && !truncatedByMaxTokens {
				return fmt.Errorf("unfinished anthropic tool_use block: %w", ErrIncompleteStream)
			}
			if strings.TrimSpace(pt.id) == "" || strings.TrimSpace(pt.name) == "" {
				if truncatedByMaxTokens {
					continue
				}
				return fmt.Errorf("incomplete anthropic tool_use identity: %w", ErrIncompleteStream)
			}
			cleanedJSON, keep, vErr := validateCompletedToolJSON(pt.input.String(), truncatedByMaxTokens)
			if vErr != nil {
				return vErr
			}
			if !keep {
				continue
			}
			emit(StreamEvent{
				Kind:     EventToolCallReady,
				ToolCall: &ToolCall{ID: pt.id, Name: pt.name, Input: cleanedJSON},
			})
		}
	}

	if stopReason == "" {
		stopReason = "end_turn"
	}
	emit(StreamEvent{Kind: EventUsage, Usage: &usage})
	emit(StreamEvent{Kind: EventFinish, Finish: stopReason})
	return nil
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
