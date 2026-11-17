package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	compactTriggerPercent       = 75
	compactHardCapPercent       = 90
	compactMinKeepRecentMsgs    = 6
	compactMediumKeepRecentMsgs = 14
	compactLargeKeepRecentMsgs  = 20
	// compactChunkStride quantizes the pruning cutoff to fixed message-index
	// boundaries so rebuilt turn histories keep a byte-identical prefix across
	// consecutive steps and turns instead of shifting cutoff by +1/+2 each turn
	// and invalidating upstream LLM prompt caches.
	compactChunkStride        = 12
	compactTightMinToolLen    = 800
	compactStandardMinToolLen = 2400
)

func estimateTextWeight(s string) int {
	nonASCII := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 && (s[i]&0xC0) != 0x80 {
			nonASCII++
		}
	}
	return len(s) + nonASCII
}

func estimateTokens(msgs []Message, system string) int {
	chars := estimateTextWeight(system)
	for _, m := range msgs {
		for _, b := range m.Content {
			chars += estimateTextWeight(b.Text) + len(b.Input) + 16
			if b.Type == ContentMedia && b.Data != "" {
				chars += 768
			}
		}
	}
	return chars/3 + 64
}

func estimateToolSpecsTokens(specs []ToolSpec) int {
	if len(specs) == 0 {
		return 0
	}
	chars := 0
	for _, s := range specs {
		chars += len(s.Name) + estimateTextWeight(s.Description) + 24
		if len(s.Parameters) > 0 {
			if raw, err := json.Marshal(s.Parameters); err == nil {
				chars += len(raw)
			}
		}
	}
	return chars / 3
}

func effectiveCompactContextLimit(contextLimit int, specs []ToolSpec) int {
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	if contextLimit < 16000 || len(specs) == 0 {
		return contextLimit
	}
	schemaTokens := estimateToolSpecsTokens(specs)
	adjusted := contextLimit - schemaTokens
	floor := contextLimit / 2
	if adjusted < floor {
		return floor
	}
	return adjusted
}

func preferredKeepRecentMsgs(contextLimit int) int {
	switch {
	case contextLimit >= 64000:
		return compactLargeKeepRecentMsgs
	case contextLimit >= 16000:
		return compactMediumKeepRecentMsgs
	default:
		return compactMinKeepRecentMsgs
	}
}

func preferredMinToolLen(contextLimit int) int {
	if contextLimit >= 32000 {
		return compactStandardMinToolLen
	}
	return compactTightMinToolLen
}

// PruneOldToolResults compacts large older tool outputs once estimated tokens
// exceed 75% of contextLimit while quantizing the cutoff index to preserve
// upstream KV-cache prefix stability across consecutive turns.
func PruneOldToolResults(msgs []Message, system string, contextLimit int) []Message {
	return pruneOldToolResults(msgs, system, contextLimit)
}

func pruneOldToolResults(msgs []Message, system string, contextLimit int) []Message {
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	triggerTokens := contextLimit * compactTriggerPercent / 100
	hardCapTokens := contextLimit * compactHardCapPercent / 100
	if estimateTokens(msgs, system) <= triggerTokens {
		return msgs
	}

	keepRecent := preferredKeepRecentMsgs(contextLimit)
	minToolLen := preferredMinToolLen(contextLimit)
	richPreview := contextLimit >= 32000
	rawCutoff := len(msgs) - keepRecent

	pruned := msgs
	if rawCutoff > 0 {
		quantizedCutoff := (rawCutoff / compactChunkStride) * compactChunkStride
		if quantizedCutoff <= 0 {
			pruned = pruneToolOutputsBeforeWithOpts(msgs, rawCutoff, minToolLen, richPreview)
		} else {
			for stride := compactChunkStride; stride <= quantizedCutoff; stride += compactChunkStride {
				pruned = pruneToolOutputsBeforeWithOpts(msgs, stride, minToolLen, richPreview)
				if estimateTokens(pruned, system) <= triggerTokens {
					break
				}
			}
		}

		if estimateTokens(pruned, system) > hardCapTokens {
			pruned = pruneToolOutputsBeforeWithOpts(msgs, rawCutoff, minToolLen, richPreview)
		}
	}

	if estimateTokens(pruned, system) > hardCapTokens {
		tightCutoff := len(msgs) - compactMinKeepRecentMsgs
		if tightCutoff > 0 {
			pruned = pruneToolOutputsBeforeWithOpts(msgs, tightCutoff, compactTightMinToolLen, false)
		}
	}
	return pruned
}

func pruneToolOutputsBeforeWithOpts(msgs []Message, cutoff, minToolLen int, richPreview bool) []Message {
	if cutoff <= 0 {
		return msgs
	}
	if cutoff > len(msgs) {
		cutoff = len(msgs)
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < cutoff; i++ {
		if out[i].Role != RoleUser {
			continue
		}
		changed := false
		blocks := append([]ContentBlock(nil), out[i].Content...)
		for j := range blocks {
			if blocks[j].Type == ContentToolResult && len(blocks[j].Text) > minToolLen &&
				!strings.HasPrefix(blocks[j].Text, "[tool output pruned:") {
				blocks[j].Text = summarizePrunedToolOutput(blocks[j].Text, richPreview)
				changed = true
			} else if blocks[j].Type == ContentMedia && blocks[j].Data != "" {
				blocks[j] = ContentBlock{
					Type: ContentText,
					Text: fmt.Sprintf("[omitted earlier media attachment: %s (%s)]", blocks[j].Filename, blocks[j].MimeType),
				}
				changed = true
			}
		}
		if changed {
			out[i].Content = blocks
		}
	}
	return out
}

func summarizePrunedToolOutput(s string, richPreview bool) string {
	headLines := 4
	tailLines := 6
	maxHead := 360
	maxTail := 520
	if richPreview {
		headLines = 8
		tailLines = 8
		maxHead = 640
		maxTail = 720
	}
	norm := strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	var preview string
	if len(lines) <= headLines+tailLines {
		if len(norm) > maxHead+maxTail {
			preview = norm[:maxHead] + "\n... [middle omitted] ...\n" + norm[len(norm)-maxTail:]
		} else {
			preview = norm
		}
	} else {
		head := strings.Join(lines[:headLines], "\n")
		if len(head) > maxHead {
			head = head[:maxHead] + "..."
		}
		tail := strings.Join(lines[len(lines)-tailLines:], "\n")
		if len(tail) > maxTail {
			tail = "..." + tail[len(tail)-maxTail:]
		}
		omitted := len(lines) - headLines - tailLines
		preview = fmt.Sprintf("%s\n... [%d lines omitted] ...\n%s", head, omitted, tail)
	}
	return fmt.Sprintf("[tool output pruned: %d chars; use read with offset/limit only if specific omitted lines are needed]\n%s", len(s), preview)
}
