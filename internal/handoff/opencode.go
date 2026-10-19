package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func externalOpenCodeDBPaths() []string {
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_EXTERNAL_DB")); custom != "" {
		return []string{filepath.Clean(custom)}
	}
	if customDir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); customDir != "" {
		return []string{filepath.Join(filepath.Clean(customDir), "opencode.db")}
	}
	home, _ := os.UserHomeDir()
	hoxiDB := ""
	hoxiEngineDB := ""
	if home != "" {
		hoxiDB = filepath.Clean(filepath.Join(home, ".hoxi", "hoxi.db"))
		hoxiEngineDB = filepath.Clean(filepath.Join(home, ".hoxi", "engine-data", "opencode", "opencode.db"))
	}
	var candidates []string
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "opencode", "opencode.db"))
	}
	if localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localApp != "" {
		candidates = append(candidates,
			filepath.Join(localApp, "opencode", "opencode.db"),
			filepath.Join(localApp, "opencode", "Data", "opencode.db"),
		)
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates, filepath.Join(appData, "opencode", "opencode.db"))
	}
	var out []string
	seen := map[string]struct{}{}
	for _, p := range candidates {
		clean := filepath.Clean(p)
		if (hoxiDB != "" && clean == hoxiDB) || (hoxiEngineDB != "" && clean == hoxiEngineDB) {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		if st, err := os.Stat(clean); err == nil && !st.IsDir() {
			seen[clean] = struct{}{}
			out = append(out, clean)
		}
	}
	return out
}

func resolveExternalOpenCodeBinary() string {
	if p, err := exec.LookPath("opencode"); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	if home, _ := os.UserHomeDir(); home != "" {
		for _, sub := range []string{
			filepath.Join(home, ".opencode", "bin", "opencode"),
			filepath.Join(home, ".hoxi", "bin", "opencode"),
		} {
			if st, err := os.Stat(sub); err == nil && !st.IsDir() {
				return sub
			}
		}
	}
	return ""
}

func parseOpenCodeModelField(raw string) (providerID, modelID string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "{") {
		var obj map[string]interface{}
		if json.Unmarshal([]byte(raw), &obj) == nil {
			p := strings.TrimSpace(asString(obj["providerID"]))
			m := firstNonEmpty(strings.TrimSpace(asString(obj["id"])), strings.TrimSpace(asString(obj["modelID"])))
			return p, m
		}
	}
	if slash := strings.IndexByte(raw, '/'); slash > 0 && slash < len(raw)-1 {
		return strings.TrimSpace(raw[:slash]), strings.TrimSpace(raw[slash+1:])
	}
	return "", raw
}

func formatOpenCodeModelLabel(raw string) string {
	p, m := parseOpenCodeModelField(raw)
	if p != "" && m != "" {
		return p + "/" + m
	}
	return m
}

func splitOpenCodeProviderModel(label string) (providerID, modelID string) {
	return parseOpenCodeModelField(label)
}

func firstNonZeroInt64(vals ...int64) int64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

type openCodeDBSessionRow struct {
	ID          string `json:"id"`
	Directory   string `json:"directory"`
	Title       string `json:"title"`
	TimeCreated int64  `json:"timeCreated"`
	TimeUpdated int64  `json:"timeUpdated"`
	Model       string `json:"model"`
	TurnCount   int    `json:"turnCount"`
}

