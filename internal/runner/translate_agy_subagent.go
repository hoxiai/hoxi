package runner

import (
	"fmt"
	"strings"
	"time"
)

// Antigravity dispatches subagents asynchronously: the `subagent` step goes
// ACTIVE -> DONE within milliseconds, the subagents' own steps never reach
// stdout, and each report comes back later as a content-less `system_message`
// step whose text only exists in the parent's brain transcript. The `task`
// part therefore stays running after the dispatch is DONE and completes once
// every dispatched subagent has reported (or the turn ends).

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
	steps          int
	currentTool    string
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
	partID := fmt.Sprintf("prt_%s_tool_%d", t.messageID, su.StepIndex)
	tracks := t.subagentsByPart[partID]
	for i, spec := range agySubagentSpecs(su) {
		if i >= len(tracks) {
			tracks = append(tracks, &agySubagentTrack{partID: partID, lastActivityMs: now})
		}
		tr := tracks[i]
		tr.role = firstNonEmptyStr(spec.Role, tr.role)
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
	}
	t.subagentsByPart[partID] = tracks

	part, exists := t.partsByID[partID]
	if !exists {
		part = &MessagePart{
			ID:        partID,
			SessionID: t.sessionID,
			MessageID: t.messageID,
			Type:      "tool",
			Tool:      "task",
			CallID:    fmt.Sprintf("call_%s_%d", t.messageID, su.StepIndex),
		}
		t.partsByID[partID] = part
		t.partOrder = append(t.partOrder, partID)
	}
	startMs := t.stepStartMs[su.StepIndex]
	state := &ToolState{
		Status: "running",
		Input:  subagentTaskInput(tracks),
		Title:  subagentRoles(tracks),
		Time:   &PartTime{Start: startMs},
	}
	if su.State == "ERROR" {
		state.Status = "error"
		state.Error = "subagent dispatch failed"
		if su.ToolInfo != nil && su.ToolInfo.Error != nil && su.ToolInfo.Error.Message != "" {
			state.Error = su.ToolInfo.Error.Message
		}
		state.Time = &PartTime{Start: startMs, End: now}
		for _, tr := range tracks {
			tr.reported = true
		}
	}
	part.State = state
	t.refreshSubagentTask(partID, now)
	return []SSEEvent{partUpdated(*part)}
}

// handleAgySystemMessage renders a subagent report and settles its task part.
func (t *TurnTranslator) handleAgySystemMessage(su *agyStepUpdate, now int64) []SSEEvent {
	var content string
	if t.lookupStep != nil && t.externalID != "" {
		content = t.lookupStep(t.externalID, su.StepIndex)
	}
	sender, body := parseAgySystemMessage(content)
	track := t.subagentByConv[sender]
	if track == nil {
		track = t.firstUnreportedSubagent()
	}
	if track == nil && strings.TrimSpace(body) == "" {
		return nil
	}

	input := map[string]interface{}{}
	title := ""
	if sender != "" {
		input["sender"] = sender
	}
	if track != nil {
		input["role"] = track.role
		title = track.role
	}
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
			Input:  input,
			Output: body,
			Title:  title,
			Time:   &PartTime{Start: now, End: now + 1},
		},
	}
	if _, exists := t.partsByID[reportID]; !exists {
		t.partOrder = append(t.partOrder, reportID)
	}
	t.partsByID[reportID] = report

	evts := []SSEEvent{}
	if track != nil {
		track.reported = true
		track.lastActivityMs = now
		if task := t.partsByID[track.partID]; task != nil {
			t.refreshSubagentTask(track.partID, now)
			evts = append(evts, partUpdated(*task))
		}
	}
	return append(evts, partUpdated(*report))
}

// finishAgySubagents settles task parts that are still running when the turn ends.
func (t *TurnTranslator) finishAgySubagents(failed bool, now int64) []SSEEvent {
	var evts []SSEEvent
	for _, partID := range t.partOrder {
		tracks, ok := t.subagentsByPart[partID]
		part := t.partsByID[partID]
		if !ok || part == nil || part.State == nil || part.State.Status != "running" {
			continue
		}
		for _, tr := range tracks {
			tr.reported = true
		}
		t.refreshSubagentTask(partID, now)
		if failed {
			part.State.Status = "error"
			part.State.Error = "turn ended before all subagents reported"
		}
		evts = append(evts, partUpdated(*part))
	}
	return evts
}

