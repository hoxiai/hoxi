package handoff

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func codeBuddyDotDirs() []string {
	if env := strings.TrimSpace(os.Getenv("CODEBUDDY_HOME")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var out []string
	for _, sub := range []string{".codebuddy", ".codebuddycn"} {
		p := filepath.Join(home, sub)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func codeBuddyAppSupportDirs() []string {
	if env := strings.TrimSpace(os.Getenv("CODEBUDDY_DATA_DIR")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Library", "Application Support", "CodeBuddy CN"),
		filepath.Join(home, "Library", "Application Support", "CodeBuddy"),
		filepath.Join(home, "Library", "Application Support", "CodeBuddyExtension"),
		filepath.Join(home, ".config", "CodeBuddy CN"),
		filepath.Join(home, ".config", "CodeBuddy"),
		filepath.Join(home, ".config", "CodeBuddyExtension"),
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates,
			filepath.Join(appData, "CodeBuddy CN"),
			filepath.Join(appData, "CodeBuddy"),
			filepath.Join(appData, "CodeBuddyExtension"),
		)
	}
	var out []string
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func decodeCodeBuddyBase64Dir(encoded string) string {
	s := strings.TrimSpace(encoded)
	if s == "" {
		return ""
	}
	s = strings.TrimRight(s, "_")
	if decoded, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(decoded) > 0 {
		return cleanDirPath(string(decoded))
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(decoded) > 0 {
		return cleanDirPath(string(decoded))
	}
	return ""
}

func md5HexString(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

type codeBuddySessionCandidate struct {
	summary    ExternalSessionSummary
	sessionDir string
	copilotDir string
}

func collectCodeBuddyCandidates(targetDir string) (map[string]*codeBuddySessionCandidate, bool) {
	dotDirs := codeBuddyDotDirs()
	appDirs := codeBuddyAppSupportDirs()
	installed := len(dotDirs) > 0 || len(appDirs) > 0
	byID := map[string]*codeBuddySessionCandidate{}
	if !installed {
		return byID, false
	}

	cwdBySessionID := map[string]string{}
	cwdByProjHash := map[string]string{}
	var primaryCopilotDir string

	// 1. Scan codebuddy-sessions.vscdb & genie-history in CodeBuddy / CodeBuddy CN app dirs
	for _, appDir := range appDirs {
		copDir := filepath.Join(appDir, "User", "globalStorage", "tencent-cloud.coding-copilot")
		if st, err := os.Stat(copDir); err == nil && st.IsDir() && primaryCopilotDir == "" {
			primaryCopilotDir = copDir
		}

		// genie-history/<base64-cwd> maps both projHash and sessionID to cwd
		genieRoot := filepath.Join(copDir, "genie-history")
		if gDirs, err := os.ReadDir(genieRoot); err == nil {
			for _, gd := range gDirs {
				if !gd.IsDir() {
					continue
				}
				cwd := decodeCodeBuddyBase64Dir(gd.Name())
				if cwd == "" {
					continue
				}
				cwdByProjHash[md5HexString(cwd)] = cwd
				curBytes, err := os.ReadFile(filepath.Join(genieRoot, gd.Name(), "current.json"))
				if err == nil {
					var curDoc struct {
						ConversationID string `json:"conversationId"`
					}
					if json.Unmarshal(curBytes, &curDoc) == nil && curDoc.ConversationID != "" {
						cwdBySessionID[curDoc.ConversationID] = cwd
					}
				}
				if cEntries, err := os.ReadDir(filepath.Join(genieRoot, gd.Name(), "conversations")); err == nil {
					for _, ce := range cEntries {
						if ce.IsDir() {
							cwdBySessionID[ce.Name()] = cwd
						}
					}
				}
			}
		}

		// codebuddy-sessions.vscdb
		vscdb := filepath.Join(appDir, "codebuddy-sessions.vscdb")
		if _, err := os.Stat(vscdb); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", vscdb, "SELECT value FROM ItemTable WHERE key LIKE 'session:%' LIMIT 150;")
			cmd.WaitDelay = time.Second
			rawOut, err := cmd.Output()
			cancel()
			if err == nil && len(bytes.TrimSpace(rawOut)) > 0 {
				var rows []struct {
					Value string `json:"value"`
				}
				if json.Unmarshal(rawOut, &rows) == nil {
					for _, r := range rows {
						var item struct {
							ConversationID string `json:"conversationId"`
							CWD            string `json:"cwd"`
							Title          string `json:"title"`
							CreatedAt      int64  `json:"createdAt"`
							UpdatedAt      int64  `json:"updatedAt"`
							IsPlayground   bool   `json:"isPlayground"`
						}
						if json.Unmarshal([]byte(r.Value), &item) != nil || item.ConversationID == "" {
							continue
						}
						cwd := cleanDirPath(item.CWD)
						if cwd != "" {
							cwdBySessionID[item.ConversationID] = cwd
							cwdByProjHash[md5HexString(cwd)] = cwd
						}
						title := strings.TrimSpace(item.Title)
						if title == "" {
							title = "CodeBuddy Session " + item.ConversationID[:minInt(8, len(item.ConversationID))]
						}
						byID[item.ConversationID] = &codeBuddySessionCandidate{
							copilotDir: copDir,
							summary: ExternalSessionSummary{
								ID:          item.ConversationID,
								Source:      ExternalToolCodeBuddy,
								SourceLabel: "CodeBuddy",
								CWD:         cwd,
								Title:       title,
								CreatedAt:   item.CreatedAt,
								UpdatedAt:   item.UpdatedAt,
								MatchesCWD:  sameOrSubPath(cwd, targetDir),
							},
						}
					}
				}
			}
		}
	}

	// 2. Scan CodeBuddyExtension/Data/<uid>/CodeBuddyIDE/<uid>/history/<proj-hash>/
	for _, appDir := range appDirs {
		dataRoot := filepath.Join(appDir, "Data")
		uDirs, err := os.ReadDir(dataRoot)
		if err != nil {
			continue
		}
		for _, uDir := range uDirs {
			if !uDir.IsDir() {
				continue
			}
			ideRoot := filepath.Join(dataRoot, uDir.Name(), "CodeBuddyIDE")
			ideUsers, err := os.ReadDir(ideRoot)
			if err != nil {
				continue
			}
			for _, iu := range ideUsers {
				if !iu.IsDir() {
					continue
				}
				histRoot := filepath.Join(ideRoot, iu.Name(), "history")
				projDirs, err := os.ReadDir(histRoot)
				if err != nil {
					continue
				}
				for _, pd := range projDirs {
					if !pd.IsDir() {
						continue
					}
					projHash := pd.Name()
					projPath := filepath.Join(histRoot, projHash)
					idxBytes, err := os.ReadFile(filepath.Join(projPath, "index.json"))
					if err != nil {
						continue
					}
					var idxDoc struct {
						Conversations []struct {
							ID              string            `json:"id"`
							Name            string            `json:"name"`
							CreatedAt       string            `json:"createdAt"`
							LastMessageAt   string            `json:"lastMessageAt"`
							SelectedModelID string            `json:"selectedModelId"`
							ModelMap        map[string]string `json:"modelMap"`
						} `json:"conversations"`
					}
					if json.Unmarshal(idxBytes, &idxDoc) != nil {
						continue
					}
					for _, conv := range idxDoc.Conversations {
						sID := strings.TrimSpace(conv.ID)
						if sID == "" {
							continue
						}
						sessDir := filepath.Join(projPath, sID)
						hasMessages := false
						if st, err := os.Stat(filepath.Join(sessDir, "messages")); err == nil && st.IsDir() {
							hasMessages = true
						}
						name := strings.TrimSpace(conv.Name)
						if name == "" && !hasMessages {
							// Skip empty uninitialized stub conversations
							continue
						}
						model := strings.TrimSpace(conv.SelectedModelID)
						if model == "" && conv.ModelMap != nil {
							for _, mv := range conv.ModelMap {
								if strings.TrimSpace(mv) != "" {
									model = strings.TrimSpace(mv)
									break
								}
							}
						}
						cwd := firstNonEmpty(cwdBySessionID[sID], cwdByProjHash[projHash])
						if cwd == "" && hasMessages {
							cwd = sniffCodeBuddyWorkspaceFolder(sessDir)
							if cwd != "" {
								cwdByProjHash[projHash] = cwd
							}
						}
						createdMs := parseTimeMs(conv.CreatedAt)
						updatedMs := parseTimeMs(conv.LastMessageAt)
						if updatedMs == 0 {
							updatedMs = createdMs
						}

						if existing, ok := byID[sID]; ok {
							if hasMessages {
								existing.sessionDir = sessDir
							}
							if existing.copilotDir == "" {
								existing.copilotDir = primaryCopilotDir
							}
							if name != "" && strings.HasPrefix(existing.summary.Title, "CodeBuddy Session ") {
								existing.summary.Title = name
							}
							if existing.summary.CWD == "" && cwd != "" {
								existing.summary.CWD = cwd
								existing.summary.MatchesCWD = sameOrSubPath(cwd, targetDir)
							}
							if existing.summary.Model == "" && model != "" {
								existing.summary.Model = model
							}
							if updatedMs > existing.summary.UpdatedAt {
								existing.summary.UpdatedAt = updatedMs
							}
						} else {
							if name == "" {
								name = "CodeBuddy Session " + sID[:minInt(8, len(sID))]
							}
							var sDir string
							if hasMessages {
								sDir = sessDir
							}
							byID[sID] = &codeBuddySessionCandidate{
								sessionDir: sDir,
								copilotDir: primaryCopilotDir,
								summary: ExternalSessionSummary{
									ID:          sID,
									Source:      ExternalToolCodeBuddy,
									SourceLabel: "CodeBuddy",
									CWD:         cwd,
									Title:       name,
									Model:       model,
									CreatedAt:   createdMs,
									UpdatedAt:   updatedMs,
									MatchesCWD:  sameOrSubPath(cwd, targetDir),
								},
							}
						}
					}
				}
			}
		}
	}

	return byID, installed
}

func sniffCodeBuddyWorkspaceFolder(sessionDir string) string {
	idxBytes, err := os.ReadFile(filepath.Join(sessionDir, "index.json"))
	if err != nil {
		return ""
	}
	var idx struct {
		Messages []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"messages"`
	}
	if json.Unmarshal(idxBytes, &idx) != nil {
		return ""
	}
	for i, m := range idx.Messages {
		if i > 3 {
			break
		}
		if m.Role != "user" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sessionDir, "messages", m.ID+".json"))
		if err != nil {
			continue
		}
		if cwd := extractWorkspaceFolderTag(string(raw)); cwd != "" {
			return cwd
		}
	}
	return ""
}

func extractWorkspaceFolderTag(s string) string {
	const marker = "Workspace Folder:"
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(marker):]
	for _, sep := range []string{"\\n", "\n", "<", "\""} {
		if end := strings.Index(rest, sep); end >= 0 {
			rest = rest[:end]
		}
	}
	return cleanDirPath(strings.TrimSpace(rest))
}

func scanCodeBuddySessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	byID, installed := collectCodeBuddyCandidates(targetDir)
	stats := ExternalSourceStats{
		Installed:    installed,
		SessionCount: len(byID),
	}
	if len(byID) == 0 {
		return nil, stats
	}

	qLower := strings.ToLower(strings.TrimSpace(query))
	results := make([]ExternalSessionSummary, 0, len(byID))
	for _, cand := range byID {
		sum := cand.summary
		if qLower != "" {
			haystack := strings.ToLower(sum.Title + " " + sum.Summary + " " + sum.CWD + " " + sum.Model)
			if !strings.Contains(haystack, qLower) {
				continue
			}
		}
		results = append(results, sum)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].MatchesCWD != results[j].MatchesCWD {
			return results[i].MatchesCWD
		}
		return results[i].UpdatedAt > results[j].UpdatedAt
	})
	return results, stats
}

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
	toolByID := map[string]*NormalizedToolCall{}

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
						toolByID[last.Tools[i].CallID] = &last.Tools[i]
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
				last := &turns[len(turns)-1]
				for i := range last.Tools {
					if last.Tools[i].CallID != "" {
						toolByID[last.Tools[i].CallID] = &last.Tools[i]
					}
				}
			}

		case "tool":
			attachCodeBuddyToolResults(msgDoc["content"], toolByID)
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

func attachCodeBuddyToolResults(rawContent interface{}, toolByID map[string]*NormalizedToolCall) {
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
		tc, exists := toolByID[callID]
		if !exists {
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
			tc.Output = truncateRunes(strings.Join(outParts, "\n"), 4000)
		}
	}
}

func loadCodeBuddyTodosAndBrain(copilotDir, sessionID string) ([]ExternalPlanStep, string) {
	if copilotDir == "" || sessionID == "" {
		return nil, ""
	}
	var plans []ExternalPlanStep
	todoBytes, err := os.ReadFile(filepath.Join(copilotDir, "todos", sessionID+".json"))
	if err == nil {
		var doc struct {
			Todos []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"todos"`
		}
		if json.Unmarshal(todoBytes, &doc) == nil {
			for _, td := range doc.Todos {
				if strings.TrimSpace(td.Content) != "" {
					plans = append(plans, ExternalPlanStep{
						Title:  strings.TrimSpace(td.Content),
						Status: firstNonEmpty(td.Status, "pending"),
					})
				}
			}
		}
	}

	var brainDocs []string
	brainDir := filepath.Join(copilotDir, "brain", sessionID)
	if files, err := os.ReadDir(brainDir); err == nil {
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
				if raw, err := os.ReadFile(filepath.Join(brainDir, f.Name())); err == nil {
					brainDocs = append(brainDocs, truncateRunes(strings.TrimSpace(string(raw)), 800))
				}
			}
		}
	}
	return plans, strings.Join(brainDocs, "\n\n")
}

func loadCodeBuddySessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	targetDir := firstNonEmpty(hintCWD, activeRoot)
	byID, _ := collectCodeBuddyCandidates(targetDir)
	cand, ok := byID[sessionID]
	if !ok || cand == nil {
		return nil, fmt.Errorf("codebuddy session not found: %s", sessionID)
	}

	turns, msgPlans, msgCWD := parseCodeBuddySessionMessages(cand.sessionDir, cand.summary.CreatedAt, cand.summary.Model)
	todoPlans, brainRecap := loadCodeBuddyTodosAndBrain(cand.copilotDir, sessionID)
	if len(todoPlans) > 0 {
		msgPlans = todoPlans
	}
	if len(turns) == 0 {
		turns = []NormalizedTurn{
			{
				Role:      "user",
				Timestamp: cand.summary.CreatedAt,
				Text:      cand.summary.Title,
			},
			{
				Role:      "assistant",
				Timestamp: cand.summary.UpdatedAt,
				Text:      firstNonEmpty(brainRecap, cand.summary.Summary, "Imported CodeBuddy session: "+cand.summary.Title),
				ModelID:   cand.summary.Model,
			},
		}
	}

	sum := cand.summary
	if sum.CWD == "" {
		sum.CWD = firstNonEmpty(msgCWD, hintCWD)
	}
	if sum.Model == "" {
		for _, t := range turns {
			if t.ModelID != "" {
				sum.Model = t.ModelID
				break
			}
		}
	}
	var lastTurnSummary string
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" && turns[i].Text != "" {
			lastTurnSummary = truncateRunes(turns[i].Text, 360)
			break
		}
	}
	if sum.Summary == "" {
		sum.Summary = truncateRunes(firstNonEmpty(lastTurnSummary, brainRecap), 240)
	}
	sum.TurnCount = len(turns)
	sum.MatchesCWD = sameOrSubPath(sum.CWD, activeRoot)

	return &ExternalSessionDetail{
		Summary:         sum,
		Turns:           turns,
		PlanSteps:       msgPlans,
		ChangedFiles:    extractChangedFilesFromTurns(turns),
		LastTurnSummary: lastTurnSummary,
		LastRecap:       brainRecap,
	}, nil
}

func detectCodeBuddyCLIStatus() LocalCLIStatusItem {
	sessions, stats := scanCodeBuddySessions("", "")
	var models []string
	var defModel string
	for _, s := range sessions {
		if s.Model != "" {
			models = appendUniqueString(models, s.Model)
			if defModel == "" {
				defModel = s.Model
			}
		}
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolCodeBuddy,
		Name:          "CodeBuddy",
		Installed:     stats.Installed,
		Authenticated: false,
		Connected:     false,
		DefaultModel:  defModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "session-handoff",
		ProviderID:    "codebuddy",
	}
}
