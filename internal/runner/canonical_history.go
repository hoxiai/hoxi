package runner

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

// Canonical tool recovery outcomes stored in ToolState.Metadata["outcome"]
// while keeping ToolState.Status within the 4 wire UI states
// ("pending" | "running" | "completed" | "error").
const (
	ToolOutcomeCompleted   = "completed"
	ToolOutcomeFailed      = "failed"
	ToolOutcomeCancelled   = "cancelled"
	ToolOutcomeInterrupted = "interrupted"
	ToolOutcomeUnknown     = "outcome_unknown"

	TurnFinishInterrupted = "interrupted"
)

// isMutatingRunnerTool reports whether a tool call may mutate workspace files,
// external processes, or remote state so recovery logic never blindly replays
// an uncertain execution.
func isMutatingRunnerTool(toolName string, input map[string]interface{}) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch lower {
	case "", "read", "glob", "grep", "code_search", "symbol_inspect", "impact_analysis",
		"todoread", "todowrite", "question", "webfetch", "mcp_list_tools", "skill", "goal_report", "task":
		return false
	case "hoxi_browser":
		act := strings.ToLower(strings.TrimSpace(firstString(input["action"])))
		return act != "snapshot" && act != "screenshot"
	case "hoxi_tools":
		act := strings.ToLower(strings.TrimSpace(firstString(input["action"])))
		return act != "discover" && act != "inspect"
	case "hoxi_remote", "hoxi_data":
		act := strings.ToLower(strings.TrimSpace(firstString(input["action"])))
		switch act {
		case "list", "sql_query":
			return false
		case "http_request":
			m := strings.ToUpper(strings.TrimSpace(firstString(input["method"])))
			return m != "" && m != "GET" && m != "HEAD"
		default:
			return true
		}
	case "write", "edit", "multiedit", "apply_patch", "bash", "shell", "create_file",
		"mcp_call_tool", "hoxi_computer", "hoxi_image":
		return true
	default:
		if strings.HasSuffix(lower, "_read") || strings.HasPrefix(lower, "list_") || strings.HasPrefix(lower, "get_") {
			return false
		}
		return true
	}
}

// isMutatingToolPart evaluates whether a recorded MessagePart may have mutated
// workspace or external state, accounting for writable/general `task` subagents.
func isMutatingToolPart(p MessagePart) bool {
	if p.State == nil {
		return isMutatingRunnerTool(p.Tool, nil)
	}
	if strings.EqualFold(strings.TrimSpace(p.Tool), "task") {
		if mut, ok := p.State.Metadata["mutated"].(bool); ok && mut {
			return true
		}
		if strings.TrimSpace(p.State.Status) == "running" {
			if ro, ok := p.State.Metadata["readOnly"].(bool); ok {
				return !ro
			}
			sub := strings.ToLower(strings.TrimSpace(firstString(p.State.Input["subagent_type"], p.State.Input["agent"])))
			return sub == "general"
		}
		return false
	}
	return isMutatingRunnerTool(p.Tool, p.State.Input)
}

// settleInFlightToolPart transitions an unfinished ("pending" or "running")
// tool part to a durable settled state, explicitly distinguishing unstarted
// cancellations, interrupted read-only calls, and mutating calls whose side
// effects are unknown (`outcome_unknown`).
func settleInFlightToolPart(p *MessagePart, nowMs int64, userCancelled bool) bool {
	if p == nil || p.Type != "tool" || p.State == nil {
		return false
	}
	prevStatus := strings.TrimSpace(p.State.Status)
	if prevStatus != "pending" && prevStatus != "running" {
		return false
	}
	startMs := nowMs
	if p.State.Time != nil && p.State.Time.Start > 0 {
		startMs = p.State.Time.Start
	}
	if p.State.Metadata == nil {
		p.State.Metadata = make(map[string]interface{}, 2)
	}

	var outcome, defaultMsg string
	switch {
	case prevStatus == "pending":
		outcome = ToolOutcomeCancelled
		defaultMsg = "Tool call did not start because the turn ended before execution."
	case isMutatingToolPart(*p):
		outcome = ToolOutcomeUnknown
		defaultMsg = "[outcome_unknown] Tool execution was interrupted while running; side effects may have occurred. Inspect workspace state before retrying."
	case userCancelled:
		outcome = ToolOutcomeCancelled
		defaultMsg = "Read-only tool execution was cancelled."
	default:
		outcome = ToolOutcomeInterrupted
		defaultMsg = interruptedToolError
	}

	p.State.Status = "error"
	p.State.Metadata["outcome"] = outcome
	if strings.TrimSpace(p.State.Error) == "" {
		p.State.Error = defaultMsg
	}
	p.State.Time = &PartTime{Start: startMs, End: nowMs}
	return true
}

