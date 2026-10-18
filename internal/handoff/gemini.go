package handoff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func geminiRootDir() string {
	if env := strings.TrimSpace(os.Getenv("GEMINI_HOME")); env != "" {
		return filepath.Clean(env)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini")
}

func antigravityDataDirs() []string {
	if env := strings.TrimSpace(os.Getenv("ANTIGRAVITY_HOME")); env != "" {
		return []string{filepath.Clean(env)}
	}
	gRoot := geminiRootDir()
	if gRoot == "" {
		return nil
	}
	var dirs []string
	for _, sub := range []string{"antigravity", "antigravity-ide", "antigravity-cli"} {
		candidate := filepath.Join(gRoot, sub)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			dirs = append(dirs, candidate)
		}
	}
	return dirs
}

type antigravitySummaryRow struct {
	ConversationID   string `json:"conversation_id"`
	Title            string `json:"title"`
	Preview          string `json:"preview"`
	StepCount        int    `json:"step_count"`
	LastModifiedTime string `json:"last_modified_time"`
	LastUserInput    string `json:"last_user_input_time"`
	WorkspaceURIs    string `json:"workspace_uris"`
	Status           string `json:"status"`
}

func parseFileURIListFirstPath(rawJSON string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return ""
	}
	var uris []string
	if json.Unmarshal([]byte(rawJSON), &uris) != nil || len(uris) == 0 {
		return ""
	}
	for _, u := range uris {
		u = strings.TrimSpace(u)
		if strings.HasPrefix(u, "file://") {
			parsed, err := url.Parse(u)
			if err == nil && parsed.Path != "" {
				p := parsed.Path
				if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
					p = p[1:]
				}
				return filepath.FromSlash(p)
			}
		} else if u != "" {
			return filepath.Clean(u)
		}
	}
	return ""
}

func parseAntigravityTimeMs(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, raw); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func scanGeminiAntigravitySessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	dirs := antigravityDataDirs()
	stats := ExternalSourceStats{Installed: len(dirs) > 0}
	if len(dirs) == 0 {
		return nil, stats
	}

	qLower := strings.ToLower(strings.TrimSpace(query))
	seen := map[string]struct{}{}
	var results []ExternalSessionSummary

	for _, aDir := range dirs {
		dbPath := filepath.Join(aDir, "conversation_summaries.db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		countCmd := exec.CommandContext(ctx, "sqlite3", dbPath, "SELECT count(*) FROM conversation_summaries WHERE nesting_depth = 0 AND killed = 0;")
		countCmd.WaitDelay = time.Second
		countOut, countErr := countCmd.Output()
		cancel()
		if countErr == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(countOut))); err == nil && n > stats.SessionCount {
				stats.SessionCount = n
			}
		}

		sqlQuery := `SELECT conversation_id, COALESCE(title,''), COALESCE( substr(preview,1,280), '' ), step_count, COALESCE(last_modified_time,''), COALESCE(last_user_input_time,''), COALESCE(workspace_uris,''), COALESCE(status,'')
FROM conversation_summaries
WHERE nesting_depth = 0 AND killed = 0
ORDER BY last_modified_time DESC
LIMIT 120;`

		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		queryCmd := exec.CommandContext(ctx, "sqlite3", "-json", dbPath, sqlQuery)
		queryCmd.WaitDelay = time.Second
		rawOut, err := queryCmd.Output()
		cancel()
		if err != nil || len(bytes.TrimSpace(rawOut)) == 0 {
			continue
		}

		var rows []antigravitySummaryRow
		if json.Unmarshal(rawOut, &rows) != nil {
			continue
		}

		for _, r := range rows {
			id := strings.TrimSpace(r.ConversationID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}

			cwd := parseFileURIListFirstPath(r.WorkspaceURIs)
			title := strings.TrimSpace(r.Title)
			if title == "" {
				title = truncateRunes(firstLineText(r.Preview), 80)
			}
			if title == "" {
				title = "Antigravity " + id[:minInt(8, len(id))]
			}
			summary := strings.TrimSpace(r.Preview)

			if qLower != "" {
				haystack := strings.ToLower(title + " " + summary + " " + cwd)
				if !strings.Contains(haystack, qLower) {
					continue
				}
			}

			updatedMs := parseAntigravityTimeMs(r.LastModifiedTime)
			createdMs := parseAntigravityTimeMs(r.LastUserInput)
			if createdMs == 0 || createdMs > updatedMs {
				createdMs = updatedMs
			}

			results = append(results, ExternalSessionSummary{
				ID:          id,
				Source:      ExternalToolGemini,
				SourceLabel: "Antigravity (Gemini)",
				CWD:         cwd,
				Title:       title,
				Summary:     truncateRunes(summary, 240),
				CreatedAt:   createdMs,
				UpdatedAt:   updatedMs,
				TurnCount:   r.StepCount,
				MatchesCWD:  sameOrSubPath(cwd, targetDir),
			})
		}
	}
	return results, stats
}

