package agent

import (
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	// geminiSyntheticThoughtSignature is Google's documented validator bypass
	// sentinel for replaying historical functionCall parts originally produced
	// by another model (or without a thoughtSignature) on Gemini thinking models.
	geminiSyntheticThoughtSignature = "skip_thought_signature_validator"

	interruptedToolResultNotice = "[Tool execution was interrupted before a result was recorded.]"
)

// hardenMessagesForRequest enforces structural invariants required across
// Anthropic, OpenAI, and Gemini APIs without modifying well-formed histories
// (preserving byte-identical prefixes for upstream KV prompt caches):
//   - when coalesceSameRole is true (Anthropic / Gemini), coalesces consecutive
//     same-role messages (e.g. steering appended to a tool-result turn),
//   - ensures every assistant ContentToolUse has a matching ContentToolResult in
//     the immediately following user turn (synthesizing an interrupted notice on
//     interrupted/recovered sessions),
//   - converts orphaned or duplicate ContentToolResult blocks into plain text so
//     context is preserved without triggering upstream 400 tool_use_id errors.
func hardenMessagesForRequest(msgs []Message, coalesceSameRole bool) []Message {
	if len(msgs) == 0 || isRequestHistoryWellFormed(msgs, coalesceSameRole) {
		return msgs
	}

	// Pass 1: drop empty non-system turns and optionally coalesce adjacent same-role messages.
	coalesced := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == RoleSystem {
			coalesced = append(coalesced, m)
			continue
		}
		if len(m.Content) == 0 {
			continue
		}
		if coalesceSameRole && len(coalesced) > 0 && coalesced[len(coalesced)-1].Role == m.Role {
			prev := coalesced[len(coalesced)-1]
			merged := make([]ContentBlock, len(prev.Content)+len(m.Content))
			copy(merged, prev.Content)
			copy(merged[len(prev.Content):], m.Content)
			coalesced[len(coalesced)-1] = Message{Role: prev.Role, Content: merged}
			continue
		}
		blocks := make([]ContentBlock, len(m.Content))
		copy(blocks, m.Content)
		coalesced = append(coalesced, Message{Role: m.Role, Content: blocks})
	}

	// Pass 2: pair every assistant tool_use with a following user tool_result and
	// neutralize orphaned or duplicate tool_result blocks.
	out := make([]Message, 0, len(coalesced)+2)
	for i := 0; i < len(coalesced); i++ {
		m := coalesced[i]
		if m.Role == RoleSystem {
			out = append(out, m)
			continue
		}
		if m.Role == RoleAssistant {
			callIDs := collectToolUseIDs(m.Content)
			out = append(out, m)
			if len(callIDs) == 0 {
				continue
			}
			expected := make(map[string]bool, len(callIDs))
			for _, id := range callIDs {
				expected[id] = true
			}
			var nextBlocks []ContentBlock
			if i+1 < len(coalesced) && coalesced[i+1].Role == RoleUser {
				nextBlocks = coalesced[i+1].Content
				i++
			}
			seen := make(map[string]bool, len(callIDs))
			cleaned := make([]ContentBlock, 0, len(nextBlocks)+len(callIDs))
			for _, b := range nextBlocks {
				if b.Type != ContentToolResult {
					cleaned = append(cleaned, b)
					continue
				}
				if b.ToolUseID != "" && expected[b.ToolUseID] && !seen[b.ToolUseID] {
					seen[b.ToolUseID] = true
					cleaned = append(cleaned, b)
					continue
				}
				cleaned = append(cleaned, orphanToolResultAsText(b))
			}
			var missing []ContentBlock
			for _, id := range callIDs {
				if !seen[id] {
					missing = append(missing, ContentBlock{
						Type:      ContentToolResult,
						ToolUseID: id,
						Text:      interruptedToolResultNotice,
						IsError:   true,
					})
				}
			}
			if len(missing) > 0 {
				cleaned = append(missing, cleaned...)
			}
			out = append(out, Message{Role: RoleUser, Content: cleaned})
			continue
		}

		// RoleUser not immediately preceded by an assistant tool_use turn: any
		// ContentToolResult here is orphaned.
		cleaned := make([]ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if b.Type == ContentToolResult {
				cleaned = append(cleaned, orphanToolResultAsText(b))
				continue
			}
			cleaned = append(cleaned, b)
		}
		if coalesceSameRole && len(out) > 0 && out[len(out)-1].Role == RoleUser {
			prev := out[len(out)-1]
			merged := make([]ContentBlock, len(prev.Content)+len(cleaned))
			copy(merged, prev.Content)
			copy(merged[len(prev.Content):], cleaned)
			out[len(out)-1] = Message{Role: RoleUser, Content: merged}
			continue
		}
		out = append(out, Message{Role: RoleUser, Content: cleaned})
	}
	return out
}

func isRequestHistoryWellFormed(msgs []Message, coalesceSameRole bool) bool {
	var prevRole Role
	var pendingCalls map[string]bool
	for i, m := range msgs {
		if m.Role == RoleSystem {
			continue
		}
		if len(m.Content) == 0 {
			return false
		}
		if coalesceSameRole && prevRole == m.Role {
			return false
		}
		if m.Role == RoleAssistant {
			if len(pendingCalls) > 0 {
				return false
			}
			ids := collectToolUseIDs(m.Content)
			if len(ids) > 0 {
				if i == len(msgs)-1 {
					return false
				}
				pendingCalls = make(map[string]bool, len(ids))
				for _, id := range ids {
					pendingCalls[id] = true
				}
			}
		} else if m.Role == RoleUser {
			seen := make(map[string]bool, len(pendingCalls))
			for _, b := range m.Content {
				if b.Type != ContentToolResult {
					continue
				}
				if b.ToolUseID == "" || !pendingCalls[b.ToolUseID] || seen[b.ToolUseID] {
					return false
				}
				seen[b.ToolUseID] = true
			}
			if len(seen) != len(pendingCalls) {
				return false
			}
			pendingCalls = nil
		}
		prevRole = m.Role
	}
	return len(pendingCalls) == 0
}

func collectToolUseIDs(blocks []ContentBlock) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, b := range blocks {
		if b.Type == ContentToolUse && b.ID != "" && !seen[b.ID] {
			seen[b.ID] = true
			ids = append(ids, b.ID)
		}
	}
	return ids
}

func orphanToolResultAsText(b ContentBlock) ContentBlock {
	txt := strings.TrimSpace(b.Text)
	if txt == "" {
		txt = "(empty)"
	}
	return ContentBlock{
		Type: ContentText,
		Text: fmt.Sprintf("[Prior tool output (%s)]:\n%s", b.ToolUseID, txt),
	}
}

func geminiNeedsThoughtSignatureFallback(target llm.StreamTarget, hasThinkingConfig bool) bool {
	if hasThinkingConfig {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(target.ModelID))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "gemini-1.") || strings.Contains(lower, "gemini-2.0") {
		return false
	}
	return strings.Contains(lower, "gemini-2.5") ||
		strings.Contains(lower, "gemini-3") ||
		strings.Contains(lower, "gemini-4") ||
		strings.Contains(lower, "thinking")
}
