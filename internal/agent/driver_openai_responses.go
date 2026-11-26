package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func isCodexBackendTarget(target llm.StreamTarget) bool {
	return llm.IsCodexBackendURL(target.BaseURL)
}

func (d *openaiDriver) buildResponsesRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	codexBackend := isCodexBackendTarget(req.Target)
	hardened := hardenMessagesForRequest(req.Messages, false)
	inputItems := make([]map[string]any, 0, len(hardened))
	for _, m := range hardened {
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
					summary := make([]map[string]any, 0, 1)
					if strings.TrimSpace(b.Text) != "" {
						summary = append(summary, map[string]any{"type": "summary_text", "text": b.Text})
					}
					item := map[string]any{
						"type":              "reasoning",
						"encrypted_content": b.Signature,
						"summary":           summary,
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
	} else if codexBackend || isOpenAIResponsesReasoningModel(req.Target.ModelID) {
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

func parseOpenAIResponsesSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	type respCall struct {
		callID string
		name   string
		args   strings.Builder
	}
	calls := make(map[string]*respCall)
	emittedCalls := make(map[string]bool)
	emittedReasoningSig := make(map[string]bool)
	streamedTextByItem := make(map[string]*strings.Builder)
	var totalStreamedText strings.Builder
	var sawDeltaWithoutItemID bool
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

	emitFunctionCallItem := func(item map[string]any, allowIncomplete bool) error {
		itemID, _ := item["id"].(string)
		callID, _ := item["call_id"].(string)
		name, _ := item["name"].(string)
		argsStr, _ := item["arguments"].(string)
		itemIncomplete := allowIncomplete || item["status"] == "incomplete"
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
		if emittedCalls[callID] {
			return nil
		}
		cleanedArgs, keep, vErr := validateCompletedToolJSON(argsStr, itemIncomplete)
		if vErr != nil {
			return vErr
		}
		if !keep {
			emit(StreamEvent{Kind: EventTruncatedTool})
			return nil
		}
		emittedCalls[callID] = true
		emit(StreamEvent{
			Kind:     EventToolCallReady,
			ToolCall: &ToolCall{ID: callID, Name: name, Input: cleanedArgs},
		})
		return nil
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
				if itemID, _ := raw["item_id"].(string); itemID != "" {
					sb := streamedTextByItem[itemID]
					if sb == nil {
						sb = &strings.Builder{}
						streamedTextByItem[itemID] = sb
					}
					sb.WriteString(d)
				} else {
					sawDeltaWithoutItemID = true
				}
				totalStreamedText.WriteString(d)
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
					emittedReasoningSig[enc] = true
					emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: reasoningBlockIdx, Delta: enc})
				}
				sealReasoningBlock()
			case "function_call":
				return emitFunctionCallItem(item, false)
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
				// Backfill any output items omitted from incremental delta/output_item.done
				// events by compatible gateways, deduplicated against already-streamed items.
				if output, ok := respObj["output"].([]any); ok {
					matchedAnyStreamedItem := false
					for _, rawItem := range output {
						item, _ := rawItem.(map[string]any)
						if item == nil {
							continue
						}
						switch item["type"] {
						case "function_call":
							if err := emitFunctionCallItem(item, incomplete); err != nil {
								return err
							}
						case "reasoning":
							enc, _ := item["encrypted_content"].(string)
							if enc != "" && !emittedReasoningSig[enc] {
								sealReasoningBlock()
								ensureReasoningBlock()
								if summaries, ok := item["summary"].([]any); ok {
									var parts []string
									for _, s := range summaries {
										if sm, _ := s.(map[string]any); sm != nil {
											if txt, _ := sm["text"].(string); txt != "" {
												parts = append(parts, txt)
											}
										}
									}
									if joined := strings.Join(parts, "\n\n"); joined != "" {
										emit(StreamEvent{Kind: EventThinkingDelta, BlockIndex: reasoningBlockIdx, Delta: joined})
									}
								}
								emittedReasoningSig[enc] = true
								emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: reasoningBlockIdx, Delta: enc})
								sealReasoningBlock()
							}
						case "message":
							itemID, _ := item["id"].(string)
							var fullText strings.Builder
							if content, ok := item["content"].([]any); ok {
								for _, rawBlk := range content {
									blk, _ := rawBlk.(map[string]any)
									if blk != nil && blk["type"] == "output_text" {
										if txt, _ := blk["text"].(string); txt != "" {
											fullText.WriteString(txt)
										}
									}
								}
							}
							completed := fullText.String()
							if completed == "" {
								continue
							}
							var streamedSoFar string
							if itemID != "" {
								if sb := streamedTextByItem[itemID]; sb != nil {
									streamedSoFar = sb.String()
									matchedAnyStreamedItem = true
								} else if sawDeltaWithoutItemID || len(streamedTextByItem) == 0 {
									streamedSoFar = totalStreamedText.String()
								} else if totalStreamedText.Len() > 0 && !matchedAnyStreamedItem && strings.HasPrefix(completed, totalStreamedText.String()) {
									streamedSoFar = totalStreamedText.String()
									matchedAnyStreamedItem = true
								}
							} else {
								streamedSoFar = totalStreamedText.String()
							}
							if strings.HasPrefix(completed, streamedSoFar) {
								if rem := strings.TrimPrefix(completed, streamedSoFar); rem != "" {
									sealReasoningBlock()
									if itemID != "" {
										sb := streamedTextByItem[itemID]
										if sb == nil {
											sb = &strings.Builder{}
											streamedTextByItem[itemID] = sb
										}
										sb.WriteString(rem)
									}
									totalStreamedText.WriteString(rem)
									emit(StreamEvent{Kind: EventTextDelta, Delta: rem})
								}
							}
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
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		// Once an explicit terminal event (response.completed / response.incomplete)
		// has already delivered final output and usage, a subsequent transport reset
		// before clean EOF must not discard or re-run the completed step.
		if !terminal || !isPostTerminalDisconnectError(err) {
			return err
		}
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
