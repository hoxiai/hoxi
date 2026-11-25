package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
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

var openAIChatModelTokenRe = regexp.MustCompile(`(?i)(?:^|[/:_-])(?:codex|gpt-(?:4o|4\.1|5|6|7)|o[134](?:$|[._:-]))`)

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

func isStrictNonOpenAIChatHost(target llm.StreamTarget) bool {
	raw := strings.TrimSpace(target.BaseURL)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	for _, suffix := range []string{
		"deepseek.com", "groq.com", "cerebras.ai", "together.xyz", "fireworks.ai",
		"dashscope.aliyuncs.com", "siliconflow.cn", "siliconflow.com",
		"x.ai", "googleapis.com", "mistral.ai", "anthropic.com",
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func isOpenAIChatModel(modelID string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" || strings.Contains(lower, "gpt-oss") || strings.Contains(lower, "oss-") {
		return false
	}
	return openAIChatModelTokenRe.MatchString(lower)
}

func supportsOpenAIChatPromptCacheKey(target llm.StreamTarget) bool {
	if isStrictOpenAIChatHost(target) {
		return true
	}
	if isStrictNonOpenAIChatHost(target) {
		return false
	}
	return isOpenAIChatModel(target.ModelID)
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
	allowReasoningContent := !isStrictOpenAIChatHost(req.Target) && !isOpenAIChatModel(req.Target.ModelID)
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
	if cacheKey != "" && supportsOpenAIChatPromptCacheKey(req.Target) {
		payload["prompt_cache_key"] = cacheKey
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

func (d *openaiDriver) ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	if d.responsesAPI {
		return parseOpenAIResponsesSSE(ctx, body, emit)
	}
	return parseOpenAIChatSSE(ctx, body, emit)
}

func isLostChatUsageTrailerError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || llm.IsHTTP2ProtocolError(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
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
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		// Losing only the optional include_usage trailer / [DONE] after a valid
		// finish_reason must not discard completed choices or re-run the step.
		if finishReason == "" || !isLostChatUsageTrailerError(err) {
			return err
		}
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
