package runner

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

func (u claudeUsage) thinkingTokens() int {
	if u.OutputTokensDetails != nil {
		return u.OutputTokensDetails.ThinkingTokens
	}
	return 0
}

type claudeModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	ThinkingTokens           int     `json:"thinkingTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

type claudeRawEvent struct {
	Type         string                      `json:"type"`
	Subtype      string                      `json:"subtype"`
	SessionID    string                      `json:"session_id"`
	Model        string                      `json:"model"`
	IsError      bool                        `json:"is_error"`
	Result       string                      `json:"result"`
	Error        string                      `json:"error"`
	TotalCostUSD float64                     `json:"total_cost_usd"`
	Usage        *claudeUsage                `json:"usage"`
	ModelUsage   map[string]claudeModelUsage `json:"modelUsage"`
	// Subagent plumbing: inner messages carry parent_tool_use_id, and
	// system/task_notification reports a background subagent's outcome.
	ParentToolUseID string `json:"parent_tool_use_id"`
	ToolUseID       string `json:"tool_use_id"`
	Status          string `json:"status"`
	Summary         string `json:"summary"`
	APIMessageID    string `json:"api_message_id"`
	// system/api_retry: Claude Code is backing off before retrying the API call.
	Attempt      int     `json:"attempt"`
	RetryDelayMs float64 `json:"retry_delay_ms"`
	ErrorStatus  *int    `json:"error_status"`
	Event        *struct {
		Type    string       `json:"type"`
		Index   int          `json:"index"`
		Usage   *claudeUsage `json:"usage"`
		Message *struct {
			ID    string       `json:"id"`
			Model string       `json:"model"`
			Usage *claudeUsage `json:"usage"`
		} `json:"message"`
		Delta *struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	} `json:"event"`
	Message *struct {
		ID      string       `json:"id"`
		Model   string       `json:"model"`
		Role    string       `json:"role"`
		Usage   *claudeUsage `json:"usage"`
		Content []struct {
			Type      string                 `json:"type"`
			Text      string                 `json:"text"`
			Thinking  string                 `json:"thinking"`
			ID        string                 `json:"id"`
			Name      string                 `json:"name"`
			Input     map[string]interface{} `json:"input"`
			ToolUseID string                 `json:"tool_use_id"`
			Content   interface{}            `json:"content"`
			IsError   bool                   `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
}

func mergeClaudeUsage(prev claudeUsage, incoming *claudeUsage) claudeUsage {
	if incoming == nil {
		return prev
	}
	if incoming.InputTokens > prev.InputTokens {
		prev.InputTokens = incoming.InputTokens
	}
	if incoming.OutputTokens > prev.OutputTokens {
		prev.OutputTokens = incoming.OutputTokens
	}
	if incoming.CacheReadInputTokens > prev.CacheReadInputTokens {
		prev.CacheReadInputTokens = incoming.CacheReadInputTokens
	}
	if incoming.CacheCreationInputTokens > prev.CacheCreationInputTokens {
		prev.CacheCreationInputTokens = incoming.CacheCreationInputTokens
	}
	if th := incoming.thinkingTokens(); th > prev.thinkingTokens() {
		prev.OutputTokensDetails = &struct {
			ThinkingTokens int `json:"thinking_tokens"`
		}{ThinkingTokens: th}
	}
	return prev
}

func sumClaudeUsageMap(m map[string]claudeUsage) *MessageTokens {
	var in, out, reasoning, cacheRead, cacheWrite int
	for _, u := range m {
		in += u.InputTokens
		out += u.OutputTokens
		reasoning += u.thinkingTokens()
		cacheRead += u.CacheReadInputTokens
		cacheWrite += u.CacheCreationInputTokens
	}
	total := in + out + reasoning + cacheRead + cacheWrite
	if total == 0 {
		return nil
	}
	tokens := &MessageTokens{
		Total:     total,
		Input:     in,
		Output:    out,
		Reasoning: reasoning,
	}
	tokens.Cache.Read = cacheRead
	tokens.Cache.Write = cacheWrite
	return tokens
}

func (t *TurnTranslator) noteClaudeMsgUsage(msgID string, u *claudeUsage) {
	if u == nil {
		return
	}
	key := strings.TrimSpace(msgID)
	if key == "" {
		key = "_default"
	}
	t.claudeMsgUsage[key] = mergeClaudeUsage(t.claudeMsgUsage[key], u)
	if tokens := sumClaudeUsageMap(t.claudeMsgUsage); tokens != nil {
		t.msgInfo.Tokens = tokens
	}
}

