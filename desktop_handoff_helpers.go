package main

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
