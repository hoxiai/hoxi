package handoff

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func grokHomeDir() string {
	if env := strings.TrimSpace(os.Getenv("GROK_HOME")); env != "" {
		return filepath.Clean(env)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".grok")
}

type grokSummaryJSON struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	GeneratedTitle  string `json:"generated_title"`
	SessionSummary  string `json:"session_summary"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	CurrentModelID  string `json:"current_model_id"`
	GitRootDir      string `json:"git_root_dir"`
	HeadCommit      string `json:"head_commit"`
	HeadBranch      string `json:"head_branch"`
	LastTurnSummary string `json:"last_turn_summary"`
	LastRecap       string `json:"last_recap"`
}

func parseTimeMs(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func scanGrokSessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	gDir := grokHomeDir()
	stats := ExternalSourceStats{}
	if gDir == "" {
		return nil, stats
	}
	if _, err := os.Stat(gDir); err == nil {
		stats.Installed = true
	}
	sessionsRoot := filepath.Join(gDir, "sessions")
	cwdDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return nil, stats
	}

	qLower := strings.ToLower(strings.TrimSpace(query))
	var results []ExternalSessionSummary

	for _, cwdEntry := range cwdDirs {
		if !cwdEntry.IsDir() {
			continue
		}
		decodedCWD, _ := url.QueryUnescape(cwdEntry.Name())
		subPath := filepath.Join(sessionsRoot, cwdEntry.Name())
		sessionDirs, err := os.ReadDir(subPath)
		if err != nil {
			continue
		}
		for _, sEntry := range sessionDirs {
			if !sEntry.IsDir() {
				continue
			}
			stats.SessionCount++
			sDir := filepath.Join(subPath, sEntry.Name())
			sumBytes, err := os.ReadFile(filepath.Join(sDir, "summary.json"))
			if err != nil {
				continue
			}
			var sum grokSummaryJSON
			if json.Unmarshal(sumBytes, &sum) != nil {
				continue
			}
			sessionID := strings.TrimSpace(sum.Info.ID)
			if sessionID == "" {
				sessionID = sEntry.Name()
			}
			cwd := strings.TrimSpace(sum.Info.CWD)
			if cwd == "" {
				cwd = decodedCWD
			}
			title := strings.TrimSpace(sum.GeneratedTitle)
			if title == "" {
				title = "Grok Session " + sessionID[:minInt(8, len(sessionID))]
			}
			summary := strings.TrimSpace(sum.SessionSummary)
			if summary == "" {
				summary = strings.TrimSpace(sum.LastTurnSummary)
			}

			if qLower != "" {
				haystack := strings.ToLower(title + " " + summary + " " + cwd + " " + sum.HeadBranch)
				if !strings.Contains(haystack, qLower) {
					continue
				}
			}

			createdMs := parseTimeMs(sum.CreatedAt)
			updatedMs := parseTimeMs(sum.UpdatedAt)
			if updatedMs == 0 {
				if fi, err := os.Stat(filepath.Join(sDir, "summary.json")); err == nil {
					updatedMs = fi.ModTime().UnixMilli()
				}
			}
			if createdMs == 0 {
				createdMs = updatedMs
			}

			results = append(results, ExternalSessionSummary{
				ID:          sessionID,
				Source:      ExternalToolGrok,
				SourceLabel: "Grok CLI",
				CWD:         cwd,
				Title:       title,
				Summary:     truncateRunes(summary, 280),
				Model:       sum.CurrentModelID,
				GitBranch:   sum.HeadBranch,
				GitCommit:   shortCommit(sum.HeadCommit),
				CreatedAt:   createdMs,
				UpdatedAt:   updatedMs,
				MatchesCWD:  sameOrSubPath(cwd, targetDir),
			})
		}
	}
	return results, stats
}

func findGrokSessionDir(sessionID, hintCWD string) string {
	gDir := grokHomeDir()
	if gDir == "" {
		return ""
	}
	sessionsRoot := filepath.Join(gDir, "sessions")
	if hintCWD != "" {
		encoded := url.QueryEscape(cleanDirPath(hintCWD))
		candidate := filepath.Join(sessionsRoot, encoded, sessionID)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	cwdDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return ""
	}
	for _, cwdEntry := range cwdDirs {
		if !cwdEntry.IsDir() {
			continue
		}
		candidate := filepath.Join(sessionsRoot, cwdEntry.Name(), sessionID)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	return ""
}

func loadGrokSessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	sDir := findGrokSessionDir(sessionID, hintCWD)
	if sDir == "" {
		return nil, fmt.Errorf("grok session not found: %s", sessionID)
	}
	var sum grokSummaryJSON
	if sumBytes, err := os.ReadFile(filepath.Join(sDir, "summary.json")); err == nil {
		_ = json.Unmarshal(sumBytes, &sum)
	}

	cwd := strings.TrimSpace(sum.Info.CWD)
	if cwd == "" {
		cwd = hintCWD
	}
	title := strings.TrimSpace(sum.GeneratedTitle)
	if title == "" {
		title = "Grok Session " + sessionID[:minInt(8, len(sessionID))]
	}
	createdMs := parseTimeMs(sum.CreatedAt)
	updatedMs := parseTimeMs(sum.UpdatedAt)
	if updatedMs == 0 {
		updatedMs = time.Now().UnixMilli()
	}
	if createdMs == 0 {
		createdMs = updatedMs
	}

	turns := parseGrokChatHistory(filepath.Join(sDir, "chat_history.jsonl"), createdMs, sum.CurrentModelID)
	planSteps := parseGrokPlanJSON(filepath.Join(sDir, "plan.json"))
	changedFiles := extractChangedFilesFromTurns(turns)

	summaryObj := ExternalSessionSummary{
		ID:          sessionID,
		Source:      ExternalToolGrok,
		SourceLabel: "Grok CLI",
		CWD:         cwd,
		Title:       title,
		Summary:     strings.TrimSpace(sum.SessionSummary),
		Model:       sum.CurrentModelID,
		GitBranch:   sum.HeadBranch,
		GitCommit:   shortCommit(sum.HeadCommit),
		CreatedAt:   createdMs,
		UpdatedAt:   updatedMs,
		TurnCount:   len(turns),
		MatchesCWD:  sameOrSubPath(cwd, activeRoot),
	}

	return &ExternalSessionDetail{
		Summary:         summaryObj,
		Turns:           turns,
		PlanSteps:       planSteps,
		ChangedFiles:    changedFiles,
		LastTurnSummary: strings.TrimSpace(sum.LastTurnSummary),
		LastRecap:       strings.TrimSpace(sum.LastRecap),
	}, nil
}

func parseGrokChatHistory(historyPath string, baseTs int64, defaultModel string) []NormalizedTurn {
	f, err := os.Open(historyPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var turns []NormalizedTurn
	var pendingReasoning []string
	toolIndexByID := map[string]*NormalizedToolCall{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	step := int64(0)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		step++
		ts := baseTs + step*1000
		typ, _ := item["type"].(string)

		switch typ {
		case "user":
			synReason, _ := item["synthetic_reason"].(string)
			if synReason == "system_reminder" {
				continue
			}
			text := extractGrokContentText(item["content"])
			if strings.HasPrefix(text, "<user_info>") || strings.HasPrefix(text, "<system-reminder>") {
				continue
			}
			text = cleanUserQueryTags(text)
			if text == "" {
				continue
			}
			turns = append(turns, NormalizedTurn{
				Role:      "user",
				Timestamp: ts,
				Text:      text,
			})

		case "reasoning":
			rText := extractGrokSummaryText(item["summary"])
			if rText != "" {
				pendingReasoning = append(pendingReasoning, rText)
			}

		case "assistant":
			contentText := extractGrokContentText(item["content"])
			modelID, _ := item["model_id"].(string)
			if modelID == "" {
				modelID = defaultModel
			}
			var tCalls []NormalizedToolCall
			if rawCalls, ok := item["tool_calls"].([]interface{}); ok {
				for _, rc := range rawCalls {
					cm, ok := rc.(map[string]interface{})
					if !ok {
						continue
					}
					callID, _ := cm["id"].(string)
					rawName, _ := cm["name"].(string)
					argsStr, _ := cm["arguments"].(string)
					inputMap := map[string]interface{}{}
					if argsStr != "" {
						_ = json.Unmarshal([]byte(argsStr), &inputMap)
					}
					normTool := normalizeExternalToolName(rawName)
					tc := NormalizedToolCall{
						CallID: callID,
						Tool:   normTool,
						Title:  rawName,
						Input:  inputMap,
						Status: "completed",
					}
					tCalls = append(tCalls, tc)
				}
			}

			reasoningJoined := strings.Join(pendingReasoning, "\n\n")
			pendingReasoning = nil

			// Merge consecutive assistant tool steps under the same logical assistant turn if preceding turn is assistant
			if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
				last := &turns[len(turns)-1]
				if reasoningJoined != "" {
					if last.Reasoning != "" {
						last.Reasoning += "\n\n" + reasoningJoined
					} else {
						last.Reasoning = reasoningJoined
					}
				}
				if contentText != "" {
					if last.Text != "" {
						last.Text += "\n\n" + contentText
					} else {
						last.Text = contentText
					}
				}
				startIdx := len(last.Tools)
				last.Tools = append(last.Tools, tCalls...)
				for i := startIdx; i < len(last.Tools); i++ {
					if last.Tools[i].CallID != "" {
						toolIndexByID[last.Tools[i].CallID] = &last.Tools[i]
					}
				}
			} else {
				turn := NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts,
					Text:      contentText,
					Reasoning: reasoningJoined,
					Tools:     tCalls,
					ModelID:   modelID,
				}
				turns = append(turns, turn)
				last := &turns[len(turns)-1]
				for i := range last.Tools {
					if last.Tools[i].CallID != "" {
						toolIndexByID[last.Tools[i].CallID] = &last.Tools[i]
					}
				}
			}

		case "tool_result":
			callID, _ := item["tool_call_id"].(string)
			outText := extractGrokContentText(item["content"])
			if tc, ok := toolIndexByID[callID]; ok {
				tc.Output = truncateRunes(outText, 4000)
			}
		}
	}
	return turns
}

func extractGrokContentText(v interface{}) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case []interface{}:
		var parts []string
		for _, elem := range val {
			if m, ok := elem.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
					parts = append(parts, strings.TrimSpace(t))
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func extractGrokSummaryText(v interface{}) string {
	arr, ok := v.([]interface{})
	if !ok {
		return ""
	}
	var parts []string
	for _, elem := range arr {
		if m, ok := elem.(map[string]interface{}); ok {
			if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
				parts = append(parts, strings.TrimSpace(t))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func cleanUserQueryTags(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "<user_query>"); idx >= 0 {
		s = s[idx+len("<user_query>"):]
		if endIdx := strings.Index(s, "</user_query>"); endIdx >= 0 {
			s = s[:endIdx]
		}
	}
	return strings.TrimSpace(s)
}

func parseGrokPlanJSON(planPath string) []ExternalPlanStep {
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return nil
	}
	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var steps []ExternalPlanStep
	if todos, ok := doc["todos"].(map[string]interface{}); ok {
		for _, v := range todos {
			if tm, ok := v.(map[string]interface{}); ok {
				title, _ := tm["content"].(string)
				if title == "" {
					title, _ = tm["title"].(string)
				}
				status, _ := tm["status"].(string)
				if title != "" {
					steps = append(steps, ExternalPlanStep{Title: title, Status: status})
				}
			}
		}
	}
	return steps
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func shortCommit(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
