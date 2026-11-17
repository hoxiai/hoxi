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

type openaiDriver struct {
	responsesAPI bool
}

func newOpenAIDriver(responsesAPI bool) ModelDriver {
	return &openaiDriver{responsesAPI: responsesAPI}
}

func (d *openaiDriver) Protocol() llm.StreamProtocol {
	if d.responsesAPI {
		return llm.ProtocolOpenAIResponses
	}
	return llm.ProtocolOpenAI
}

func (d *openaiDriver) SystemPromptPatch() string {
	return "Batch independent read/grep/glob calls in a single turn. Always inspect target lines before calling edit."
}

func (d *openaiDriver) BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	if d.responsesAPI {
		return d.buildResponsesRequest(req)
	}
	return d.buildChatCompletionsRequest(req)
}

func (d *openaiDriver) buildChatCompletionsRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		var textParts []string
		var contentItems []map[string]any
		hasMedia := false
		var toolCalls []map[string]any
		var toolResults []map[string]any
		for _, b := range m.Content {
			switch b.Type {
			case ContentText:
				if b.Text != "" {
					textParts = append(textParts, b.Text)
					contentItems = append(contentItems, map[string]any{"type": "text", "text": b.Text})
				}
			case ContentMedia:
				if b.Data == "" || b.MimeType == "" {
					continue
				}
				hasMedia = true
				dataURI := "data:" + b.MimeType + ";base64," + b.Data
				if strings.EqualFold(b.MimeType, "application/pdf") {
					fname := b.Filename
					if fname == "" {
						fname = "document.pdf"
					}
					contentItems = append(contentItems, map[string]any{
						"type": "file",
						"file": map[string]any{"filename": fname, "file_data": dataURI},
					})
				} else {
					contentItems = append(contentItems, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": dataURI},
					})
				}
			case ContentToolUse:
				args := strings.TrimSpace(string(b.Input))
				if args == "" {
					args = "{}"
				}
				toolCalls = append(toolCalls, map[string]any{
					"id": b.ID, "type": "function",
					"function": map[string]any{"name": b.Name, "arguments": args},
				})
			case ContentToolResult:
				toolResults = append(toolResults, map[string]any{
					"role": "tool", "tool_call_id": b.ToolUseID, "content": b.Text,
				})
			}
		}
		if m.Role == RoleUser && len(toolResults) > 0 {
			msgs = append(msgs, toolResults...)
			toolResults = nil
		}
		if len(contentItems) > 0 || len(toolCalls) > 0 {
			msg := map[string]any{"role": string(m.Role)}
			if hasMedia && m.Role == RoleUser {
				msg["content"] = contentItems
			} else if len(textParts) > 0 {
				msg["content"] = strings.Join(textParts, "\n")
			} else {
				msg["content"] = ""
			}
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
			msgs = append(msgs, msg)
		}
		msgs = append(msgs, toolResults...)
	}

	payload := map[string]any{
		"model":          req.Target.ModelID,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		payload["max_completion_tokens"] = req.MaxTokens
	}
	if eff := strings.TrimSpace(req.Effort); eff != "" && eff != "none" {
		payload["reasoning_effort"] = eff
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": t.Name, "description": t.Description, "parameters": t.Parameters,
				},
			})
		}
		payload["tools"] = tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	return llm.StreamHTTPRequest{Target: req.Target, Body: raw}, nil
}

func (d *openaiDriver) buildResponsesRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	inputItems := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		hasMedia := false
		for _, b := range m.Content {
			if b.Type == ContentMedia && b.Data != "" && b.MimeType != "" {
				hasMedia = true
				break
			}
		}
		var userMulti []map[string]any
		for _, b := range m.Content {
			switch b.Type {
			case ContentThinking:
				if b.Signature != "" {
					item := map[string]any{
						"type":              "reasoning",
						"encrypted_content": b.Signature,
					}
					if strings.TrimSpace(b.Text) != "" {
						item["summary"] = []map[string]any{{"type": "summary_text", "text": b.Text}}
					}
					inputItems = append(inputItems, item)
				}
			case ContentText:
				if strings.TrimSpace(b.Text) != "" {
					if hasMedia && m.Role == RoleUser {
						userMulti = append(userMulti, map[string]any{"type": "input_text", "text": b.Text})
					} else {
						inputItems = append(inputItems, map[string]any{"role": string(m.Role), "content": b.Text})
					}
				}
			case ContentMedia:
				if b.Data == "" || b.MimeType == "" {
					continue
				}
				dataURI := "data:" + b.MimeType + ";base64," + b.Data
				if strings.EqualFold(b.MimeType, "application/pdf") {
					fname := b.Filename
					if fname == "" {
						fname = "document.pdf"
					}
					userMulti = append(userMulti, map[string]any{
						"type": "input_file", "filename": fname, "file_data": dataURI,
					})
				} else {
					userMulti = append(userMulti, map[string]any{
						"type": "input_image", "image_url": dataURI,
					})
				}
			case ContentToolUse:
				args := strings.TrimSpace(string(b.Input))
				if args == "" {
					args = "{}"
				}
				inputItems = append(inputItems, map[string]any{
					"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": args,
				})
			case ContentToolResult:
				inputItems = append(inputItems, map[string]any{
					"type": "function_call_output", "call_id": b.ToolUseID, "output": b.Text,
				})
			}
		}
		if len(userMulti) > 0 {
			inputItems = append(inputItems, map[string]any{"role": "user", "content": userMulti})
		}
	}
	payload := map[string]any{
		"model": req.Target.ModelID, "input": inputItems, "stream": true, "store": false,
	}
	if strings.TrimSpace(req.System) != "" {
		payload["instructions"] = req.System
	}
	if req.MaxTokens > 0 {
		payload["max_output_tokens"] = req.MaxTokens
	}
	if eff := strings.TrimSpace(req.Effort); eff != "" && eff != "none" {
		payload["reasoning"] = map[string]string{"effort": eff}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters,
			})
		}
		payload["tools"] = tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	return llm.StreamHTTPRequest{Target: req.Target, Body: raw}, nil
}

