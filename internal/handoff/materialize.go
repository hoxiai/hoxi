package handoff

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func makeAscendingOpenCodeID(prefix string, tsMs int64, seq int) string {
	if tsMs <= 0 {
		tsMs = time.Now().UnixMilli()
	}
	// Combine timestamp and sequence into a strictly increasing 48-bit integer (12 hex chars)
	combined := ((uint64(tsMs) & 0x1ffffffffff) << 7) | uint64(seq&0x7f)
	combined &= 0xffffffffffff

	randBuf := make([]byte, 14)
	_, _ = rand.Read(randBuf)
	var suffix strings.Builder
	for _, b := range randBuf {
		suffix.WriteByte(base62Alphabet[int(b)%len(base62Alphabet)])
	}
	return fmt.Sprintf("%s_%012x%s", prefix, combined, suffix.String())
}

func sanitizeToolCallID(raw string, seq int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Sprintf("call_handoff_%d", seq)
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func sqlEscapeLiteral(s string) string {
	cleaned := strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(cleaned, "'", "''") + "'"
}

func (g *Service) materializeExternalSession(ctx context.Context, req HandoffImportRequest) (HandoffImportResponse, error) {
	detail, err := loadExternalSessionDetail(req.Source, req.SessionID, req.SourceCWD, g.rootDir)
	if err != nil {
		return HandoffImportResponse{}, err
	}

	targetDir := cleanDirPath(req.TargetDirectory)
	if targetDir == "" || !dirExists(targetDir) {
		if detail.Summary.CWD != "" && dirExists(detail.Summary.CWD) {
			targetDir = cleanDirPath(detail.Summary.CWD)
		} else {
			targetDir = cleanDirPath(g.rootDir)
		}
	}
	if targetDir == "" {
		targetDir, _ = os.Getwd()
	}

	sessionTitle := fmt.Sprintf("[%s] %s", detail.Summary.SourceLabel, detail.Summary.Title)
	sessionObj, err := g.createOpenCodeSessionInDir(ctx, targetDir, sessionTitle)
	if err != nil {
		return HandoffImportResponse{}, fmt.Errorf("create opencode session: %w", err)
	}

	newSessionID, _ := sessionObj["id"].(string)
	if newSessionID == "" {
		return HandoffImportResponse{}, fmt.Errorf("opencode returned empty session id")
	}

	providerID, modelID := g.resolveDefaultHandoffModel(detail.Summary, req)
	g.ensureHandoffModelReady(detail.Summary.Source, providerID, modelID, detail)
	if err := insertNormalizedTurnsToOpenCodeDB(newSessionID, targetDir, providerID, modelID, detail); err != nil {
		return HandoffImportResponse{}, fmt.Errorf("insert session history: %w", err)
	}

	relMemPath := filepath.Join(".opencode", "memory", "sessions", newSessionID+".md")
	absMemPath := filepath.Join(targetDir, relMemPath)
	_ = writeEightDimHandoffMemory(absMemPath, newSessionID, targetDir, detail)

	return HandoffImportResponse{
		OK:                 true,
		Session:            sessionObj,
		ImportedTurns:      len(detail.Turns),
		MemoryPath:         relMemPath,
		TargetDirectory:    targetDir,
		ResolvedProviderID: providerID,
		ResolvedModelID:    modelID,
	}, nil
}

func (g *Service) ensureHandoffModelReady(source ExternalToolID, providerID, modelID string, detail *ExternalSessionDetail) {
	if providerID == "" {
		return
	}
	provMap, _ := g.loadHoxiConfigDoc()["provider"].(map[string]interface{})
	existingProv, hasProv := provMap[providerID].(map[string]interface{})
	existingOpts, _ := existingProv["options"].(map[string]interface{})
	existingBaseURL, _ := existingOpts["baseURL"].(string)
	hasDeprecatedBridge := strings.Contains(existingBaseURL, "/api/hoxi/bridge/claude-code/v1") ||
		strings.Contains(existingBaseURL, "/api/hoxi/bridge/gemini-antigravity/v1")
	switch {
	case providerID == "claude-code" && (!hasProv || hasDeprecatedBridge):
		_ = g.importClaudeCLIProvider()
	case providerID == "grok-cli" && !hasProv:
		_ = g.importGrokCLIProvider()
	case providerID == "google" && (!hasProv || hasDeprecatedBridge):
		_ = g.importGeminiCLIProvider()
	case providerID == "openai" && !hasProv:
		_ = g.importCodexCLIProvider()
	}

	doc := g.loadHoxiConfigDoc()
	provMap, _ = doc["provider"].(map[string]interface{})
	pObj, ok := provMap[providerID].(map[string]interface{})
	if !ok {
		return
	}
	modelsMap, _ := pObj["models"].(map[string]interface{})
	opts, _ := pObj["options"].(map[string]interface{})
	baseURL, _ := opts["baseURL"].(string)
	textOnly := strings.Contains(baseURL, "/api/hoxi/bridge/claude-code/v1") || strings.Contains(baseURL, "/api/hoxi/bridge/gemini-antigravity/v1")
	if modelsMap == nil {
		modelsMap = map[string]interface{}{}
	}
	candidates := []string{modelID}
	if detail != nil && isNativeCLIProvider(source, providerID) {
		candidates = appendUniqueString(candidates, detail.Summary.Model)
		for _, t := range detail.Turns {
			candidates = appendUniqueString(candidates, t.ModelID)
		}
	}
	changed := false
	for _, mID := range candidates {
		mID = strings.TrimSpace(mID)
		if mID != "" && !strings.ContainsAny(mID, " ()") && modelsMap[mID] == nil {
			modelsMap[mID] = map[string]interface{}{
				"name": mID, "tool_call": !textOnly, "reasoning": true, "attachment": true,
				"modalities": map[string]interface{}{"input": []string{"text", "image"}, "output": []string{"text"}},
				"limit":      map[string]interface{}{"context": 200000, "output": 32768},
			}
			changed = true
		}
	}
	if changed {
		pObj["models"] = modelsMap
		provMap[providerID] = pObj
		doc["provider"] = provMap
		_ = g.persistHoxiConfigDoc(doc)
	}
}

func isNativeCLIProvider(source ExternalToolID, providerID string) bool {
	return (source == ExternalToolGemini && providerID == "google") || (source == ExternalToolClaude && providerID == "claude-code") ||
		(source == ExternalToolCodex && providerID == "openai") || (source == ExternalToolGrok && providerID == "grok-cli")
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return p != "" && err == nil && st.IsDir()
}

func (g *Service) createOpenCodeSessionInDir(ctx context.Context, targetDir, title string) (map[string]interface{}, error) {
	if !g.openCode.Available() {
		return nil, fmt.Errorf("opencode daemon is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var sessionObj map[string]interface{}
	if err := g.openCode.JSON(ctx, http.MethodPost, "/session", url.Values{"directory": {targetDir}}, map[string]string{"title": title}, &sessionObj); err != nil {
		return nil, err
	}
	return sessionObj, nil
}

func (g *Service) resolveDefaultHandoffModel(sum ExternalSessionSummary, req HandoffImportRequest) (string, string) {
	if strings.TrimSpace(req.PreferredProviderID) != "" && strings.TrimSpace(req.PreferredModelID) != "" {
		return strings.TrimSpace(req.PreferredProviderID), strings.TrimSpace(req.PreferredModelID)
	}
	switch sum.Source {
	case ExternalToolGrok:
		return "grok-cli", firstNonEmpty(sum.Model, loadGrokLocalCredentials().DefaultModel)
	case ExternalToolClaude:
		_, _, defModel, _ := readClaudeSettingsEnv()
		return "claude-code", firstNonEmpty(sum.Model, defModel)
	case ExternalToolCodex:
		defModel, _, _ := readCodexLocalConfig()
		return "openai", firstNonEmpty(sum.Model, defModel)
	case ExternalToolGemini:
		_, defModel, _ := readGeminiLocalKeyAndModels()
		return "google", firstNonEmpty(sum.Model, defModel)
	case ExternalToolOpenCode:
		provID, modID := splitOpenCodeProviderModel(sum.Model)
		if provID != "" && modID != "" {
			return provID, modID
		}
		return "opencode", firstNonEmpty(modID, sum.Model, "opencode")
	default:
		return string(sum.Source), firstNonEmpty(sum.Model, string(sum.Source))
	}
}

func insertNormalizedTurnsToOpenCodeDB(
	sessionID, targetDir, providerID, modelID string,
	detail *ExternalSessionDetail,
) error {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return fmt.Errorf("opencode.db not found")
	}
	turns := detail.Turns
	if len(turns) == 0 {
		turns = []NormalizedTurn{
			{
				Role:      "user",
				Timestamp: detail.Summary.CreatedAt,
				Text:      detail.Summary.Title,
			},
			{
				Role:      "assistant",
				Timestamp: detail.Summary.UpdatedAt,
				Text:      firstNonEmpty(detail.Summary.Summary, detail.LastTurnSummary, "Session imported into Hoxi."),
			},
		}
	}

	var sqlBuf strings.Builder
	sqlBuf.WriteString("PRAGMA busy_timeout = 5000;\nBEGIN TRANSACTION;\n")

	baseTs := time.Now().UnixMilli() - int64(len(turns)*2000)
	seq := 0
	lastUserMsgID := ""

	for idx, turn := range turns {
		seq++
		ts := baseTs + int64(idx*1000)
		msgID := makeAscendingOpenCodeID("msg", ts, seq)

		if turn.Role == "user" {
			lastUserMsgID = msgID
			msgData := map[string]interface{}{
				"role":  "user",
				"time":  map[string]interface{}{"created": ts},
				"agent": "build",
				"model": map[string]interface{}{
					"providerID": providerID,
					"modelID":    modelID,
				},
				"summary": map[string]interface{}{"diffs": []interface{}{}},
			}
			msgBytes, _ := json.Marshal(msgData)
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO message (id, session_id, time_created, time_updated, data) VALUES (%s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(msgID),
				sqlEscapeLiteral(sessionID),
				ts,
				ts,
				sqlEscapeLiteral(string(msgBytes)),
			))

			seq++
			partID := makeAscendingOpenCodeID("prt", ts, seq)
			partData := map[string]interface{}{
				"type": "text",
				"text": turn.Text,
			}
			partBytes, _ := json.Marshal(partData)
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (%s, %s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(partID),
				sqlEscapeLiteral(msgID),
				sqlEscapeLiteral(sessionID),
				ts,
				ts,
				sqlEscapeLiteral(string(partBytes)),
			))
			continue
		}

		// Assistant turn
		if lastUserMsgID == "" {
			// Ensure every assistant message has a valid parent user message
			userMsgID := makeAscendingOpenCodeID("msg", ts-100, seq)
			lastUserMsgID = userMsgID
			uData, _ := json.Marshal(map[string]interface{}{
				"role":    "user",
				"time":    map[string]interface{}{"created": ts - 100},
				"agent":   "build",
				"model":   map[string]interface{}{"providerID": providerID, "modelID": modelID},
				"summary": map[string]interface{}{"diffs": []interface{}{}},
			})
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO message (id, session_id, time_created, time_updated, data) VALUES (%s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(userMsgID),
				sqlEscapeLiteral(sessionID),
				ts-100,
				ts-100,
				sqlEscapeLiteral(string(uData)),
			))
			seq++
			uPartID := makeAscendingOpenCodeID("prt", ts-90, seq)
			uPartBytes, _ := json.Marshal(map[string]interface{}{
				"type": "text",
				"text": detail.Summary.Title,
			})
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (%s, %s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(uPartID),
				sqlEscapeLiteral(userMsgID),
				sqlEscapeLiteral(sessionID),
				ts-90,
				ts-90,
				sqlEscapeLiteral(string(uPartBytes)),
			))
		}

		effModel := modelID
		if turn.ModelID != "" {
			effModel = turn.ModelID
		}
		msgData := map[string]interface{}{
			"parentID":   lastUserMsgID,
			"role":       "assistant",
			"mode":       "build",
			"agent":      "build",
			"path":       map[string]interface{}{"cwd": targetDir, "root": targetDir},
			"cost":       0,
			"tokens":     map[string]interface{}{"total": 0, "input": 0, "output": 0, "reasoning": 0, "cache": map[string]interface{}{"write": 0, "read": 0}},
			"modelID":    effModel,
			"providerID": providerID,
			"time":       map[string]interface{}{"created": ts, "completed": ts + 500},
			"finish":     "stop",
		}
		msgBytes, _ := json.Marshal(msgData)
		sqlBuf.WriteString(fmt.Sprintf(
			"INSERT OR REPLACE INTO message (id, session_id, time_created, time_updated, data) VALUES (%s, %s, %d, %d, %s);\n",
			sqlEscapeLiteral(msgID),
			sqlEscapeLiteral(sessionID),
			ts,
			ts+500,
			sqlEscapeLiteral(string(msgBytes)),
		))

		if strings.TrimSpace(turn.Reasoning) != "" {
			seq++
			rPartID := makeAscendingOpenCodeID("prt", ts+10, seq)
			rData, _ := json.Marshal(map[string]interface{}{
				"type": "reasoning",
				"text": turn.Reasoning,
				"time": map[string]interface{}{"start": ts, "end": ts + 200},
			})
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (%s, %s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(rPartID),
				sqlEscapeLiteral(msgID),
				sqlEscapeLiteral(sessionID),
				ts+10,
				ts+200,
				sqlEscapeLiteral(string(rData)),
			))
		}

		for tIdx, tc := range turn.Tools {
			seq++
			tPartID := makeAscendingOpenCodeID("prt", ts+20+int64(tIdx), seq)
			callID := sanitizeToolCallID(tc.CallID, seq)
			inMap := tc.Input
			if inMap == nil {
				inMap = map[string]interface{}{}
			}
			title := tc.Title
			if title == "" {
				title = tc.Tool
			}
			tData, _ := json.Marshal(map[string]interface{}{
				"type":   "tool",
				"tool":   tc.Tool,
				"callID": callID,
				"state": map[string]interface{}{
					"status":   "completed",
					"input":    inMap,
					"output":   tc.Output,
					"title":    title,
					"metadata": map[string]interface{}{"truncated": false},
					"time":     map[string]interface{}{"start": ts + 20, "end": ts + 150},
				},
			})
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (%s, %s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(tPartID),
				sqlEscapeLiteral(msgID),
				sqlEscapeLiteral(sessionID),
				ts+20+int64(tIdx),
				ts+150+int64(tIdx),
				sqlEscapeLiteral(string(tData)),
			))
		}

		replyText := strings.TrimSpace(turn.Text)
		if replyText == "" && len(turn.Tools) > 0 {
			replyText = fmt.Sprintf("Executed %d tool step(s).", len(turn.Tools))
		}
		if replyText != "" {
			seq++
			txtPartID := makeAscendingOpenCodeID("prt", ts+300, seq)
			txtData, _ := json.Marshal(map[string]interface{}{
				"type": "text",
				"text": replyText,
			})
			sqlBuf.WriteString(fmt.Sprintf(
				"INSERT OR REPLACE INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (%s, %s, %s, %d, %d, %s);\n",
				sqlEscapeLiteral(txtPartID),
				sqlEscapeLiteral(msgID),
				sqlEscapeLiteral(sessionID),
				ts+300,
				ts+400,
				sqlEscapeLiteral(string(txtData)),
			))
		}
	}

	sqlBuf.WriteString("COMMIT;\n")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(sqlBuf.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3 error: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
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
