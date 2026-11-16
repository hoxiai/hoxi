package runner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Claude Code runs subagents through its `Agent` (formerly `Task`) tool. The
// subagent's own messages arrive on the same stream tagged with
// `parent_tool_use_id`; its report is the Agent tool_result (foreground) or a
// `system/task_notification` (background). Inner messages go to the
// subagent's child session instead of the parent's timeline.

type claudeSubagentTask struct {
	partID     string
	background bool
	done       bool
}

func isClaudeSubagentTool(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "agent" || lower == "task"
}

// startClaudeSubagent opens the child session for an Agent tool_use and links
// it from the parent's task part.
func (t *TurnTranslator) startClaudeSubagent(callID, partID string, input map[string]interface{}, now int64) []SSEEvent {
	task := &claudeSubagentTask{partID: partID}
	task.background, _ = input["run_in_background"].(bool)
	t.claudeTasks[callID] = task
	evts := t.openChild(callID, ChildSessionSpec{
		Title:   firstString(input["description"], input["subagent_type"], "subagent"),
		Prompt:  firstString(input["prompt"], input["description"]),
		ModelID: firstString(input["model"]),
	}, now)
	if part := t.partsByID[partID]; part != nil && part.State != nil {
		meta := map[string]interface{}{"runner": string(RuntimeClaudeCode), "lastActivityAt": now}
		if id := t.childSessionID(callID); id != "" {
			meta["sessionId"] = id
		}
		part.State.Metadata = meta
	}
	return evts
}

// finishClaudeSubagent completes the task part with the subagent's report.
func (t *TurnTranslator) finishClaudeSubagent(callID, report string, failed bool, now int64) []SSEEvent {
	task := t.claudeTasks[callID]
	if task == nil || task.done {
		return nil
	}
	task.done = true
	var evts []SSEEvent
	if part := t.partsByID[task.partID]; part != nil && part.State != nil {
		part.State.Status = "completed"
		part.State.Output = report
		if failed {
			part.State.Status = "error"
			part.State.Error = firstNonEmptyStr(report, "subagent failed")
			part.State.Output = ""
		}
		part.State.Time = &PartTime{Start: part.State.Time.Start, End: now}
		evts = append(evts, partUpdated(*part))
	}
	return append(evts, t.closeChild(callID, failed, now)...)
}

func (t *TurnTranslator) finishClaudeSubagents(failed bool, now int64) []SSEEvent {
	var evts []SSEEvent
	for callID, task := range t.claudeTasks {
		if !task.done {
			evts = append(evts, t.finishClaudeSubagent(callID, "", failed, now)...)
		}
	}
	return evts
}

// feedClaudeChildLine records a subagent's own message in its child session.
func (t *TurnTranslator) feedClaudeChildLine(env *claudeRawEvent, now int64) []SSEEvent {
	c := t.children[env.ParentToolUseID]
	var evts []SSEEvent
	if env.Message != nil && env.Message.Usage != nil {
		msgID := firstNonEmptyStr(env.Message.ID, env.APIMessageID)
		normUsage := env.Message.Usage.normalizeForRuntime(t.runtime)
		if t.noteClaudeMsgUsage("child:"+env.ParentToolUseID+":"+msgID, normUsage) {
			evts = append(evts, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": t.msgInfo}})
		}
		if c != nil && c.noteClaudeUsage(msgID, normUsage) {
			evts = append(evts, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": c.asst}})
		}
	}
	if c == nil || env.Message == nil {
		return evts
	}
	msgID := firstNonEmptyStr(env.Message.ID, env.APIMessageID)
	for idx, blk := range env.Message.Content {
		switch {
		case env.Type == "assistant" && blk.Type == "thinking":
			evts = append(evts, c.upsertText("reasoning", fmt.Sprintf("%s_reason_0", msgID), blk.Thinking, now, now)...)
		case env.Type == "assistant" && blk.Type == "text":
			evts = append(evts, c.upsertText("text", fmt.Sprintf("%s_text_0", msgID), blk.Text, now, now)...)
		case env.Type == "assistant" && blk.Type == "tool_use":
			toolKey := firstNonEmptyStr(blk.ID, fmt.Sprintf("%s_tool_%d", msgID, idx))
			evts = append(evts, c.startTool(toolKey, blk.ID, blk.Name, blk.Input, now)...)
		case env.Type == "user" && blk.Type == "tool_result":
			evts = append(evts, c.finishTool(blk.ToolUseID, claudeToolResultText(blk.Content), blk.IsError, now)...)
		}
	}
	if task := t.claudeTasks[env.ParentToolUseID]; task != nil && len(evts) > 0 {
		if part := t.partsByID[task.partID]; part != nil && part.State != nil && part.State.Metadata != nil {
			part.State.Metadata["lastActivityAt"] = now
			evts = append(evts, partUpdated(*part))
		}
	}
	return evts
}

func claudeToolResultText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var texts []string
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok && m["type"] == "text" {
				if s, ok := m["text"].(string); ok {
					texts = append(texts, s)
				}
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	raw, _ := json.Marshal(content)
	return string(raw)
}

// claudeSubagentReport unwraps the hand-back frame Claude Code puts around a
// subagent's report: an explanatory header, then the report indented by two
// spaces, then resume/usage trailers at column zero.
func claudeSubagentReport(text string) string {
	const marker = "The report follows:\n"
	i := strings.Index(text, marker)
	if i < 0 {
		return strings.TrimSpace(text)
	}
	var lines []string
	for _, line := range strings.Split(text[i+len(marker):], "\n") {
		body, ok := strings.CutPrefix(line, "  ")
		if !ok && line != "" {
			break
		}
		lines = append(lines, body)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