func queryExternalOpenCodeSessions(dbPath string) []openCodeDBSessionRow {
	sqlRich := `
SELECT json_group_array(json_object(
  'id', s.id,
  'directory', COALESCE(s.directory, ''),
  'title', COALESCE(s.title, ''),
  'timeCreated', COALESCE(s.time_created, 0),
  'timeUpdated', COALESCE(s.time_updated, 0),
  'model', COALESCE(s.model, ''),
  'turnCount', (SELECT count(*) FROM message m WHERE m.session_id = s.id)
))
FROM (
  SELECT id, directory, title, time_created, time_updated, model
  FROM session
  WHERE parent_id IS NULL
    AND (metadata IS NULL OR metadata NOT LIKE '%"hoxiConversation"%')
    AND title NOT LIKE '[Grok CLI]%'
    AND title NOT LIKE '[Claude Code]%'
    AND title NOT LIKE '[Codex%'
    AND title NOT LIKE '[Gemini%'
    AND title NOT LIKE '[CodeBuddy]%'
    AND title NOT LIKE '[Trae]%'
    AND title NOT LIKE '[Qoder]%'
    AND title NOT LIKE '[OpenCode%'
  ORDER BY time_updated DESC
  LIMIT 200
) s;`

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", dbPath, sqlRich)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var rows []openCodeDBSessionRow
	if json.Unmarshal(bytes.TrimSpace(out), &rows) != nil {
		return nil
	}
	return rows
}

func scanOpenCodeSessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	dbPaths := externalOpenCodeDBPaths()
	stats := ExternalSourceStats{
		Installed: len(dbPaths) > 0 || resolveExternalOpenCodeBinary() != "",
	}
	if len(dbPaths) == 0 {
		return nil, stats
	}
	qLower := strings.ToLower(strings.TrimSpace(query))
	var results []ExternalSessionSummary
	seen := map[string]struct{}{}

	for _, dbPath := range dbPaths {
		rows := queryExternalOpenCodeSessions(dbPath)
		for _, row := range rows {
			id := strings.TrimSpace(row.ID)
			if id == "" || row.TurnCount <= 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			stats.SessionCount++

			cwd := cleanDirPath(row.Directory)
			title := firstNonEmpty(strings.TrimSpace(row.Title), id)
			modelLabel := formatOpenCodeModelLabel(row.Model)
			if qLower != "" {
				blob := strings.ToLower(title + " " + cwd + " " + modelLabel + " " + id)
				if !strings.Contains(blob, qLower) {
					continue
				}
			}
			results = append(results, ExternalSessionSummary{
				ID:          id,
				Source:      ExternalToolOpenCode,
				SourceLabel: "OpenCode CLI",
				CWD:         cwd,
				Title:       truncateRunes(title, 90),
				Model:       modelLabel,
				CreatedAt:   row.TimeCreated,
				UpdatedAt:   firstNonZeroInt64(row.TimeUpdated, row.TimeCreated),
				TurnCount:   row.TurnCount,
				MatchesCWD:  targetDir != "" && sameOrSubPath(cwd, targetDir),
			})
		}
	}
	return results, stats
}

func loadOpenCodeSessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("opencode session id is required")
	}
	for _, dbPath := range externalOpenCodeDBPaths() {
		detail, err := loadOpenCodeSessionFromDB(dbPath, sessionID, hintCWD, activeRoot)
		if err == nil && detail != nil {
			return detail, nil
		}
	}
	return nil, fmt.Errorf("opencode session not found: %s", sessionID)
}

