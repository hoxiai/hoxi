package handoff

import (
	"bufio"
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
			u := extractCodeBuddyUsage(extraDoc, msgDoc)
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
				last.Usage = last.Usage.add(u)
			} else {
				turns = append(turns, NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts,
					Text:      text,
					Reasoning: reasoning,
					Tools:     calls,
					ModelID:   modelID,
					Usage:     u,
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

func extractCodeBuddyUsage(providerDoc, msgDoc map[string]interface{}) TurnUsage {
	if providerDoc != nil {
		if u := parseTurnUsage(providerDoc["rawUsage"]); !u.empty() {
			return u
		}
		if u := parseTurnUsage(providerDoc["usage"]); !u.empty() {
			return u
		}
	}
	if msgDoc != nil {
		if u := parseTurnUsage(msgDoc["usage"]); !u.empty() {
			return u
		}
	}
	return TurnUsage{}
}

func sniffCodeBuddyCLIJSONL(path, fallbackCWD string, fallbackMs int64) (cwd, title, model string, createdMs, updatedMs int64, turnCount int) {
	f, err := os.Open(path)
	if err != nil {
		return fallbackCWD, "", "", fallbackMs, fallbackMs, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var row map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		if cwd == "" {
			cwd = cleanDirPath(asString(row["cwd"]))
		}
		ts := asInt64(row["timestamp"])
		if ts == 0 {
			ts = parseTimeMs(asString(row["timestamp"]))
		}
		if ts > 0 {
			if createdMs == 0 || ts < createdMs {
				createdMs = ts
			}
			if ts > updatedMs {
				updatedMs = ts
			}
		}
		if pd, ok := row["providerData"].(map[string]interface{}); ok && model == "" {
			model = firstNonEmpty(asString(pd["model"]), asString(pd["requestModelId"]))
		}
		switch asString(row["type"]) {
		case "message":
			role := asString(row["role"])
			if role == "user" || role == "assistant" {
				turnCount++
			}
			if role == "user" && title == "" {
				if txt := extractCodeBuddyCLIContentText(row["content"]); txt != "" {
					title = truncateRunes(strings.TrimSpace(strings.SplitN(txt, "\n", 2)[0]), 80)
				}
			}
		case "function_call":
			if turnCount == 0 {
				turnCount = 1
			}
		}
	}
	if cwd == "" {
		cwd = fallbackCWD
	}
	if createdMs == 0 {
		createdMs = fallbackMs
	}
	if updatedMs == 0 {
		updatedMs = createdMs
	}
	return cwd, title, model, createdMs, updatedMs, turnCount
}

func extractCodeBuddyCLIContentText(raw interface{}) string {
	arr, ok := raw.([]interface{})
	if !ok {
		return strings.TrimSpace(asString(raw))
	}
	var parts []string
	for _, elem := range arr {
		m, ok := elem.(map[string]interface{})
		if !ok {
			continue
		}
		if t := strings.TrimSpace(asString(m["text"])); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

func parseCodeBuddyCLIJSONL(path string, baseTs int64, defaultModel string) ([]NormalizedTurn, []ExternalPlanStep, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, ""
	}
	defer f.Close()

	var turns []NormalizedTurn
	var detectedCWD string
	toolCallIDIndex := map[string]int{}
	seenMsgUsage := map[string]TurnUsage{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	stepIdx := 0
	for sc.Scan() {
		var row map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		stepIdx++
		if detectedCWD == "" {
			detectedCWD = cleanDirPath(asString(row["cwd"]))
		}
		ts := asInt64(row["timestamp"])
		if ts == 0 {
			ts = parseTimeMs(asString(row["timestamp"]))
		}
		if ts == 0 {
			ts = baseTs + int64(stepIdx)*1000
		}
		pd, _ := row["providerData"].(map[string]interface{})
		msgDoc, _ := row["message"].(map[string]interface{})
		modelID := firstNonEmpty(asString(pd["model"]), asString(pd["requestModelId"]), defaultModel)
		msgID := firstNonEmpty(asString(pd["messageId"]), asString(row["id"]))

		ensureAssistantTurn := func() *NormalizedTurn {
			if len(turns) == 0 || turns[len(turns)-1].Role != "assistant" {
				turns = append(turns, NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts,
					ModelID:   modelID,
				})
			}
			last := &turns[len(turns)-1]
			if last.ModelID == "" && modelID != "" {
				last.ModelID = modelID
			}
			return last
		}

		applyStepUsage := func(last *NormalizedTurn) {
			u := extractCodeBuddyUsage(pd, msgDoc)
			if u.empty() {
				return
			}
			if msgID != "" {
				prev := seenMsgUsage[msgID]
				delta := TurnUsage{
					Input:      max(0, u.Input-prev.Input),
					Output:     max(0, u.Output-prev.Output),
					Reasoning:  max(0, u.Reasoning-prev.Reasoning),
					CacheRead:  max(0, u.CacheRead-prev.CacheRead),
					CacheWrite: max(0, u.CacheWrite-prev.CacheWrite),
					Cost:       max(0, u.Cost-prev.Cost),
				}
				seenMsgUsage[msgID] = TurnUsage{
					Input:      max(prev.Input, u.Input),
					Output:     max(prev.Output, u.Output),
					Reasoning:  max(prev.Reasoning, u.Reasoning),
					CacheRead:  max(prev.CacheRead, u.CacheRead),
					CacheWrite: max(prev.CacheWrite, u.CacheWrite),
					Cost:       max(prev.Cost, u.Cost),
				}
				u = delta
			}
			last.Usage = last.Usage.add(u)
		}

		switch asString(row["type"]) {
		case "message":
			role := asString(row["role"])
			if role == "user" {
				txt := extractCodeBuddyCLIContentText(row["content"])
				if txt == "" {
					continue
				}
				turns = append(turns, NormalizedTurn{
					Role:      "user",
					Timestamp: ts,
					Text:      txt,
					ModelID:   modelID,
				})
			} else if role == "assistant" {
				txt := extractCodeBuddyCLIContentText(row["content"])
				last := ensureAssistantTurn()
				if txt != "" {
					last.Text = strings.TrimSpace(last.Text + "\n\n" + txt)
				}
				applyStepUsage(last)
			}
		case "reasoning":
			rTxt := extractCodeBuddyCLIContentText(row["rawContent"])
			if rTxt == "" {
				rTxt = extractCodeBuddyCLIContentText(row["content"])
			}
			if rTxt != "" {
				last := ensureAssistantTurn()
				last.Reasoning = strings.TrimSpace(last.Reasoning + "\n\n" + rTxt)
			}
		case "function_call":
			last := ensureAssistantTurn()
			if rTxt := strings.TrimSpace(asString(pd["reasoning"])); rTxt != "" && !strings.Contains(last.Reasoning, rTxt) {
				last.Reasoning = strings.TrimSpace(last.Reasoning + "\n\n" + rTxt)
			}
			callID := firstNonEmpty(asString(row["callId"]), asString(row["id"]))
			rawName := asString(row["name"])
			var argsMap map[string]interface{}
			if argStr := asString(row["arguments"]); argStr != "" {
				_ = json.Unmarshal([]byte(argStr), &argsMap)
			} else if m, ok := row["arguments"].(map[string]interface{}); ok {
				argsMap = m
			}
			if argsMap == nil {
				argsMap = map[string]interface{}{}
			}
			last.Tools = append(last.Tools, NormalizedToolCall{
				CallID: callID,
				Tool:   normalizeExternalToolName(rawName),
				Title:  rawName,
				Input:  argsMap,
				Status: "completed",
			})
			if callID != "" {
				toolCallIDIndex[callID] = len(turns) - 1
			}
			applyStepUsage(last)
		case "function_call_result":
			callID := asString(row["callId"])
			if idx, ok := toolCallIDIndex[callID]; ok && idx < len(turns) {
				outTxt := strings.TrimSpace(asString(row["output"]))
				if outTxt != "" {
					for i := range turns[idx].Tools {
						if turns[idx].Tools[i].CallID == callID {
							turns[idx].Tools[i].Output = truncateRunes(outTxt, 4000)
							break
						}
					}
				}
			}
		}
	}
	return turns, nil, detectedCWD
}