// settleSessionInFlightParts settles any unfinished tool or reasoning parts for
// sessionID in Store and broadcasts the updated parts to active subscribers.
func (s *Service) settleSessionInFlightParts(sessionID string, now int64, userCancelled bool) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	for i := range rec.Turns {
		for j := range rec.Turns[i].Parts {
			p := rec.Turns[i].Parts[j]
			if settleInFlightToolPart(&p, now, userCancelled) {
				s.broadcast(sessionID, SSEEvent{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": p},
				})
			} else if p.Type == "reasoning" && (p.Time == nil || p.Time.End == 0) {
				start := now
				if p.Time != nil && p.Time.Start > 0 {
					start = p.Time.Start
				}
				p.Time = &PartTime{Start: start, End: now}
				s.broadcast(sessionID, SSEEvent{
					Type:       "message.part.updated",
					Properties: map[string]interface{}{"part": p},
				})
			}
		}
	}
}

func stringMetaField(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

func intMetaField(meta map[string]interface{}, key string) int {
	if meta == nil {
		return 0
	}
	switch v := meta[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func partStep(p MessagePart) int {
	if n := intMetaField(p.Metadata, "step"); n > 0 {
		return n
	}
	if p.State != nil {
		return intMetaField(p.State.Metadata, "step")
	}
	return 0
}

func findLatestCompactionCheckpoint(turns []MessageTurn) (turnIdx int, summary, scope string, keepFromStep int, found bool) {
	for i := len(turns) - 1; i >= 0; i-- {
		parts := turns[i].Parts
		for j := len(parts) - 1; j >= 0; j-- {
			p := parts[j]
			if p.Type == "compaction" && strings.TrimSpace(p.Text) != "" {
				return i, strings.TrimSpace(p.Text), stringMetaField(p.Metadata, "scope"), intMetaField(p.Metadata, "keepFromStep"), true
			}
		}
	}
	return -1, "", "", 0, false
}

func appendCanonicalMessage(msgs []NativeMessage, role string, blocks []NativeContentBlock) []NativeMessage {
	if len(blocks) == 0 {
		return msgs
	}
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == role {
		msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, blocks...)
		return msgs
	}
	copied := make([]NativeContentBlock, len(blocks))
	copy(copied, blocks)
	return append(msgs, NativeMessage{Role: role, Content: copied})
}

func (s *Service) appendToolMediaBlocks(meta map[string]interface{}, dst []NativeContentBlock) []NativeContentBlock {
	if s == nil || meta == nil {
		return dst
	}
	raw := meta["mediaAttachments"]
	if raw == nil {
		return dst
	}
	resolveEntry := func(entry map[string]interface{}) {
		if entry == nil {
			return
		}
		urlStr := stringMetaField(entry, "url")
		if urlStr == "" {
			if id := stringMetaField(entry, "id"); id != "" {
				urlStr = "hoxi-attachment://" + id
			}
		}
		if urlStr == "" {
			return
		}
		if mb, ok := s.resolveNativeHistoryFileBlock(MessagePart{
			Type:     "file",
			URL:      urlStr,
			Mime:     stringMetaField(entry, "mime"),
			Filename: stringMetaField(entry, "filename"),
		}); ok {
			dst = append(dst, mb)
		}
	}
	switch list := raw.(type) {
	case []map[string]interface{}:
		for _, item := range list {
			resolveEntry(item)
		}
	case []interface{}:
		for _, rawItem := range list {
			if m, ok := rawItem.(map[string]interface{}); ok {
				resolveEntry(m)
			}
		}
	}
	return dst
}

// keepReasoningAffinity reports whether a recorded reasoning part came from the
// exact same provider and model as the outgoing request, returning its stored
// cryptographic signature when present. Switching models strips private
// thinking/signatures while keeping canonical text and tool history intact.
func keepReasoningAffinity(turnInfo MessageInfo, part MessagePart, targetProviderID, targetModelID string) (sig string, keep bool) {
	sig = stringMetaField(part.Metadata, "signature")
	srcProv := firstNonEmptyStr(stringMetaField(part.Metadata, "providerID"), turnInfo.ProviderID)
	srcModel := firstNonEmptyStr(stringMetaField(part.Metadata, "modelID"), turnInfo.ModelID)
	targetProv := strings.TrimSpace(targetProviderID)
	targetMod := strings.TrimSpace(targetModelID)
	if srcProv == "" || srcModel == "" || targetProv == "" || targetMod == "" {
		return "", false
	}
	if !strings.EqualFold(srcProv, targetProv) || !strings.EqualFold(srcModel, targetMod) {
		return "", false
	}
	if strings.TrimSpace(part.Text) == "" && sig == "" {
		return "", false
	}
	return sig, true
}

func sameProducerModel(turnInfo MessageInfo, targetProviderID, targetModelID string) bool {
	if strings.TrimSpace(turnInfo.ProviderID) == "" || strings.TrimSpace(turnInfo.ModelID) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(turnInfo.ProviderID), strings.TrimSpace(targetProviderID)) &&
		strings.EqualFold(strings.TrimSpace(turnInfo.ModelID), strings.TrimSpace(targetModelID))
}

// projectCanonicalNativeHistory projects Hoxi's durable SessionRecord.Turns
// into outgoing NativeMessage context with four invariants:
//  1. Durable overflow checkpoint replay: resumes from the latest reactive
//     compaction checkpoint when present instead of re-sending overflowed turns.
//  2. Step-by-step exchange reconstruction: preserves individual [assistant ->
//     user(tool_result)] steps in chronological order rather than flattening.
//  3. Producer-affinity reasoning replay: private thinking blocks and signatures
//     replay only to the same (providerID, modelID) that generated them.
//  4. Answered tool-call & media repair: pairs every emitted `tool_use` with a
//     `tool_result` (plus persisted tool media attachments), synthesizing
//     `[outcome_unknown]` for crashed mutating calls and dropping unstarted orphans.
func (s *Service) projectCanonicalNativeHistory(turns []MessageTurn, targetProviderID, targetModelID string) []NativeMessage {
	var msgs []NativeMessage
	startIdx := 0
	firstTurnMinStep := 0

	if compIdx, summary, scope, keepFromStep, ok := findLatestCompactionCheckpoint(turns); ok {
		msgs = appendCanonicalMessage(msgs, "user", []NativeContentBlock{{
			Type: "text",
			Text: "<context_checkpoint>\n" + summary + "\n</context_checkpoint>\nContinue working from this compacted session checkpoint.",
		}})
		msgs = appendCanonicalMessage(msgs, "assistant", []NativeContentBlock{{
			Type: "text",
			Text: "Understood. Continuing from the compacted checkpoint.",
		}})
		if scope == "before_step" {
			startIdx = compIdx
			firstTurnMinStep = max(1, keepFromStep)
		} else {
			startIdx = compIdx
			if parentID := strings.TrimSpace(turns[compIdx].Info.ParentID); parentID != "" {
				for k := compIdx - 1; k >= 0; k-- {
					if turns[k].Info.ID == parentID {
						startIdx = k
						break
					}
				}
			} else if compIdx > 0 && strings.EqualFold(strings.TrimSpace(turns[compIdx-1].Info.Role), "user") {
				startIdx = compIdx - 1
			}
		}
	}

	for idx := startIdx; idx < len(turns); idx++ {
		t := turns[idx]
		minStep := 0
		if idx == startIdx && firstTurnMinStep > 0 {
			minStep = firstTurnMinStep
		}
		role := strings.ToLower(strings.TrimSpace(t.Info.Role))
		switch role {
		case "user":
			var blocks []NativeContentBlock
			for _, p := range t.Parts {
				switch p.Type {
				case "text":
					if strings.TrimSpace(p.Text) != "" {
						blocks = append(blocks, NativeContentBlock{Type: "text", Text: p.Text})
					}
				case "file":
					if mb, ok := s.resolveNativeHistoryFileBlock(p); ok {
						blocks = append(blocks, mb)
					}
				}
			}
			msgs = appendCanonicalMessage(msgs, "user", blocks)

		case "assistant":
			var asstBlocks, resultBlocks []NativeContentBlock
			curStep := 0
			sameModel := sameProducerModel(t.Info, targetProviderID, targetModelID)

			flushStep := func() {
				msgs = appendCanonicalMessage(msgs, "assistant", asstBlocks)
				msgs = appendCanonicalMessage(msgs, "user", resultBlocks)
				asstBlocks = nil
				resultBlocks = nil
			}

			for _, p := range t.Parts {
				if p.Type == "compaction" {
					continue
				}
				ps := partStep(p)
				if minStep > 0 && ps > 0 && ps < minStep {
					continue
				}
				if ps > 0 {
					if curStep > 0 && ps != curStep {
						flushStep()
					}
					curStep = ps
				} else if (p.Type == "reasoning" || p.Type == "text") && len(resultBlocks) > 0 {
					flushStep()
				}

				switch p.Type {
				case "reasoning":
					if sig, ok := keepReasoningAffinity(t.Info, p, targetProviderID, targetModelID); ok {
						asstBlocks = append(asstBlocks, NativeContentBlock{
							Type:      "thinking",
							Text:      p.Text,
							Signature: sig,
						})
					}
				case "text":
					if strings.TrimSpace(p.Text) != "" {
						asstBlocks = append(asstBlocks, NativeContentBlock{Type: "text", Text: p.Text})
					}
				case "tool":
					if p.State == nil {
						continue
					}
					callID := strings.TrimSpace(firstNonEmptyStr(p.CallID, p.ID))
					toolName := strings.TrimSpace(p.Tool)
					if callID == "" || toolName == "" {
						continue
					}
					status := strings.TrimSpace(p.State.Status)
					outcome := stringMetaField(p.State.Metadata, "outcome")

					// Drop unstarted pending calls or interrupted read-only running calls
					// that never recorded a result so no orphan tool_use is sent upstream.
					if status == "pending" || outcome == ToolOutcomeCancelled {
						continue
					}
					if status == "running" && !isMutatingToolPart(p) {
						continue
					}

					rawIn, mErr := json.Marshal(p.State.Input)
					if mErr != nil || len(rawIn) == 0 || string(rawIn) == "null" {
						rawIn = []byte("{}")
					}
					var toolSig string
					if sameModel {
						toolSig = stringMetaField(p.State.Metadata, "thoughtSignature")
					}
					asstBlocks = append(asstBlocks, NativeContentBlock{
						Type:      "tool_use",
						ID:        callID,
						Name:      toolName,
						Input:     rawIn,
						Signature: toolSig,
					})

					switch status {
					case "completed":
						resultBlocks = append(resultBlocks, NativeContentBlock{
							Type:      "tool_result",
							ToolUseID: callID,
							Text:      p.State.Output,
							IsError:   false,
						})
						resultBlocks = s.appendToolMediaBlocks(p.State.Metadata, resultBlocks)
					case "running":
						// Unsettled mutating tool from a mid-flight snapshot/crash: warn explicitly.
						resultBlocks = append(resultBlocks, NativeContentBlock{
							Type:      "tool_result",
							ToolUseID: callID,
							Text:      "[outcome_unknown] Tool was still running when the previous turn interrupted. Verify workspace files or git status before repeating mutating commands.",
							IsError:   true,
						})
					default:
						errOut := firstNonEmptyStr(p.State.Error, p.State.Output, interruptedToolError)
						if outcome == ToolOutcomeUnknown && !strings.Contains(errOut, "outcome_unknown") {
							errOut = "[outcome_unknown] " + errOut
						}
						resultBlocks = append(resultBlocks, NativeContentBlock{
							Type:      "tool_result",
							ToolUseID: callID,
							Text:      errOut,
							IsError:   true,
						})
					}
				}
			}
			flushStep()
		}
	}
	return msgs
}

func isIncompleteNativeStreamErr(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "model stream ended before terminal frame") ||
		strings.Contains(lower, "incomplete tool call") ||
		strings.Contains(lower, "unfinished anthropic tool_use") ||
		strings.Contains(lower, "unfinished responses tool call") ||
		strings.Contains(lower, "unexpected eof")
}

