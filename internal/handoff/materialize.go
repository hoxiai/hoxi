package handoff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// MaterializedTokens carries normalized token counts for an imported assistant turn.
type MaterializedTokens struct {
	Total      int `json:"total"`
	Context    int `json:"context,omitempty"`
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// MaterializedPart represents one canonical message part written into Hoxi's session store.
type MaterializedPart struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"` // "text" | "reasoning" | "tool"
	Text     string                 `json:"text,omitempty"`
	Tool     string                 `json:"tool,omitempty"`
	CallID   string                 `json:"callID,omitempty"`
	Title    string                 `json:"title,omitempty"`
	Status   string                 `json:"status,omitempty"`
	Input    map[string]interface{} `json:"input,omitempty"`
	Output   string                 `json:"output,omitempty"`
	StartMs  int64                  `json:"startMs,omitempty"`
	EndMs    int64                  `json:"endMs,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// MaterializedTurn represents one normalized user or assistant turn for direct Hoxi store import.
type MaterializedTurn struct {
	ID          string              `json:"id"`
	ParentID    string              `json:"parentID,omitempty"`
	Role        string              `json:"role"`
	CreatedMs   int64               `json:"createdMs"`
	CompletedMs int64               `json:"completedMs,omitempty"`
	ProviderID  string              `json:"providerID,omitempty"`
	ModelID     string              `json:"modelID,omitempty"`
	Mode        string              `json:"mode,omitempty"`
	Cost        float64             `json:"cost,omitempty"`
	Finish      string              `json:"finish,omitempty"`
	Tokens      *MaterializedTokens `json:"tokens,omitempty"`
	Parts       []MaterializedPart  `json:"parts"`
}

// NativeMaterializeParams describes a parsed external CLI session ready to persist in Hoxi's native store.
type NativeMaterializeParams struct {
	TargetDirectory string             `json:"targetDirectory"`
	Title           string             `json:"title"`
	ProviderID      string             `json:"providerID"`
	ModelID         string             `json:"modelID"`
	Source          string             `json:"source"`
	SourceSessionID string             `json:"sourceSessionID"`
	CreatedAt       int64              `json:"createdAt"`
	UpdatedAt       int64              `json:"updatedAt"`
	Turns           []MaterializedTurn `json:"turns"`
}

// NativeSessionMaterializer writes a normalized external session directly into Hoxi's session store.
type NativeSessionMaterializer func(ctx context.Context, params NativeMaterializeParams) (map[string]interface{}, error)

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

func buildMaterializedTurns(targetDir, providerID, modelID string, detail *ExternalSessionDetail) []MaterializedTurn {
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

	baseTs := time.Now().UnixMilli() - int64(len(turns)*2000)
	seq := 0
	lastUserMsgID := ""
	out := make([]MaterializedTurn, 0, len(turns)+1)

	for idx, turn := range turns {
		seq++
		ts := turn.Timestamp
		if ts <= 0 {
			ts = baseTs + int64(idx*1000)
		}
		msgID := makeAscendingOpenCodeID("msg", ts, seq)

		if turn.Role == "user" {
			lastUserMsgID = msgID
			seq++
			partID := makeAscendingOpenCodeID("prt", ts, seq)
			out = append(out, MaterializedTurn{
				ID:         msgID,
				Role:       "user",
				CreatedMs:  ts,
				ProviderID: providerID,
				ModelID:    modelID,
				Mode:       "build",
				Parts: []MaterializedPart{{
					ID:   partID,
					Type: "text",
					Text: turn.Text,
				}},
			})
			continue
		}

		if lastUserMsgID == "" {
			userMsgID := makeAscendingOpenCodeID("msg", ts-100, seq)
			lastUserMsgID = userMsgID
			seq++
			uPartID := makeAscendingOpenCodeID("prt", ts-90, seq)
			out = append(out, MaterializedTurn{
				ID:         userMsgID,
				Role:       "user",
				CreatedMs:  ts - 100,
				ProviderID: providerID,
				ModelID:    modelID,
				Mode:       "build",
				Parts: []MaterializedPart{{
					ID:   uPartID,
					Type: "text",
					Text: detail.Summary.Title,
				}},
			})
		}

		effModel := modelID
		if turn.ModelID != "" {
			if _, parsedMod := splitOpenCodeProviderModel(turn.ModelID); parsedMod != "" {
				effModel = parsedMod
			} else {
				effModel = turn.ModelID
			}
		}
		usage := turn.Usage
		var tok *MaterializedTokens
		if !usage.empty() {
			ctxTok := int(usage.totalContextTokens())
			tok = &MaterializedTokens{
				Total:      ctxTok,
				Context:    ctxTok,
				Input:      int(usage.Input),
				Output:     int(usage.Output),
				Reasoning:  int(usage.Reasoning),
				CacheRead:  int(usage.CacheRead),
				CacheWrite: int(usage.CacheWrite),
			}
		}

		var parts []MaterializedPart
		if strings.TrimSpace(turn.Reasoning) != "" {
			seq++
			rPartID := makeAscendingOpenCodeID("prt", ts+10, seq)
			parts = append(parts, MaterializedPart{
				ID:      rPartID,
				Type:    "reasoning",
				Text:    turn.Reasoning,
				StartMs: ts,
				EndMs:   ts + 200,
				Metadata: map[string]interface{}{
					"providerID": providerID,
					"modelID":    effModel,
				},
			})
		}

		for tIdx, tc := range turn.Tools {
			seq++
			tPartID := makeAscendingOpenCodeID("prt", ts+20+int64(tIdx), seq)
			callID := sanitizeToolCallID(tc.CallID, seq)
			inMap := tc.Input
			if inMap == nil {
				inMap = map[string]interface{}{}
			}
			title := firstNonEmpty(tc.Title, tc.Tool)
			status := strings.ToLower(strings.TrimSpace(tc.Status))
			outcome := "completed"
			if status == "error" || status == "failed" {
				status = "error"
				outcome = "failed"
			} else {
				status = "completed"
			}
			parts = append(parts, MaterializedPart{
				ID:      tPartID,
				Type:    "tool",
				Tool:    tc.Tool,
				CallID:  callID,
				Title:   title,
				Status:  status,
				Input:   inMap,
				Output:  tc.Output,
				StartMs: ts + 20 + int64(tIdx),
				EndMs:   ts + 150 + int64(tIdx),
				Metadata: map[string]interface{}{
					"imported":  true,
					"outcome":   outcome,
					"truncated": false,
				},
			})
		}

		replyText := strings.TrimSpace(turn.Text)
		if replyText == "" && len(turn.Tools) > 0 {
			replyText = fmt.Sprintf("Executed %d tool step(s).", len(turn.Tools))
		}
		if replyText != "" {
			seq++
			txtPartID := makeAscendingOpenCodeID("prt", ts+300, seq)
			parts = append(parts, MaterializedPart{
				ID:   txtPartID,
				Type: "text",
				Text: replyText,
			})
		}

		out = append(out, MaterializedTurn{
			ID:          msgID,
			ParentID:    lastUserMsgID,
			Role:        "assistant",
			CreatedMs:   ts,
			CompletedMs: ts + 500,
			ProviderID:  providerID,
			ModelID:     effModel,
			Mode:        "build",
			Cost:        usage.Cost,
			Finish:      "stop",
			Tokens:      tok,
			Parts:       parts,
		})
	}
	return out
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
	providerID, modelID := g.resolveDefaultHandoffModel(detail.Summary, req)
	g.ensureHandoffModelReady(detail.Summary.Source, providerID, modelID, detail)

	var sessionObj map[string]interface{}
	var newSessionID string

	if matFn := g.getNativeMaterializer(); matFn != nil {
		matTurns := buildMaterializedTurns(targetDir, providerID, modelID, detail)
		sessionObj, err = matFn(ctx, NativeMaterializeParams{
			TargetDirectory: targetDir,
			Title:           sessionTitle,
			ProviderID:      providerID,
			ModelID:         modelID,
			Source:          string(detail.Summary.Source),
			SourceSessionID: detail.Summary.ID,
			CreatedAt:       detail.Summary.CreatedAt,
			UpdatedAt:       detail.Summary.UpdatedAt,
			Turns:           matTurns,
		})
		if err != nil {
			return HandoffImportResponse{}, fmt.Errorf("create native hoxi session: %w", err)
		}
		newSessionID, _ = sessionObj["id"].(string)
		if newSessionID == "" {
			return HandoffImportResponse{}, fmt.Errorf("native materializer returned empty session id")
		}
	} else {
		sessionObj, err = g.createOpenCodeSessionInDir(ctx, targetDir, sessionTitle)
		if err != nil {
			return HandoffImportResponse{}, fmt.Errorf("create opencode session: %w", err)
		}
		newSessionID, _ = sessionObj["id"].(string)
		if newSessionID == "" {
			return HandoffImportResponse{}, fmt.Errorf("opencode returned empty session id")
		}
		if err := insertNormalizedTurnsToOpenCodeDB(newSessionID, targetDir, providerID, modelID, detail); err != nil {
			return HandoffImportResponse{}, fmt.Errorf("insert session history: %w", err)
		}
	}

	// Memory is Hoxi state and belongs under .hoxi/, never under the upstream
	// .opencode config root (which OpenCode would then populate with a plugin
	// dependency bundle inside the user's project).
	memoryRoot := hoxipaths.ProjectRootForWorktree(targetDir)
	if memoryRoot == "" {
		memoryRoot = targetDir
	}
	relMemPath := filepath.Join(hoxipaths.ProjectMemoryRelPath(), "sessions", newSessionID+".md")
	absMemPath := filepath.Join(memoryRoot, relMemPath)
	_ = writeEightDimHandoffMemory(absMemPath, newSessionID, memoryRoot, detail)

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
	if providerID == "" || providerID == "auto" {
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

	candidates := []string{modelID}
	if detail != nil && isNativeCLIProvider(source, providerID) {
		candidates = appendUniqueString(candidates, detail.Summary.Model)
		for _, t := range detail.Turns {
			candidates = appendUniqueString(candidates, t.ModelID)
		}
	}
	_ = g.updateHoxiConfigDoc(func(doc map[string]interface{}) error {
		provMap, _ := doc["provider"].(map[string]interface{})
		pObj, ok := provMap[providerID].(map[string]interface{})
		if !ok {
			return nil
		}
		modelsMap, _ := pObj["models"].(map[string]interface{})
		opts, _ := pObj["options"].(map[string]interface{})
		baseURL, _ := opts["baseURL"].(string)
		textOnly := strings.Contains(baseURL, "/api/hoxi/bridge/claude-code/v1") || strings.Contains(baseURL, "/api/hoxi/bridge/gemini-antigravity/v1")
		if modelsMap == nil {
			modelsMap = map[string]interface{}{}
		}
		for _, mID := range candidates {
			mID = strings.TrimSpace(mID)
			if mID != "" && !strings.ContainsAny(mID, " ()") && modelsMap[mID] == nil {
				modelsMap[mID] = map[string]interface{}{
					"name": mID, "tool_call": !textOnly, "reasoning": true, "attachment": true,
					"modalities": map[string]interface{}{"input": []string{"text", "image"}, "output": []string{"text"}},
					"limit":      map[string]interface{}{"context": 200000, "output": 32768},
				}
			}
		}
		pObj["models"] = modelsMap
		provMap[providerID] = pObj
		doc["provider"] = provMap
		return nil
	})
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

func isRuntimePlaceholderProvider(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	return lower == "native" || lower == "opencode" || lower == "oc"
}

func (g *Service) resolveDefaultHandoffModel(sum ExternalSessionSummary, req HandoffImportRequest) (string, string) {
	prefProv := strings.TrimSpace(req.PreferredProviderID)
	prefMod := strings.TrimSpace(req.PreferredModelID)
	if prefProv != "" && prefMod != "" && !isRuntimePlaceholderProvider(prefProv) {
		return prefProv, prefMod
	}
	if prefMod != "" && strings.Contains(prefMod, "/") {
		if p, m := splitOpenCodeProviderModel(prefMod); p != "" && m != "" {
			return p, m
		}
	}
	switch sum.Source {
	case ExternalToolGrok:
		return "grok-cli", firstNonEmpty(prefMod, sum.Model, loadGrokLocalCredentials().DefaultModel)
	case ExternalToolClaude:
		_, _, defModel, _ := readClaudeSettingsEnv()
		return "claude-code", firstNonEmpty(prefMod, sum.Model, defModel)
	case ExternalToolCodex:
		defModel, _, _ := readCodexLocalConfig()
		return "openai", firstNonEmpty(prefMod, sum.Model, defModel)
	case ExternalToolGemini:
		_, defModel, _ := readGeminiLocalKeyAndModels()
		return "google", firstNonEmpty(prefMod, sum.Model, defModel)
	case ExternalToolOpenCode:
		provID, modID := splitOpenCodeProviderModel(sum.Model)
		if provID != "" && modID != "" {
			return provID, modID
		}
		if g != nil {
			if cfgModel := asString(g.loadHoxiConfigDoc()["model"]); cfgModel != "" {
				if cp, cm := splitOpenCodeProviderModel(cfgModel); cp != "" && cm != "" {
					return cp, cm
				}
			}
		}
		return "auto", firstNonEmpty(prefMod, modID, sum.Model)
	default:
		return string(sum.Source), firstNonEmpty(prefMod, sum.Model, string(sum.Source))
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
	matTurns := buildMaterializedTurns(targetDir, providerID, modelID, detail)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := sqlitecli.WithImmediateTx(ctx, dbPath, 5*time.Second, func(ctx context.Context, conn *sql.Conn) error {
		msgStmt, err := conn.PrepareContext(ctx, `INSERT OR REPLACE INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?);`)
		if err != nil {
			return err
		}
		defer msgStmt.Close()

		partStmt, err := conn.PrepareContext(ctx, `INSERT OR REPLACE INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?);`)
		if err != nil {
			return err
		}
		defer partStmt.Close()

		execMsg := func(id string, created, updated int64, data map[string]interface{}) error {
			b, _ := json.Marshal(data)
			_, err := msgStmt.ExecContext(ctx, id, sessionID, created, updated, string(b))
			return err
		}
		execPart := func(id, msgID string, created, updated int64, data map[string]interface{}) error {
			b, _ := json.Marshal(data)
			_, err := partStmt.ExecContext(ctx, id, msgID, sessionID, created, updated, string(b))
			return err
		}

		for _, mt := range matTurns {
			if mt.Role == "user" {
				if err := execMsg(mt.ID, mt.CreatedMs, mt.CreatedMs, map[string]interface{}{
					"role":    "user",
					"time":    map[string]interface{}{"created": mt.CreatedMs},
					"agent":   "build",
					"model":   map[string]interface{}{"providerID": mt.ProviderID, "modelID": mt.ModelID},
					"summary": map[string]interface{}{"diffs": []interface{}{}},
				}); err != nil {
					return err
				}
				for _, p := range mt.Parts {
					if err := execPart(p.ID, mt.ID, mt.CreatedMs, mt.CreatedMs, map[string]interface{}{
						"type": "text",
						"text": p.Text,
					}); err != nil {
						return err
					}
				}
				continue
			}

			tok := mt.Tokens
			if tok == nil {
				tok = &MaterializedTokens{}
			}
			if err := execMsg(mt.ID, mt.CreatedMs, mt.CompletedMs, map[string]interface{}{
				"parentID": mt.ParentID,
				"role":     "assistant",
				"mode":     "build",
				"agent":    "build",
				"path":     map[string]interface{}{"cwd": targetDir, "root": targetDir},
				"cost":     mt.Cost,
				"tokens": map[string]interface{}{
					"total":     tok.Total,
					"input":     tok.Input,
					"output":    tok.Output,
					"reasoning": tok.Reasoning,
					"cache":     map[string]interface{}{"write": tok.CacheWrite, "read": tok.CacheRead},
				},
				"modelID":    mt.ModelID,
				"providerID": mt.ProviderID,
				"imported":   true,
				"time":       map[string]interface{}{"created": mt.CreatedMs, "completed": mt.CompletedMs},
				"finish":     firstNonEmpty(mt.Finish, "stop"),
			}); err != nil {
				return err
			}

			for _, p := range mt.Parts {
				switch p.Type {
				case "reasoning":
					if err := execPart(p.ID, mt.ID, p.StartMs+10, p.EndMs, map[string]interface{}{
						"type": "reasoning",
						"text": p.Text,
						"time": map[string]interface{}{"start": p.StartMs, "end": p.EndMs},
					}); err != nil {
						return err
					}
				case "tool":
					if err := execPart(p.ID, mt.ID, p.StartMs, p.EndMs, map[string]interface{}{
						"type":     "tool",
						"tool":     p.Tool,
						"callID":   p.CallID,
						"imported": true,
						"state": map[string]interface{}{
							"status":   p.Status,
							"input":    p.Input,
							"output":   p.Output,
							"title":    p.Title,
							"metadata": p.Metadata,
							"time":     map[string]interface{}{"start": p.StartMs, "end": p.EndMs},
						},
					}); err != nil {
						return err
					}
				case "text":
					if err := execPart(p.ID, mt.ID, mt.CreatedMs+300, mt.CreatedMs+400, map[string]interface{}{
						"type": "text",
						"text": p.Text,
					}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("sqlite3 error: %w", err)
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

	return hoxipaths.WriteFileAtomic(memPath, []byte(md), 0o600)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
