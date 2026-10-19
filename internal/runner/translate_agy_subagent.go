package runner

import (
	"fmt"
	"strings"
	"time"
)

// Antigravity dispatches subagents asynchronously: the `subagent` step goes
// ACTIVE -> DONE within milliseconds, the subagents' own steps never reach
// stdout, and each report comes back later as a content-less `system_message`
// step whose text only exists in the parent's brain transcript.
//
// Each dispatched subagent gets its own `task` part and child session. The
// part is pending until the dispatch is DONE, running until the subagent's
// report arrives, then completed with the report as output. The child session
// is filled from the subagent's own transcript, which agy names in `log_uri`.

type agySubagentInfo struct {
	Subagents []agySubagentSpec `json:"subagents"`
}

type agySubagentSpec struct {
	TypeName       string `json:"type_name"`
	Role           string `json:"role"`
	Model          string `json:"model"`
	InitialPrompt  string `json:"initial_prompt"`
	ConversationID string `json:"conversation_id"`
	LogURI         string `json:"log_uri"`
}

type agySubagentTrack struct {
	partID         string
	role           string
	typeName       string
	model          string
	prompt         string
	conversationID string
	logPath        string
	reported       bool
	lastActivityMs int64
	readOffset     int64
}

func isAgySubagentDispatch(su *agyStepUpdate) bool {
	if su.StepType == "subagent" {
		return true
	}
	name := su.ToolName
	if name == "" && su.ToolInfo != nil {
		name = su.ToolInfo.Name
	}
	return strings.EqualFold(strings.TrimSpace(name), "invoke_subagent")
}

// agySubagentSpecs reads the dispatch payload from `subagent_info` or, for the
// `tool` shaped variant, from `tool_info.parameters.Subagents`.
func agySubagentSpecs(su *agyStepUpdate) []agySubagentSpec {
	if su.SubagentInfo != nil && len(su.SubagentInfo.Subagents) > 0 {
		return su.SubagentInfo.Subagents
	}
	if su.ToolInfo == nil {
		return nil
	}
	list, _ := su.ToolInfo.Parameters["Subagents"].([]interface{})
	specs := make([]agySubagentSpec, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		specs = append(specs, agySubagentSpec{
			TypeName:       firstString(m["TypeName"], m["type_name"]),
			Role:           firstString(m["Role"], m["role"]),
			Model:          firstString(m["Model"], m["model"]),
			InitialPrompt:  firstString(m["Prompt"], m["InitialPrompt"], m["initial_prompt"]),
			ConversationID: firstString(m["ConversationId"], m["conversation_id"]),
		})
	}
	return specs
}

func (t *TurnTranslator) handleAgySubagentStep(su *agyStepUpdate, now int64) []SSEEvent {
	var evts []SSEEvent
	startMs := t.stepStartMs[su.StepIndex]
	for i, spec := range agySubagentSpecs(su) {
		partID := fmt.Sprintf("prt_%s_tool_%d_%d", t.messageID, su.StepIndex, i)
		tr := t.subagentByPart[partID]
		if tr == nil {
			tr = &agySubagentTrack{partID: partID, lastActivityMs: now}
			t.subagentByPart[partID] = tr
			t.subagentOrder = append(t.subagentOrder, partID)
		}
		tr.role = firstNonEmptyStr(spec.Role, tr.role, spec.TypeName, "subagent")
		tr.typeName = firstNonEmptyStr(spec.TypeName, tr.typeName)
		tr.model = firstNonEmptyStr(spec.Model, tr.model)
		tr.prompt = firstNonEmptyStr(spec.InitialPrompt, tr.prompt)
		if cid := strings.TrimSpace(spec.ConversationID); cid != "" && tr.conversationID == "" {
			tr.conversationID = cid
			t.subagentByConv[cid] = tr
		}
		if tr.logPath == "" {
			tr.logPath = resolveAgySubagentLog(spec.LogURI, tr.conversationID)
		}

		part, exists := t.partsByID[partID]
		if !exists {
			part = &MessagePart{
				ID:        partID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "tool",
				Tool:      "task",
				CallID:    fmt.Sprintf("call_%s_%d_%d", t.messageID, su.StepIndex, i),
				State:     &ToolState{Status: "pending", Time: &PartTime{Start: startMs}},
			}
			t.partsByID[partID] = part
			t.partOrder = append(t.partOrder, partID)
		}
		state := part.State
		state.Input = map[string]interface{}{
			"description":   tr.role,
			"subagent_type": firstNonEmptyStr(tr.typeName, "general"),
			"prompt":        tr.prompt,
		}
		if tr.model != "" {
			state.Input["model"] = tr.model
		}
		state.Title = tr.role
		switch {
		case tr.reported:
		case su.State == "ERROR":
			tr.reported = true
			state.Status = "error"
			state.Error = "subagent dispatch failed"
			if su.ToolInfo != nil && su.ToolInfo.Error != nil && su.ToolInfo.Error.Message != "" {
				state.Error = su.ToolInfo.Error.Message
			}
			state.Time = &PartTime{Start: startMs, End: now}
		case su.State == "DONE":
			state.Status = "running"
			evts = append(evts, t.openChild(partID, ChildSessionSpec{
				Title:      tr.role,
				Prompt:     tr.prompt,
				ModelID:    tr.model,
				ExternalID: tr.conversationID,
			}, now)...)
		}
		t.refreshSubagentMeta(tr)
		evts = append(evts, partUpdated(*part))
	}
	return evts
}