// turnMutatingEffectState inspects an assistant message (and any delegated child
// subagent sessions linked from `task` tool parts) in Store to report whether
// any mutating tool already executed (`hasMutatingSideEffect`) or ended in an
// indeterminate `outcome_unknown` state (`hasUnknownOutcome`).
func (s *Service) turnMutatingEffectState(sessionID, asstMsgID string) (hasMutatingSideEffect, hasUnknownOutcome bool) {
	if s == nil || sessionID == "" {
		return false, false
	}
	var childSessionIDs []string
	s.store.VisitTurns(sessionID, func(rec SessionRecord) {
		for i := len(rec.Turns) - 1; i >= 0; i-- {
			t := rec.Turns[i]
			if asstMsgID != "" && t.Info.ID != asstMsgID {
				continue
			}
			if t.Info.Role != "assistant" {
				break
			}
			for _, p := range t.Parts {
				if p.Type != "tool" || p.State == nil {
					continue
				}
				if strings.EqualFold(strings.TrimSpace(p.Tool), "task") {
					if cid := stringMetaField(p.State.Metadata, "sessionId"); cid != "" {
						childSessionIDs = append(childSessionIDs, cid)
					}
				}
				if !isMutatingToolPart(p) {
					continue
				}
				outcome := stringMetaField(p.State.Metadata, "outcome")
				if outcome == ToolOutcomeUnknown || p.State.Status == "running" {
					hasMutatingSideEffect = true
					hasUnknownOutcome = true
				} else if p.State.Status == "completed" || (p.State.Status == "error" && outcome != ToolOutcomeCancelled) {
					hasMutatingSideEffect = true
				}
			}
			break
		}
	})
	for _, cid := range childSessionIDs {
		cSide, cUnknown := s.childSessionMutatingState(cid)
		if cSide {
			hasMutatingSideEffect = true
		}
		if cUnknown {
			hasUnknownOutcome = true
		}
	}
	return hasMutatingSideEffect, hasUnknownOutcome
}

