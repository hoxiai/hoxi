package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func parseCodeBuddySessionMessages(sessionDir string, baseTs int64, defaultModel string) ([]NormalizedTurn, []ExternalPlanStep, string) {
	if sessionDir == "" {
		return nil, nil, ""
	}
	idxBytes, err := os.ReadFile(filepath.Join(sessionDir, "index.json"))
	if err != nil {
		return nil, nil, ""
	}
	var idxDoc struct {
		Messages []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"messages"`
	}
	if json.Unmarshal(idxBytes, &idxDoc) != nil || len(idxDoc.Messages) == 0 {
		return nil, nil, ""
	}

	var turns []NormalizedTurn
	var plans []ExternalPlanStep
	var detectedCWD string
	toolCallIDIndex := map[string]int{}

	for stepIdx, mRef := range idxDoc.Messages {
		msgPath := filepath.Join(sessionDir, "messages", mRef.ID+".json")
		rawBytes, err := os.ReadFile(msgPath)
		if err != nil {
			continue
		}
		var env struct {
			ID        string `json:"id"`
			Role      string `json:"role"`
			Message   string `json:"message"`
			Extra     string `json:"extra"`
			CreatedAt string `json:"createdAt"`
		}
		if json.Unmarshal(rawBytes, &env) != nil {
			continue
		}
		ts := parseTimeMs(env.CreatedAt)
		if ts == 0 {
			ts = baseTs + int64(stepIdx+1)*1000
		}

		var extraDoc map[string]interface{}
		if env.Extra != "" {
			_ = json.Unmarshal([]byte(env.Extra), &extraDoc)
		}
		modelID := firstNonEmpty(asString(extraDoc["modelId"]), defaultModel)

		var msgDoc map[string]interface{}
		if env.Message != "" {
			_ = json.Unmarshal([]byte(env.Message), &msgDoc)
		}
		role := firstNonEmpty(env.Role, asString(msgDoc["role"]), mRef.Role)

		switch role {
		case "user":
			if detectedCWD == "" {
				detectedCWD = extractWorkspaceFolderTag(env.Message)
			}
			userText := extractCodeBuddyUserText(extraDoc, msgDoc["content"])
			if userText == "" {
				continue
			}
			turns = append(turns, NormalizedTurn{
				Role:      "user",
				Timestamp: ts,
				Text:      userText,
				ModelID:   modelID,
			})

		case "assistant":
			text, reasoning, calls, stepPlans := extractCodeBuddyAssistantContent(msgDoc["content"])
			if len(stepPlans) > 0 {
				plans = stepPlans
			}
			if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
				last := &turns[len(turns)-1]
				if reasoning != "" {
					last.Reasoning = strings.TrimSpace(last.Reasoning + "\n\n" + reasoning)
				}
				if text != "" {
					last.Text = strings.TrimSpace(last.Text + "\n\n" + text)
				}
				start := len(last.Tools)
				last.Tools = append(last.Tools, calls...)
				for i := start; i < len(last.Tools); i++ {
					if last.Tools[i].CallID != "" {
						toolCallIDIndex[last.Tools[i].CallID] = len(turns) - 1
					}
				}
			} else {
				turns = append(turns, NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts,
					Text:      text,
					Reasoning: reasoning,
					Tools:     calls,
					ModelID:   modelID,
				})
				for i := range turns[len(turns)-1].Tools {
					if turns[len(turns)-1].Tools[i].CallID != "" {
						toolCallIDIndex[turns[len(turns)-1].Tools[i].CallID] = len(turns) - 1
					}
				}
			}

		case "tool":
			attachCodeBuddyToolResults(msgDoc["content"], &turns, toolCallIDIndex)
		}
	}

	return turns, plans, detectedCWD
}

func extractCodeBuddyUserText(extraDoc map[string]interface{}, rawContent interface{}) string {
	if extraDoc != nil {
		if blocks, ok := extraDoc["sourceContentBlocks"].([]interface{}); ok {
			var parts []string
			for _, b := range blocks {
				if bm, ok := b.(map[string]interface{}); ok {
					if t := strings.TrimSpace(asString(bm["text"])); t != "" {
						parts = append(parts, t)
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	raw := extractGrokContentText(rawContent)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "<user_query>") {
		return cleanUserQueryTags(raw)
	}
	for _, endTag := range []string{"</artifact_directory_path>", "</user_info>"} {
		if idx := strings.LastIndex(raw, endTag); idx >= 0 {
			raw = raw[idx+len(endTag):]
		}
	}
	return strings.TrimSpace(raw)
}

func extractCodeBuddyAssistantContent(rawContent interface{}) (text, reasoning string, calls []NormalizedToolCall, plans []ExternalPlanStep) {
	arr, ok := rawContent.([]interface{})
	if !ok {
		return strings.TrimSpace(asString(rawContent)), "", nil, nil
	}
	var texts, thoughts []string
	for _, elem := range arr {
		m, ok := elem.(map[string]interface{})
		if !ok {
			continue
		}
		switch asString(m["type"]) {
		case "text":
			if t := strings.TrimSpace(asString(m["text"])); t != "" {
				texts = append(texts, t)
			}
		case "reasoning", "thinking":
			if r := firstNonEmpty(asString(m["text"]), asString(m["thinking"])); r != "" {
				thoughts = append(thoughts, r)
			}
		case "tool-call", "tool_use":
			callID := firstNonEmpty(asString(m["toolCallId"]), asString(m["id"]))
			rawName := firstNonEmpty(asString(m["toolName"]), asString(m["name"]))
			argsMap, _ := m["args"].(map[string]interface{})
			if argsMap == nil {
				argsMap, _ = m["input"].(map[string]interface{})
			}
			if argsMap == nil {
				argsMap = map[string]interface{}{}
			}
			if rawName == "todo_write" || rawName == "TodoWrite" {
				if todos, ok := argsMap["todos"].([]interface{}); ok {
					for _, td := range todos {
						if tm, ok := td.(map[string]interface{}); ok {
							title := firstNonEmpty(asString(tm["content"]), asString(tm["title"]))
							if title != "" {
								plans = append(plans, ExternalPlanStep{
									Title:  title,
									Status: firstNonEmpty(asString(tm["status"]), "pending"),
								})
							}
						}
					}
				}
			}
			calls = append(calls, NormalizedToolCall{
				CallID: callID,
				Tool:   normalizeExternalToolName(rawName),
				Title:  rawName,
				Input:  argsMap,
				Status: "completed",
			})
		}
	}
	return strings.Join(texts, "\n\n"), strings.Join(thoughts, "\n\n"), calls, plans
}

func attachCodeBuddyToolResults(rawContent interface{}, turns *[]NormalizedTurn, callIDIndex map[string]int) {
	arr, ok := rawContent.([]interface{})
	if !ok {
		return
	}
	for _, elem := range arr {
		m, ok := elem.(map[string]interface{})
		if !ok || asString(m["type"]) != "tool-result" {
			continue
		}
		callID := asString(m["toolCallId"])
		turnIdx, exists := callIDIndex[callID]
		if !exists || turnIdx >= len(*turns) {
			continue
		}
		resWrapper, _ := m["result"].(map[string]interface{})
		inner, _ := resWrapper["result"].(map[string]interface{})
		var outParts []string
		for _, k := range []string{"stdout", "stderr", "content"} {
			if s := strings.TrimSpace(asString(inner[k])); s != "" {
				outParts = append(outParts, s)
			}
		}
		if errMsg := strings.TrimSpace(asString(resWrapper["errorMessage"])); errMsg != "" {
			outParts = append(outParts, errMsg)
		}
		if len(outParts) > 0 {
			for i := range (*turns)[turnIdx].Tools {
				if (*turns)[turnIdx].Tools[i].CallID == callID {
					(*turns)[turnIdx].Tools[i].Output = truncateRunes(strings.Join(outParts, "\n"), 4000)
					break
				}
			}
		}
	}
}