// handleAgySystemMessage attaches a subagent report to its task part.
func (t *TurnTranslator) handleAgySystemMessage(su *agyStepUpdate, now int64) []SSEEvent {
	var content string
	if t.lookupStep != nil && t.externalID != "" {
		content = t.lookupStep(t.externalID, su.StepIndex)
	}
	sender, body := parseAgySystemMessage(content)
	tr := t.subagentByConv[sender]
	if tr == nil || tr.reported {
		tr = t.firstUnreportedSubagent()
	}
	if tr != nil {
		return t.settleAgySubagent(tr, body, false, now)
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}
	// Not a subagent report (or one we cannot attribute): keep it visible.
	reportID := fmt.Sprintf("prt_%s_report_%d", t.messageID, su.StepIndex)
	report := &MessagePart{
		ID:        reportID,
		SessionID: t.sessionID,
		MessageID: t.messageID,
		Type:      "tool",
		Tool:      "agent_report",
		CallID:    fmt.Sprintf("call_%s_%d", t.messageID, su.StepIndex),
		State: &ToolState{
			Status: "completed",
			Input:  map[string]interface{}{"sender": sender},
			Output: body,
			Time:   &PartTime{Start: now, End: now + 1},
		},
	}
	if _, exists := t.partsByID[reportID]; !exists {
		t.partOrder = append(t.partOrder, reportID)
	}
	t.partsByID[reportID] = report
	return []SSEEvent{partUpdated(*report)}
}

// settleAgySubagent drains the subagent's transcript one last time, completes
// its task part (with the report, when known) and closes its child session.
func (t *TurnTranslator) settleAgySubagent(tr *agySubagentTrack, report string, failed bool, now int64) []SSEEvent {
	evts := t.pollAgySubagent(tr)
	tr.reported = true
	tr.lastActivityMs = now
	if part := t.partsByID[tr.partID]; part != nil && part.State != nil {
		if failed {
			part.State.Status = "error"
			part.State.Error = "turn ended before the subagent reported"
		} else {
			part.State.Status = "completed"
			part.State.Output = report
		}
		part.State.Time = &PartTime{Start: part.State.Time.Start, End: now}
		t.refreshSubagentMeta(tr)
		evts = append(evts, partUpdated(*part))
	}
	return append(evts, t.closeChild(tr.partID, failed, now)...)
}

// finishAgySubagents settles subagents that are still out when the turn ends
// and picks up rows reported subagents wrote after their report.
func (t *TurnTranslator) finishAgySubagents(failed bool, now int64) []SSEEvent {
	var evts []SSEEvent
	for _, partID := range t.subagentOrder {
		if tr := t.subagentByPart[partID]; !tr.reported {
			evts = append(evts, t.settleAgySubagent(tr, "", failed, now)...)
		} else {
			evts = append(evts, t.pollAgySubagent(tr)...)
		}
	}
	return evts
}

func (t *TurnTranslator) refreshSubagentMeta(tr *agySubagentTrack) {
	part := t.partsByID[tr.partID]
	if part == nil || part.State == nil {
		return
	}
	meta := map[string]interface{}{
		"runner":         string(RuntimeAntigravity),
		"lastActivityAt": max(tr.lastActivityMs, t.lastActivityMs),
	}
	if id := t.childSessionID(tr.partID); id != "" {
		meta["sessionId"] = id
	}
	part.State.Metadata = meta
}

