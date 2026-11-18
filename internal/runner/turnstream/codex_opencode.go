package turnstream

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (t *TurnTranslator) feedCodexLine(line string) []SSEEvent {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(line), &raw) != nil {
		return nil
	}
	nowMs := time.Now().UnixMilli()
	evtType, _ := raw["type"].(string)

	switch evtType {
	case "thread.started":
		if tid, _ := raw["thread_id"].(string); strings.TrimSpace(tid) != "" {
			t.externalID = strings.TrimSpace(tid)
		}
		return nil

	case "item.started", "item.updated", "item.completed":
		item, ok := raw["item"].(map[string]interface{})
		if !ok {
			return nil
		}
		itemID, _ := item["id"].(string)
		if itemID == "" {
			t.geminiStepIdx++
			itemID = fmt.Sprintf("cdx_%d", t.geminiStepIdx)
		}
		itemType, _ := item["type"].(string)
		completed := evtType == "item.completed"

		switch itemType {
		case "reasoning":
			txt, _ := item["text"].(string)
			partID := "part_cdx_r_" + itemID
			part := t.partsByID[partID]
			if part == nil {
				part = &MessagePart{
					ID:        partID,
					SessionID: t.sessionID,
					MessageID: t.messageID,
					Type:      "reasoning",
					Time:      &PartTime{Start: nowMs},
				}
				t.partsByID[partID] = part
				t.partOrder = append(t.partOrder, partID)
			}
			t.setPartText(part, txt)
			if completed && part.Time != nil {
				part.Time.End = nowMs
			}
			return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}}}

		case "agent_message":
			txt, _ := item["text"].(string)
			partID := "part_cdx_t_" + itemID
			part := t.partsByID[partID]
			if part == nil {
				part = &MessagePart{
					ID:        partID,
					SessionID: t.sessionID,
					MessageID: t.messageID,
					Type:      "text",
					Time:      &PartTime{Start: nowMs},
				}
				t.partsByID[partID] = part
				t.partOrder = append(t.partOrder, partID)
				t.hasTextPart = true
			}
			t.setPartText(part, txt)
			if completed && part.Time != nil {
				part.Time.End = nowMs
			}
			return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}}}

		case "command_execution":
			cmdStr, _ := item["command"].(string)
			aggOut, _ := item["aggregated_output"].(string)
			partID := "part_cdx_cmd_" + itemID
			part := t.partsByID[partID]
			startMs := nowMs
			if part != nil && part.State != nil && part.State.Time != nil && part.State.Time.Start > 0 {
				startMs = part.State.Time.Start
			}
			status := "running"
			errStr := ""
			pt := &PartTime{Start: startMs}
			if completed {
				pt.End = nowMs
				status = "completed"
				if codeFloat, ok := item["exit_code"].(float64); ok && int(codeFloat) != 0 {
					status = "error"
					errStr = fmt.Sprintf("exit code %d", int(codeFloat))
				}
			}
			next := &MessagePart{
				ID:        partID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "tool",
				Tool:      "bash",
				CallID:    itemID,
				State: &ToolState{
					Status: status,
					Title:  cmdStr,
					Input:  map[string]interface{}{"command": cmdStr},
					Output: aggOut,
					Error:  errStr,
					Time:   pt,
				},
			}
			if part == nil {
				t.partOrder = append(t.partOrder, partID)
			}
			t.partsByID[partID] = next
			return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*next)}}}

		case "file_change":
			partID := "part_cdx_fc_" + itemID
			changes, _ := item["changes"].([]interface{})
			var paths []string
			for _, rawCh := range changes {
				if ch, ok := rawCh.(map[string]interface{}); ok {
					if p, _ := ch["path"].(string); p != "" {
						paths = append(paths, p)
					}
				}
			}
			title := strings.Join(paths, ", ")
			if title == "" {
				title = "file_change"
			}
			status := "running"
			pt := &PartTime{Start: nowMs}
			if completed {
				status = "completed"
				pt.End = nowMs
			}
			next := &MessagePart{
				ID:        partID,
				SessionID: t.sessionID,
				MessageID: t.messageID,
				Type:      "tool",
				Tool:      "edit",
				CallID:    itemID,
				State: &ToolState{
					Status: status,
					Title:  title,
					Input:  map[string]interface{}{"files": paths},
					Output: "Updated " + title,
					Time:   pt,
				},
			}
			if t.partsByID[partID] == nil {
				t.partOrder = append(t.partOrder, partID)
			}
			t.partsByID[partID] = next
			return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*next)}}}
		}

	case "turn.completed":
		if u, ok := raw["usage"].(map[string]interface{}); ok {
			inTok := intFromMap(u, "input_tokens")
			cacheTok := intFromMap(u, "cached_input_tokens")
			outTok := intFromMap(u, "output_tokens")
			tok := &MessageTokens{
				Total:  inTok + outTok,
				Input:  inTok,
				Output: outTok,
			}
			tok.Cache.Read = cacheTok
			t.MsgInfo.Tokens = tok
		}
		t.MsgInfo.Time.Completed = nowMs
		t.MsgInfo.Finish = "stop"
		t.turnFinished = true
		return []SSEEvent{
			{Type: "message.updated", Properties: map[string]interface{}{"info": cloneMessageInfo(t.MsgInfo)}},
			{Type: "session.idle", Properties: map[string]interface{}{"sessionID": t.sessionID}},
			{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		}

	case "turn.failed", "error":
		errMsg := ""
		if errObj, ok := raw["error"].(map[string]interface{}); ok {
			errMsg, _ = errObj["message"].(string)
		}
		if errMsg == "" {
			errMsg, _ = raw["message"].(string)
		}
		if errMsg == "" {
			errMsg = "Codex CLI turn failed"
		}
		t.geminiLastErrMsg = errMsg
		msgErr := newClassifiedMessageError("CodexTurnError", errMsg, 0)
		t.MsgInfo.Time.Completed = nowMs
		t.MsgInfo.Error = msgErr
		t.MsgInfo.Finish = "error"
		t.turnFinished = true
		return []SSEEvent{
			{Type: "message.updated", Properties: map[string]interface{}{"info": cloneMessageInfo(t.MsgInfo)}},
			{Type: "session.error", Properties: map[string]interface{}{"sessionID": t.sessionID, "error": msgErr}},
			{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		}
	}
	return nil
}

