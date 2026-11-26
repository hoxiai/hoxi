package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/llm"
)

var errSummaryTruncated = errors.New("summarization truncated at output token limit")

const compactionSystemPrompt = `You are a senior software engineering context compaction engine.
Distill the provided conversation transcript into a dense, lossless technical checkpoint in Markdown so work can continue seamlessly without prior raw messages.

Preserve verbatim:
1. Core user goal, active requirements, and explicit constraints/preferences.
2. Exact file paths read, created, or modified, plus key function/type symbols and architectural invariants.
3. Completed milestones, verified test/build results, and any errors encountered + how they were resolved.
4. Remaining TODO items and exact next technical step.

The input may be a contiguous transcript segment or ordered checkpoints from earlier segments.
Preserve fragmentary requirements even when their surrounding context is in another segment.
When merging checkpoints, retain their facts and chronology; later explicit user corrections supersede earlier instructions.`

// Summarize covers the entire transcript in bounded, ordered chunks, then merges
// their checkpoints. Nothing is discarded before the model sees it. A failed
// chunk or non-converging reduction fails the operation without a partial summary.
func (e *Engine) Summarize(ctx context.Context, providerID, modelID, transcript string) (string, error) {
	return e.SummarizeWithUsage(ctx, providerID, modelID, transcript, nil)
}

func (e *Engine) SummarizeWithUsage(ctx context.Context, providerID, modelID, transcript string, record func(TokenUsage)) (string, error) {
	if e.stream == nil {
		return "", fmt.Errorf("native stream client is not configured")
	}
	target := e.stream.ResolveStreamTarget(providerID, modelID)
	if !target.HasEndpoint {
		return "", fmt.Errorf("no HTTP endpoint configured for %s/%s", providerID, modelID)
	}
	drv, err := e.drivers.Create(target.Protocol)
	if err != nil {
		return "", err
	}
	maxOut := 8192
	if target.OutputLimit > 0 && target.OutputLimit < maxOut {
		maxOut = target.OutputLimit
	}
	contextLimit := target.ContextLimit
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	maxOut = min(maxOut, contextLimit/4)
	// One byte per input token is deliberately conservative for arbitrary code
	// and multilingual text. Reserve space for the prompt and response framing.
	chunkBytes := min(100000, contextLimit-maxOut-2048)
	if chunkBytes < 1024 || maxOut <= 0 {
		return "", fmt.Errorf("model context window is too small for summarization")
	}
	if strings.TrimSpace(transcript) == "" {
		return "", fmt.Errorf("summarization transcript is empty")
	}
	const maxSummaryCalls = 256
	const minAdaptiveChunkBytes = 4096
	calls := 0
	for pass := 0; pass < 8; pass++ {
		pending := splitSummaryTranscript(transcript, chunkBytes)
		var summaries []string
		for len(pending) > 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if calls+len(pending) > maxSummaryCalls {
				return "", fmt.Errorf("summarization exceeds %d requests; original context retained", maxSummaryCalls)
			}
			chunk := pending[0]
			summary, err := e.summarizeChunkWithFallback(ctx, &target, &drv, maxOut, chunk, record)
			calls++
			if err != nil {
				// Overflowing or truncated segments are re-split instead of failing, so
				// checkpoints already paid for in earlier segments are kept.
				if ctx.Err() == nil && len(chunk) > minAdaptiveChunkBytes && (isContextOverflowError(err) || errors.Is(err, errSummaryTruncated)) {
					// Earlier segments already have complete checkpoints. Resize only
					// the rejected segment and the source that still follows it.
					chunkBytes = max(minAdaptiveChunkBytes, len(chunk)/2)
					pending = splitSummaryTranscript(strings.Join(pending, ""), chunkBytes)
					continue
				}
				return "", fmt.Errorf("summarize segment %d/%d: %w", len(summaries)+1, len(summaries)+len(pending), err)
			}
			if len(summaries) == 0 && len(pending) == 1 {
				return summary, nil
			}
			summaries = append(summaries, summary)
			pending = pending[1:]
		}
		var checkpoints strings.Builder
		for i, summary := range summaries {
			fmt.Fprintf(&checkpoints, "[Checkpoint %d/%d in chronological order]\n%s\n\n", i+1, len(summaries), summary)
		}
		next := checkpoints.String()
		if len(next) >= len(transcript) {
			return "", fmt.Errorf("summarization did not reduce context; original context retained")
		}
		transcript = next
	}
	return "", fmt.Errorf("summarization did not converge; original context retained")
}