func (t *TurnTranslator) firstUnreportedSubagent() *agySubagentTrack {
	for _, partID := range t.subagentOrder {
		if tr := t.subagentByPart[partID]; !tr.reported {
			return tr
		}
	}
	return nil
}

func (t *TurnTranslator) hasWaitingSubagents() bool {
	return t.firstUnreportedSubagent() != nil
}

// noteSubagentStep covers subagent steps leaking onto stdout under their own
// conversation ID: they count as subagent activity instead of becoming parts.
func (t *TurnTranslator) noteSubagentStep(su *agyStepUpdate, now int64) []SSEEvent {
	tr := t.subagentByConv[su.ConversationID]
	if tr == nil || tr.reported {
		return nil
	}
	tr.lastActivityMs = now
	t.refreshSubagentMeta(tr)
	return []SSEEvent{partUpdated(*t.partsByID[tr.partID])}
}

// PollSubagentActivity mirrors what running subagents appended to their brain
// transcripts into their child sessions. It only touches files while
// subagents are outstanding and emits nothing when they did not move.
func (t *TurnTranslator) PollSubagentActivity() []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turnFinished {
		return nil
	}
	var evts []SSEEvent
	for _, partID := range t.subagentOrder {
		tr := t.subagentByPart[partID]
		if tr.reported {
			continue
		}
		if childEvts := t.pollAgySubagent(tr); len(childEvts) > 0 {
			t.refreshSubagentMeta(tr)
			evts = append(evts, childEvts...)
			evts = append(evts, partUpdated(*t.partsByID[partID]))
		}
	}
	return evts
}

func (t *TurnTranslator) pollAgySubagent(tr *agySubagentTrack) []SSEEvent {
	if tr.logPath == "" {
		tr.logPath = resolveAgySubagentLog("", tr.conversationID)
	}
	if tr.logPath == "" {
		return nil
	}
	rows, modMs, ok := advanceAgySubagentLog(tr)
	if !ok {
		return nil
	}
	tr.lastActivityMs = max(tr.lastActivityMs, modMs)
	c := t.children[tr.partID]
	if c == nil {
		return nil
	}
	var evts []SSEEvent
	for _, row := range rows {
		evts = append(evts, agyRowToChildEvents(c, row, modMs)...)
	}
	return evts
}

// agyRowToChildEvents maps one subagent transcript row onto child parts:
// planner responses carry thinking, text and tool calls; GENERIC rows are
// the results of those calls, in order.
func agyRowToChildEvents(c *childSink, row agyTranscriptRow, fallbackMs int64) []SSEEvent {
	at := fallbackMs
	if ts, err := time.Parse(time.RFC3339, row.CreatedAt); err == nil {
		at = ts.UnixMilli()
	}
	var evts []SSEEvent
	switch row.Type {
	case "PLANNER_RESPONSE":
		evts = append(evts, c.upsertText("reasoning", fmt.Sprintf("reason_%d", row.StepIndex), row.Thinking, at, at)...)
		evts = append(evts, c.upsertText("text", fmt.Sprintf("text_%d", row.StepIndex), row.Content, at, at)...)
		for j, call := range row.ToolCalls {
			suffix := fmt.Sprintf("tool_%d_%d", row.StepIndex, j)
			evts = append(evts, c.startTool(suffix, "", call.Name, decodeAgyToolArgs(call.Args), at)...)
		}
	case "GENERIC":
		evts = append(evts, c.finishTool("", stripAgyResultHeader(row.Content), false, at)...)
	}
	return evts
}

// parseAgySystemMessage extracts sender and body from a transcript row such as
// "<SYSTEM_MESSAGE>\n[Message] timestamp=… sender=<conv> priority=… content=<text>".
func parseAgySystemMessage(raw string) (sender, body string) {
	text := strings.TrimSpace(raw)
	if i := strings.Index(text, "<SYSTEM_MESSAGE>"); i >= 0 {
		text = text[i+len("<SYSTEM_MESSAGE>"):]
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "</SYSTEM_MESSAGE>"))
	header := text
	if i := strings.Index(header, "content="); i >= 0 {
		header = header[:i]
		body = strings.TrimSpace(text[i+len("content="):])
	} else {
		body = text
	}
	for _, field := range strings.Fields(header) {
		if v, ok := strings.CutPrefix(field, "sender="); ok {
			sender = v
		}
	}
	return sender, body
}

func partUpdated(p MessagePart) SSEEvent {
	return SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(p)}}
}
