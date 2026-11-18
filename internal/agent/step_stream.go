package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

type stepStreamResult struct {
	asstBlocks     []ContentBlock
	toolCalls      []ToolCall
	visibleTextLen int
	stepFinish     string
	emittedVisible bool
}

type thinkSegment struct {
	id              string
	blockIdx        int
	text            strings.Builder
	sig             strings.Builder
	redacted        string
	started         bool
	sealed          bool
	emittedSig      string
	emittedRedacted string
}

type textSegment struct {
	id      string
	text    strings.Builder
	started bool
	sealed  bool
}

type orderedSegRef struct {
	isThink bool
	idx     int
}

func (e *Engine) reportStreamOutcome(ctx context.Context, target llm.StreamTarget, latency time.Duration, streamErr error) {
	if e == nil || e.stream == nil || ctx.Err() != nil {
		return
	}
	reporter, ok := e.stream.(llm.StreamOutcomeReporter)
	if !ok {
		return
	}
	reporter.RecordStreamOutcome(target.ProviderID, target.ModelID, latency.Milliseconds(), streamErr)
}

// collectStepStream parses one model SSE stream while preserving per-block
// thinking/redacted_thinking/signature/text boundaries and updating sealed
// thinking parts in place by PartID instead of duplicating `think_sig` parts.
func collectStepStream(
	ctx context.Context,
	drv ModelDriver,
	body io.Reader,
	target llm.StreamTarget,
	step int,
	nextPartID func(string) string,
	emit func(EngineEvent),
) (stepStreamResult, error) {
	var res stepStreamResult
	var thinkSegs []*thinkSegment
	var textSegs []*textSegment
	var ordered []orderedSegRef
	var activeThink *thinkSegment
	var activeText *textSegment

	emitThinkEnd := func(seg *thinkSegment) {
		if seg == nil || (!seg.started && seg.sig.Len() == 0 && seg.redacted == "") {
			return
		}
		if seg.id == "" {
			seg.id = nextPartID("think")
		}
		var meta map[string]any
		if seg.redacted != "" {
			meta = map[string]any{"redactedData": seg.redacted}
		}
		sigStr := seg.sig.String()
		emit(EngineEvent{
			Kind:      EngineThinkingEnd,
			Step:      step,
			PartID:    seg.id,
			Text:      seg.text.String(),
			Signature: sigStr,
			Metadata:  meta,
		})
		seg.emittedSig = sigStr
		seg.emittedRedacted = seg.redacted
	}

	sealActiveThink := func() {
		if activeThink == nil {
			return
		}
		if !activeThink.sealed {
			activeThink.sealed = true
			emitThinkEnd(activeThink)
		}
		activeThink = nil
	}

	sealActiveText := func() {
		if activeText == nil {
			return
		}
		if !activeText.sealed {
			activeText.sealed = true
			if activeText.started {
				emit(EngineEvent{
					Kind:   EngineTextEnd,
					Step:   step,
					PartID: activeText.id,
					Text:   activeText.text.String(),
				})
			}
		}
		activeText = nil
	}

	findOrCreateThinkSeg := func(blockIdx int) *thinkSegment {
		if blockIdx > 0 {
			for _, seg := range thinkSegs {
				if seg.blockIdx == blockIdx {
					return seg
				}
			}
		}
		seg := &thinkSegment{
			id:       nextPartID("think"),
			blockIdx: blockIdx,
		}
		idx := len(thinkSegs)
		thinkSegs = append(thinkSegs, seg)
		ordered = append(ordered, orderedSegRef{isThink: true, idx: idx})
		return seg
	}

	parseErr := drv.ParseSSE(ctx, body, func(se StreamEvent) {
		switch se.Kind {
		case EventThinkingDelta:
			if se.Delta == "" {
				return
			}
			res.emittedVisible = true
			sealActiveText()
			if activeThink != nil && se.BlockIndex > 0 && activeThink.blockIdx > 0 && se.BlockIndex != activeThink.blockIdx {
				sealActiveThink()
			}
			if activeThink == nil {
				activeThink = findOrCreateThinkSeg(se.BlockIndex)
			}
			if !activeThink.started {
				activeThink.started = true
				emit(EngineEvent{Kind: EngineThinkingStart, Step: step, PartID: activeThink.id})
			}
			activeThink.text.WriteString(se.Delta)
			emit(EngineEvent{Kind: EngineThinkingDelta, Step: step, PartID: activeThink.id, Text: se.Delta})

		case EventSignatureDelta:
			if se.Delta == "" {
				return
			}
			var targetSeg *thinkSegment
			if se.BlockIndex > 0 {
				if activeThink != nil && activeThink.blockIdx != se.BlockIndex {
					sealActiveThink()
				}
				targetSeg = findOrCreateThinkSeg(se.BlockIndex)
				if !targetSeg.sealed {
					activeThink = targetSeg
				}
			} else if activeThink != nil {
				targetSeg = activeThink
			} else if len(thinkSegs) > 0 {
				targetSeg = thinkSegs[len(thinkSegs)-1]
			} else {
				targetSeg = findOrCreateThinkSeg(0)
				activeThink = targetSeg
			}
			targetSeg.sig.WriteString(se.Delta)

		case EventRedactedThinking:
			if se.Delta == "" {
				return
			}
			sealActiveText()
			sealActiveThink()
			seg := findOrCreateThinkSeg(se.BlockIndex)
			seg.redacted = se.Delta
			seg.sealed = true
			emitThinkEnd(seg)

		case EventThinkingEnd:
			if activeThink != nil && (se.BlockIndex == 0 || activeThink.blockIdx == se.BlockIndex) {
				sealActiveThink()
			}

		case EventTextDelta:
			if se.Delta == "" {
				return
			}
			res.emittedVisible = true
			sealActiveThink()
			if activeText == nil {
				seg := &textSegment{id: nextPartID("text"), started: true}
				idx := len(textSegs)
				textSegs = append(textSegs, seg)
				ordered = append(ordered, orderedSegRef{isThink: false, idx: idx})
				activeText = seg
				emit(EngineEvent{Kind: EngineTextStart, Step: step, PartID: seg.id})
			}
			activeText.text.WriteString(se.Delta)
			res.visibleTextLen += len(se.Delta)
			emit(EngineEvent{Kind: EngineTextDelta, Step: step, PartID: activeText.id, Text: se.Delta})

		case EventToolCallReady:
			if se.ToolCall != nil {
				res.toolCalls = append(res.toolCalls, *se.ToolCall)
			}

		case EventUsage:
			if se.Usage != nil {
				u := *se.Usage
				if u.CostUSD <= 0 {
					u.CostUSD = target.Cost.ComputeUSD(u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite)
				}
				emit(EngineEvent{Kind: EngineTurnUsage, Step: step, Usage: &u})
			}

		case EventFinish:
			if se.Finish != "" {
				res.stepFinish = se.Finish
			}
		}
	})

	sealActiveThink()
	sealActiveText()
	for _, seg := range thinkSegs {
		if seg.sealed && (seg.sig.String() != seg.emittedSig || seg.redacted != seg.emittedRedacted) {
			emitThinkEnd(seg)
		}
	}

	for _, ref := range ordered {
		if ref.isThink {
			seg := thinkSegs[ref.idx]
			txt := seg.text.String()
			sig := seg.sig.String()
			if txt != "" || sig != "" || seg.redacted != "" {
				res.asstBlocks = append(res.asstBlocks, ContentBlock{
					Type:      ContentThinking,
					Text:      txt,
					Signature: sig,
					Data:      seg.redacted,
				})
			}
		} else {
			seg := textSegs[ref.idx]
			if txt := seg.text.String(); txt != "" {
				res.asstBlocks = append(res.asstBlocks, ContentBlock{
					Type: ContentText,
					Text: txt,
				})
			}
		}
	}
	for _, tc := range res.toolCalls {
		res.asstBlocks = append(res.asstBlocks, ContentBlock{
			Type:      ContentToolUse,
			ID:        tc.ID,
			Name:      tc.Name,
			Input:     tc.Input,
			Signature: tc.Signature,
		})
	}
	return res, parseErr
}