func (s *Service) childSessionMutatingState(childSessionID string) (hasSideEffect, hasUnknown bool) {
	if s == nil || childSessionID == "" {
		return false, false
	}
	s.store.VisitTurns(childSessionID, func(rec SessionRecord) {
		for _, t := range rec.Turns {
			if t.Info.Role != "assistant" {
				continue
			}
			for _, p := range t.Parts {
				if p.Type != "tool" || p.State == nil || !isMutatingToolPart(p) {
					continue
				}
				outcome := stringMetaField(p.State.Metadata, "outcome")
				if outcome == ToolOutcomeUnknown || p.State.Status == "running" {
					hasSideEffect = true
					hasUnknown = true
				} else if p.State.Status == "completed" || (p.State.Status == "error" && outcome != ToolOutcomeCancelled) {
					hasSideEffect = true
				}
			}
		}
	})
	return hasSideEffect, hasUnknown
}

// classifyNativeTurnFailure builds the terminal finish string and MessageError
// for a failed or interrupted NativeEngine turn, disabling blind prompt-replay
// (`IsRetryable = false` and `Kind = "outcome_unknown"`) whenever mutating
// tools already ran or ended with indeterminate outcomes.
func (s *Service) classifyNativeTurnFailure(sessionID, asstMsgID string, err error, stallErr *MessageError) (finish string, msgErr *MessageError) {
	hasSideEffect, _ := s.turnMutatingEffectState(sessionID, asstMsgID)
	if stallErr != nil {
		cloned := cloneMessageInfo(MessageInfo{Error: stallErr}).Error
		if cloned != nil && cloned.Data != nil && hasSideEffect {
			cloned.Data.IsRetryable = false
			cloned.Data.Kind = ToolOutcomeUnknown
		}
		return "error", cloned
	}
	if isIncompleteNativeStreamErr(err) {
		kind := string(llm.FaultKindNetwork)
		if hasSideEffect {
			kind = ToolOutcomeUnknown
		}
		return TurnFinishInterrupted, &MessageError{
			Name: "StreamInterruptedError",
			Data: &MessageErrorData{
				Message:     boundEngineText(err.Error()),
				IsRetryable: !hasSideEffect,
				Kind:        kind,
			},
		}
	}
	classified := newClassifiedMessageError("NativeAgentError", err.Error(), 0)
	if classified != nil && classified.Data != nil && hasSideEffect {
		classified.Data.IsRetryable = false
		classified.Data.Kind = ToolOutcomeUnknown
	}
	return "error", classified
}