func (d *openaiDriver) ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	if d.responsesAPI {
		return parseOpenAIResponsesSSE(ctx, body, emit)
	}
	return parseOpenAIChatSSE(ctx, body, emit)
}

func parseOpenAIChatSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	type tcAcc struct {
		id   string
		name string
		args strings.Builder
	}
	calls := make(map[int]*tcAcc)
	var finishReason string
	var sawDone bool
	var usage *TokenUsage
	var inlineThink inlineThinkFilter
	structuredReasoning := false

	emitSegments := func(segs []inlineThinkSegment) {
		for _, seg := range segs {
			if seg.text == "" {
				continue
			}
			if seg.thinking {
				if structuredReasoning {
					continue
				}
				emit(StreamEvent{Kind: EventThinkingDelta, Delta: seg.text})
			} else {
				emit(StreamEvent{Kind: EventTextDelta, Delta: seg.text})
			}
		}
	}

	err := readSSEFrames(ctx, body, func(frame sseRawFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			sawDone = true
			return nil
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return nil
		}
		if errObj, ok := chunk["error"].(map[string]any); ok {
			msg, _ := errObj["message"].(string)
			if msg == "" {
				msg = data
			}
			return fmt.Errorf("openai stream error: %s", msg)
		}
		if u, ok := chunk["usage"].(map[string]any); ok {
			tu := &TokenUsage{
				Input:  intFromAny(u["prompt_tokens"]),
				Output: intFromAny(u["completion_tokens"]),
			}
			if details, ok := u["prompt_tokens_details"].(map[string]any); ok {
				tu.CacheRead = intFromAny(details["cached_tokens"])
			} else if hit := intFromAny(u["prompt_cache_hit_tokens"]); hit > 0 {
				tu.CacheRead = hit
			}
			if details, ok := u["completion_tokens_details"].(map[string]any); ok {
				tu.Reasoning = intFromAny(details["reasoning_tokens"])
			}
			normalizeInclusiveUsage(tu)
			usage = tu
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			return nil
		}
		c0, _ := choices[0].(map[string]any)
		if fr, _ := c0["finish_reason"].(string); fr != "" {
			finishReason = fr
		}
		delta, _ := c0["delta"].(map[string]any)
		if delta == nil {
			return nil
		}
		for _, field := range []string{"reasoning_content", "reasoning"} {
			if r, _ := delta[field].(string); r != "" {
				structuredReasoning = true
				emit(StreamEvent{Kind: EventThinkingDelta, Delta: r})
			}
		}
		if c, _ := delta["content"].(string); c != "" {
			if structuredReasoning {
				emit(StreamEvent{Kind: EventTextDelta, Delta: c})
			} else {
				emitSegments(inlineThink.push(c))
			}
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, rawTC := range tcs {
				tc, _ := rawTC.(map[string]any)
				if tc == nil {
					continue
				}
				idx := intFromAny(tc["index"])
				acc := calls[idx]
				if acc == nil {
					acc = &tcAcc{}
					calls[idx] = acc
				}
				if id, _ := tc["id"].(string); id != "" {
					acc.id = id
				}
				if fn, ok := tc["function"].(map[string]any); ok {
					if name, _ := fn["name"].(string); name != "" {
						acc.name += name
					}
					if args, _ := fn["arguments"].(string); args != "" {
						acc.args.WriteString(args)
					}
				}
			}
		}
		return nil
	})
	emitSegments(inlineThink.flush())
	if err != nil {
		return err
	}
	if !sawDone && finishReason == "" {
		return ErrIncompleteStream
	}

	truncatedByLength := finishReason == "length"
	if len(calls) > 0 {
		indices := make([]int, 0, len(calls))
		for idx := range calls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			c := calls[idx]
			if strings.TrimSpace(c.id) == "" || strings.TrimSpace(c.name) == "" {
				if truncatedByLength {
					continue
				}
				return fmt.Errorf("incomplete tool call identity: %w", ErrIncompleteStream)
			}
			cleanedArgs, keep, vErr := validateCompletedToolJSON(c.args.String(), truncatedByLength)
			if vErr != nil {
				return vErr
			}
			if !keep {
				continue
			}
			emit(StreamEvent{
				Kind:     EventToolCallReady,
				ToolCall: &ToolCall{ID: c.id, Name: c.name, Input: cleanedArgs},
			})
		}
	}
	if usage != nil {
		emit(StreamEvent{Kind: EventUsage, Usage: usage})
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	emit(StreamEvent{Kind: EventFinish, Finish: finishReason})
	return nil
}

func parseOpenAIResponsesSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	type respCall struct {
		callID string
		name   string
		args   strings.Builder
	}
	calls := make(map[string]*respCall)
	emittedCalls := make(map[string]bool)
	var terminal bool
	var incomplete bool
	finishReason := "stop"

	err := readSSEFrames(ctx, body, func(frame sseRawFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var raw map[string]any
		if json.Unmarshal([]byte(data), &raw) != nil {
			return nil
		}
		eventType := frame.Event
		if t, _ := raw["type"].(string); t != "" {
			eventType = t
		}
		switch eventType {
		case "error", "response.failed":
			return fmt.Errorf("openai responses stream error: %s", data)
		case "response.output_text.delta":
			if d, _ := raw["delta"].(string); d != "" {
				emit(StreamEvent{Kind: EventTextDelta, Delta: d})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if d, _ := raw["delta"].(string); d != "" {
				emit(StreamEvent{Kind: EventThinkingDelta, Delta: d})
			}
		case "response.output_item.added":
			if item, ok := raw["item"].(map[string]any); ok && item["type"] == "function_call" {
				itemID, _ := item["id"].(string)
				callID, _ := item["call_id"].(string)
				name, _ := item["name"].(string)
				if itemID != "" {
					calls[itemID] = &respCall{callID: callID, name: name}
				}
			}
		case "response.function_call_arguments.delta":
			itemID, _ := raw["item_id"].(string)
			if c := calls[itemID]; c != nil {
				if d, _ := raw["delta"].(string); d != "" {
					c.args.WriteString(d)
				}
			}
		case "response.output_item.done":
			item, ok := raw["item"].(map[string]any)
			if !ok {
				return nil
			}
			switch item["type"] {
			case "reasoning":
				if enc, _ := item["encrypted_content"].(string); enc != "" {
					emit(StreamEvent{Kind: EventSignatureDelta, Delta: enc})
				}
			case "function_call":
				itemID, _ := item["id"].(string)
				callID, _ := item["call_id"].(string)
				name, _ := item["name"].(string)
				argsStr, _ := item["arguments"].(string)
				itemIncomplete := item["status"] == "incomplete"
				if c := calls[itemID]; c != nil {
					if argsStr == "" {
						argsStr = c.args.String()
					}
					if callID == "" {
						callID = c.callID
					}
					if name == "" {
						name = c.name
					}
					delete(calls, itemID)
				}
				if callID == "" || name == "" {
					if itemIncomplete {
						return nil
					}
					return fmt.Errorf("incomplete responses tool call identity: %w", ErrIncompleteStream)
				}
				cleanedArgs, keep, vErr := validateCompletedToolJSON(argsStr, itemIncomplete)
				if vErr != nil {
					return vErr
				}
				if keep && !emittedCalls[callID] {
					emittedCalls[callID] = true
					emit(StreamEvent{
						Kind:     EventToolCallReady,
						ToolCall: &ToolCall{ID: callID, Name: name, Input: cleanedArgs},
					})
				}
			}
		case "response.completed", "response.incomplete":
			terminal = true
			if eventType == "response.incomplete" {
				incomplete = true
				finishReason = "incomplete"
			}
			if respObj, ok := raw["response"].(map[string]any); ok {
				if incomplete {
					if details, ok := respObj["incomplete_details"].(map[string]any); ok {
						switch details["reason"] {
						case "max_output_tokens":
							finishReason = "length"
						case "content_filter":
							finishReason = "content_filter"
						}
					}
				}
				if u, ok := respObj["usage"].(map[string]any); ok {
					tu := &TokenUsage{
						Input:  intFromAny(u["input_tokens"]),
						Output: intFromAny(u["output_tokens"]),
					}
					if inDet, ok := u["input_tokens_details"].(map[string]any); ok {
						tu.CacheRead = intFromAny(inDet["cached_tokens"])
					}
					if outDet, ok := u["output_tokens_details"].(map[string]any); ok {
						tu.Reasoning = intFromAny(outDet["reasoning_tokens"])
					}
					normalizeInclusiveUsage(tu)
					emit(StreamEvent{Kind: EventUsage, Usage: tu})
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !terminal {
		return ErrIncompleteStream
	}
	// Any buffered function_call that never reached output_item.done without length truncation is corrupt.
	if len(calls) > 0 && !incomplete {
		return fmt.Errorf("unfinished responses tool call stream: %w", ErrIncompleteStream)
	}
	emit(StreamEvent{Kind: EventFinish, Finish: finishReason})
	return nil
}