func isUnsupportedAnthropicMessagesEndpoint(err error) bool {
	var httpErr *llm.StreamHTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return false
	}
	return httpErr.StatusCode == http.StatusNotFound ||
		httpErr.StatusCode == http.StatusMethodNotAllowed ||
		httpErr.StatusCode == http.StatusNotImplemented
}

// isRecoverableThinkingSignatureError reports whether err is an upstream 400 /
// invalid_request_error rejecting replayed thinking blocks or signatures (e.g.
// across heterogeneous local reverse-proxy channels or /v1/chat/completions
// relays that drop signatures while forcing Claude thinking upstream).
func isRecoverableThinkingSignatureError(err error) bool {
	if err == nil || isContextOverflowError(err) {
		return false
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "thoughtsignature") || strings.Contains(lower, "thought_signature") {
		return true
	}
	if !strings.Contains(lower, "thinking") && !strings.Contains(lower, "redacted_thinking") {
		return false
	}
	return strings.Contains(lower, "signature") ||
		strings.Contains(lower, "must start with a thinking block") ||
		strings.Contains(lower, "expected `thinking`") ||
		strings.Contains(lower, "expected 'thinking'") ||
		strings.Contains(lower, "cannot be modified")
}

func tailToolExchangeHasSignedThinking(msgs []Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != RoleAssistant {
			continue
		}
		hasToolUse := false
		hasSignedThink := false
		for _, b := range msgs[i].Content {
			if b.Type == ContentToolUse {
				hasToolUse = true
			}
			if b.Type == ContentThinking && (b.Signature != "" || b.Data != "") {
				hasSignedThink = true
			}
		}
		if !hasToolUse {
			return true
		}
		return hasSignedThink
	}
	return true
}