func (t *TurnTranslator) applyClaudeResultUsage(env *claudeRawEvent) {
	if env == nil {
		return
	}
	if env.TotalCostUSD > 0 {
		t.msgInfo.Cost = env.TotalCostUSD
	}
	if len(env.ModelUsage) > 0 {
		var in, out, reasoning, cacheRead, cacheWrite int
		var modelCost float64
		for _, mu := range env.ModelUsage {
			in += mu.InputTokens
			out += mu.OutputTokens
			reasoning += mu.ThinkingTokens
			cacheRead += mu.CacheReadInputTokens
			cacheWrite += mu.CacheCreationInputTokens
			modelCost += mu.CostUSD
		}
		if env.Usage != nil && env.Usage.thinkingTokens() > reasoning {
			reasoning = env.Usage.thinkingTokens()
		}
		if t.msgInfo.Cost == 0 && modelCost > 0 {
			t.msgInfo.Cost = modelCost
		}
		total := in + out + reasoning + cacheRead + cacheWrite
		if total > 0 {
			tokens := &MessageTokens{
				Total:     total,
				Input:     in,
				Output:    out,
				Reasoning: reasoning,
			}
			tokens.Cache.Read = cacheRead
			tokens.Cache.Write = cacheWrite
			t.msgInfo.Tokens = tokens
			return
		}
	}
	if env.Usage != nil {
		in := env.Usage.InputTokens
		out := env.Usage.OutputTokens
		reasoning := env.Usage.thinkingTokens()
		cacheRead := env.Usage.CacheReadInputTokens
		cacheWrite := env.Usage.CacheCreationInputTokens
		// Fold in any child subagent messages when modelUsage was not emitted.
		for k, cu := range t.claudeMsgUsage {
			if strings.HasPrefix(k, "child:") {
				in += cu.InputTokens
				out += cu.OutputTokens
				reasoning += cu.thinkingTokens()
				cacheRead += cu.CacheReadInputTokens
				cacheWrite += cu.CacheCreationInputTokens
			}
		}
		if streamed := sumClaudeUsageMap(t.claudeMsgUsage); streamed != nil {
			in = max(in, streamed.Input)
			out = max(out, streamed.Output)
			reasoning = max(reasoning, streamed.Reasoning)
			cacheRead = max(cacheRead, streamed.Cache.Read)
			cacheWrite = max(cacheWrite, streamed.Cache.Write)
		}
		total := in + out + reasoning + cacheRead + cacheWrite
		if total > 0 {
			tokens := &MessageTokens{
				Total:     total,
				Input:     in,
				Output:    out,
				Reasoning: reasoning,
			}
			tokens.Cache.Read = cacheRead
			tokens.Cache.Write = cacheWrite
			t.msgInfo.Tokens = tokens
		}
	}
}

func (t *TurnTranslator) advanceClaudeMessage(id string, forceNewStep bool) {
	id = strings.TrimSpace(id)
	if forceNewStep || (id != "" && t.claudeMessageID != "" && id != t.claudeMessageID) {
		t.claudeReasonPartID = ""
		t.claudeTextPartID = ""
		t.stepStartMs = make(map[int]int64)
	}
	if id != "" {
		t.claudeMessageID = id
	}
}

func (t *TurnTranslator) closeActiveClaudeReasoning(now int64) []SSEEvent {
	if t.claudeReasonPartID == "" {
		return nil
	}
	existing, exists := t.partsByID[t.claudeReasonPartID]
	if !exists || existing == nil || existing.Type != "reasoning" {
		return nil
	}
	if existing.Time != nil && existing.Time.End > 0 {
		return nil
	}
	start := t.startedAtMs
	if existing.Time != nil && existing.Time.Start > 0 {
		start = existing.Time.Start
	}
	end := now
	if end < start {
		end = start
	}
	existing.Time = &PartTime{Start: start, End: end}
	return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*existing)}}}
}

func (t *TurnTranslator) currentClaudeReasonPartID() string {
	if t.claudeReasonPartID == "" {
		t.claudeReasonPartID = fmt.Sprintf("prt_%s_%s_reason_0", t.messageID, t.claudeMessageID)
	}
	return t.claudeReasonPartID
}

func (t *TurnTranslator) currentClaudeTextPartID() string {
	if t.claudeTextPartID == "" {
		t.claudeTextPartID = fmt.Sprintf("prt_%s_%s_text_0", t.messageID, t.claudeMessageID)
	}
	return t.claudeTextPartID
}

