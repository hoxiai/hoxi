package runner

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type geminiStreamTokenBucket struct {
	Prompt     int `json:"prompt"`
	Input      int `json:"input"`
	Candidates int `json:"candidates"`
	Output     int `json:"output"`
	Total      int `json:"total"`
	Cached     int `json:"cached"`
	Thoughts   int `json:"thoughts"`
}

type geminiStreamStats struct {
	TotalTokens  int                      `json:"total_tokens"`
	InputTokens  int                      `json:"input_tokens"`
	OutputTokens int                      `json:"output_tokens"`
	Input        int                      `json:"input"`
	Cached       int                      `json:"cached"`
	Thoughts     int                      `json:"thoughts"`
	Tokens       *geminiStreamTokenBucket `json:"tokens"`
	Models       map[string]struct {
		Tokens geminiStreamTokenBucket `json:"tokens"`
	} `json:"models"`
}

type geminiRawEvent struct {
	Type       string                 `json:"type"`
	Event      string                 `json:"event"`
	SessionID  string                 `json:"session_id"`
	Model      string                 `json:"model"`
	Role       string                 `json:"role"`
	Content    string                 `json:"content"`
	Delta      bool                   `json:"delta"`
	Thought    string                 `json:"thought"`
	Thinking   string                 `json:"thinking"`
	ToolName   string                 `json:"tool_name"`
	Name       string                 `json:"name"`
	ToolID     string                 `json:"tool_id"`
	ID         string                 `json:"id"`
	Parameters map[string]interface{} `json:"parameters"`
	Args       map[string]interface{} `json:"args"`
	Input      map[string]interface{} `json:"input"`
	Status     string                 `json:"status"`
	Output     interface{}            `json:"output"`
	Response   string                 `json:"response"`
	Severity   string                 `json:"severity"`
	Message    string                 `json:"message"`
	Error      json.RawMessage        `json:"error"`
	Stats      *geminiStreamStats     `json:"stats"`
}

func (t *TurnTranslator) currentGeminiReasonPartID() string {
	if t.geminiReasonPartID == "" {
		t.geminiReasonPartID = fmt.Sprintf("prt_%s_reason_%d", t.messageID, t.geminiStepIdx)
	}
	return t.geminiReasonPartID
}

func (t *TurnTranslator) currentGeminiTextPartID() string {
	if t.geminiTextPartID == "" {
		t.geminiTextPartID = fmt.Sprintf("prt_%s_text_%d", t.messageID, t.geminiStepIdx)
	}
	return t.geminiTextPartID
}