func findAntigravityBrainDir(sessionID string) string {
	for _, aDir := range antigravityDataDirs() {
		bDir := filepath.Join(aDir, "brain", sessionID)
		if st, err := os.Stat(bDir); err == nil && st.IsDir() {
			return bDir
		}
	}
	return ""
}

func extractAntigravityUserPrompt(raw string) (promptText, modelChange string) {
	if idx := strings.Index(raw, "changed setting `Model Selection`"); idx >= 0 {
		rest := raw[idx:]
		if toIdx := strings.Index(rest, " to "); toIdx >= 0 && toIdx < 60 {
			afterTo := rest[toIdx+4:]
			if dotIdx := strings.Index(afterTo, "."); dotIdx > 0 && dotIdx < 60 {
				candidate := strings.TrimSpace(afterTo[:dotIdx])
				if candidate != "" && candidate != "None" && !strings.ContainsAny(candidate, "\n\r\"\\") {
					modelChange = candidate
				}
			}
		}
	}
	const startTag = "<USER_REQUEST>"
	const endTag = "</USER_REQUEST>"
	if s := strings.Index(raw, startTag); s >= 0 {
		body := raw[s+len(startTag):]
		if e := strings.Index(body, endTag); e >= 0 {
			return strings.TrimSpace(body[:e]), modelChange
		}
		return strings.TrimSpace(body), modelChange
	}
	if strings.Contains(raw, "<SYSTEM_MESSAGE>") {
		return "", modelChange
	}
	return strings.TrimSpace(raw), modelChange
}

func unquoteAntigravityArg(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	trimmed := strings.TrimSpace(s)
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		var unq string
		if json.Unmarshal([]byte(trimmed), &unq) == nil {
			return unq
		}
	}
	return s
}

func normalizeAntigravityToolInput(rawName string, rawArgs map[string]interface{}) (string, map[string]interface{}) {
	clean := map[string]interface{}{}
	for k, v := range rawArgs {
		clean[k] = unquoteAntigravityArg(v)
	}
	if fp := firstNonEmpty(asString(clean["AbsolutePath"]), asString(clean["TargetFile"]), asString(clean["SearchPath"]), asString(clean["DirectoryPath"])); fp != "" {
		clean["filePath"] = fp
	}
	if cmd := asString(clean["CommandLine"]); cmd != "" {
		clean["command"] = cmd
	}
	title := firstNonEmpty(asString(clean["toolSummary"]), asString(clean["toolAction"]), rawName)
	return title, clean
}

func parseAntigravityTaskChecklist(brainDir string) []ExternalPlanStep {
	if brainDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(brainDir, "task.md"))
	if err != nil {
		return nil
	}
	var steps []ExternalPlanStep
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- [x] "), strings.HasPrefix(trimmed, "- [X] "):
			steps = append(steps, ExternalPlanStep{Title: strings.TrimSpace(trimmed[6:]), Status: "completed"})
		case strings.HasPrefix(trimmed, "- [/] "):
			steps = append(steps, ExternalPlanStep{Title: strings.TrimSpace(trimmed[6:]), Status: "in_progress"})
		case strings.HasPrefix(trimmed, "- [ ] "):
			steps = append(steps, ExternalPlanStep{Title: strings.TrimSpace(trimmed[6:]), Status: "pending"})
		}
	}
	return steps
}

func loadGeminiAntigravitySessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	sessions, _ := scanGeminiAntigravitySessions(activeRoot, "")
	var matched ExternalSessionSummary
	for _, s := range sessions {
		if s.ID == sessionID {
			matched = s
			break
		}
	}
	if matched.ID == "" {
		matched = ExternalSessionSummary{
			ID:          sessionID,
			Source:      ExternalToolGemini,
			SourceLabel: "Antigravity (Gemini)",
			CWD:         hintCWD,
			Title:       "Antigravity " + sessionID[:minInt(8, len(sessionID))],
			MatchesCWD:  sameOrSubPath(hintCWD, activeRoot),
		}
	}

	brainDir := findAntigravityBrainDir(sessionID)
	if brainDir == "" {
		return &ExternalSessionDetail{Summary: matched}, nil
	}

	transcriptPath := filepath.Join(brainDir, ".system_generated", "logs", "transcript.jsonl")
	f, err := os.Open(transcriptPath)
	if err != nil {
		return &ExternalSessionDetail{
			Summary:   matched,
			PlanSteps: parseAntigravityTaskChecklist(brainDir),
		}, nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var turns []NormalizedTurn
	var pendingOutputs []*NormalizedToolCall
	var lastAssistantText string
	var detectedModel string
	callSeq := 0

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var step map[string]interface{}
		if json.Unmarshal(line, &step) != nil {
			continue
		}
		stepType := asString(step["type"])
		tsMs := parseAntigravityTimeMs(asString(step["created_at"]))

		switch stepType {
		case "USER_INPUT":
			pendingOutputs = nil
			userText, modelName := extractAntigravityUserPrompt(asString(step["content"]))
			if modelName != "" {
				detectedModel = modelName
			}
			if userText != "" {
				turns = append(turns, NormalizedTurn{
					Role:      "user",
					Timestamp: tsMs,
					Text:      userText,
				})
			}

		case "PLANNER_RESPONSE":
			pendingOutputs = nil
			content := strings.TrimSpace(asString(step["content"]))
			thinking := strings.TrimSpace(asString(step["thinking"]))
			var calls []NormalizedToolCall
			if rawCalls, ok := step["tool_calls"].([]interface{}); ok {
				for _, rc := range rawCalls {
					cm, ok := rc.(map[string]interface{})
					if !ok {
						continue
					}
					callSeq++
					rawName := asString(cm["name"])
					rawArgs, _ := cm["args"].(map[string]interface{})
					if rawArgs == nil {
						rawArgs, _ = cm["arguments"].(map[string]interface{})
					}
					title, cleanArgs := normalizeAntigravityToolInput(rawName, rawArgs)
					calls = append(calls, NormalizedToolCall{
						CallID: fmt.Sprintf("agy_call_%d", callSeq),
						Tool:   normalizeExternalToolName(rawName),
						Title:  title,
						Input:  cleanArgs,
						Status: "completed",
					})
				}
			}
			if content == "" && thinking == "" && len(calls) == 0 {
				continue
			}
			if content != "" {
				lastAssistantText = content
			}
			if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
				prev := &turns[len(turns)-1]
				if content != "" {
					if prev.Text != "" {
						prev.Text += "\n\n" + content
					} else {
						prev.Text = content
					}
				}
				if thinking != "" {
					if prev.Reasoning != "" {
						prev.Reasoning += "\n\n" + thinking
					} else {
						prev.Reasoning = thinking
					}
				}
				startIdx := len(prev.Tools)
				prev.Tools = append(prev.Tools, calls...)
				for i := startIdx; i < len(prev.Tools); i++ {
					pendingOutputs = append(pendingOutputs, &prev.Tools[i])
				}
			} else {
				turns = append(turns, NormalizedTurn{
					Role:      "assistant",
					Timestamp: tsMs,
					Text:      content,
					Reasoning: thinking,
					Tools:     calls,
					ModelID:   detectedModel,
				})
				lastTurn := &turns[len(turns)-1]
				for i := range lastTurn.Tools {
					pendingOutputs = append(pendingOutputs, &lastTurn.Tools[i])
				}
			}

		case "GENERIC":
			outText := strings.TrimSpace(asString(step["content"]))
			if outText != "" && len(pendingOutputs) > 0 {
				target := pendingOutputs[0]
				pendingOutputs = pendingOutputs[1:]
				target.Output = truncateRunes(outText, 4000)
			}
		}
	}

	if detectedModel != "" && matched.Model == "" {
		matched.Model = detectedModel
	}
	matched.TurnCount = len(turns)
	if matched.Summary == "" && lastAssistantText != "" {
		matched.Summary = truncateRunes(lastAssistantText, 240)
	}

	return &ExternalSessionDetail{
		Summary:         matched,
		Turns:           turns,
		PlanSteps:       parseAntigravityTaskChecklist(brainDir),
		ChangedFiles:    extractChangedFilesFromTurns(turns),
		LastTurnSummary: truncateRunes(lastAssistantText, 400),
	}, nil
}
