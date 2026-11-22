package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		blocks := make([]map[string]any, 0, len(m.Content))
		for _, b := range m.Content {
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
				} else {
					block = map[string]any{"type": "text", "text": unsupportedMediaText(b, d.Protocol())}
				}
			case ContentThinking:
				if b.Data != "" {
					block = map[string]any{
						"type": "redacted_thinking",
						"data": b.Data,
					}
					break
				}
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
			blocks = append(blocks, block)
		}
		if len(blocks) == 0 {
			continue
		}
		if m.Role == RoleUser && len(blocks) > 1 {
			// Anthropic requires all tool_result blocks to precede image/document/text blocks in a user message.
			var trBlocks, otherBlocks []map[string]any
			for _, blk := range blocks {
				if blk["type"] == "tool_result" {
					trBlocks = append(trBlocks, blk)
				} else {
					otherBlocks = append(otherBlocks, blk)
				}
			}
			if len(trBlocks) > 0 && len(otherBlocks) > 0 {
				blocks = append(trBlocks, otherBlocks...)
			}
		}
		msgs = append(msgs, map[string]any{"role": string(m.Role), "content": blocks})
	}
	markAnthropicTrailingUserCacheBreakpoints(msgs)

	payload := map[string]any{
		"model":      req.Target.ModelID,
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   msgs,
	}
	cacheKey := effectiveModelCacheKey(req)
	if stickyID := anthropicStickyUserID(req.Target.ProviderID, cacheKey); stickyID != "" {
		payload["metadata"] = map[string]string{"user_id": stickyID}
	}
	var sysBlocks []map[string]any
	if strings.TrimSpace(req.System) != "" {
		sysBlocks = append(sysBlocks, map[string]any{
			"type":          "text",
			"text":          req.System,
			"cache_control": map[string]string{"type": "ephemeral"},
		})
	}
	if suffix := strings.TrimSpace(req.SystemSuffix); suffix != "" {
		sysBlocks = append(sysBlocks, map[string]any{
			"type": "text",
			"text": suffix,
		})
	}
	if len(sysBlocks) > 0 {
		payload["system"] = sysBlocks
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
	var extraHeaders map[string]string
	if req.FastMode && supportsAnthropicFastMode(req.Target) {
		payload["speed"] = "fast"
		extraHeaders = map[string]string{"anthropic-beta": anthropicFastModeBetaHeader}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	return llm.StreamHTTPRequest{
		Target:       req.Target,
		Body:         raw,
		ExtraHeaders: extraHeaders,
		SessionID:    cacheKey,
	}, nil
}

func (d *anthropicDriver) ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	type pendingTool struct {
		id      string
		name    string
		input   strings.Builder
		stopped bool
	}
	toolsByIdx := make(map[int]*pendingTool)
	thinkingBlocks := make(map[int]bool)
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
					if tier := normalizeFastServiceTier(u["speed"]); tier != "" {
						usage.ServiceTier = tier
					} else if tier := normalizeFastServiceTier(u["service_tier"]); tier != "" {
						usage.ServiceTier = tier
					}
				}
			}
		case "content_block_start":
			idx := intFromAny(raw["index"])
			blockIdx := idx + 1
			if cb, ok := raw["content_block"].(map[string]any); ok {
				switch cb["type"] {
				case "tool_use":
					id, _ := cb["id"].(string)
					name, _ := cb["name"].(string)
					pt := &pendingTool{id: id, name: name}
					if inputMap, ok := cb["input"].(map[string]any); ok && len(inputMap) > 0 {
						if b, err := json.Marshal(inputMap); err == nil {
							pt.input.Write(b)
						}
					}
					toolsByIdx[idx] = pt
				case "thinking":
					thinkingBlocks[idx] = true
					if txt, _ := cb["thinking"].(string); txt != "" {
						emit(StreamEvent{Kind: EventThinkingDelta, BlockIndex: blockIdx, Delta: txt})
					}
					if sig, _ := cb["signature"].(string); sig != "" {
						emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: blockIdx, Delta: sig})
					}
				case "redacted_thinking":
					if redacted, _ := cb["data"].(string); redacted != "" {
						emit(StreamEvent{Kind: EventRedactedThinking, BlockIndex: blockIdx, Delta: redacted})
					}
				case "text":
					if txt, _ := cb["text"].(string); txt != "" {
						emit(StreamEvent{Kind: EventTextDelta, BlockIndex: blockIdx, Delta: txt})
					}
				}
			}
		case "content_block_delta":
			idx := intFromAny(raw["index"])
			blockIdx := idx + 1
			delta, ok := raw["delta"].(map[string]any)
			if !ok {
				return nil
			}
			switch delta["type"] {
			case "text_delta":
				if txt, _ := delta["text"].(string); txt != "" {
					emit(StreamEvent{Kind: EventTextDelta, BlockIndex: blockIdx, Delta: txt})
				}
			case "thinking_delta":
				thinkingBlocks[idx] = true
				if txt, _ := delta["thinking"].(string); txt != "" {
					emit(StreamEvent{Kind: EventThinkingDelta, BlockIndex: blockIdx, Delta: txt})
				}
			case "signature_delta":
				thinkingBlocks[idx] = true
				if sig, _ := delta["signature"].(string); sig != "" {
					emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: blockIdx, Delta: sig})
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
			if thinkingBlocks[idx] {
				emit(StreamEvent{Kind: EventThinkingEnd, BlockIndex: idx + 1})
			}
		case "message_delta":
			if u, ok := raw["usage"].(map[string]any); ok {
				usage.Output += intFromAny(u["output_tokens"])
				if tier := normalizeFastServiceTier(u["speed"]); tier != "" {
					usage.ServiceTier = tier
				} else if tier := normalizeFastServiceTier(u["service_tier"]); tier != "" {
					usage.ServiceTier = tier
				}
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
					emit(StreamEvent{Kind: EventTruncatedTool})
					continue
				}
				return fmt.Errorf("incomplete anthropic tool_use identity: %w", ErrIncompleteStream)
			}
			cleanedJSON, keep, vErr := validateCompletedToolJSON(pt.input.String(), truncatedByMaxTokens)
			if vErr != nil {
				return vErr
			}
			if !keep {
				emit(StreamEvent{Kind: EventTruncatedTool})
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

// anthropicStickyUserID derives a deterministic, zero-PII opaque session key
// matching the user_<64hex>_account__session_<uuid> convention used by Claude
// account pools (Sub2API, CRS, NewAPI, LiteLLM) so shifting cache_control
// breakpoints do not switch upstream accounts or invalidate KV caches mid-session.
func anthropicStickyUserID(providerID, sessionID string) string {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return ""
	}
	prov := strings.ToLower(strings.TrimSpace(providerID))
	userSum := sha256.Sum256([]byte("hoxi-anthropic-pool-user:v1:" + prov))
	sessSum := sha256.Sum256([]byte("hoxi-anthropic-pool-session:v1:" + prov + ":" + sid))
	u := sessSum[:16]
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	sessUUID := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	return "user_" + hex.EncodeToString(userSum[:]) + "_account__session_" + sessUUID
}

// markAnthropicTrailingUserCacheBreakpoints marks up to the last two user
// messages with ephemeral cache breakpoints (keeping total breakpoints within
// Anthropic's 4-block limit alongside tools[last] and system[0]). Marking the
// penultimate user message preserves prefix cache hits even when a parallel
// tool batch exceeds Anthropic's 20-block lookback window.
func markAnthropicTrailingUserCacheBreakpoints(msgs []map[string]any) {
	marked := 0
	for i := len(msgs) - 1; i >= 0 && marked < 2; i-- {
		if msgs[i]["role"] != string(RoleUser) {
			continue
		}
		blocks, ok := msgs[i]["content"].([]map[string]any)
		if !ok || len(blocks) == 0 {
			continue
		}
		for j := len(blocks) - 1; j >= 0; j-- {
			tp, _ := blocks[j]["type"].(string)
			if tp == "thinking" || tp == "redacted_thinking" {
				continue
			}
			blocks[j]["cache_control"] = map[string]string{"type": "ephemeral"}
			marked++
			break
		}
	}
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