// recoverMessagesFromThinkingSignatureError strips invalidated thinking blocks
// and folds unsigned [assistant(tool_use) -> user(tool_result)] exchanges into
// structured <completed_tool_steps> user context so forced-thinking proxy models
// (e.g. `-high` presets on Vertex/Antigravity relays) never see an unsigned
// assistant tool_use message while retaining 100% of executed tool outputs.
func recoverMessagesFromThinkingSignatureError(msgs []Message) ([]Message, bool) {
	changed := false
	cleaned := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		var kept []ContentBlock
		for _, b := range m.Content {
			if b.Type == ContentThinking {
				changed = true
				continue
			}
			if b.Type == ContentToolUse && b.Signature != "" {
				b.Signature = ""
				changed = true
			}
			kept = append(kept, b)
		}
		if len(kept) == 0 {
			changed = true
			continue
		}
		if len(cleaned) > 0 && cleaned[len(cleaned)-1].Role == m.Role {
			cleaned[len(cleaned)-1].Content = append(cleaned[len(cleaned)-1].Content, kept...)
			changed = true
			continue
		}
		cleaned = append(cleaned, Message{Role: m.Role, Content: kept})
	}

	out := make([]Message, 0, len(cleaned))
	appendBlocks := func(role Role, blocks []ContentBlock) {
		if len(blocks) == 0 {
			return
		}
		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Content = append(out[len(out)-1].Content, blocks...)
			return
		}
		copied := make([]ContentBlock, len(blocks))
		copy(copied, blocks)
		out = append(out, Message{Role: role, Content: copied})
	}

	for i := 0; i < len(cleaned); {
		m := cleaned[i]
		if m.Role == RoleAssistant {
			var notes []string
			var uses []ContentBlock
			for _, b := range m.Content {
				switch b.Type {
				case ContentText:
					if strings.TrimSpace(b.Text) != "" {
						notes = append(notes, strings.TrimSpace(b.Text))
					}
				case ContentToolUse:
					uses = append(uses, b)
				}
			}
			if len(uses) > 0 {
				changed = true
				resultsByID := make(map[string]string, len(uses))
				var extraUserBlocks []ContentBlock
				if i+1 < len(cleaned) && cleaned[i+1].Role == RoleUser {
					for _, ub := range cleaned[i+1].Content {
						if ub.Type == ContentToolResult {
							resultsByID[ub.ToolUseID] = ub.Text
						} else {
							extraUserBlocks = append(extraUserBlocks, ub)
						}
					}
					i += 2
				} else {
					i++
				}

				var sb strings.Builder
				sb.WriteString("<completed_tool_steps>\n")
				if len(notes) > 0 {
					sb.WriteString("Assistant note: ")
					sb.WriteString(strings.Join(notes, "\n"))
					sb.WriteByte('\n')
				}
				for _, u := range uses {
					inStr := strings.TrimSpace(string(u.Input))
					if inStr == "" {
						inStr = "{}"
					}
					sb.WriteString(fmt.Sprintf("[Tool `%s` input: %s]\nResult:\n%s\n\n", u.Name, inStr, resultsByID[u.ID]))
				}
				sb.WriteString("</completed_tool_steps>\n[System notice: upstream rejected replayed thinking signatures for the prior tool step. All tool outputs above have already been executed in this workspace — continue directly from these results without re-running completed commands.]")

				folded := make([]ContentBlock, 0, 1+len(extraUserBlocks))
				folded = append(folded, ContentBlock{Type: ContentText, Text: sb.String()})
				folded = append(folded, extraUserBlocks...)
				appendBlocks(RoleUser, folded)
				continue
			}
		}
		appendBlocks(m.Role, m.Content)
		i++
	}
	return out, changed
}

// messagesRequireThinkingDisabled reports whether msgs still has an assistant
// message after the latest plain user prompt where Anthropic would reject
// `thinking` enabled without a signed thinking block.
func messagesRequireThinkingDisabled(msgs []Message) bool {
	if len(msgs) == 0 {
		return false
	}
	lastPlainUser := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != RoleUser {
			continue
		}
		hasToolResult := false
		for _, b := range msgs[i].Content {
			if b.Type == ContentToolResult {
				hasToolResult = true
				break
			}
		}
		if !hasToolResult {
			lastPlainUser = i
			break
		}
	}
	if lastPlainUser < 0 {
		return true
	}
	return lastPlainUser < len(msgs)-1
}