func (t *TurnTranslator) closeActiveGeminiPart(partID string, now int64) []SSEEvent {
	if partID == "" {
		return nil
	}
	existing, exists := t.partsByID[partID]
	if !exists || existing == nil {
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

func (t *TurnTranslator) streamGeminiReasoning(delta string, isDelta bool, now int64) []SSEEvent {
	if delta == "" {
		return nil
	}
	rID := t.currentGeminiReasonPartID()
	var evts []SSEEvent
	part, exists := t.partsByID[rID]
	if !exists {
		part = &MessagePart{
			ID:        rID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "reasoning",
			Text:      "",
			Time:      &PartTime{Start: now},
		}
		t.partsByID[rID] = part
		t.partOrder = append(t.partOrder, rID)
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		part.Text = delta
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
	}
	if isDelta {
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
	}
	part.Text = delta
	evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
	return evts
}

func (t *TurnTranslator) feedGeminiLine(line string) []SSEEvent {
	var env geminiRawEvent
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}
	if strings.TrimSpace(env.Type) == "" && strings.TrimSpace(env.Event) != "" {
		return t.feedAgyLine(line)
	}
	now := time.Now().UnixMilli()
	t.lastActivityMs = now
	if sid := strings.TrimSpace(env.SessionID); sid != "" {
		t.externalID = sid
	}

	switch env.Type {
	case "init":
		if m := strings.TrimSpace(env.Model); m != "" && (t.msgInfo.ModelID == "" || strings.EqualFold(t.msgInfo.ModelID, "auto")) {
			t.msgInfo.ModelID = m
			return []SSEEvent{{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}}}
		}
		return nil

	case "thought", "thinking":
		thought := firstNonEmptyStr(env.Content, env.Thought, env.Thinking, env.Message)
		return t.streamGeminiReasoning(thought, env.Delta || env.Content != "", now)

	case "message":
		role := strings.ToLower(strings.TrimSpace(env.Role))
		if role == "user" {
			return nil
		}
		var evts []SSEEvent
		if thought := firstNonEmptyStr(env.Thought, env.Thinking); thought != "" {
			evts = append(evts, t.streamGeminiReasoning(thought, env.Delta, now)...)
		}
		if env.Content == "" {
			return evts
		}
		evts = append(evts, t.closeActiveGeminiPart(t.geminiReasonPartID, now)...)
		t.hasTextPart = true
		txtID := t.currentGeminiTextPartID()
		part, exists := t.partsByID[txtID]
		if !exists {
			part = &MessagePart{
				ID:        txtID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "text",
				Text:      "",
				Time:      &PartTime{Start: now},
			}
			t.partsByID[txtID] = part
			t.partOrder = append(t.partOrder, txtID)
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
			part.Text = env.Content
			evts = append(evts, SSEEvent{
				Type: "message.part.delta",
				Properties: map[string]interface{}{
					"sessionID": t.sessionID,
					"messageID": t.messageID,
					"partID":    txtID,
					"field":     "text",
					"delta":     env.Content,
				},
			})
			return evts
		}
		if env.Delta {
			part.Text += env.Content
			evts = append(evts, SSEEvent{
				Type: "message.part.delta",
				Properties: map[string]interface{}{
					"sessionID": t.sessionID,
					"messageID": t.messageID,
					"partID":    txtID,
					"field":     "text",
					"delta":     env.Content,
				},
			})
			return evts
		}
		part.Text = env.Content
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		return evts

	case "tool_use":
		var evts []SSEEvent
		evts = append(evts, t.closeActiveGeminiPart(t.geminiReasonPartID, now)...)
		evts = append(evts, t.closeActiveGeminiPart(t.geminiTextPartID, now)...)
		t.geminiStepIdx++
		t.geminiReasonPartID = ""
		t.geminiTextPartID = ""

		callID := firstNonEmptyStr(env.ToolID, env.ID, fmt.Sprintf("call_%s_%d", t.messageID, t.geminiStepIdx))
		rawName := firstNonEmptyStr(env.ToolName, env.Name)
		rawParams := env.Parameters
		if len(rawParams) == 0 {
			rawParams = env.Args
		}
		if len(rawParams) == 0 {
			rawParams = env.Input
		}
		normTool, title, cleanInput := normalizeRunnerTool(rawName, rawParams)
		partID := "prt_tool_" + callID
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
		evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(part)}})
		return evts

	case "tool_result":
		callID := firstNonEmptyStr(env.ToolID, env.ID)
		if callID == "" {
			return nil
		}
		partID := "prt_tool_" + callID
		part, exists := t.partsByID[partID]
		if !exists || part == nil || part.State == nil {
			return nil
		}
		startMs := now
		if part.State.Time != nil && part.State.Time.Start > 0 {
			startMs = part.State.Time.Start
		}
		outStr := geminiToolOutputString(env.Output)
		errStr := geminiErrorText(env.Error)
		if strings.EqualFold(env.Status, "error") || errStr != "" {
			part.State.Status = "error"
			part.State.Error = firstNonEmptyStr(errStr, outStr, "tool execution failed")
		} else {
			part.State.Status = "completed"
			part.State.Output = outStr
		}
		part.State.Time = &PartTime{Start: startMs, End: now}
		return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}}}

	case "error":
		if !strings.EqualFold(strings.TrimSpace(env.Severity), "warning") {
			if msg := firstNonEmptyStr(env.Message, geminiErrorText(env.Error)); msg != "" {
				t.geminiLastErrMsg = msg
			}
		}
		return nil

	case "result":
		t.turnFinished = true
		t.msgInfo.Time.Completed = now
		t.applyGeminiStats(env.Stats)

		var evts []SSEEvent
		evts = append(evts, t.closeActiveGeminiPart(t.geminiReasonPartID, now)...)
		evts = append(evts, t.closeActiveGeminiPart(t.geminiTextPartID, now)...)

		errText := geminiErrorText(env.Error)
		isErr := strings.EqualFold(env.Status, "error") || errText != ""
		if isErr {
			errMsg := firstNonEmptyStr(errText, env.Message, t.geminiLastErrMsg, "Gemini CLI execution failed")
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
		if !t.hasTextPart && strings.TrimSpace(env.Response) != "" {
			txtID := fmt.Sprintf("prt_%s_text_final", t.messageID)
			part := MessagePart{
				ID:        txtID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "text",
				Text:      env.Response,
				Time:      &PartTime{Start: t.startedAtMs, End: now},
			}
			t.partsByID[txtID] = &part
			t.partOrder = append(t.partOrder, txtID)
			t.hasTextPart = true
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

func (t *TurnTranslator) applyGeminiStats(stats *geminiStreamStats) {
	if stats == nil {
		return
	}
	inTok := firstNonZeroInt(stats.InputTokens, stats.Input)
	outTok := stats.OutputTokens
	reasonTok := stats.Thoughts
	cacheRead := stats.Cached
	totalTok := stats.TotalTokens
	if stats.Tokens != nil {
		inTok = firstNonZeroInt(inTok, stats.Tokens.Prompt, stats.Tokens.Input)
		outTok = firstNonZeroInt(outTok, stats.Tokens.Candidates, stats.Tokens.Output)
		reasonTok = firstNonZeroInt(reasonTok, stats.Tokens.Thoughts)
		cacheRead = firstNonZeroInt(cacheRead, stats.Tokens.Cached)
		totalTok = firstNonZeroInt(totalTok, stats.Tokens.Total)
	}
	if len(stats.Models) > 0 {
		var mIn, mOut, mReason, mCache, mTotal int
		for _, m := range stats.Models {
			mIn += firstNonZeroInt(m.Tokens.Prompt, m.Tokens.Input)
			mOut += firstNonZeroInt(m.Tokens.Candidates, m.Tokens.Output)
			mReason += m.Tokens.Thoughts
			mCache += m.Tokens.Cached
			mTotal += m.Tokens.Total
		}
		inTok = firstNonZeroInt(inTok, mIn)
		outTok = firstNonZeroInt(outTok, mOut)
		reasonTok = firstNonZeroInt(reasonTok, mReason)
		cacheRead = firstNonZeroInt(cacheRead, mCache)
		totalTok = firstNonZeroInt(totalTok, mTotal)
	}
	if totalTok == 0 {
		totalTok = inTok + outTok + reasonTok + cacheRead
	}
	if totalTok == 0 && inTok == 0 && outTok == 0 {
		return
	}
	tokens := &MessageTokens{
		Total:     totalTok,
		Input:     inTok,
		Output:    outTok,
		Reasoning: reasonTok,
	}
	tokens.Cache.Read = cacheRead
	t.msgInfo.Tokens = tokens
}

func firstNonZeroInt(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func geminiToolOutputString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func geminiErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return firstNonEmptyStr(obj.Message, obj.Type)
	}
	return strings.TrimSpace(string(raw))
}
