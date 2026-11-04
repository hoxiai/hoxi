package runner

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type agyStepUpdate struct {
	ConversationID  string  `json:"conversation_id"`
	StepIndex       int     `json:"step_index"`
	State           string  `json:"state"`
	StepType        string  `json:"step_type"`
	TextDelta       string  `json:"text_delta"`
	Thinking        string  `json:"thinking"`
	ThinkingDelta   string  `json:"thinking_delta"`
	DurationSeconds float64 `json:"duration_seconds"`
	ToolName        string  `json:"tool_name"`
	ToolInfo        *struct {
		Name       string                 `json:"name"`
		Parameters map[string]interface{} `json:"parameters"`
		Output     string                 `json:"output"`
		Error      *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"tool_info"`
	SubagentInfo *agySubagentInfo `json:"subagent_info"`
	Usage        *agyUsage        `json:"usage"`
}

type agyResult struct {
	ConversationID  string    `json:"conversation_id"`
	Status          string    `json:"status"`
	Response        string    `json:"response"`
	Error           string    `json:"error"`
	DurationSeconds float64   `json:"duration_seconds"`
	NumTurns        int       `json:"num_turns"`
	Usage           *agyUsage `json:"usage"`
	DeniedActions   []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
}

type agyRawEvent struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	Init           *struct {
		Model          string `json:"model"`
		CWD            string `json:"cwd"`
		PermissionMode string `json:"permission_mode"`
	} `json:"init"`
	StepUpdate *agyStepUpdate `json:"step_update"`
	Result     *agyResult     `json:"result"`
}

type agyUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

func (u agyUsage) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.ThinkingTokens == 0 && u.CacheReadTokens == 0 && u.TotalTokens == 0
}

func (t *TurnTranslator) feedAgyLine(line string) []SSEEvent {
	var env agyRawEvent
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}
	t.lastActivityMs = time.Now().UnixMilli()
	if !t.noteAgyConversation(env.ConversationID) {
		return nil
	}

	switch env.Event {
	case "init":
		if env.Init != nil && env.Init.Model != "" && (t.msgInfo.ModelID == "" || strings.EqualFold(t.msgInfo.ModelID, "auto")) {
			t.msgInfo.ModelID = env.Init.Model
			return []SSEEvent{{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}}}
		}
		return nil

	case "step_update":
		su := env.StepUpdate
		if su == nil {
			return nil
		}
		now := time.Now().UnixMilli()
		// Step indexes are per conversation; never let another one's steps
		// overwrite the parent's parts.
		if !t.noteAgyConversation(su.ConversationID) {
			return t.noteSubagentStep(su, now)
		}
		if _, ok := t.stepStartMs[su.StepIndex]; !ok {
			t.stepStartMs[su.StepIndex] = now
		}

		switch su.StepType {
		case "agent_response":
			return t.handleAgyAgentResponse(su, now)
		case "subagent":
			t.agyFinalResponseDone = false
			return t.handleAgySubagentStep(su, now)
		case "tool":
			t.agyFinalResponseDone = false
			if isAgySubagentDispatch(su) {
				return t.handleAgySubagentStep(su, now)
			}
			return t.handleAgyToolStep(su, now)
		case "system_message":
			if su.State != "DONE" {
				return nil
			}
			return t.handleAgySystemMessage(su, now)
		}
		return nil

	case "result":
		res := env.Result
		if res == nil {
			return nil
		}
		if !t.noteAgyConversation(res.ConversationID) {
			return nil
		}
		return t.handleAgyResult(res)
	}
	return nil
}

func (t *TurnTranslator) handleAgyAgentResponse(su *agyStepUpdate, now int64) []SSEEvent {
	t.agyFinalResponseDone = false
	var evts []SSEEvent
	startMs := t.stepStartMs[su.StepIndex]
	endMs := now
	if su.DurationSeconds > 0 {
		if calcStart := endMs - int64(su.DurationSeconds*1000); calcStart > 0 && calcStart < startMs {
			startMs = calcStart
		}
	}

	// 1. Handle reasoning/thinking
	thinkDelta := su.ThinkingDelta
	if thinkDelta == "" && su.Thinking != "" {
		thinkDelta = su.Thinking
	}
	if thinkDelta == "" && su.State == "DONE" && su.Usage != nil && su.Usage.ThinkingTokens > 0 && t.lookupThink != nil && t.externalID != "" {
		thinkDelta = t.lookupThink(t.externalID, su.StepIndex)
	}
	rID := fmt.Sprintf("prt_%s_reason_%d", t.messageID, su.StepIndex)
	txtID := fmt.Sprintf("prt_%s_text_%d", t.messageID, su.StepIndex)
	if thinkDelta != "" {
		part, exists := t.partsByID[rID]
		if !exists {
			part = &MessagePart{
				ID:        rID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "reasoning",
				Text:      "",
				Time:      &PartTime{Start: startMs},
			}
			t.partsByID[rID] = part
			t.insertPartOrderBefore(rID, txtID)
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		t.appendPartText(part, thinkDelta)
		evts = append(evts, SSEEvent{
			Type: "message.part.delta",
			Properties: map[string]interface{}{
				"sessionID": t.sessionID,
				"messageID": t.messageID,
				"partID":    rID,
				"field":     "text",
				"delta":     thinkDelta,
			},
		})
	}
	if su.TextDelta != "" || su.State == "DONE" {
		if rPart, exists := t.partsByID[rID]; exists && (rPart.Time == nil || rPart.Time.End == 0) {
			rStart := startMs
			if rPart.Time != nil && rPart.Time.Start > 0 && rPart.Time.Start < rStart {
				rStart = rPart.Time.Start
			}
			rEnd := endMs
			if txtPart, hasTxt := t.partsByID[txtID]; hasTxt && txtPart.Time != nil && txtPart.Time.Start > rStart && txtPart.Time.Start <= endMs {
				rEnd = txtPart.Time.Start
			}
			rPart.Time = &PartTime{Start: rStart, End: rEnd}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*rPart)}})
		}
	}

	// 2. Handle text delta
	if su.TextDelta != "" {
		t.hasTextPart = true
		part, exists := t.partsByID[txtID]
		if !exists {
			part = &MessagePart{
				ID:        txtID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "text",
				Text:      "",
				Time:      &PartTime{Start: startMs},
			}
			t.partsByID[txtID] = part
			t.partOrder = append(t.partOrder, txtID)
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		t.appendPartText(part, su.TextDelta)
		evts = append(evts, SSEEvent{
			Type: "message.part.delta",
			Properties: map[string]interface{}{
				"sessionID": t.sessionID,
				"messageID": t.messageID,
				"partID":    txtID,
				"field":     "text",
				"delta":     su.TextDelta,
			},
		})
	}
	if su.State == "DONE" {
		var tokensUpdated bool
		if su.Usage != nil {
			tokensUpdated = t.noteAgyStepUsage(fmt.Sprintf("step:%d", su.StepIndex), *su.Usage)
		}
		if part, exists := t.partsByID[txtID]; exists {
			txtStart := startMs
			if _, hasReason := t.partsByID[rID]; hasReason && part.Time != nil && part.Time.Start > txtStart && part.Time.Start <= endMs {
				txtStart = part.Time.Start
			}
			part.Time = &PartTime{Start: txtStart, End: endMs}
			t.agyFinalResponseDone = strings.TrimSpace(part.Text) != ""
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		if tokensUpdated {
			evts = append(evts, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}})
		}
	}
	return evts
}

func (t *TurnTranslator) noteAgyStepUsage(key string, u agyUsage) bool {
	if u.empty() {
		return false
	}
	t.agyStepUsage[key] = u
	var in, out, reasoning, cacheRead, total int
	for _, s := range t.agyStepUsage {
		in += s.InputTokens
		out += s.OutputTokens
		reasoning += s.ThinkingTokens
		cacheRead += s.CacheReadTokens
		total += s.TotalTokens
	}
	if total == 0 {
		total = in + out + reasoning + cacheRead
	}
	tokens := &MessageTokens{
		Total:     total,
		Input:     in,
		Output:    out,
		Reasoning: reasoning,
	}
	tokens.Cache.Read = cacheRead
	if messageTokensEqual(t.msgInfo.Tokens, tokens) {
		return false
	}
	t.msgInfo.Tokens = tokens
	return true
}

func (t *TurnTranslator) handleAgyToolStep(su *agyStepUpdate, now int64) []SSEEvent {
	var evts []SSEEvent
	partID := fmt.Sprintf("prt_%s_tool_%d", t.messageID, su.StepIndex)
	callID := fmt.Sprintf("call_%s_%d", t.messageID, su.StepIndex)
	rawName := su.ToolName
	var rawParams map[string]interface{}
	var output, errMsg string
	if su.ToolInfo != nil {
		if rawName == "" {
			rawName = su.ToolInfo.Name
		}
		rawParams = su.ToolInfo.Parameters
		output = su.ToolInfo.Output
		if su.ToolInfo.Error != nil {
			errMsg = su.ToolInfo.Error.Message
		}
	}
	if existing, ok := t.partsByID[partID]; ok && existing.State != nil && len(existing.State.Input) > 0 {
		rawParams = mergeAgyEditParams(rawParams, existing.State.Input)
		if existing.Tool == "write" && strings.EqualFold(strings.TrimSpace(rawName), "edit_file") {
			rawName = "write_file"
		}
	}
	if t.editPayloads != nil && agyEditPayloadNames[strings.ToLower(strings.TrimSpace(rawName))] && agyParamsLackEditPayload(rawParams) {
		if name, full := t.editPayloads.lookup(su.StepIndex, rawParams, su.State == "DONE"); full != nil {
			rawParams = mergeAgyEditParams(rawParams, full)
			// Stdout names every edit step edit_file, even the writes, so the
			// planned call is what distinguishes creating a file from editing one.
			if name != "" {
				rawName = name
			}
		}
	}
	normTool, title, cleanInput := normalizeRunnerTool(rawName, rawParams)
	startMs := t.stepStartMs[su.StepIndex]
	endMs := now
	if su.DurationSeconds > 0 {
		endMs = startMs + int64(su.DurationSeconds*1000)
		if endMs <= startMs {
			endMs = startMs + 1
		}
	}

	part, exists := t.partsByID[partID]
	if !exists {
		pendingPart := MessagePart{
			ID:        partID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "tool",
			Tool:      normTool,
			CallID:    callID,
			State: &ToolState{
				Status: "pending",
				Input:  cleanInput,
				Title:  title,
				Time:   &PartTime{Start: startMs},
			},
		}
		t.partsByID[partID] = &pendingPart
		t.partOrder = append(t.partOrder, partID)
		part = &pendingPart
		if su.State != "PENDING" {
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
	}

	status := "running"
	partTime := &PartTime{Start: startMs}
	switch su.State {
	case "PENDING":
		status = "pending"
	case "ACTIVE":
		status = "running"
	case "DONE":
		status = "completed"
		partTime = &PartTime{Start: startMs, End: endMs}
	case "ERROR":
		status = "error"
		partTime = &PartTime{Start: startMs, End: endMs}
		if errMsg == "" {
			errMsg = "tool execution failed"
		}
	}

	part.Tool = normTool
	part.State = &ToolState{
		Status: status,
		Input:  cleanInput,
		Output: output,
		Title:  title,
		Error:  errMsg,
		Time:   partTime,
	}
	if normTool == "agent_wait" && t.hasWaitingSubagents() {
		part.State.Metadata = map[string]interface{}{"waitingFor": "subagents"}
	}
	evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
	return evts
}

func (t *TurnTranslator) handleAgyResult(res *agyResult) []SSEEvent {
	t.turnFinished = true
	now := time.Now().UnixMilli()
	var evts []SSEEvent

	isErr := res.Status == "ERROR" && !t.isStaleAgyResultErrorLocked(res)
	t.msgInfo.Time.Completed = now
	evts = append(evts, t.finishAgySubagents(isErr, now)...)
	evts = append(evts, t.closeAllChildren(isErr, now)...)
	// On resumed conversations (`--conversation <id>`), `result.usage` reports the
	// cumulative total across the entire conversation since step 0, and on errors
	// or interruptions it reports all zeros. Prefer the per-step usage accumulated
	// during this turn, falling back to `result.usage` only when no step usage was seen.
	if len(t.agyStepUsage) == 0 && res.Usage != nil {
		t.noteAgyStepUsage("result", *res.Usage)
	}

	if !t.hasTextPart && strings.TrimSpace(res.Response) != "" {
		txtID := fmt.Sprintf("prt_%s_text_final", t.messageID)
		part := MessagePart{
			ID:        txtID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "text",
			Text:      res.Response,
			Time:      &PartTime{Start: t.startedAtMs, End: now},
		}
		t.partsByID[txtID] = &part
		t.partOrder = append(t.partOrder, txtID)
		t.hasTextPart = true
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
	} else if !t.hasTextPart && len(res.DeniedActions) > 0 {
		var names []string
		for _, da := range res.DeniedActions {
			if da.DisplayName != "" {
				names = append(names, da.DisplayName)
			} else if da.Action != "" {
				names = append(names, da.Action)
			}
		}
		// Hook-gated run_command / view_file calls never reach here; a soft-deny
		// means the Hoxi approval hook was missing or the tool is not covered.
		notice := fmt.Sprintf("Action blocked by Antigravity headless permission policy (%s). Hoxi's approval hook did not handle it; check `~/.gemini/config/hooks.json` (entry `%s`).", strings.Join(names, ", "), agyHookName)
		txtID := fmt.Sprintf("prt_%s_text_denied", t.messageID)
		part := MessagePart{
			ID:        txtID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "text",
			Text:      notice,
			Time:      &PartTime{Start: t.startedAtMs, End: now},
		}
		t.partsByID[txtID] = &part
		t.partOrder = append(t.partOrder, txtID)
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
	}

	if isErr {
		if strings.EqualFold(strings.TrimSpace(res.Error), "interrupted") {
			t.msgInfo.Finish = "aborted"
			evts = append(evts,
				SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
			)
			return evts
		}
		errMsg := strings.TrimSpace(res.Error)
		if errMsg == "" {
			errMsg = t.lastClaudeErrorText()
		}
		if errMsg == "" {
			errMsg = "Antigravity CLI execution error"
		}
		msgErr := newClassifiedMessageError("RunnerError", errMsg, 0)
		t.msgInfo.Error = msgErr
		t.msgInfo.Finish = "error"
		evts = append(evts,
			SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
			SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": t.sessionID, "error": msgErr}},
			SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		)
		return evts
	}

	t.msgInfo.Finish = "stop"
	evts = append(evts,
		SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
		SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		SSEEvent{Type: "session.idle", Properties: map[string]interface{}{"sessionID": t.sessionID}},
	)
	return evts
}