func (t *TurnTranslator) feedClaudeLine(line string) []SSEEvent {
	var env claudeRawEvent
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}
	if env.SessionID != "" {
		t.externalID = env.SessionID
	}
	now := time.Now().UnixMilli()
	if env.Type == "system" && env.Subtype == "api_retry" {
		return []SSEEvent{t.claudeRetryStatus(&env, now)}
	}
	evts := t.feedClaudeEvent(&env, now)
	if t.claudeRetrying && env.Type != "system" {
		// Output after a retry means the request went through.
		t.claudeRetrying = false
		busy := SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "busy"}}}
		evts = append([]SSEEvent{busy}, evts...)
	}
	return evts
}

// claudeRetryStatus surfaces Claude Code's own API backoff (`system/api_retry`)
// as a retry status, so the timeline shows a countdown instead of silence.
func (t *TurnTranslator) claudeRetryStatus(env *claudeRawEvent, now int64) SSEEvent {
	t.claudeRetrying = true
	status := SessionStatus{
		Type:    "retry",
		Attempt: env.Attempt,
		Message: claudeRetryMessage(env.Error, env.ErrorStatus),
		Next:    now + int64(env.RetryDelayMs),
	}
	return SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": status}}
}

func claudeRetryMessage(category string, httpStatus *int) string {
	labels := map[string]string{
		"rate_limit":             "Rate limited",
		"overloaded":             "Provider is overloaded",
		"server_error":           "Server error",
		"authentication_failed":  "Authentication failed",
		"cloud_credential_error": "Cloud credentials error",
	}
	msg := labels[category]
	if msg == "" {
		msg = "API request failed"
	}
	if httpStatus != nil && *httpStatus > 0 {
		msg += fmt.Sprintf(" (HTTP %d)", *httpStatus)
	}
	return msg
}