// refreshSubagentTask recomputes a task part's status, output and progress metadata.
func (t *TurnTranslator) refreshSubagentTask(partID string, now int64) {
	part := t.partsByID[partID]
	tracks := t.subagentsByPart[partID]
	if part == nil || part.State == nil {
		return
	}
	reported := 0
	lastActivity := t.lastActivityMs
	items := make([]map[string]interface{}, 0, len(tracks))
	for _, tr := range tracks {
		status := "running"
		if tr.reported {
			status = "reported"
			reported++
		}
		if tr.lastActivityMs > lastActivity {
			lastActivity = tr.lastActivityMs
		}
		items = append(items, map[string]interface{}{
			"role":           tr.role,
			"type":           tr.typeName,
			"conversationID": tr.conversationID,
			"status":         status,
			"steps":          tr.steps,
			"currentTool":    tr.currentTool,
			"lastActivityAt": tr.lastActivityMs,
		})
	}
	part.State.Metadata = map[string]interface{}{
		"runner":         string(RuntimeAntigravity),
		"subagents":      items,
		"reported":       reported,
		"total":          len(tracks),
		"lastActivityAt": lastActivity,
	}
	if part.State.Status == "error" {
		return
	}
	if len(tracks) > 0 && reported == len(tracks) {
		part.State.Status = "completed"
		part.State.Output = fmt.Sprintf("%d/%d subagents reported", reported, len(tracks))
		part.State.Time = &PartTime{Start: part.State.Time.Start, End: now}
	}
}

func (t *TurnTranslator) firstUnreportedSubagent() *agySubagentTrack {
	for _, partID := range t.partOrder {
		for _, tr := range t.subagentsByPart[partID] {
			if !tr.reported {
				return tr
			}
		}
	}
	return nil
}

func (t *TurnTranslator) hasWaitingSubagents() bool {
	return t.firstUnreportedSubagent() != nil
}

// noteSubagentStep covers subagent steps leaking onto stdout under their own
// conversation ID: they advance the parent's task part instead of becoming parts.
func (t *TurnTranslator) noteSubagentStep(su *agyStepUpdate, now int64) []SSEEvent {
	tr := t.subagentByConv[su.ConversationID]
	if tr == nil {
		return nil
	}
	if su.StepIndex+1 > tr.steps {
		tr.steps = su.StepIndex + 1
	}
	if name := firstNonEmptyStr(su.ToolName); name != "" {
		tr.currentTool = name
	}
	tr.lastActivityMs = now
	part := t.partsByID[tr.partID]
	if part == nil {
		return nil
	}
	t.refreshSubagentTask(tr.partID, now)
	return []SSEEvent{partUpdated(*part)}
}

// PollSubagentActivity reads what running subagents appended to their brain
// transcripts since the last poll. It only stats files while subagents are
// outstanding, and emits a part update only when something changed.
func (t *TurnTranslator) PollSubagentActivity() []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turnFinished || !t.hasWaitingSubagents() {
		return nil
	}
	now := time.Now().UnixMilli()
	changed := map[string]bool{}
	for _, partID := range t.partOrder {
		for _, tr := range t.subagentsByPart[partID] {
			if tr.reported {
				continue
			}
			if tr.logPath == "" {
				tr.logPath = resolveAgySubagentLog("", tr.conversationID)
			}
			if tr.logPath != "" && advanceAgySubagentLog(tr) {
				changed[partID] = true
			}
		}
	}
	var evts []SSEEvent
	for _, partID := range t.partOrder {
		if changed[partID] {
			t.refreshSubagentTask(partID, now)
			evts = append(evts, partUpdated(*t.partsByID[partID]))
		}
	}
	return evts
}

func subagentRoles(tracks []*agySubagentTrack) string {
	roles := make([]string, 0, len(tracks))
	for _, tr := range tracks {
		roles = append(roles, firstNonEmptyStr(tr.role, tr.typeName, "subagent"))
	}
	return strings.Join(roles, ", ")
}

func subagentTaskInput(tracks []*agySubagentTrack) map[string]interface{} {
	list := make([]map[string]interface{}, 0, len(tracks))
	for _, tr := range tracks {
		item := map[string]interface{}{"role": tr.role, "type": tr.typeName, "prompt": tr.prompt}
		if tr.model != "" {
			item["model"] = tr.model
		}
		list = append(list, item)
	}
	return map[string]interface{}{
		"description":   subagentRoles(tracks),
		"subagent_type": firstNonEmptyStr(firstTrackType(tracks), "general"),
		"subagents":     list,
	}
}

func firstTrackType(tracks []*agySubagentTrack) string {
	for _, tr := range tracks {
		if tr.typeName != "" {
			return tr.typeName
		}
	}
	return ""
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
	if nl := strings.Index(header, "content="); nl >= 0 {
		header = header[:nl]
		body = strings.TrimSpace(text[nl+len("content="):])
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