func (t *TurnTranslator) feedOpenCodeCLILine(line string) []SSEEvent {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(line), &raw) != nil {
		return nil
	}
	nowMs := time.Now().UnixMilli()
	if sid, _ := raw["sessionID"].(string); strings.TrimSpace(sid) != "" {
		t.externalID = strings.TrimSpace(sid)
	}
	evtType, _ := raw["type"].(string)
	partRaw, _ := json.Marshal(raw["part"])

	switch evtType {
	case "text", "reasoning", "tool_use":
		var p MessagePart
		if len(partRaw) == 0 || json.Unmarshal(partRaw, &p) != nil {
			return nil
		}
		if p.ID == "" {
			t.geminiStepIdx++
			p.ID = fmt.Sprintf("part_oc_%d", t.geminiStepIdx)
		}
		p.SessionID = t.sessionID
		p.MessageID = t.messageID
		if evtType == "tool_use" && p.Type == "" {
			p.Type = "tool"
		} else if p.Type == "" {
			p.Type = evtType
		}
		if p.Type == "text" {
			t.hasTextPart = true
		}
		if t.partsByID[p.ID] == nil {
			t.partOrder = append(t.partOrder, p.ID)
		}
		cp := clonePart(p)
		t.partsByID[p.ID] = &cp
		return []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": p}}}

	case "step_finish":
		if partMap, ok := raw["part"].(map[string]interface{}); ok {
			if cost, ok := partMap["cost"].(float64); ok && cost > 0 {
				t.MsgInfo.Cost += cost
			}
			if tokMap, ok := partMap["tokens"].(map[string]interface{}); ok {
				if t.MsgInfo.Tokens == nil {
					t.MsgInfo.Tokens = &MessageTokens{}
				}
				inTok := intFromMap(tokMap, "input")
				outTok := intFromMap(tokMap, "output")
				reasTok := intFromMap(tokMap, "reasoning")
				t.MsgInfo.Tokens.Input += inTok
				t.MsgInfo.Tokens.Output += outTok
				t.MsgInfo.Tokens.Reasoning += reasTok
				t.MsgInfo.Tokens.Total += inTok + outTok + reasTok
				if cacheMap, ok := tokMap["cache"].(map[string]interface{}); ok {
					t.MsgInfo.Tokens.Cache.Read += intFromMap(cacheMap, "read")
					t.MsgInfo.Tokens.Cache.Write += intFromMap(cacheMap, "write")
				}
			}
			reason, _ := partMap["reason"].(string)
			if reason == "tool-calls" || reason == "tool_calls" {
				return nil
			}
		}
		t.MsgInfo.Time.Completed = nowMs
		t.MsgInfo.Finish = "stop"
		t.turnFinished = true
		return []SSEEvent{
			{Type: "message.updated", Properties: map[string]interface{}{"info": cloneMessageInfo(t.MsgInfo)}},
			{Type: "session.idle", Properties: map[string]interface{}{"sessionID": t.sessionID}},
			{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		}

	case "error":
		errMsg := "OpenCode CLI error"
		if errObj, ok := raw["error"].(map[string]interface{}); ok {
			if dataObj, ok := errObj["data"].(map[string]interface{}); ok {
				if m, _ := dataObj["message"].(string); m != "" {
					errMsg = m
				}
			} else if m, _ := errObj["message"].(string); m != "" {
				errMsg = m
			}
		}
		t.geminiLastErrMsg = errMsg
		msgErr := newClassifiedMessageError("OpenCodeCLIError", errMsg, 0)
		t.MsgInfo.Time.Completed = nowMs
		t.MsgInfo.Error = msgErr
		t.MsgInfo.Finish = "error"
		t.turnFinished = true
		return []SSEEvent{
			{Type: "message.updated", Properties: map[string]interface{}{"info": cloneMessageInfo(t.MsgInfo)}},
			{Type: "session.error", Properties: map[string]interface{}{"sessionID": t.sessionID, "error": msgErr}},
			{Type: "session.status", Properties: map[string]interface{}{"sessionID": t.sessionID, "status": SessionStatus{Type: "idle"}}},
		}
	}
	return nil
}

func intFromMap(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
