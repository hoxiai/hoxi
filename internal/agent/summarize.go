package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const compactionSystemPrompt = `You are a senior software engineering context compaction engine.
Distill the provided conversation transcript into a dense, lossless technical checkpoint in Markdown so work can continue seamlessly without prior raw messages.

Preserve verbatim:
1. Core user goal, active requirements, and explicit constraints/preferences.
2. Exact file paths read, created, or modified, plus key function/type symbols and architectural invariants.
3. Completed milestones, verified test/build results, and any errors encountered + how they were resolved.
4. Remaining TODO items and exact next technical step.`

// Summarize runs a single-step streaming summarization pass using the configured
// provider/model driver so /compact works across Anthropic, OpenAI, Responses, and Gemini.
func (e *Engine) Summarize(ctx context.Context, providerID, modelID, transcript string) (string, error) {
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
	httpReq, err := drv.BuildRequest(ModelRequest{
		Target:    target,
		System:    compactionSystemPrompt,
		MaxTokens: maxOut,
		Effort:    "low",
		Messages: []Message{{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type: ContentText,
				Text: "Summarize the following engineering conversation into a structured technical checkpoint:\n\n<transcript>\n" + strings.TrimSpace(transcript) + "\n</transcript>",
			}},
		}},
	})
	if err != nil {
		return "", err
	}
	resp, err := e.stream.OpenStream(ctx, httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out strings.Builder
	var finishReason string
	if err := drv.ParseSSE(ctx, resp.Body, func(se StreamEvent) {
		switch se.Kind {
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
		return "", err
	}
	if isTruncatedFinishReason(finishReason) {
		return "", fmt.Errorf("summarization truncated at output token limit (%s)", finishReason)
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
	return llm.ClassifyFault("", err.Error(), 0, time.Now()).Kind == llm.FaultKindContext
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
) ([]Message, *compactionCheckpointInfo, bool) {
	if len(msgs) == 0 {
		return nil, nil, false
	}
	origChars := countMessageChars(msgs)
	slim := slimMessagesForOverflow(msgs)
	slimChars := countMessageChars(slim)

	// Identify the minimal trailing anchor to keep intact: either the current
	// user prompt slice (at step 1), the final user message, or the final
	// [assistant(tool_use), user(tool_result)] pair so every tool_result retains
	// its matching tool_use call ID.
	tailStart := len(slim) - 1
	keptPriorToolPair := false
	if currentStep <= 1 && historyLen >= 2 && historyLen < len(slim) {
		tailStart = historyLen
	} else if hasToolResultBlocks(slim[tailStart]) && tailStart > 0 && slim[tailStart-1].Role == RoleAssistant {
		tailStart--
		keptPriorToolPair = true
	}

	if tailStart >= 2 {
		transcript := renderOverflowTranscript(slim[:tailStart], 48000)
		if transcript != "" {
			if summary, err := e.Summarize(ctx, providerID, modelID, transcript); err == nil && strings.TrimSpace(summary) != "" {
				trimmedSummary := strings.TrimSpace(summary)
				checkpoint := Message{
					Role: RoleUser,
					Content: []ContentBlock{{
						Type: ContentText,
						Text: "<context_checkpoint>\n" + trimmedSummary + "\n</context_checkpoint>\nContinue working from this compacted session checkpoint.",
					}},
				}
				var out []Message
				out = append(out, checkpoint)
				if slim[tailStart].Role == RoleUser {
					out = append(out, Message{
						Role:    RoleAssistant,
						Content: []ContentBlock{{Type: ContentText, Text: "Understood. Continuing from the compacted checkpoint."}},
					})
				}
				out = append(out, slim[tailStart:]...)
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
	}

	if slimChars < origChars*3/4 {
		return slim, nil, true
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

func countMessageChars(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			total += len(b.Text) + len(b.Input) + len(b.Data)
		}
	}
	return total
}

func slimMessagesForOverflow(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	lastIdx := len(msgs) - 1
	for i, m := range msgs {
		blocks := make([]ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case ContentMedia:
				if i == lastIdx {
					blocks = append(blocks, b)
				} else {
					blocks = append(blocks, ContentBlock{
						Type: ContentText,
						Text: fmt.Sprintf("[omitted earlier media attachment: %s (%s)]", b.Filename, b.MimeType),
					})
				}
			case ContentToolResult:
				if len(b.Text) > 320 {
					cp := b
					cp.Text = fmt.Sprintf("[tool output emergency-pruned: %d chars]\n%s", len(b.Text), b.Text[:240]+"...")
					blocks = append(blocks, cp)
				} else {
					blocks = append(blocks, b)
				}
			default:
				if b.Type == ContentText && i < lastIdx && len(b.Text) > 2400 {
					cp := b
					cp.Text = b.Text[:1200] + "\n...[truncated]...\n" + b.Text[len(b.Text)-800:]
					blocks = append(blocks, cp)
				} else {
					blocks = append(blocks, b)
				}
			}
		}
		out[i] = Message{Role: m.Role, Content: blocks}
	}
	return out
}

func renderOverflowTranscript(msgs []Message, maxChars int) string {
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
				fmt.Fprintf(&b, "tool_use %s(%s)\n", blk.Name, truncateInline(string(blk.Input), 200))
			case ContentToolResult:
				fmt.Fprintf(&b, "tool_result[%s]: %s\n", blk.ToolUseID, truncateInline(blk.Text, 300))
			}
		}
		b.WriteByte('\n')
	}
	s := strings.TrimSpace(b.String())
	if maxChars > 0 && len(s) > maxChars {
		half := maxChars / 2
		s = s[:half] + "\n\n... [middle transcript omitted] ...\n\n" + s[len(s)-half:]
	}
	return s
}

func truncateInline(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
