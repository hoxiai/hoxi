package handoff

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
)

func codexHomeDir() string {
	return agentcli.CodexHomeDir()
}

func findCodexStateDB() string {
	cDir := codexHomeDir()
	if cDir == "" {
		return ""
	}
	for _, name := range []string{"state_5.sqlite", "state_4.sqlite", "state.sqlite"} {
		candidate := filepath.Join(cDir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

type codexThreadRow struct {
	ID               string `json:"id"`
	RolloutPath      string `json:"rollout_path"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
	CWD              string `json:"cwd"`
	Title            string `json:"title"`
	GitBranch        string `json:"git_branch"`
	GitSHA           string `json:"git_sha"`
	Model            string `json:"model"`
	FirstUserMessage string `json:"first_user_message"`
}

func scanCodexSessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	cDir := codexHomeDir()
	stats := ExternalSourceStats{}
	if cDir == "" {
		return nil, stats
	}
	if _, err := os.Stat(cDir); err == nil {
		stats.Installed = true
	}
	dbPath := findCodexStateDB()
	if dbPath == "" {
		return nil, stats
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var rows []codexThreadRow
	err := sqlitecli.WithReadOnly(ctx, dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		_ = db.QueryRowContext(ctx, `SELECT count(*) FROM threads WHERE archived = 0;`).Scan(&stats.SessionCount)
		r, err := db.QueryContext(ctx, `
			SELECT COALESCE(id,''), COALESCE(rollout_path,''), COALESCE(created_at,0), COALESCE(updated_at,0), COALESCE(cwd,''), COALESCE(title,''), COALESCE(git_branch,''), COALESCE(git_sha,''), COALESCE(model,''), COALESCE(first_user_message,'')
			FROM threads
			WHERE archived = 0 AND COALESCE(title,'') != 'Guardian review'
			ORDER BY updated_at DESC
			LIMIT 120;`)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var row codexThreadRow
			if err := r.Scan(&row.ID, &row.RolloutPath, &row.CreatedAt, &row.UpdatedAt, &row.CWD, &row.Title, &row.GitBranch, &row.GitSHA, &row.Model, &row.FirstUserMessage); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return r.Err()
	})
	if err != nil || len(rows) == 0 {
		return nil, stats
	}

	cfgDefaultModel, _, _ := readCodexLocalConfig()
	qLower := strings.ToLower(strings.TrimSpace(query))
	var results []ExternalSessionSummary

	for _, r := range rows {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = truncateRunes(firstLineText(r.FirstUserMessage), 80)
		}
		if title == "" {
			title = "Codex Thread " + r.ID[:minInt(8, len(r.ID))]
		}
		summary := strings.TrimSpace(r.FirstUserMessage)
		if qLower != "" {
			haystack := strings.ToLower(title + " " + summary + " " + r.CWD + " " + r.GitBranch)
			if !strings.Contains(haystack, qLower) {
				continue
			}
		}
		createdMs := r.CreatedAt
		if createdMs > 0 && createdMs < 10000000000 {
			createdMs *= 1000
		}
		updatedMs := r.UpdatedAt
		if updatedMs > 0 && updatedMs < 10000000000 {
			updatedMs *= 1000
		}

		results = append(results, ExternalSessionSummary{
			ID:          r.ID,
			Source:      ExternalToolCodex,
			SourceLabel: "OpenAI Codex",
			CWD:         r.CWD,
			Title:       title,
			Summary:     truncateRunes(summary, 240),
			Model:       firstNonEmpty(r.Model, cfgDefaultModel),
			GitBranch:   r.GitBranch,
			GitCommit:   shortCommit(r.GitSHA),
			CreatedAt:   createdMs,
			UpdatedAt:   updatedMs,
			MatchesCWD:  sameOrSubPath(r.CWD, targetDir),
		})
	}
	return results, stats
}

func loadCodexSessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	dbPath := findCodexStateDB()
	if dbPath == "" {
		return nil, fmt.Errorf("codex state database not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var row codexThreadRow
	err := sqlitecli.QueryRowReadOnly(ctx, dbPath, 5*time.Second,
		`SELECT COALESCE(id,''), COALESCE(rollout_path,''), COALESCE(created_at,0), COALESCE(updated_at,0), COALESCE(cwd,''), COALESCE(title,''), COALESCE(git_branch,''), COALESCE(git_sha,''), COALESCE(model,''), COALESCE(first_user_message,'') FROM threads WHERE id = ? LIMIT 1;`,
		[]any{sessionID},
		&row.ID, &row.RolloutPath, &row.CreatedAt, &row.UpdatedAt, &row.CWD, &row.Title, &row.GitBranch, &row.GitSHA, &row.Model, &row.FirstUserMessage,
	)
	if err != nil || row.ID == "" {
		return nil, fmt.Errorf("codex session not found: %s", sessionID)
	}
	createdMs := row.CreatedAt
	if createdMs > 0 && createdMs < 10000000000 {
		createdMs *= 1000
	}
	updatedMs := row.UpdatedAt
	if updatedMs > 0 && updatedMs < 10000000000 {
		updatedMs *= 1000
	}
	cwd := strings.TrimSpace(row.CWD)
	if cwd == "" {
		cwd = hintCWD
	}

	turns := parseCodexRolloutFile(row.RolloutPath, createdMs, row.Model)
	title := strings.TrimSpace(row.Title)
	if title == "" {
		title = "Codex Thread " + row.ID[:minInt(8, len(row.ID))]
	}

	sum := ExternalSessionSummary{
		ID:          row.ID,
		Source:      ExternalToolCodex,
		SourceLabel: "OpenAI Codex",
		CWD:         cwd,
		Title:       title,
		Summary:     strings.TrimSpace(row.FirstUserMessage),
		Model:       row.Model,
		GitBranch:   row.GitBranch,
		GitCommit:   shortCommit(row.GitSHA),
		CreatedAt:   createdMs,
		UpdatedAt:   updatedMs,
		TurnCount:   len(turns),
		MatchesCWD:  sameOrSubPath(cwd, activeRoot),
	}

	return &ExternalSessionDetail{
		Summary:      sum,
		Turns:        turns,
		ChangedFiles: extractChangedFilesFromTurns(turns),
	}, nil
}

func openCodexRolloutReader(rolloutPath string) (io.ReadCloser, error) {
	if strings.HasSuffix(rolloutPath, ".zst") {
		out, err := codexCommandOutput("zstd", "-dc", rolloutPath)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(out)), nil
	}
	return os.Open(rolloutPath)
}

func codexCommandOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

func isSyntheticCodexUserPrompt(txt string) bool {
	trimmed := strings.TrimSpace(txt)
	if trimmed == "" {
		return true
	}
	prefixes := []string{
		"# AGENTS.md instructions",
		"<INSTRUCTIONS>",
		"<environment_context>",
		"<external_codex_",
		"<user_instructions>",
		"<permissions instructions>",
		"<system-reminder>",
		"<subagent_notification>",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

func parseCodexRolloutFile(rolloutPath string, baseTs int64, defaultModel string) []NormalizedTurn {
	rc, err := openCodexRolloutReader(rolloutPath)
	if err != nil {
		return nil
	}
	defer rc.Close()

	var turns []NormalizedTurn
	toolCallIDIndex := map[string]int{} // map callID to turn index in turns slice
	var pendingUsage TurnUsage
	pendingUsageReady := false
	var pendingLegacyUsage TurnUsage
	pendingLegacyUsageReady := false
	legacyUsageByTurn := make(map[int]TurnUsage)

	scanner := bufio.NewScanner(rc)
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
		ts := parseTimeMs(asString(item["timestamp"]))
		if ts <= 0 {
			ts = baseTs + step*1000
		}
		payload, _ := item["payload"].(map[string]interface{})
		// Modern Codex records usage on token_usage_record; older files fall back
		// to event_msg/token_count. Attach usage only to an assistant turn.
		if usage := codexItemUsage(item, payload); !usage.empty() {
			if asString(payload["type"]) == "token_count" {
				if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
					legacyUsageByTurn[len(turns)-1] = usage
				} else {
					pendingLegacyUsage = usage
					pendingLegacyUsageReady = true
				}
			} else if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
				attachCodexUsage(&turns, usage, ts, defaultModel)
			} else {
				pendingUsage = pendingUsage.add(usage)
				pendingUsageReady = true
			}
		}
		if asString(item["type"]) != "response_item" || payload == nil {
			continue
		}
		pType := asString(payload["type"])

		switch pType {
		case "message":
			role := asString(payload["role"])
			txt := extractCodexMessageText(payload["content"])
			if role == "user" {
				if isSyntheticCodexUserPrompt(txt) {
					continue
				}
				turns = append(turns, NormalizedTurn{
					Role:      "user",
					Timestamp: ts,
					Text:      txt,
				})
			} else if role == "assistant" && txt != "" {
				phase := asString(payload["phase"])
				if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
					last := &turns[len(turns)-1]
					if phase == "commentary" {
						if last.Reasoning != "" {
							last.Reasoning += "\n\n" + txt
						} else {
							last.Reasoning = txt
						}
					} else {
						if last.Text != "" {
							last.Text += "\n\n" + txt
						} else {
							last.Text = txt
						}
					}
				} else {
					turn := NormalizedTurn{
						Role:      "assistant",
						Timestamp: ts,
						ModelID:   defaultModel,
					}
					if phase == "commentary" {
						turn.Reasoning = txt
					} else {
						turn.Text = txt
					}
					turns = append(turns, turn)
				}
			}

		case "reasoning":
			rText := extractGrokSummaryText(payload["summary"])
			if rText != "" {
				if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
					last := &turns[len(turns)-1]
					if last.Reasoning != "" {
						last.Reasoning += "\n\n" + rText
					} else {
						last.Reasoning = rText
					}
				} else {
					turns = append(turns, NormalizedTurn{
						Role:      "assistant",
						Timestamp: ts,
						Reasoning: rText,
						ModelID:   defaultModel,
					})
				}
			}

		case "function_call", "custom_tool_call":
			callID := asString(payload["call_id"])
			rawName := asString(payload["name"])
			inputMap := map[string]interface{}{}
			if argsStr := asString(payload["arguments"]); argsStr != "" {
				_ = json.Unmarshal([]byte(argsStr), &inputMap)
			} else if inStr := asString(payload["input"]); inStr != "" {
				inputMap["command"] = inStr
			}
			tc := NormalizedToolCall{
				CallID: callID,
				Tool:   normalizeExternalToolName(rawName),
				Title:  rawName,
				Input:  inputMap,
				Status: "completed",
			}
			if len(turns) == 0 || turns[len(turns)-1].Role != "assistant" {
				turns = append(turns, NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts,
					ModelID:   defaultModel,
				})
			}
			last := &turns[len(turns)-1]
			last.Tools = append(last.Tools, tc)
			if callID != "" {
				toolCallIDIndex[callID] = len(turns) - 1
			}

		case "function_call_output", "custom_tool_call_output":
			callID := asString(payload["call_id"])
			if turnIdx, exists := toolCallIDIndex[callID]; exists && turnIdx < len(turns) {
				for i := range turns[turnIdx].Tools {
					if turns[turnIdx].Tools[i].CallID == callID {
						out := extractCodexMessageText(payload["output"])
						if out == "" {
							out = asString(payload["output"])
						}
						turns[turnIdx].Tools[i].Output = truncateRunes(out, 4000)
						break
					}
				}
			}
		}
		if pendingUsageReady && len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
			attachCodexUsage(&turns, pendingUsage, ts, defaultModel)
			pendingUsage = TurnUsage{}
			pendingUsageReady = false
		}
		if pendingLegacyUsageReady && len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
			legacyUsageByTurn[len(turns)-1] = pendingLegacyUsage
			pendingLegacyUsage = TurnUsage{}
			pendingLegacyUsageReady = false
		}
	}
	if pendingUsageReady && len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
		attachCodexUsage(&turns, pendingUsage, baseTs+step*1000, defaultModel)
	}
	if pendingLegacyUsageReady && len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
		legacyUsageByTurn[len(turns)-1] = pendingLegacyUsage
	}
	for turnIndex, usage := range legacyUsageByTurn {
		if turnIndex >= 0 && turnIndex < len(turns) && turns[turnIndex].Role == "assistant" && turns[turnIndex].Usage.empty() {
			turns[turnIndex].Usage = usage
		}
	}
	return turns
}

func codexItemUsage(item, payload map[string]interface{}) TurnUsage {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	// token_usage_record is emitted once per model response; sum its usage
	// records. Its turn_token_usage and thread_token_usage siblings are already
	// cumulative snapshots. token_count is a legacy fallback and must not be
	// combined with records when modern records exist.
	if asString(item["type"]) == "token_usage_record" {
		return parseTurnUsage(payload["usage"])
	}
	if asString(payload["type"]) == "token_count" {
		info, _ := payload["info"].(map[string]interface{})
		if usage := parseTurnUsage(info["last_token_usage"]); !usage.empty() {
			return usage
		}
		if usage := parseTurnUsage(payload["last_token_usage"]); !usage.empty() {
			return usage
		}
		// Old token_count formats may expose only one usage object. Prefer the
		// latest per-turn snapshot when available; never use thread total here.
		if usage := parseTurnUsage(info["turn_token_usage"]); !usage.empty() {
			return usage
		}
		return parseTurnUsage(payload)
	}
	return TurnUsage{}
}

func attachCodexUsage(turns *[]NormalizedTurn, usage TurnUsage, ts int64, model string) {
	if len(*turns) == 0 || (*turns)[len(*turns)-1].Role != "assistant" {
		return
	}
	last := &(*turns)[len(*turns)-1]
	last.Usage = last.Usage.add(usage)
}

func extractCodexMessageText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []interface{}:
		var parts []string
		for _, elem := range v {
			if m, ok := elem.(map[string]interface{}); ok {
				if t := strings.TrimSpace(asString(m["text"])); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func detectCodexCLIStatus(g *Service, hoxiMap map[string]interface{}) LocalCLIStatusItem {
	cDir := codexHomeDir()
	_, stats := scanCodexSessions("", "")
	codexBin := agentcli.ResolveCodexBinary()
	defaultModel, version, models := readCodexLocalConfig()
	var authMode, accessToken, apiKey string
	if cDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cDir, "auth.json")); err == nil {
			var doc map[string]interface{}
			if json.Unmarshal(raw, &doc) == nil {
				authMode = asString(doc["auth_mode"])
				apiKey = asString(doc["OPENAI_API_KEY"])
				if tokens, ok := doc["tokens"].(map[string]interface{}); ok {
					accessToken = asString(tokens["access_token"])
				}
			}
		}
	}
	_, inHoxi := hoxiMap["openai"]
	hasStoredAuth := g.resolveStoredProviderKey("openai", "") != ""
	return LocalCLIStatusItem{
		ID:            ExternalToolCodex,
		Name:          "OpenAI Codex",
		Installed:     stats.Installed || codexBin != "",
		Authenticated: accessToken != "" || apiKey != "" || hasStoredAuth,
		Connected:     inHoxi,
		Version:       version,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      authMode,
		ProviderID:    "openai",
		Quota:         collectCodexQuotaSnapshot(cDir, time.Now()),
	}
}

func readCodexLocalConfig() (defaultModel, version string, models []string) {
	snap := agentcli.DiscoverCodexCatalog(false)
	models = make([]string, 0, len(snap.Models))
	for _, m := range snap.Models {
		if m.Slug != "" {
			models = appendUniqueString(models, m.Slug)
		}
	}
	return snap.DefaultModel, snap.Version, models
}

func buildCodexProviderEntry(snap agentcli.CodexCatalogSnapshot, apiKey string, existingOpts map[string]interface{}) (map[string]interface{}, []string) {
	specs := snap.Models
	if len(specs) == 0 {
		specs = agentcli.DefaultCodexCatalogModels()
	}
	modelsMap := make(map[string]interface{}, len(specs))
	slugs := make([]string, 0, len(specs))
	orderSlice := make([]interface{}, 0, len(specs))
	for _, spec := range specs {
		slug := strings.TrimSpace(spec.Slug)
		if slug == "" {
			continue
		}
		if _, exists := modelsMap[slug]; exists {
			continue
		}
		slugs = append(slugs, slug)
		orderSlice = append(orderSlice, slug)

		label := strings.TrimSpace(spec.DisplayName)
		if label == "" {
			label = slug
		}
		if !strings.Contains(strings.ToLower(label), "(codex)") {
			label = label + " (Codex)"
		}
		inputs := []interface{}{"text"}
		if spec.Image {
			inputs = []interface{}{"text", "image"}
		}
		entry := map[string]interface{}{
			"name":       label,
			"tool_call":  true,
			"reasoning":  true,
			"attachment": spec.Image,
			"modalities": map[string]interface{}{
				"input":  inputs,
				"output": []interface{}{"text"},
			},
		}
		if len(spec.Efforts) > 0 {
			variants := make(map[string]interface{}, len(spec.Efforts))
			for _, eff := range spec.Efforts {
				variants[eff] = map[string]interface{}{"reasoningEffort": eff}
			}
			entry["variants"] = variants
		}
		modelsMap[slug] = entry
	}

	opts := map[string]interface{}{}
	for k, v := range existingOpts {
		opts[k] = v
	}
	opts["modelOrder"] = orderSlice
	defaultModel := strings.TrimSpace(snap.DefaultModel)
	if defaultModel == "" && len(slugs) > 0 {
		defaultModel = slugs[0]
	}
	if defaultModel != "" {
		opts["defaultModel"] = defaultModel
	}
	if ver := strings.TrimPrefix(strings.TrimSpace(snap.Version), "v"); ver != "" {
		opts["codexVersion"] = ver
	}
	if apiKey != "" {
		opts["apiKey"] = apiKey
	}

	provEntry := map[string]interface{}{
		"name":    "OpenAI Codex (Local)",
		"npm":     "@ai-sdk/openai",
		"models":  modelsMap,
		"options": opts,
	}
	return provEntry, slugs
}

func (g *Service) importCodexCLIProvider() LocalCLIImportResponse {
	cDir := codexHomeDir()
	if cDir == "" {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "~/.codex not found"}
	}
	provCfg := agentcli.ReadCodexProviderConfig()
	authPath := filepath.Join(cDir, "auth.json")
	var authBody map[string]interface{}
	if raw, err := os.ReadFile(authPath); err == nil {
		var doc map[string]interface{}
		if json.Unmarshal(raw, &doc) != nil {
			return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "Invalid ~/.codex/auth.json"}
		}
		authBody = readCodexAuthBody(authPath)
	} else if provCfg.BaseURL == "" {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "~/.codex/auth.json not found"}
	}
	if authBody == nil && provCfg.BaseURL != "" {
		envName := firstNonEmpty(provCfg.EnvKey, "OPENAI_API_KEY")
		if envKey := strings.TrimSpace(os.Getenv(envName)); envKey != "" {
			authBody = map[string]interface{}{"type": "api", "key": envKey}
		}
	}
	if authBody == nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "No valid tokens in ~/.codex/auth.json"}
	}
	apiKey := strings.TrimSpace(asString(authBody["key"]))

	if !isEngineOAuthNewer(readEngineOpenCodeAuthEntry("openai"), authBody) {
		g.mergeOpenCodeEngineAuthFile(map[string]interface{}{"openai": authBody})
		g.sendEngineAuth("openai", authBody, 5*time.Second)
	}

	var initOpts map[string]interface{}
	if apiKey != "" && provCfg.BaseURL != "" {
		initOpts = map[string]interface{}{"baseURL": provCfg.BaseURL}
	}
	snap := agentcli.DiscoverCodexCatalog(true)
	provEntry, models := buildCodexProviderEntry(snap, apiKey, initOpts)
	if apiKey != "" && strings.EqualFold(provCfg.WireAPI, "chat") {
		provEntry["npm"] = "@ai-sdk/openai-compatible"
	}
	if err := g.updateHoxiConfigDoc(func(cfgDoc map[string]interface{}) error {
		provMap, _ := cfgDoc["provider"].(map[string]interface{})
		if provMap == nil {
			provMap = map[string]interface{}{}
		}
		provMap["openai"] = provEntry
		cfgDoc["provider"] = provMap
		return nil
	}); err != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: err.Error()}
	}

	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolCodex),
		ProviderID: "openai",
		Models:     models,
	}
}

// readCodexAuthBody reads the credential the Codex CLI currently holds.
// When the access token is a JWT with an "exp" claim, its real expiry (in ms)
// is forwarded so OpenCode does not treat expires==0 as already-expired-in-1970
// and immediately consume the shared refresh_token while the JWT is still valid.
// When the token is opaque (no JWT exp), expires stays 0 (unknown) rather than
// inventing a synthetic deadline.
func readCodexAuthBody(path string) map[string]interface{} {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	if apiKey := strings.TrimSpace(asString(doc["OPENAI_API_KEY"])); apiKey != "" {
		return map[string]interface{}{"type": "api", "key": apiKey}
	}
	tokens, ok := doc["tokens"].(map[string]interface{})
	if !ok {
		return nil
	}
	access := strings.TrimSpace(asString(tokens["access_token"]))
	refresh := strings.TrimSpace(asString(tokens["refresh_token"]))
	if access == "" || refresh == "" {
		return nil
	}
	expiresMs := parseJWTExpiryMs(access)
	if expiresMs == 0 {
		expiresMs = parseJWTExpiryMs(asString(tokens["id_token"]))
	}
	return map[string]interface{}{
		"type":      "oauth",
		"access":    access,
		"refresh":   refresh,
		"expires":   expiresMs,
		"accountId": strings.TrimSpace(asString(tokens["account_id"])),
	}
}

func parseJWTExpiryMs(token string) int64 {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[1] == "" {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return 0
	}
	return int64(claims.Exp * 1000)
}

// codexAuthFingerprint identifies a credential without the refresh token,
// which is only forwarded verbatim and must never be what we compare on.
func codexAuthFingerprint(body map[string]interface{}) string {
	if body == nil {
		return ""
	}
	return asString(body["type"]) + "\x00" + asString(body["key"]) + "\x00" + asString(body["access"]) + "\x00" + asString(body["accountId"])
}
