package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
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
	return ""
}

func (d *openaiDriver) BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	if d.responsesAPI {
		return d.buildResponsesRequest(req)
	}
	return d.buildChatCompletionsRequest(req)
}

func isStrictOpenAIChatHost(target llm.StreamTarget) bool {
	raw := strings.TrimSpace(target.BaseURL)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return host == "api.openai.com" || strings.HasSuffix(host, ".openai.com") || strings.HasSuffix(host, ".openai.azure.com")
}

func requiresToolReasoningField(target llm.StreamTarget, msgs []Message) bool {
	for _, m := range msgs {
		if m.Role != RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			if b.Type == ContentThinking && b.Text != "" {
				return true
			}
		}
	}
	lowerMod := strings.ToLower(strings.TrimSpace(target.ModelID))
	return strings.Contains(lowerMod, "deepseek-reasoner") ||
		strings.Contains(lowerMod, "deepseek-r1") ||
		(strings.Contains(lowerMod, "kimi") && strings.Contains(lowerMod, "thinking")) ||
		strings.HasSuffix(lowerMod, ":thinking")
}

func assistantMediaSummaryText(b ContentBlock) string {
	label := strings.TrimSpace(b.Filename)
	if label == "" {
		label = strings.TrimSpace(b.MimeType)
	}
	if label == "" {
		label = "attachment"
	}
	return "[Generated media: " + label + "]"
}

