package handoff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func codexHomeDir() string {
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return filepath.Clean(env)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
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

	// Count total threads and fetch top 100 recent threads
	countOut, err := codexCommandOutput("sqlite3", dbPath, "SELECT count(*) FROM threads WHERE archived = 0;")
	if err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(countOut))); err == nil {
			stats.SessionCount = n
		}
	}

	sqlQuery := `SELECT id, rollout_path, created_at, updated_at, cwd, title, COALESCE(git_branch,''), COALESCE(git_sha,''), COALESCE(model,''), COALESCE(first_user_message,'')
FROM threads
WHERE archived = 0 AND title != 'Guardian review'
ORDER BY updated_at DESC
LIMIT 120;`

	rawJSON, err := codexCommandOutput("sqlite3", "-json", dbPath, sqlQuery)
	if err != nil || len(bytes.TrimSpace(rawJSON)) == 0 {
		return nil, stats
	}

	var rows []codexThreadRow
	if json.Unmarshal(rawJSON, &rows) != nil {
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
	safeID := strings.ReplaceAll(sessionID, "'", "''")
	sqlQuery := fmt.Sprintf(`SELECT id, rollout_path, created_at, updated_at, cwd, title, COALESCE(git_branch,''), COALESCE(git_sha,''), COALESCE(model,''), COALESCE(first_user_message,'')
FROM threads WHERE id = '%s' LIMIT 1;`, safeID)

	rawJSON, err := codexCommandOutput("sqlite3", "-json", dbPath, sqlQuery)
	if err != nil {
		return nil, err
	}
	var rows []codexThreadRow
	if json.Unmarshal(rawJSON, &rows) != nil || len(rows) == 0 {
		return nil, fmt.Errorf("codex session not found: %s", sessionID)
	}
	row := rows[0]
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
	cmd := exec.CommandContext(ctx, name, args...)
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
	toolIndexByID := map[string]*NormalizedToolCall{}

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
		if asString(item["type"]) != "response_item" {
			continue
		}
		payload, ok := item["payload"].(map[string]interface{})
		if !ok {
			continue
		}
		step++
		ts := baseTs + step*1000
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
				toolIndexByID[callID] = &last.Tools[len(last.Tools)-1]
			}

		case "function_call_output", "custom_tool_call_output":
			callID := asString(payload["call_id"])
			if tc, exists := toolIndexByID[callID]; exists {
				out := extractCodexMessageText(payload["output"])
				if out == "" {
					out = asString(payload["output"])
				}
				tc.Output = truncateRunes(out, 4000)
			}
		}
	}
	return turns
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

func detectCodexCLIStatus(g *desktopGateway, hoxiMap map[string]interface{}) LocalCLIStatusItem {
	cDir := codexHomeDir()
	sessions, stats := scanCodexSessions("", "")
	defaultModel, version, models := readCodexLocalConfig()
	for _, s := range sessions {
		if s.Model != "" {
			models = appendUniqueString(models, s.Model)
			if defaultModel == "" {
				defaultModel = s.Model
			}
		}
	}
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
	hasOpenAIKey := g.resolveStoredProviderKey("openai", "") != "" || inHoxi
	return LocalCLIStatusItem{
		ID:            ExternalToolCodex,
		Name:          "OpenAI Codex",
		Installed:     stats.Installed,
		Authenticated: accessToken != "" || apiKey != "",
		Connected:     hasOpenAIKey,
		Version:       version,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      authMode,
		ProviderID:    "openai",
	}
}