func (e *Engine) summarizeChunkWithFallback(
	ctx context.Context,
	target *llm.StreamTarget,
	drv *ModelDriver,
	maxOut int,
	chunk string,
	record func(TokenUsage),
) (string, error) {
	summary, err := e.summarizeChunk(ctx, *target, *drv, maxOut, chunk, record)
	if err != nil && target.FallbackOpenAI && ctx.Err() == nil && isUnsupportedPromotedProtocolEndpoint(target.Protocol, err) {
		target.Protocol = llm.ProtocolOpenAI
		target.FallbackOpenAI = false
		fallbackDrv, drvErr := e.drivers.Create(target.Protocol)
		if drvErr != nil {
			return "", drvErr
		}
		*drv = fallbackDrv
		return e.summarizeChunk(ctx, *target, *drv, maxOut, chunk, record)
	}
	return summary, err
}

func splitSummaryTranscript(text string, maxBytes int) []string {
	text = ensureValidUTF8(text)
	var chunks []string
	for len(text) > maxBytes {
		end := maxBytes
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		if newline := strings.LastIndexByte(text[:end], '\n'); newline >= end/2 {
			end = newline + 1
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func (e *Engine) summarizeChunk(ctx context.Context, target llm.StreamTarget, drv ModelDriver, maxOut int, transcript string, record func(TokenUsage)) (string, error) {
	httpReq, err := drv.BuildRequest(ModelRequest{
		Target:    target,
		System:    compactionSystemPrompt,
		MaxTokens: maxOut,
		Effort:    "low",
		Messages: []Message{{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type: ContentText,
				Text: "Summarize the following engineering conversation into a structured technical checkpoint:\n\n<transcript>\n" + transcript + "\n</transcript>",
			}},
		}},
	})
	if err != nil {
		return "", err
	}
	openStarted := time.Now()
	resp, err := e.stream.OpenStream(ctx, httpReq)
	headerLatency := time.Since(openStarted)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out strings.Builder
	var finishReason string
	if err := drv.ParseSSE(ctx, resp.Body, func(se StreamEvent) {
		switch se.Kind {
		case EventUsage:
			if se.Usage != nil && record != nil {
				record(pricedUsage(target, *se.Usage))
			}
		case EventTextDelta:
			if se.Delta != "" {
				out.WriteString(se.Delta)
			}
		case EventFinish:
			if se.Finish != "" {
				finishReason = se.Finish
			}
		}
	}); err != nil {
		e.reportStreamOutcome(ctx, target, time.Since(openStarted), err)
		return "", err
	}
	e.reportStreamOutcome(ctx, target, headerLatency, nil)
	if isTruncatedFinishReason(finishReason) {
		return "", fmt.Errorf("%w (%s)", errSummaryTruncated, finishReason)
	}
	summary := strings.TrimSpace(out.String())
	if summary == "" {
		return "", fmt.Errorf("summarization returned empty output")
	}
	return summary, nil
}

func isTruncatedFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens", "incomplete":
		return true
	default:
		return false
	}
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	return llm.ClassifyError(err, time.Now()).Kind == llm.FaultKindContext
}

type compactionCheckpointInfo struct {
	Summary      string
	Scope        string
	KeepFromStep int
}

// compactContextOverflow performs a single-shot emergency context compaction
// only after an upstream provider explicitly rejects a request for exceeding
// context window limits, preserving normal-path KV-cache prefixes untouched.
func (e *Engine) compactContextOverflow(
	ctx context.Context,
	providerID, modelID string,
	msgs []Message,
	historyLen int,
	currentStep int,
	emit func(EngineEvent),
) ([]Message, *compactionCheckpointInfo, bool) {
	return e.compactContextOverflowWithInPlace(ctx, providerID, modelID, msgs, historyLen, currentStep, nil, emit)
}