func (t *TurnTranslator) feedClaudeEvent(env *claudeRawEvent, now int64) []SSEEvent {
	if env.ParentToolUseID != "" {
		return t.feedClaudeChildLine(env, now)
	}

	switch env.Type {
	case "system":
		if env.Subtype == "task_notification" && env.ToolUseID != "" {
			return t.finishClaudeSubagent(env.ToolUseID, strings.TrimSpace(env.Summary), env.Status == "failed" || env.Status == "error", now)
		}
		if env.Subtype == "init" && env.Model != "" && t.msgInfo.ModelID == "" {
			t.msgInfo.ModelID = env.Model
			return []SSEEvent{{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}}}
		}
		return nil

	case "stream_event":
		if env.Event == nil {
			return nil
		}
		if env.Event.Type == "message_start" {
			msgID := env.APIMessageID
			if env.Event.Message != nil {
				if env.Event.Message.ID != "" {
					msgID = env.Event.Message.ID
				}
				if env.Event.Message.Model != "" && t.msgInfo.ModelID == "" {
					t.msgInfo.ModelID = env.Event.Message.Model
				}
				t.noteClaudeMsgUsage(msgID, env.Event.Message.Usage)
			}
			t.advanceClaudeMessage(msgID, true)
		} else if env.APIMessageID != "" {
			t.advanceClaudeMessage(env.APIMessageID, false)
		}
		if env.Event.Usage != nil {
			t.noteClaudeMsgUsage(firstNonEmptyStr(t.claudeMessageID, env.APIMessageID), env.Event.Usage)
		}
		if env.Event.Type == "content_block_start" && env.Event.ContentBlock != nil &&
			env.Event.ContentBlock.Type != "" && env.Event.ContentBlock.Type != "thinking" {
			return t.closeActiveClaudeReasoning(now)
		}
		if env.Event.Delta == nil {
			return nil
		}
		idx := env.Event.Index
		if _, ok := t.stepStartMs[idx]; !ok {
			t.stepStartMs[idx] = now
		}
		startMs := t.stepStartMs[idx]
		switch env.Event.Delta.Type {
		case "thinking_delta":
			delta := env.Event.Delta.Thinking
			if delta == "" {
				return nil
			}
			rID := t.currentClaudeReasonPartID()
			var evts []SSEEvent
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
				t.partOrder = append(t.partOrder, rID)
				evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
			}
			part.Text += delta
			evts = append(evts, SSEEvent{
				Type: "message.part.delta",
				Properties: map[string]interface{}{
					"sessionID": t.sessionID,
					"messageID": t.messageID,
					"partID":    rID,
					"field":     "text",
					"delta":     delta,
				},
			})
			return evts

		case "text_delta":
			delta := env.Event.Delta.Text
			if delta == "" {
				return nil
			}
			evts := t.closeActiveClaudeReasoning(now)
			t.hasTextPart = true
			txtID := t.currentClaudeTextPartID()
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
			part.Text += delta
			evts = append(evts, SSEEvent{
				Type: "message.part.delta",
				Properties: map[string]interface{}{
					"sessionID": t.sessionID,
					"messageID": t.messageID,
					"partID":    txtID,
					"field":     "text",
					"delta":     delta,
				},
			})
			return evts
		}
		return nil

	case "assistant":
		if env.Message == nil {
			return nil
		}
		// Claude Code and CodeBuddy emit a separate "assistant" snapshot per
		// content block (each with len(content) == 1 at slice index 0), while
		// stream_event uses the API block index across thinking/text/tool_use.
		msgID := firstNonEmptyStr(env.Message.ID, env.APIMessageID)
		t.advanceClaudeMessage(msgID, false)
		if env.Message.Model != "" && t.msgInfo.ModelID == "" {
			t.msgInfo.ModelID = env.Message.Model
		}
		t.noteClaudeMsgUsage(msgID, env.Message.Usage)
		var evts []SSEEvent
		for idx, blk := range env.Message.Content {
			switch blk.Type {
			case "thinking":
				if strings.TrimSpace(blk.Thinking) == "" {
					continue
				}
				rID := t.currentClaudeReasonPartID()
				if existing, exists := t.partsByID[rID]; exists {
					start := t.startedAtMs
					end := now
					if existing.Time != nil {
						if existing.Time.Start > 0 {
							start = existing.Time.Start
						}
						if existing.Time.End > 0 {
							end = existing.Time.End
						}
					}
					existing.Text = blk.Thinking
					existing.Time = &PartTime{Start: start, End: end}
					evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*existing)}})
				} else {
					part := MessagePart{
						ID:        rID,
						SessionID: t.sessionID,
						MessageID: t.messageID,
						Type:      "reasoning",
						Text:      blk.Thinking,
						Time:      &PartTime{Start: t.startedAtMs, End: now},
					}
					t.partsByID[rID] = &part
					t.partOrder = append(t.partOrder, rID)
					evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
				}
			case "text":
				if strings.TrimSpace(blk.Text) == "" {
					continue
				}
				evts = append(evts, t.closeActiveClaudeReasoning(now)...)
				t.hasTextPart = true
				txtID := t.currentClaudeTextPartID()
				if existing, exists := t.partsByID[txtID]; exists {
					start := t.startedAtMs
					if existing.Time != nil && existing.Time.Start > 0 {
						start = existing.Time.Start
					}
					existing.Text = blk.Text
					existing.Time = &PartTime{Start: start, End: now}
					evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*existing)}})
				} else {
					part := MessagePart{
						ID:        txtID,
						SessionID: t.sessionID,
						MessageID: t.messageID,
						Type:      "text",
						Text:      blk.Text,
						Time:      &PartTime{Start: t.startedAtMs, End: now},
					}
					t.partsByID[txtID] = &part
					t.partOrder = append(t.partOrder, txtID)
					evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
				}
			case "tool_use":
				evts = append(evts, t.closeActiveClaudeReasoning(now)...)
				callID := blk.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%s_%d", t.messageID, idx)
				}
				partID := "prt_tool_" + callID
				normTool, title, cleanInput := normalizeRunnerTool(blk.Name, blk.Input)
				part := MessagePart{
					ID:        partID,
					SessionID: t.sessionID,
					MessageID: t.messageID,
					Type:      "tool",
					Tool:      normTool,
					CallID:    callID,
					State: &ToolState{
						Status: "running",
						Input:  cleanInput,
						Title:  title,
						Time:   &PartTime{Start: now},
					},
				}
				t.partsByID[partID] = &part
				t.partOrder = append(t.partOrder, partID)
				if isClaudeSubagentTool(blk.Name) {
					evts = append(evts, t.startClaudeSubagent(callID, partID, blk.Input, now)...)
				}
				evts = append(evts, partUpdated(part))
			}
		}
		return evts

	case "user":
		if env.Message == nil {
			return nil
		}
		t.claudeMessageID = ""
		t.claudeReasonPartID = ""
		t.claudeTextPartID = ""
		t.stepStartMs = make(map[int]int64)
		var evts []SSEEvent
		for _, blk := range env.Message.Content {
			if blk.Type != "tool_result" || blk.ToolUseID == "" {
				continue
			}
			if task := t.claudeTasks[blk.ToolUseID]; task != nil {
				// A background launch acknowledges immediately; its report comes
				// later as a task_notification.
				if !task.background {
					report := claudeSubagentReport(claudeToolResultText(blk.Content))
					evts = append(evts, t.finishClaudeSubagent(blk.ToolUseID, report, blk.IsError, now)...)
				}
				continue
			}
			partID := "prt_tool_" + blk.ToolUseID
			part, exists := t.partsByID[partID]
			if !exists || part.State == nil {
				continue
			}
			startMs := part.State.Time.Start
			outStr := ""
			if s, ok := blk.Content.(string); ok {
				outStr = s
			} else if raw, err := json.Marshal(blk.Content); err == nil {
				outStr = string(raw)
			}
			if blk.IsError {
				part.State.Status = "error"
				part.State.Error = outStr
			} else {
				part.State.Status = "completed"
				part.State.Output = outStr
			}
			part.State.Time = &PartTime{Start: startMs, End: now}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
		return evts

	case "result":
		t.turnFinished = true
		t.msgInfo.Time.Completed = now
		t.applyClaudeResultUsage(env)
		evts := t.finishClaudeSubagents(env.IsError, now)
		evts = append(evts, t.closeAllChildren(env.IsError, now)...)
		if env.IsError {
			errMsg := strings.TrimSpace(env.Result)
			if errMsg == "" {
				errMsg = strings.TrimSpace(env.Error)
			}
			if errMsg == "" {
				errMsg = "Claude Code CLI execution failed"
			}
			msgErr := &MessageError{
				Name: "RunnerError",
				Data: &MessageErrorData{Message: errMsg, IsRetryable: false},
			}
			t.msgInfo.Error = msgErr
			t.msgInfo.Finish = "error"
			evts = append(evts,
				SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
				SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": t.sessionID, "error": msgErr}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
			)
			return evts
		}
		if !t.hasTextPart && strings.TrimSpace(env.Result) != "" {
			txtID := fmt.Sprintf("prt_%s_text_final", t.messageID)
			part := MessagePart{
				ID:        txtID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "text",
				Text:      env.Result,
				Time:      &PartTime{Start: t.startedAtMs, End: now},
			}
			t.partsByID[txtID] = &part
			t.partOrder = append(t.partOrder, txtID)
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}})
		}
		t.msgInfo.Finish = "stop"
		evts = append(evts,
			SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}},
			SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
			SSEEvent{Type: "session.idle", Properties: map[string]interface{}{"sessionID": t.sessionID}},
		)
		return evts
	}
	return nil
}

