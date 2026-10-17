package handoff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func readGeminiLocalKeyAndModels() (apiKey, defaultModel string, models []string) {
	apiKey = firstNonEmpty(
		strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		strings.TrimSpace(os.Getenv("GOOGLE_GENERATIVE_AI_API_KEY")),
		strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")),
	)
	gRoot := geminiRootDir()
	if gRoot != "" {
		if raw, err := os.ReadFile(filepath.Join(gRoot, "settings.json")); err == nil {
			var doc map[string]interface{}
			if json.Unmarshal(raw, &doc) == nil {
				if m := strings.TrimSpace(asString(doc["model"])); m != "" {
					defaultModel = m
					models = appendUniqueString(models, m)
				}
				if envMap, ok := doc["env"].(map[string]interface{}); ok && apiKey == "" {
					apiKey = firstNonEmpty(
						asString(envMap["GEMINI_API_KEY"]),
						asString(envMap["GOOGLE_GENERATIVE_AI_API_KEY"]),
					)
				}
			}
		}
	}
	return apiKey, defaultModel, models
}

func detectGeminiCLIStatus(g *desktopGateway, hoxiMap map[string]interface{}) LocalCLIStatusItem {
	_, stats := scanGeminiAntigravitySessions("", "")
	apiKey, defaultModel, models := readGeminiLocalKeyAndModels()
	_, inHoxi := hoxiMap["google"]
	hasGoogleKey := g.resolveStoredProviderKey("google", "") != "" || inHoxi
	hasOAuth := false
	if gRoot := geminiRootDir(); gRoot != "" {
		if _, err := os.Stat(filepath.Join(gRoot, "jetski-standalone-oauth-token")); err == nil {
			hasOAuth = true
		}
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolGemini,
		Name:          "Antigravity / Gemini",
		Installed:     stats.Installed,
		Authenticated: apiKey != "" || hasGoogleKey || hasOAuth || stats.SessionCount > 0,
		Connected:     hasGoogleKey,
		DefaultModel:  defaultModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "antigravity",
		ProviderID:    "google",
	}
}

func (g *desktopGateway) importGeminiCLIProvider() LocalCLIImportResponse {
	apiKey, _, models := readGeminiLocalKeyAndModels()
	if apiKey == "" {
		apiKey = g.resolveStoredProviderKey("google", "")
	}
	if apiKey == "" {
		return LocalCLIImportResponse{
			OK:     false,
			ToolID: string(ExternalToolGemini),
			Error:  "No GEMINI_API_KEY or GOOGLE_GENERATIVE_AI_API_KEY found in environment or ~/.gemini/settings.json",
		}
	}
	if g.openCodePort > 0 {
		b, _ := json.Marshal(map[string]string{"type": "api", "key": apiKey})
		u := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/auth/google"
		if req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(b)); err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.SetBasicAuth("opencode", g.password)
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
			if resp, err := client.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
		g.reloadOpenCodeGlobalConfig()
	}
	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolGemini),
		ProviderID: "google",
		Models:     models,
	}
}