func (e *Engine) compactContextOverflowWithInPlace(
	ctx context.Context,
	providerID, modelID string,
	msgs []Message,
	historyLen int,
	currentStep int,
	inPlace *inPlaceCompactionContext,
	emit func(EngineEvent),
) ([]Message, *compactionCheckpointInfo, bool) {
	if len(msgs) == 0 {
		return nil, nil, false
	}

	// Identify the minimal trailing anchor to keep intact: either the current
	// user prompt slice (at step 1), the final user message, or the final
	// [assistant(tool_use), user(tool_result)] pair so every tool_result retains
	// its matching tool_use call ID.
	tailStart, keptPriorToolPair := compactionTailAnchor(msgs, historyLen, currentStep)

	if tailStart >= 2 {
		record := func(u TokenUsage) {
			if emit != nil {
				emit(EngineEvent{Kind: EngineSummaryUsage, Step: currentStep, Usage: &u})
			}
		}
		var trimmedSummary string
		if inPlace != nil && inPlace.eligible() {
			if summary, ok := e.tryInPlaceCompaction(ctx, inPlace, msgs, msgs[tailStart:], record); ok {
				trimmedSummary = strings.TrimSpace(summary)
			} else if ctx.Err() != nil {
				return nil, nil, false
			}
		}
		if trimmedSummary == "" {
			transcript := renderOverflowTranscript(msgs[:tailStart])
			if transcript != "" {
				if summary, err := e.SummarizeWithUsage(ctx, providerID, modelID, transcript, record); err == nil && strings.TrimSpace(summary) != "" {
					trimmedSummary = strings.TrimSpace(summary)
				}
			}
		}
		if trimmedSummary != "" {
			checkpoint := Message{
				Role: RoleUser,
				Content: []ContentBlock{{
					Type: ContentText,
					Text: "<context_checkpoint>\n" + trimmedSummary + "\n</context_checkpoint>\nContinue working from this compacted session checkpoint.",
				}},
			}
			var out []Message
			out = append(out, checkpoint)
			if msgs[tailStart].Role == RoleUser {
				out = append(out, Message{
					Role:    RoleAssistant,
					Content: []ContentBlock{{Type: ContentText, Text: "Understood. Continuing from the compacted checkpoint."}},
				})
			}
			out = append(out, msgs[tailStart:]...)
			info := &compactionCheckpointInfo{
				Summary:      trimmedSummary,
				Scope:        "before_current_user",
				KeepFromStep: 1,
			}
			if currentStep > 1 {
				info.Scope = "before_step"
				if keptPriorToolPair {
					info.KeepFromStep = max(1, currentStep-1)
				} else {
					info.KeepFromStep = currentStep
				}
			}
			return out, info, true
		}
	}

	return nil, nil, false
}

func hasToolResultBlocks(m Message) bool {
	for _, b := range m.Content {
		if b.Type == ContentToolResult {
			return true
		}
	}
	return false
}

func renderOverflowTranscript(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("[")
		b.WriteString(string(m.Role))
		b.WriteString("]\n")
		for _, blk := range m.Content {
			switch blk.Type {
			case ContentText:
				if strings.TrimSpace(blk.Text) != "" {
					b.WriteString(blk.Text)
					b.WriteByte('\n')
				}
			case ContentToolUse:
				fmt.Fprintf(&b, "tool_use[%s] %s(%s)\n", blk.ID, blk.Name, blk.Input)
			case ContentToolResult:
				fmt.Fprintf(&b, "tool_result[%s] (error=%t): %s\n", blk.ToolUseID, blk.IsError, blk.Text)
			case ContentMedia:
				fmt.Fprintf(&b, "[Attachment: %s (%s); binary content remains in session history]\n", blk.Filename, blk.MimeType)
			}
		}
		b.WriteByte('\n')
	}
	return ensureValidUTF8(strings.TrimSpace(b.String()))
}

func ensureValidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

func utf8Prefix(s string, maxBytes int) string {
	s = ensureValidUTF8(s)
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Suffix(s string, maxBytes int) string {
	s = ensureValidUTF8(s)
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