func normalizeRunnerTool(rawName string, rawParams map[string]interface{}) (normTool, title string, clean map[string]interface{}) {
	clean = make(map[string]interface{})
	for k, v := range rawParams {
		clean[k] = v
	}
	if fp := firstString(clean["AbsolutePath"], clean["TargetFile"], clean["Path"], clean["SearchPath"], clean["DirectoryPath"], clean["file_path"], clean["path"]); fp != "" {
		clean["filePath"] = fp
	}
	if cmd := firstString(clean["CommandLine"], clean["command"]); cmd != "" {
		clean["command"] = cmd
	}
	lower := strings.ToLower(strings.TrimSpace(rawName))
	switch lower {
	case "view_file", "read", "read_file":
		normTool = "read"
	case "write_to_file", "write_file", "write", "create_file":
		normTool = "write"
	case "replace_file_content", "multi_replace_file_content", "sed_file", "edit", "edit_file":
		normTool = "edit"
	case "run_command", "bash", "shell":
		normTool = "bash"
	case "grep_search", "grep":
		normTool = "grep"
	case "find_by_name", "list_dir", "glob":
		normTool = "glob"
	case "search_web", "websearch":
		normTool = "websearch"
	case "read_url_content", "webfetch":
		normTool = "webfetch"
	case "invoke_subagent", "task", "agent":
		normTool = "task"
	case "schedule":
		normTool = "agent_wait"
	case "manage_subagents":
		normTool = "agent_status"
	default:
		normTool = lower
		if normTool == "" {
			normTool = "tool"
		}
	}
	title = firstString(clean["toolSummary"], clean["toolAction"], clean["description"], clean["Prompt"], clean["Action"], rawName)
	return normTool, title, clean
}

func firstString(vals ...interface{}) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