func writeEightDimHandoffMemory(memPath, newSessionID, targetDir string, detail *ExternalSessionDetail) error {
	if err := os.MkdirAll(filepath.Dir(memPath), 0o755); err != nil {
		return err
	}

	var firstUserGoal string
	var lastAssistantText string
	for _, t := range detail.Turns {
		if t.Role == "user" && firstUserGoal == "" && strings.TrimSpace(t.Text) != "" {
			firstUserGoal = strings.TrimSpace(t.Text)
		}
		if t.Role == "assistant" && strings.TrimSpace(t.Text) != "" {
			lastAssistantText = strings.TrimSpace(t.Text)
		}
	}
	if firstUserGoal == "" {
		firstUserGoal = detail.Summary.Title
	}

	var changedLines []string
	for _, f := range detail.ChangedFiles {
		changedLines = append(changedLines, "- `"+f+"`")
	}
	if len(changedLines) == 0 {
		changedLines = append(changedLines, "- (No explicit file edits recorded in tool calls)")
	}

	var todoLines []string
	for _, step := range detail.PlanSteps {
		todoLines = append(todoLines, fmt.Sprintf("- [%s] %s", step.Status, step.Title))
	}
	if len(todoLines) == 0 {
		todoLines = append(todoLines, "- Continue from the latest turn in this handed-off session")
	}

	keyFacts := firstNonEmpty(detail.Summary.Summary, detail.LastRecap, "Session imported from "+detail.Summary.SourceLabel)
	progress := firstNonEmpty(detail.LastTurnSummary, truncateRunes(lastAssistantText, 600), "Imported "+strconv.Itoa(len(detail.Turns))+" historical turns.")

	md := strings.Join([]string{
		fmt.Sprintf("# Session Handoff Memory (%s)", newSessionID),
		fmt.Sprintf("Updated: %s", time.Now().Format(time.RFC3339)),
		fmt.Sprintf("Handed off from: **%s** (Original Session ID: `%s`)", detail.Summary.SourceLabel, detail.Summary.ID),
		"",
		"## 1. Goals",
		truncateRunes(firstUserGoal, 800),
		"",
		"## 2. Constraints",
		fmt.Sprintf("- Target Workspace Directory: `%s`", targetDir),
		fmt.Sprintf("- Source Git Branch: `%s` (Commit: `%s`)", firstNonEmpty(detail.Summary.GitBranch, "HEAD"), firstNonEmpty(detail.Summary.GitCommit, "n/a")),
		"- Maintain continuity with the imported conversation history and existing codebase changes.",
		"",
		"## 3. Key Facts",
		keyFacts,
		"",
		"## 4. Changed Files",
		strings.Join(changedLines, "\n"),
		"",
		"## 5. Errors & Fixes",
		"- Preserved in imported tool call outputs in session timeline.",
		"",
		"## 6. Todo",
		strings.Join(todoLines, "\n"),
		"",
		"## 7. Progress",
		progress,
		"",
		"## 8. Next Steps",
		"- Review the latest user instruction and immediately continue the task from where the previous CLI session paused.",
		"",
	}, "\n")

	return os.WriteFile(memPath, []byte(md), 0o644)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractClaudeAssistantContent(content interface{}) (string, string, []NormalizedToolCall, []ExternalPlanStep) {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v), "", nil, nil
	case []interface{}:
		var texts, thoughts []string
		var calls []NormalizedToolCall
		var plans []ExternalPlanStep
		for _, elem := range v {
			m, ok := elem.(map[string]interface{})
			if !ok {
				continue
			}
			switch asString(m["type"]) {
			case "text":
				if txt := strings.TrimSpace(asString(m["text"])); txt != "" {
					texts = append(texts, txt)
				}
			case "thinking":
				if th := strings.TrimSpace(asString(m["thinking"])); th != "" {
					thoughts = append(thoughts, th)
				}
			case "tool_use":
				rawName := asString(m["name"])
				inputMap, _ := m["input"].(map[string]interface{})
				if inputMap == nil {
					inputMap = map[string]interface{}{}
				}
				if strings.Contains(rawName, "update_plan") || rawName == "TodoWrite" {
					if items, ok := inputMap["items"].([]interface{}); ok {
						for _, it := range items {
							if im, ok := it.(map[string]interface{}); ok {
								plans = append(plans, ExternalPlanStep{
									Title:  asString(im["content"]),
									Status: asString(im["status"]),
								})
							}
						}
					}
				}
				calls = append(calls, NormalizedToolCall{
					CallID: asString(m["id"]),
					Tool:   normalizeExternalToolName(rawName),
					Title:  rawName,
					Input:  inputMap,
					Status: "completed",
				})
			}
		}
		return strings.Join(texts, "\n\n"), strings.Join(thoughts, "\n\n"), calls, plans
	default:
		return "", "", nil, nil
	}
}

func (g *desktopGateway) importCodexCLIProvider() LocalCLIImportResponse {
	cDir := codexHomeDir()
	if cDir == "" {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "~/.codex not found"}
	}
	raw, err := os.ReadFile(filepath.Join(cDir, "auth.json"))
	if err != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "~/.codex/auth.json not found"}
	}
	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "Invalid ~/.codex/auth.json"}
	}

	var authBody map[string]interface{}
	if apiKey := strings.TrimSpace(asString(doc["OPENAI_API_KEY"])); apiKey != "" {
		authBody = map[string]interface{}{"type": "api", "key": apiKey}
	} else if tokens, ok := doc["tokens"].(map[string]interface{}); ok {
		access := asString(tokens["access_token"])
		refresh := asString(tokens["refresh_token"])
		accountID := asString(tokens["account_id"])
		if access != "" && refresh != "" {
			authBody = map[string]interface{}{
				"type":      "oauth",
				"access":    access,
				"refresh":   refresh,
				"expires":   time.Now().Add(24 * time.Hour).UnixMilli(),
				"accountId": accountID,
			}
		}
	}
	if authBody == nil {
		return LocalCLIImportResponse{OK: false, ToolID: string(ExternalToolCodex), Error: "No valid tokens in ~/.codex/auth.json"}
	}

	if g.openCodePort > 0 {
		b, _ := json.Marshal(authBody)
		u := "http://127.0.0.1:" + strconv.Itoa(g.openCodePort) + "/auth/openai"
		req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.SetBasicAuth("opencode", g.password)
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
			if resp, err := client.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
		g.reloadOpenCodeGlobalConfig()
	}

	_, _, models := readCodexLocalConfig()
	return LocalCLIImportResponse{
		OK:         true,
		ToolID:     string(ExternalToolCodex),
		ProviderID: "openai",
		Models:     models,
	}
}
