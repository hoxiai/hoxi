package agent

import (
	"context"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const inPlaceCompactionPromptText = `<context_compaction_request>
This conversation is approaching the context budget and is being compacted into a technical checkpoint so work can continue seamlessly without older raw messages.

Do NOT call any tools. Respond with plain Markdown only.

Produce a dense, lossless technical checkpoint preserving verbatim:
1. Core user goal, active requirements, and explicit constraints/preferences.
2. Exact file paths read, created, or modified, plus key function/type symbols and architectural invariants.
3. Completed milestones, verified test/build results, and any errors encountered + how they were resolved.
4. Remaining TODO items and the exact next technical step.

Later explicit user corrections supersede earlier instructions. Output only the checkpoint content.
</context_compaction_request>`

// inPlaceCompactionContext carries the active main-turn prefix parameters so
// proactive compaction can request a handoff summary in-place, reusing the
// provider's warm KV-cache prefix instead of re-sending an uncached transcript.
type inPlaceCompactionContext struct {
	Driver         ModelDriver
	Target         llm.StreamTarget
	SessionID      string
	CacheKey       string
	System         string
	SystemSuffix   string
	Tools          []ToolSpec
	Messages       []Message
	MaxTokens      int
	Effort         string
	FastMode       bool
	CacheWarm      bool
	MeasuredTokens int
}

const minInPlaceCompactionHeadroom = 2048

func (c *inPlaceCompactionContext) eligible() bool {
	if c == nil || c.Driver == nil || !c.Target.HasEndpoint || !c.CacheWarm {
		return false
	}
	if c.Target.ContextLimit <= 0 {
		return false
	}
	// Require enough room in the window for the compaction instruction + output
	// so the in-place call will not immediately trip a 400 context overflow.
	return c.MeasuredTokens+minInPlaceCompactionHeadroom < c.Target.ContextLimit
}

// buildInPlaceCompactionMessages clones msgs and appends the compaction prompt
// while keeping messages[0 : len(msgs)-1] byte-identical (preserving upstream
// prompt-cache checkpoints) and maintaining strict user/assistant alternation.
func buildInPlaceCompactionMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs), len(msgs)+1)
	copy(out, msgs)
	promptBlock := ContentBlock{
		Type: ContentText,
		Text: inPlaceCompactionPromptText,
	}
	if len(out) > 0 && out[len(out)-1].Role == RoleUser {
		last := out[len(out)-1]
		blocks := make([]ContentBlock, len(last.Content)+1)
		copy(blocks, last.Content)
		blocks[len(last.Content)] = promptBlock
		last.Content = blocks
		out[len(out)-1] = last
		return out
	}
	return append(out, Message{
		Role:    RoleUser,
		Content: []ContentBlock{promptBlock},
	})
}

// tryInPlaceCompaction requests a checkpoint summary using the exact same
// system prompt, tools, effort, and message prefix as the active turn so the
// provider hits its warm prompt cache. Any tool call, truncation, empty reply,
// or non-cancellation stream error returns ("", false) so the caller falls back
// transparently to chunked transcript summarization.
func (e *Engine) tryInPlaceCompaction(
	ctx context.Context,
	inPlace *inPlaceCompactionContext,
	msgs []Message,
	record func(TokenUsage),
) (string, bool) {
	if e == nil || e.stream == nil || !inPlace.eligible() || ctx.Err() != nil {
		return "", false
	}
	sourceMsgs := msgs
	if len(inPlace.Messages) > 0 {
		sourceMsgs = inPlace.Messages
	}
	if len(sourceMsgs) == 0 {
		return "", false
	}
	httpReq, err := inPlace.Driver.BuildRequest(ModelRequest{
		SessionID:    inPlace.SessionID,
		CacheKey:     inPlace.CacheKey,
		Target:       inPlace.Target,
		System:       inPlace.System,
		SystemSuffix: inPlace.SystemSuffix,
		Messages:     buildInPlaceCompactionMessages(sourceMsgs),
		Tools:        inPlace.Tools,
		MaxTokens:    inPlace.MaxTokens,
		Effort:       inPlace.Effort,
		FastMode:     inPlace.FastMode,
	})
	if err != nil {
		return "", false
	}
	openStarted := time.Now()
	resp, err := e.stream.OpenStream(ctx, httpReq)
	headerLatency := time.Since(openStarted)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	var out strings.Builder
	var finishReason string
	var sawToolCall bool
	if err := inPlace.Driver.ParseSSE(ctx, resp.Body, func(se StreamEvent) {
		switch se.Kind {
		case EventUsage:
			if se.Usage != nil && record != nil {
				record(pricedUsage(inPlace.Target, *se.Usage))
			}
		case EventTextDelta:
			if se.Delta != "" {
				out.WriteString(se.Delta)
			}
		case EventToolCallReady, EventTruncatedTool:
			sawToolCall = true
		case EventFinish:
			if se.Finish != "" {
				finishReason = se.Finish
			}
		}
	}); err != nil {
		e.reportStreamOutcome(ctx, inPlace.Target, time.Since(openStarted), err)
		return "", false
	}
	e.reportStreamOutcome(ctx, inPlace.Target, headerLatency, nil)
	if sawToolCall || isTruncatedFinishReason(finishReason) {
		return "", false
	}
	summary := strings.TrimSpace(out.String())
	if summary == "" {
		return "", false
	}
	return summary, true
}
