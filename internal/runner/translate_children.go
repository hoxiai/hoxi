package runner

import (
	"fmt"
	"strings"
)

// Subagents of a runner turn (agy `invoke_subagent`, Claude Code `Agent`)
// are surfaced as child Hoxi sessions whose ParentID is the running session.
// The parent's `task` part carries `metadata.sessionId`, so the existing
// subagent UI (inline step list, banner, Open button) renders them the same
// way it renders OpenCode child sessions.

// ChildSessionSpec describes a subagent conversation to create a session for.
type ChildSessionSpec struct {
	Title      string
	Prompt     string
	ModelID    string
	ExternalID string
}

type childSink struct {
	info     SessionInfo
	asst     MessageInfo
	parts    map[string]*MessagePart
	pending  []string          // tool part IDs still waiting for a result, oldest first
	byCallID map[string]string // runtime tool call ID -> part ID
	closed   bool
}

// openChild creates the child session for key once; it returns nil when the
// host provides no session factory (tests, or a failed store write).
func (t *TurnTranslator) openChild(key string, spec ChildSessionSpec, now int64) []SSEEvent {
	if t.createChild == nil || t.children[key] != nil {
		return nil
	}
	info, ok := t.createChild(spec)
	if !ok {
		return nil
	}
	suffix := fmt.Sprintf("%d_%d", now, len(t.childOrder))
	user := MessageInfo{
		ID:        "msg_u_" + suffix,
		SessionID: info.ID,
		Role:      "user",
		Time:      MessageTime{Created: now},
		Agent:     t.mode,
	}
	c := &childSink{
		info: info,
		asst: MessageInfo{
			ID:         "msg_a_" + suffix,
			SessionID:  info.ID,
			Role:       "assistant",
			ParentID:   user.ID,
			Time:       MessageTime{Created: now + 1},
			ModelID:    firstNonEmptyStr(info.ModelID, t.msgInfo.ModelID),
			ProviderID: t.providerID,
			Mode:       t.mode,
			Agent:      t.mode,
		},
		parts:    make(map[string]*MessagePart),
		byCallID: make(map[string]string),
	}
	t.children[key] = c
	t.childOrder = append(t.childOrder, key)
	prompt := MessagePart{
		ID:        "prt_u_" + suffix,
		SessionID: info.ID,
		MessageID: user.ID,
		Type:      "text",
		Text:      firstNonEmptyStr(spec.Prompt, spec.Title),
	}
	return []SSEEvent{
		{Type: "session.created", Properties: map[string]interface{}{"info": info}},
		{Type: "session.status", Properties: map[string]interface{}{"sessionID": info.ID, "status": SessionStatus{Type: "busy"}}},
		{Type: "message.updated", Properties: map[string]interface{}{"info": user}},
		partUpdated(prompt),
		{Type: "message.updated", Properties: map[string]interface{}{"info": c.asst}},
	}
}

func (t *TurnTranslator) childSessionID(key string) string {
	if c := t.children[key]; c != nil {
		return c.info.ID
	}
	return ""
}

func (c *childSink) partID(suffix string) string {
	return "prt_" + c.asst.ID + "_" + suffix
}

// upsertText writes a whole text or reasoning part; subagent text arrives as
// complete blocks, never as deltas.
func (c *childSink) upsertText(kind, suffix, text string, start, end int64) []SSEEvent {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	id := c.partID(suffix)
	part := c.parts[id]
	if part == nil {
		part = &MessagePart{ID: id, SessionID: c.info.ID, MessageID: c.asst.ID, Type: kind}
		c.parts[id] = part
	}
	part.Text = text
	part.Time = &PartTime{Start: start, End: max(end, start)}
	return []SSEEvent{partUpdated(*part)}
}

func (c *childSink) startTool(suffix, callID, rawName string, params map[string]interface{}, start int64) []SSEEvent {
	id := c.partID(suffix)
	if c.parts[id] != nil {
		return nil
	}
	normTool, title, input := normalizeRunnerTool(rawName, params)
	part := &MessagePart{
		ID:        id,
		SessionID: c.info.ID,
		MessageID: c.asst.ID,
		Type:      "tool",
		Tool:      normTool,
		CallID:    firstNonEmptyStr(callID, id),
		State:     &ToolState{Status: "running", Input: input, Title: title, Time: &PartTime{Start: start}},
	}
	c.parts[id] = part
	c.pending = append(c.pending, id)
	if callID != "" {
		c.byCallID[callID] = id
	}
	return []SSEEvent{partUpdated(*part)}
}

// finishTool settles the tool part for callID, or the oldest open one when
// the runtime does not correlate results by ID.
func (c *childSink) finishTool(callID, output string, isError bool, end int64) []SSEEvent {
	id := c.byCallID[callID]
	if id == "" {
		if callID != "" || len(c.pending) == 0 {
			return nil
		}
		id = c.pending[0]
	}
	for i, p := range c.pending {
		if p == id {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			break
		}
	}
	part := c.parts[id]
	if part == nil || part.State == nil {
		return nil
	}
	part.State.Status = "completed"
	part.State.Output = output
	if isError {
		part.State.Status = "error"
		part.State.Error = firstNonEmptyStr(output, "tool execution failed")
		part.State.Output = ""
	}
	part.State.Time = &PartTime{Start: part.State.Time.Start, End: max(end, part.State.Time.Start+1)}
	return []SSEEvent{partUpdated(*part)}
}

// closeChild finishes the subagent's assistant message and marks it idle.
func (t *TurnTranslator) closeChild(key string, failed bool, now int64) []SSEEvent {
	c := t.children[key]
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	var evts []SSEEvent
	for len(c.pending) > 0 {
		evts = append(evts, c.finishTool("", "", failed, now)...)
	}
	c.asst.Time.Completed = now
	c.asst.Finish = "stop"
	if failed {
		c.asst.Finish = "aborted"
	}
	return append(evts,
		SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": c.asst}},
		SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": c.info.ID, "status": SessionStatus{Type: "idle"}}},
	)
}

func (t *TurnTranslator) closeAllChildren(failed bool, now int64) []SSEEvent {
	var evts []SSEEvent
	for _, key := range t.childOrder {
		evts = append(evts, t.closeChild(key, failed, now)...)
	}
	return evts
}

// ChildSessionIDs lists every child session this turn created.
func (t *TurnTranslator) ChildSessionIDs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.childOrder))
	for _, key := range t.childOrder {
		ids = append(ids, t.children[key].info.ID)
	}
	return ids
}

// AbandonTurn settles subagents when the CLI exits without a result event.
func (t *TurnTranslator) AbandonTurn(now int64) []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	evts := t.finishAgySubagents(true, now)
	evts = append(evts, t.finishClaudeSubagents(true, now)...)
	return append(evts, t.closeAllChildren(true, now)...)
}

// eventSessionID returns the session an event belongs to, so a turn's
// events can be routed to its child sessions.
func eventSessionID(evt SSEEvent) string {
	if sid, ok := evt.Properties["sessionID"].(string); ok && sid != "" {
		return sid
	}
	switch v := evt.Properties["info"].(type) {
	case MessageInfo:
		return v.SessionID
	case SessionInfo:
		return v.ID
	}
	if part, ok := evt.Properties["part"].(MessagePart); ok {
		return part.SessionID
	}
	return ""
}