func loadOpenCodeSessionFromDB(dbPath, sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	escID := sqlEscapeLiteral(sessionID)
	sqlQuery := fmt.Sprintf(`
SELECT json_object(
  'session', (
    SELECT json_object(
      'id', s.id,
      'directory', COALESCE(s.directory, ''),
      'title', COALESCE(s.title, ''),
      'timeCreated', COALESCE(s.time_created, 0),
      'timeUpdated', COALESCE(s.time_updated, 0),
      'model', COALESCE(s.model, '')
    )
    FROM session s WHERE s.id = %s
  ),
  'messages', COALESCE((
    SELECT json_group_array(json_object(
      'id', m.id,
      'timeCreated', COALESCE(m.time_created, 0),
      'data', m.data
    ))
    FROM (
      SELECT id, time_created, data FROM message WHERE session_id = %s ORDER BY time_created ASC, id ASC
    ) m
  ), json('[]')),
  'parts', COALESCE((
    SELECT json_group_array(json_object(
      'id', p.id,
      'messageID', p.message_id,
      'timeCreated', COALESCE(p.time_created, 0),
      'data', p.data
    ))
    FROM (
      SELECT id, message_id, time_created, data FROM part WHERE session_id = %s ORDER BY time_created ASC, id ASC
    ) p
  ), json('[]'))
);`, escID, escID, escID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", dbPath, sqlQuery)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("query opencode.db failed")
	}

	var payload struct {
		Session *struct {
			ID          string `json:"id"`
			Directory   string `json:"directory"`
			Title       string `json:"title"`
			TimeCreated int64  `json:"timeCreated"`
			TimeUpdated int64  `json:"timeUpdated"`
			Model       string `json:"model"`
		} `json:"session"`
		Messages []struct {
			ID          string `json:"id"`
			TimeCreated int64  `json:"timeCreated"`
			Data        string `json:"data"`
		} `json:"messages"`
		Parts []struct {
			ID          string `json:"id"`
			MessageID   string `json:"messageID"`
			TimeCreated int64  `json:"timeCreated"`
			Data        string `json:"data"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &payload); err != nil || payload.Session == nil || payload.Session.ID == "" {
		return nil, fmt.Errorf("session %s not found in %s", sessionID, dbPath)
	}

	partsByMsg := map[string][]map[string]interface{}{}
	for _, p := range payload.Parts {
		var pDoc map[string]interface{}
		if json.Unmarshal([]byte(p.Data), &pDoc) == nil && pDoc != nil {
			if pDoc["id"] == nil {
				pDoc["id"] = p.ID
			}
			partsByMsg[p.MessageID] = append(partsByMsg[p.MessageID], pDoc)
		}
	}

	var turns []NormalizedTurn
	var planSteps []ExternalPlanStep
	sessionModel := formatOpenCodeModelLabel(payload.Session.Model)

	for _, m := range payload.Messages {
		var mDoc map[string]interface{}
		if json.Unmarshal([]byte(m.Data), &mDoc) != nil || mDoc == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(mDoc["role"])))
		if role != "user" && role != "assistant" {
			continue
		}
		turnModel := extractOpenCodeMsgModel(mDoc)
		if sessionModel == "" && turnModel != "" {
			sessionModel = turnModel
		}

		var textBlocks []string
		var reasoningBlocks []string
		var tools []NormalizedToolCall

		for _, pDoc := range partsByMsg[m.ID] {
			pType := strings.TrimSpace(asString(pDoc["type"]))
			switch pType {
			case "text":
				if txt := strings.TrimSpace(asString(pDoc["text"])); txt != "" {
					textBlocks = append(textBlocks, txt)
				}
			case "reasoning":
				if rTxt := strings.TrimSpace(asString(pDoc["text"])); rTxt != "" {
					reasoningBlocks = append(reasoningBlocks, rTxt)
				}
			case "tool":
				tc, steps := parseOpenCodeToolPart(pDoc, len(tools)+1)
				tools = append(tools, tc)
				if len(steps) > 0 {
					planSteps = steps
				}
			}
		}

		text := strings.TrimSpace(strings.Join(textBlocks, "\n\n"))
		reasoning := strings.TrimSpace(strings.Join(reasoningBlocks, "\n\n"))
		if text == "" && reasoning == "" && len(tools) == 0 {
			continue
		}
		turns = append(turns, NormalizedTurn{
			Role:      role,
			Timestamp: m.TimeCreated,
			Text:      text,
			Reasoning: reasoning,
			Tools:     tools,
			ModelID:   turnModel,
		})
	}

	if len(turns) == 0 {
		return nil, fmt.Errorf("opencode session %s has no turns", sessionID)
	}

	cwd := cleanDirPath(firstNonEmpty(payload.Session.Directory, hintCWD, activeRoot))
	lastSummary := ""
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" && turns[i].Text != "" {
			lastSummary = truncateRunes(turns[i].Text, 240)
			break
		}
	}

	summary := ExternalSessionSummary{
		ID:          payload.Session.ID,
		Source:      ExternalToolOpenCode,
		SourceLabel: "OpenCode CLI",
		CWD:         cwd,
		Title:       firstNonEmpty(strings.TrimSpace(payload.Session.Title), payload.Session.ID),
		Summary:     lastSummary,
		Model:       sessionModel,
		CreatedAt:   payload.Session.TimeCreated,
		UpdatedAt:   firstNonZeroInt64(payload.Session.TimeUpdated, payload.Session.TimeCreated),
		TurnCount:   len(turns),
		MatchesCWD:  activeRoot != "" && sameOrSubPath(cwd, activeRoot),
	}

	return &ExternalSessionDetail{
		Summary:         summary,
		Turns:           turns,
		PlanSteps:       planSteps,
		ChangedFiles:    extractChangedFilesFromTurns(turns),
		LastTurnSummary: lastSummary,
		LastRecap:       lastSummary,
	}, nil
}

func extractOpenCodeMsgModel(mDoc map[string]interface{}) string {
	p := strings.TrimSpace(asString(mDoc["providerID"]))
	m := strings.TrimSpace(asString(mDoc["modelID"]))
	if modObj, ok := mDoc["model"].(map[string]interface{}); ok {
		if p == "" {
			p = strings.TrimSpace(asString(modObj["providerID"]))
		}
		if m == "" {
			m = firstNonEmpty(strings.TrimSpace(asString(modObj["modelID"])), strings.TrimSpace(asString(modObj["id"])))
		}
	}
	if p != "" && m != "" {
		return p + "/" + m
	}
	return m
}

func parseOpenCodeToolPart(pDoc map[string]interface{}, seq int) (NormalizedToolCall, []ExternalPlanStep) {
	toolName := normalizeExternalToolName(asString(pDoc["tool"]))
	callID := sanitizeToolCallID(asString(pDoc["callID"]), seq)
	state, _ := pDoc["state"].(map[string]interface{})
	status := firstNonEmpty(strings.TrimSpace(asString(state["status"])), "completed")
	title := firstNonEmpty(strings.TrimSpace(asString(state["title"])), toolName)
	input, _ := state["input"].(map[string]interface{})
	if input == nil {
		input = map[string]interface{}{}
	}
	output := truncateRunes(strings.TrimSpace(asString(state["output"])), 1200)

	var steps []ExternalPlanStep
	if strings.Contains(strings.ToLower(toolName), "todo") {
		if rawTodos, ok := input["todos"].([]interface{}); ok {
			for _, item := range rawTodos {
				if tMap, ok := item.(map[string]interface{}); ok {
					content := strings.TrimSpace(firstNonEmpty(asString(tMap["content"]), asString(tMap["title"])))
					st := strings.TrimSpace(firstNonEmpty(asString(tMap["status"]), "pending"))
					if content != "" {
						steps = append(steps, ExternalPlanStep{Title: content, Status: st})
					}
				}
			}
		}
	}

	return NormalizedToolCall{
		CallID: callID,
		Tool:   toolName,
		Title:  title,
		Input:  input,
		Output: output,
		Status: status,
	}, steps
}

func detectOpenCodeCLIStatus() LocalCLIStatusItem {
	sessions, stats := scanOpenCodeSessions("", "")
	var models []string
	defaultModel := ""
	for _, s := range sessions {
		if s.Model != "" {
			if defaultModel == "" {
				defaultModel = s.Model
			}
			models = appendUniqueString(models, s.Model)
		}
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolOpenCode,
		Name:          "OpenCode CLI",
		Installed:     stats.Installed,
		Authenticated: false,
		Connected:     false,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "session-handoff",
		ProviderID:    "opencode",
	}
}
