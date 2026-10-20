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

type openCodeConsoleAccountRow struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	URL          string `json:"url"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenExpiry  int64  `json:"tokenExpiry"`
	TimeCreated  int64  `json:"timeCreated"`
	TimeUpdated  int64  `json:"timeUpdated"`
}

type openCodeConsoleState struct {
	Accounts        []openCodeConsoleAccountRow `json:"accounts"`
	ActiveAccountID string                      `json:"activeAccountID"`
	ActiveOrgID     string                      `json:"activeOrgID"`
}

func hoxiOpenCodeDBPath() string {
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_DB")); custom != "" {
		return filepath.Clean(custom)
	}
	if custom := strings.TrimSpace(os.Getenv("HOXI_DB_PATH")); custom != "" {
		return filepath.Clean(custom)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "hoxi.db")
}

func readOpenCodeConsoleStateFromDB(dbPath string) *openCodeConsoleState {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return nil
	}
	querySQL := `
SELECT json_object(
  'accounts', COALESCE((
    SELECT json_group_array(json_object(
      'id', COALESCE(a.id, ''),
      'email', COALESCE(a.email, ''),
      'url', COALESCE(a.url, ''),
      'accessToken', COALESCE(a.access_token, ''),
      'refreshToken', COALESCE(a.refresh_token, ''),
      'tokenExpiry', COALESCE(a.token_expiry, 0),
      'timeCreated', COALESCE(a.time_created, 0),
      'timeUpdated', COALESCE(a.time_updated, 0)
    ))
    FROM account a
  ), json('[]')),
  'activeAccountID', COALESCE((SELECT active_account_id FROM account_state WHERE id = 1 LIMIT 1), ''),
  'activeOrgID', COALESCE((SELECT active_org_id FROM account_state WHERE id = 1 LIMIT 1), '')
);`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", dbPath, querySQL)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var state openCodeConsoleState
	if json.Unmarshal(bytes.TrimSpace(out), &state) != nil {
		return nil
	}
	var valid []openCodeConsoleAccountRow
	for _, a := range state.Accounts {
		if strings.TrimSpace(a.ID) != "" && (strings.TrimSpace(a.AccessToken) != "" || strings.TrimSpace(a.RefreshToken) != "") {
			valid = append(valid, a)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	state.Accounts = valid
	if strings.TrimSpace(state.ActiveAccountID) == "" {
		state.ActiveAccountID = strings.TrimSpace(valid[0].ID)
	}
	return &state
}

func readExternalOpenCodeConsoleState() *openCodeConsoleState {
	for _, dbPath := range externalOpenCodeDBPaths() {
		if state := readOpenCodeConsoleStateFromDB(dbPath); state != nil {
			return state
		}
	}
	return nil
}

func isOpenCodeConsoleStateSynced(ext *openCodeConsoleState) bool {
	if ext == nil || len(ext.Accounts) == 0 {
		return true
	}
	dst := readOpenCodeConsoleStateFromDB(hoxiOpenCodeDBPath())
	if dst == nil || len(dst.Accounts) == 0 {
		return false
	}
	if strings.TrimSpace(ext.ActiveAccountID) != "" && strings.TrimSpace(dst.ActiveAccountID) != strings.TrimSpace(ext.ActiveAccountID) {
		return false
	}
	if strings.TrimSpace(ext.ActiveOrgID) != "" && strings.TrimSpace(dst.ActiveOrgID) != strings.TrimSpace(ext.ActiveOrgID) {
		return false
	}
	return true
}

func syncOpenCodeConsoleStateToHoxiDB(ext *openCodeConsoleState) error {
	if ext == nil || len(ext.Accounts) == 0 {
		return nil
	}
	dstDB := hoxiOpenCodeDBPath()
	if dstDB == "" {
		return fmt.Errorf("cannot resolve Hoxi OpenCode database path")
	}
	if err := os.MkdirAll(filepath.Dir(dstDB), 0o700); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("BEGIN IMMEDIATE;\n")
	sb.WriteString("CREATE TABLE IF NOT EXISTS `account` (`id` text PRIMARY KEY, `email` text NOT NULL, `url` text NOT NULL, `access_token` text NOT NULL, `refresh_token` text NOT NULL, `token_expiry` integer, `time_created` integer NOT NULL, `time_updated` integer NOT NULL);\n")
	sb.WriteString("CREATE TABLE IF NOT EXISTS `account_state` (`id` integer PRIMARY KEY, `active_account_id` text, `active_org_id` text, CONSTRAINT `fk_account_state_active_account_id_account_id_fk` FOREIGN KEY (`active_account_id`) REFERENCES `account`(`id`) ON DELETE SET NULL);\n")
	for _, a := range ext.Accounts {
		fmt.Fprintf(&sb,
			"INSERT INTO `account` (`id`, `email`, `url`, `access_token`, `refresh_token`, `token_expiry`, `time_created`, `time_updated`) VALUES (%s, %s, %s, %s, %s, %d, %d, %d) ON CONFLICT(`id`) DO UPDATE SET `email`=excluded.`email`, `url`=excluded.`url`, `access_token`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR `account`.`access_token` = '' THEN excluded.`access_token` ELSE `account`.`access_token` END, `refresh_token`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR `account`.`refresh_token` = '' THEN excluded.`refresh_token` ELSE `account`.`refresh_token` END, `token_expiry`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR COALESCE(`account`.`token_expiry`, 0) = 0 THEN excluded.`token_expiry` ELSE `account`.`token_expiry` END, `time_updated`=MAX(`account`.`time_updated`, excluded.`time_updated`);\n",
			sqlEscapeLiteral(strings.TrimSpace(a.ID)),
			sqlEscapeLiteral(a.Email),
			sqlEscapeLiteral(a.URL),
			sqlEscapeLiteral(a.AccessToken),
			sqlEscapeLiteral(a.RefreshToken),
			a.TokenExpiry,
			a.TimeCreated,
			a.TimeUpdated,
		)
	}
	activeID := strings.TrimSpace(ext.ActiveAccountID)
	if activeID == "" {
		activeID = strings.TrimSpace(ext.Accounts[0].ID)
	}
	activeOrg := strings.TrimSpace(ext.ActiveOrgID)
	fmt.Fprintf(&sb,
		"INSERT INTO `account_state` (`id`, `active_account_id`, `active_org_id`) VALUES (1, %s, %s) ON CONFLICT(`id`) DO UPDATE SET `active_account_id`=excluded.`active_account_id`, `active_org_id`=CASE WHEN excluded.`active_org_id` != '' THEN excluded.`active_org_id` ELSE `account_state`.`active_org_id` END;\n",
		sqlEscapeLiteral(activeID),
		sqlEscapeLiteral(activeOrg),
	)
	sb.WriteString("COMMIT;\n")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-cmd", ".timeout 3000", dstDB)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(sb.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sync opencode console account failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func detectOpenCodeCLIStatus(g *Service) LocalCLIStatusItem {
	sessions, stats := scanOpenCodeSessions("", "")
	var latestProvider, latestModel string
	var sessionModels []string
	for _, s := range sessions {
		if s.Model != "" {
			sessionModels = appendUniqueString(sessionModels, s.Model)
			if latestProvider == "" {
				latestProvider, latestModel = parseOpenCodeModelField(s.Model)
			}
		}
	}
	provDefs := readExternalOpenCodeConfigProviders()
	authMap := readExternalOpenCodeAuth()
	consoleState := readExternalOpenCodeConsoleState()
	hasExplicitDefaultModel := false
	if defProvider, defModel := parseOpenCodeModelField(readExternalOpenCodeDefaultModel()); defProvider != "" {
		latestProvider, latestModel = defProvider, defModel
		hasExplicitDefaultModel = true
	}
	if consoleState != nil && latestProvider == "" && len(provDefs) == 0 && len(authMap) == 0 {
		latestProvider = "opencode-go"
	}

	installed := stats.Installed || len(provDefs) > 0 || len(authMap) > 0 || consoleState != nil
	authenticated := len(authMap) > 0 || consoleState != nil
	if !authenticated {
		for _, raw := range provDefs {
			def, _ := raw.(map[string]interface{})
			opts, _ := def["options"].(map[string]interface{})
			if strings.TrimSpace(asString(opts["apiKey"])) != "" {
				authenticated = true
				break
			}
		}
	}

	hoxiMap := map[string]interface{}{}
	if g != nil {
		hoxiMap = g.loadHoxiProvidersMap()
	}
	providerID, defaultModel, models := resolveOpenCodeSyncTarget(provDefs, authMap, latestProvider, latestModel)
	providerID = resolveOpenCodeProviderID(providerID, authMap, hoxiMap)
	models = appendUniqueString(models, openCodeProviderModelIDs(hoxiMap, providerID)...)
	var liveDefault string
	var liveModels []string
	if g != nil {
		liveDefault, liveModels = g.fetchLiveOpenCodeProviderModels(providerID)
		models = appendUniqueString(models, liveModels...)
	}
	// If defaultModel was inferred from an older session rather than an explicit
	// config file, verify it still exists in the active catalog; otherwise prefer
	// the live provider default (e.g. when opencode-go retires glm-5.1 for glm-5.3).
	if !hasExplicitDefaultModel && defaultModel != "" && len(models) > 0 {
		found := false
		for _, m := range models {
			if m == defaultModel {
				found = true
				break
			}
		}
		if !found {
			defaultModel = liveDefault
		}
	} else if defaultModel == "" && liveDefault != "" {
		defaultModel = liveDefault
	}
	if len(models) == 0 {
		models = sessionModels
	}

	connected := len(provDefs)+len(authMap) > 0 || consoleState != nil
	for pid := range provDefs {
		if _, ok := hoxiMap[resolveOpenCodeProviderID(pid, authMap, hoxiMap)]; !ok {
			connected = false
			break
		}
	}
	if connected {
		engineAuth := engineOpenCodeAuthIDs()
		for pid := range authMap {
			if _, ok := engineAuth[pid]; !ok {
				connected = false
				break
			}
		}
	}
	if connected && consoleState != nil && !isOpenCodeConsoleStateSynced(consoleState) {
		connected = false
	}

	return LocalCLIStatusItem{
		ID:            ExternalToolOpenCode,
		Name:          "OpenCode CLI",
		Installed:     installed,
		Authenticated: authenticated,
		Connected:     connected,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "session-handoff",
		ProviderID:    providerID,
	}
}