func (d *openaiDriver) buildChatCompletionsRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	allowReasoningContent := !isStrictOpenAIChatHost(req.Target)
	activeTurnStart := lastPlainUserMessageIndex(req.Messages)
	activeMsgs := req.Messages
	if activeTurnStart >= 0 && activeTurnStart < len(req.Messages) {
		activeMsgs = req.Messages[activeTurnStart:]
	}
	forceToolReasoning := allowReasoningContent && requiresToolReasoningField(req.Target, activeMsgs)

	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if fullSystem := appendSystemPromptSuffix(req.System, req.SystemSuffix); strings.TrimSpace(fullSystem) != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": fullSystem})
	}
	for msgIdx, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		inActiveTurn := activeTurnStart < 0 || msgIdx >= activeTurnStart
		var thinkingParts []string
		var textParts []string
		var contentItems []map[string]any
		hasMedia := false
		var toolCalls []map[string]any
		var toolResults []map[string]any
		for _, b := range m.Content {
			switch b.Type {
			case ContentThinking:
				if inActiveTurn && b.Text != "" {
					thinkingParts = append(thinkingParts, b.Text)
				}
			case ContentText:
				if b.Text != "" {
					textParts = append(textParts, b.Text)
					contentItems = append(contentItems, map[string]any{"type": "text", "text": b.Text})
				}
			case ContentMedia:
				if b.Data == "" || b.MimeType == "" {
					continue
				}
				if m.Role != RoleUser {
					summary := assistantMediaSummaryText(b)
					textParts = append(textParts, summary)
					contentItems = append(contentItems, map[string]any{"type": "text", "text": summary})
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
				} else if strings.HasPrefix(strings.ToLower(b.MimeType), "image/") {
					contentItems = append(contentItems, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": dataURI},
					})
				} else {
					contentItems = append(contentItems, map[string]any{"type": "text", "text": unsupportedMediaText(b, d.Protocol())})
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
		if len(contentItems) > 0 || len(toolCalls) > 0 || len(thinkingParts) > 0 {
			msg := map[string]any{"role": string(m.Role)}
			if hasMedia && m.Role == RoleUser {
				msg["content"] = contentItems
			} else if len(textParts) > 0 {
				msg["content"] = strings.Join(textParts, "\n")
			} else if !allowReasoningContent && len(thinkingParts) > 0 && len(toolCalls) == 0 {
				msg["content"] = "[thinking truncated]"
			} else {
				msg["content"] = ""
			}
			if m.Role == RoleAssistant && allowReasoningContent && inActiveTurn {
				if len(thinkingParts) > 0 {
					msg["reasoning_content"] = strings.Join(thinkingParts, "\n")
				} else if len(toolCalls) > 0 && forceToolReasoning {
					msg["reasoning_content"] = ""
				}
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
	cacheKey := effectiveModelCacheKey(req)
	if !allowReasoningContent {
		if cacheKey != "" {
			payload["prompt_cache_key"] = cacheKey
		}
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
	return llm.StreamHTTPRequest{Target: req.Target, Body: raw, SessionID: cacheKey}, nil
}

func isCodexBackendTarget(target llm.StreamTarget) bool {
	return llm.IsCodexBackendURL(target.BaseURL)
}

func (d *openaiDriver) buildResponsesRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	codexBackend := isCodexBackendTarget(req.Target)
	inputItems := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		hasUserMedia := false
		if m.Role == RoleUser {
			for _, b := range m.Content {
				if b.Type == ContentMedia && b.Data != "" && b.MimeType != "" {
					hasUserMedia = true
					break
				}
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
					if hasUserMedia {
						userMulti = append(userMulti, map[string]any{"type": "input_text", "text": b.Text})
					} else if codexBackend {
						inputItems = append(inputItems, llm.BuildCodexTextMessage(string(m.Role), b.Text))
					} else {
						inputItems = append(inputItems, map[string]any{"role": string(m.Role), "content": b.Text})
					}
				}
			case ContentMedia:
				if b.Data == "" || b.MimeType == "" {
					continue
				}
				if m.Role != RoleUser {
					summary := assistantMediaSummaryText(b)
					if codexBackend {
						inputItems = append(inputItems, llm.BuildCodexTextMessage(string(m.Role), summary))
					} else {
						inputItems = append(inputItems, map[string]any{"role": string(m.Role), "content": summary})
					}
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
				} else if strings.HasPrefix(strings.ToLower(b.MimeType), "image/") {
					userMulti = append(userMulti, map[string]any{
						"type": "input_image", "image_url": dataURI,
					})
				} else {
					userMulti = append(userMulti, map[string]any{"type": "input_text", "text": unsupportedMediaText(b, d.Protocol())})
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
			msgItem := map[string]any{"role": "user", "content": userMulti}
			if codexBackend {
				msgItem["type"] = "message"
			}
			inputItems = append(inputItems, msgItem)
		}
	}
	payload := map[string]any{
		"model": req.Target.ModelID, "input": inputItems, "stream": true, "store": false,
	}
	cacheKey := effectiveModelCacheKey(req)
	if cacheKey != "" {
		payload["prompt_cache_key"] = cacheKey
	}
	if fullSystem := appendSystemPromptSuffix(req.System, req.SystemSuffix); strings.TrimSpace(fullSystem) != "" {
		payload["instructions"] = fullSystem
	}
	if req.MaxTokens > 0 && !codexBackend {
		payload["max_output_tokens"] = req.MaxTokens
	}
	if eff := strings.TrimSpace(req.Effort); eff != "" && eff != "none" {
		rMap := map[string]string{"effort": eff}
		if codexBackend {
			rMap["summary"] = "auto"
		}
		payload["reasoning"] = rMap
		payload["include"] = []string{"reasoning.encrypted_content"}
	} else if codexBackend {
		payload["include"] = []string{"reasoning.encrypted_content"}
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
	if codexBackend {
		llm.NormalizeCodexResponsesMap(payload)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	return llm.StreamHTTPRequest{Target: req.Target, Body: raw, SessionID: cacheKey}, nil
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
						if acc.name == "" {
							acc.name = name
						} else if name != acc.name {
							acc.name += name
						}
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
					emit(StreamEvent{Kind: EventTruncatedTool})
					continue
				}
				return fmt.Errorf("incomplete tool call identity: %w", ErrIncompleteStream)
			}
			cleanedArgs, keep, vErr := validateCompletedToolJSON(c.args.String(), truncatedByLength)
			if vErr != nil {
				return vErr
			}
			if !keep {
				emit(StreamEvent{Kind: EventTruncatedTool})
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
	reasoningBlockIdx := 0
	reasoningActive := false
	hasReasoningDelta := false
	needReasoningBreak := false
	lastSummaryIdx := -1
	ensureReasoningBlock := func() {
		if reasoningActive {
			return
		}
		reasoningBlockIdx++
		reasoningActive = true
		hasReasoningDelta = false
		needReasoningBreak = false
		lastSummaryIdx = -1
	}
	sealReasoningBlock := func() {
		if !reasoningActive {
			return
		}
		emit(StreamEvent{Kind: EventThinkingEnd, BlockIndex: reasoningBlockIdx})
		reasoningActive = false
		hasReasoningDelta = false
		needReasoningBreak = false
		lastSummaryIdx = -1
	}

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
				sealReasoningBlock()
				emit(StreamEvent{Kind: EventTextDelta, Delta: d})
			}
		case "response.reasoning_summary_part.added":
			ensureReasoningBlock()
			if hasReasoningDelta {
				needReasoningBreak = true
			}
			if _, ok := raw["summary_index"]; ok {
				lastSummaryIdx = intFromAny(raw["summary_index"])
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if d, _ := raw["delta"].(string); d != "" {
				ensureReasoningBlock()
				if eventType == "response.reasoning_summary_text.delta" {
					if _, ok := raw["summary_index"]; ok {
						idx := intFromAny(raw["summary_index"])
						if hasReasoningDelta && lastSummaryIdx >= 0 && idx != lastSummaryIdx {
							needReasoningBreak = true
						}
						lastSummaryIdx = idx
					}
				}
				if needReasoningBreak {
					emit(StreamEvent{Kind: EventThinkingDelta, BlockIndex: reasoningBlockIdx, Delta: "\n\n"})
					needReasoningBreak = false
				}
				hasReasoningDelta = true
				emit(StreamEvent{Kind: EventThinkingDelta, BlockIndex: reasoningBlockIdx, Delta: d})
			}
		case "response.output_item.added":
			if item, ok := raw["item"].(map[string]any); ok {
				switch item["type"] {
				case "reasoning":
					sealReasoningBlock()
					ensureReasoningBlock()
				case "function_call":
					sealReasoningBlock()
					itemID, _ := item["id"].(string)
					callID, _ := item["call_id"].(string)
					name, _ := item["name"].(string)
					if itemID != "" {
						calls[itemID] = &respCall{callID: callID, name: name}
					}
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
					ensureReasoningBlock()
					emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: reasoningBlockIdx, Delta: enc})
				}
				sealReasoningBlock()
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
						emit(StreamEvent{Kind: EventTruncatedTool})
						return nil
					}
					return fmt.Errorf("incomplete responses tool call identity: %w", ErrIncompleteStream)
				}
				cleanedArgs, keep, vErr := validateCompletedToolJSON(argsStr, itemIncomplete)
				if vErr != nil {
					return vErr
				}
				if !keep {
					emit(StreamEvent{Kind: EventTruncatedTool})
					return nil
				}
				if !emittedCalls[callID] {
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
	if len(calls) > 0 {
		if !incomplete {
			return fmt.Errorf("unfinished responses tool call stream: %w", ErrIncompleteStream)
		}
		emit(StreamEvent{Kind: EventTruncatedTool})
	}
	emit(StreamEvent{Kind: EventFinish, Finish: finishReason})
	return nil
}
