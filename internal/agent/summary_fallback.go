package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func (cp *compactionCheckpointInfo) report(step, depth int, proactive bool, emit func(EngineEvent)) {
	if depth != 0 || cp == nil || cp.Summary == "" {
		return
	}
	meta := map[string]any{"scope": cp.Scope, "keepFromStep": cp.KeepFromStep, "providerID": cp.ProviderID, "modelID": cp.ModelID}
	if proactive {
		meta["proactive"] = true
	}
	emit(EngineEvent{Kind: EngineContextCompacted, Step: step, Text: cp.Summary, Metadata: meta})
}

// An explicit small_model may name a provider. Automatic economy selection stays
// within the resolved main provider, including when the turn used an auto pool.
func (e *Engine) overflowSummaryModel(provider, model string) (string, string) {
	var configured string
	if source, ok := e.stream.(llm.CompactionModelSource); ok {
		configured = source.ConfiguredCompactionModel()
	}
	p, m := llm.SplitModelRef(configured)
	if p == "" {
		p = provider
	}
	if m == "" || strings.EqualFold(m, "economy") {
		resolver, ok := e.stream.(llm.SubagentEconomyResolver)
		if !ok {
			return provider, model
		}
		m, ok = resolver.ResolveEconomyModel(p, model)
		if !ok {
			return provider, model
		}
	}
	if t := e.stream.ResolveStreamTarget(p, m); m != "" && t.HasEndpoint && t.ProviderID == p && t.ModelID == m {
		return p, m
	}
	return provider, model
}

// Project only superseded source reads, successful long writes and passing
// verification logs. Preserve unique source, user text and failed-call evidence.
func prepareSummaryMessages(msgs []Message, worktree string) []Message {
	exchanges, index := indexPruneToolExchanges(msgs, len(msgs), newPrunePathNormalizer(worktree))
	type position struct{ i, j int }
	uses := map[string]position{}
	out := append([]Message(nil), msgs...)
	changed := make([]bool, len(msgs))
	set := func(pos position, block ContentBlock) {
		if !changed[pos.i] {
			out[pos.i].Content = append([]ContentBlock(nil), msgs[pos.i].Content...)
			changed[pos.i] = true
		}
		out[pos.i].Content[pos.j] = block
	}
	for i, m := range msgs {
		for j, b := range m.Content {
			if m.Role == RoleAssistant && b.Type == ContentToolUse && b.ID != "" {
				uses[b.ID] = position{i, j}
				continue
			}
			if m.Role != RoleUser || b.Type != ContentToolResult {
				continue
			}
			pos, ok := uses[b.ToolUseID]
			delete(uses, b.ToolUseID)
			if !ok || b.IsError {
				continue
			}
			call := msgs[pos.i].Content[pos.j]
			switch call.Name {
			case "write", "apply_patch":
				if len(call.Input) > 4096 {
					if input, ok := compactOldToolUseInput(call.Name, call.Input, 768); ok {
						call.Input = input
						set(pos, call)
					}
				}
			case "read", "bash":
				body, rules := splitEmbeddedRuleBlocks(b.Text)
				if len(body) < compactStaleReadMinLen {
					continue
				}
				if call.Name == "read" {
					idx := index[[2]int{i, j}]
					if classifyReadBefore(exchanges, idx, len(msgs)) == readNotSuperseded {
						continue
					}
					body = fmt.Sprintf("[superseded read of %s, %d bytes]", exchanges[idx].readSpan.displayPath, len(body))
				} else {
					var args struct{ Command string }
					if json.Unmarshal(call.Input, &args) != nil || !verificationRe.MatchString(args.Command) || failureMarkerRe.MatchString(body) {
						continue
					}
					log, handle := splitBashForegroundLogHandle(body)
					body = summarizePrunedToolOutputForCall(log, "bash", false, false)
					if handle != "" {
						body += "\n" + handle
					}
				}
				b.Text = attachPreservedRules(body, rules)
				set(position{i, j}, b)
			}
		}
	}
	return out
}
