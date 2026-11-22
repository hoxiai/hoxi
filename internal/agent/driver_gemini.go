package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

type geminiDriver struct{}

func newGeminiDriver() ModelDriver { return &geminiDriver{} }

func (d *geminiDriver) Protocol() llm.StreamProtocol { return llm.ProtocolGoogle }

func (d *geminiDriver) SystemPromptPatch() string {
	return "Batch read-only file and search calls in parallel. Keep edits surgical and verify changes with targeted commands."
}

func (d *geminiDriver) BuildRequest(req ModelRequest) (llm.StreamHTTPRequest, error) {
	toolNames := make(map[string]string)
	contents := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}
		parts := make([]map[string]any, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case ContentThinking:
				if b.Signature != "" {
					parts = append(parts, map[string]any{
						"text":             b.Text,
						"thought":          true,
						"thoughtSignature": b.Signature,
					})
				}
			case ContentText:
				if b.Text != "" {
					parts = append(parts, map[string]any{"text": b.Text})
				}
			case ContentMedia:
				if b.Data != "" && b.MimeType != "" {
					parts = append(parts, map[string]any{
						"inlineData": map[string]any{
							"mimeType": b.MimeType,
							"data":     b.Data,
						},
					})
				}
			case ContentToolUse:
				toolNames[b.ID] = b.Name
				var args any = map[string]any{}
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &args)
				}
				fcPart := map[string]any{
					"functionCall": map[string]any{"name": b.Name, "args": args},
				}
				if b.Signature != "" {
					fcPart["thoughtSignature"] = b.Signature
				}
				parts = append(parts, fcPart)
			case ContentToolResult:
				name := toolNames[b.ToolUseID]
				if name == "" {
					name = "tool"
				}
				parts = append(parts, map[string]any{
					"functionResponse": map[string]any{
						"name":     name,
						"response": map[string]any{"output": b.Text, "isError": b.IsError},
					},
				})
			}
		}
		if len(parts) > 0 {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}

	payload := map[string]any{"contents": contents}
	if fullSystem := appendSystemPromptSuffix(req.System, req.SystemSuffix); strings.TrimSpace(fullSystem) != "" {
		payload["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": fullSystem}}}
	}
	generation := map[string]any{}
	if req.MaxTokens > 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	if thinking, ok := reasoningVariant(req)["thinkingConfig"].(map[string]any); ok {
		generation["thinkingConfig"] = thinking
	}
	if len(generation) > 0 {
		payload["generationConfig"] = generation
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Parameters,
			})
		}
		payload["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return llm.StreamHTTPRequest{}, err
	}
	return llm.StreamHTTPRequest{Target: req.Target, Body: raw, SessionID: effectiveModelCacheKey(req)}, nil
}

func (d *geminiDriver) ParseSSE(ctx context.Context, body io.Reader, emit func(StreamEvent)) error {
	thoughtBlockIdx := 1
	thoughtOpen := false
	var usage TokenUsage
	var finishReason string
	var sawTerminal bool

	err := readSSEFrames(ctx, body, func(frame sseRawFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			sawTerminal = true
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
			return fmt.Errorf("gemini stream error: %s", msg)
		}
		if um, ok := chunk["usageMetadata"].(map[string]any); ok {
			input := max(0, intFromAny(um["promptTokenCount"])) + max(0, intFromAny(um["toolUsePromptTokenCount"]))
			usage.CacheRead = min(input, max(0, intFromAny(um["cachedContentTokenCount"])))
			usage.Input = input - usage.CacheRead
			usage.Output = max(0, intFromAny(um["candidatesTokenCount"]))
			usage.Reasoning = max(0, intFromAny(um["thoughtsTokenCount"]))
		}
		cands, _ := chunk["candidates"].([]any)
		for _, rawCand := range cands {
			cand, _ := rawCand.(map[string]any)
			if cand == nil {
				continue
			}
			if fr, _ := cand["finishReason"].(string); fr != "" {
				finishReason = fr
				sawTerminal = true
			}
			content, _ := cand["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if part == nil {
					continue
				}
				sig, _ := part["thoughtSignature"].(string)
				if fc, ok := part["functionCall"].(map[string]any); ok {
					name, _ := fc["name"].(string)
					if strings.TrimSpace(name) == "" {
						continue
					}
					argsRaw, _ := json.Marshal(fc["args"])
					if len(argsRaw) == 0 || string(argsRaw) == "null" {
						argsRaw = []byte("{}")
					}
					emit(StreamEvent{
						Kind: EventToolCallReady,
						ToolCall: &ToolCall{
							ID:        "call_gem_" + rand.Text(),
							Name:      name,
							Input:     argsRaw,
							Signature: sig,
						},
					})
					continue
				}
				txt, _ := part["text"].(string)
				if thought, _ := part["thought"].(bool); thought {
					thoughtOpen = true
					if txt != "" {
						emit(StreamEvent{Kind: EventThinkingDelta, BlockIndex: thoughtBlockIdx, Delta: txt})
					}
					if sig != "" {
						emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: thoughtBlockIdx, Delta: sig})
						emit(StreamEvent{Kind: EventThinkingEnd, BlockIndex: thoughtBlockIdx})
						thoughtBlockIdx++
						thoughtOpen = false
					}
				} else {
					if sig != "" {
						emit(StreamEvent{Kind: EventSignatureDelta, BlockIndex: thoughtBlockIdx, Delta: sig})
					}
					if thoughtOpen && (sig != "" || txt != "") {
						emit(StreamEvent{Kind: EventThinkingEnd, BlockIndex: thoughtBlockIdx})
						thoughtBlockIdx++
						thoughtOpen = false
					}
					if txt != "" {
						emit(StreamEvent{Kind: EventTextDelta, Delta: txt})
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !sawTerminal {
		return ErrIncompleteStream
	}
	if usage.Input > 0 || usage.Output > 0 || usage.CacheRead > 0 || usage.CacheWrite > 0 || usage.Reasoning > 0 {
		emit(StreamEvent{Kind: EventUsage, Usage: &usage})
	}
	outFinish := "stop"
	switch strings.ToUpper(strings.TrimSpace(finishReason)) {
	case "MAX_TOKENS":
		outFinish = "length"
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "IMAGE_SAFETY":
		outFinish = "content_filter"
	}
	emit(StreamEvent{Kind: EventFinish, Finish: outFinish})
	return nil
}
