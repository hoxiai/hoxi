package handoff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

func qoderDotDirs() []string {
	if env := strings.TrimSpace(os.Getenv("QODER_HOME")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var out []string
	for _, sub := range []string{".qoder", ".qoder-cn"} {
		p := filepath.Join(home, sub)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func qoderAppSupportDirs() []string {
	if env := strings.TrimSpace(os.Getenv("QODER_DATA_DIR")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var candidates []string
	candidates = append(candidates,
		filepath.Join(home, "Library", "Application Support", "Qoder"),
		filepath.Join(home, "Library", "Application Support", "QoderCN"),
		filepath.Join(home, "Library", "Application Support", "Qoder CN"),
		filepath.Join(home, ".config", "Qoder"),
		filepath.Join(home, ".config", "QoderCN"),
	)
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates,
			filepath.Join(appData, "Qoder"),
			filepath.Join(appData, "QoderCN"),
		)
	}
	var out []string
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

type qoderDBSessionRow struct {
	SessionID          string `json:"session_id"`
	Title              string `json:"session_title"`
	ProjectURI         string `json:"project_uri"`
	ProjectName        string `json:"project_name"`
	CreatedAt          int64  `json:"gmt_create"`
	UpdatedAt          int64  `json:"gmt_modified"`
	PreferredModelInfo string `json:"preferred_model_info"`
}

type qoderSessionCandidate struct {
	summary        ExternalSessionSummary
	transcriptPath string
	cacheJSONLPath string
	localDBPath    string
}

func extractQoderModelFromJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var doc map[string]interface{}
	if json.Unmarshal([]byte(raw), &doc) == nil {
		if m := strings.TrimSpace(asString(doc["preferred_model"])); m != "" {
			return m
		}
		if m := strings.TrimSpace(asString(doc["model_key"])); m != "" {
			return m
		}
		if m := strings.TrimSpace(asString(doc["model"])); m != "" {
			return m
		}
	}
	return ""
}

func readQoderDefaultModel() string {
	for _, appDir := range qoderAppSupportDirs() {
		vscdb := filepath.Join(appDir, "User", "globalStorage", "state.vscdb")
		if _, err := os.Stat(vscdb); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", vscdb, "SELECT value FROM ItemTable WHERE key = 'chat.modelConfig.assistant' LIMIT 1;")
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		cancel()
		if err == nil {
			if m := strings.TrimSpace(string(out)); m != "" && !strings.ContainsAny(m, " {}") {
				return m
			}
		}
	}
	return ""
}

func resolveFullQoderUUID(shortID string, dotDirs []string, byID map[string]*qoderSessionCandidate) string {
	if len(shortID) > 12 {
		return shortID
	}
	prefix := shortID + "-"
	for fullID := range byID {
		if strings.HasPrefix(fullID, prefix) {
			return fullID
		}
	}
	for _, dDir := range dotDirs {
		entries, err := os.ReadDir(filepath.Join(dDir, "cache", "experts"))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				return e.Name()
			}
		}
	}
	return shortID
}

func collectQoderCandidates(targetDir string) (map[string]*qoderSessionCandidate, bool) {
	dotDirs := qoderDotDirs()
	appDirs := qoderAppSupportDirs()
	installed := len(dotDirs) > 0 || len(appDirs) > 0
	byID := map[string]*qoderSessionCandidate{}
	if !installed {
		return byID, false
	}

	defModel := readQoderDefaultModel()

	// 1. Scan SharedClientCache/cache/db/local.db chat_session rows
	for _, appDir := range appDirs {
		dbPath := filepath.Join(appDir, "SharedClientCache", "cache", "db", "local.db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		sqlQuery := `SELECT session_id, COALESCE(session_title,'') AS session_title, COALESCE(project_uri,'') AS project_uri, COALESCE(project_name,'') AS project_name, COALESCE(gmt_create,0) AS gmt_create, COALESCE(gmt_modified,0) AS gmt_modified, COALESCE(preferred_model_info,'') AS preferred_model_info
FROM chat_session
WHERE COALESCE(parent_session_id,'') = ''
ORDER BY gmt_modified DESC
LIMIT 120;`
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", "-json", dbPath, sqlQuery)
		cmd.WaitDelay = time.Second
		rawOut, err := cmd.Output()
		cancel()
		if err != nil || len(bytes.TrimSpace(rawOut)) == 0 {
			continue
		}
		var rows []qoderDBSessionRow
		if json.Unmarshal(rawOut, &rows) != nil {
			continue
		}
		for _, r := range rows {
			sID := strings.TrimSpace(r.SessionID)
			if sID == "" {
				continue
			}
			cwd := cleanDirPath(parseFileURIListFirstPath(r.ProjectURI))
			if cwd == "" {
				cwd = cleanDirPath(r.ProjectURI)
			}
			if strings.HasSuffix(cwd, ".code-workspace") {
				cwd = filepath.Dir(cwd)
			}
			model := firstNonEmpty(extractQoderModelFromJSON(r.PreferredModelInfo), defModel)
			title := strings.TrimSpace(r.Title)
			if title == "" {
				title = "Qoder Session " + sID[:minInt(8, len(sID))]
			}
			createdMs := r.CreatedAt
			updatedMs := r.UpdatedAt
			if updatedMs == 0 {
				updatedMs = createdMs
			}
			byID[sID] = &qoderSessionCandidate{
				localDBPath: dbPath,
				summary: ExternalSessionSummary{
					ID:          sID,
					Source:      ExternalToolQoder,
					SourceLabel: "Qoder",
					CWD:         cwd,
					Title:       title,
					Model:       model,
					CreatedAt:   createdMs,
					UpdatedAt:   updatedMs,
					MatchesCWD:  sameOrSubPath(cwd, targetDir),
				},
			}
		}
	}

	// 2. Scan ~/.qoder*/projects/<encoded-cwd>/transcript/<session-id>.jsonl
	for _, dDir := range dotDirs {
		projRoot := filepath.Join(dDir, "projects")
		projEntries, err := os.ReadDir(projRoot)
		if err != nil {
			continue
		}
		for _, pEntry := range projEntries {
			if !pEntry.IsDir() {
				continue
			}
			decodedCWD := decodeHyphenatedProjectPath(pEntry.Name(), targetDir)
			transDir := filepath.Join(projRoot, pEntry.Name(), "transcript")
			files, err := os.ReadDir(transDir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
					continue
				}
				fi, err := f.Info()
				if err != nil || fi.Size() < 16 {
					continue
				}
				sID := strings.TrimSuffix(f.Name(), ".jsonl")
				fullPath := filepath.Join(transDir, f.Name())
				sum, ok := quickParseClaudeSummary(fullPath, sID, fi.ModTime().UnixMilli(), targetDir)
				if !ok {
					sum = ExternalSessionSummary{
						ID:        sID,
						CWD:       decodedCWD,
						Title:     "Qoder Session " + sID[:minInt(8, len(sID))],
						CreatedAt: fi.ModTime().UnixMilli(),
						UpdatedAt: fi.ModTime().UnixMilli(),
					}
				}
				if sum.CWD == "" {
					sum.CWD = decodedCWD
				}
				sum.Source = ExternalToolQoder
				sum.SourceLabel = "Qoder"
				if sum.Model == "" {
					sum.Model = defModel
				}
				sum.MatchesCWD = sameOrSubPath(sum.CWD, targetDir)

				if existing, ok := byID[sID]; ok {
					existing.transcriptPath = fullPath
					if existing.summary.CWD == "" && sum.CWD != "" {
						existing.summary.CWD = sum.CWD
						existing.summary.MatchesCWD = sum.MatchesCWD
					}
					if existing.summary.Summary == "" {
						existing.summary.Summary = sum.Summary
					}
					if existing.summary.GitBranch == "" {
						existing.summary.GitBranch = sum.GitBranch
					}
					if sum.UpdatedAt > existing.summary.UpdatedAt {
						existing.summary.UpdatedAt = sum.UpdatedAt
					}
				} else {
					byID[sID] = &qoderSessionCandidate{
						summary:        sum,
						transcriptPath: fullPath,
					}
				}
			}
		}

		// 3. Scan ~/.qoder*/cache/projects/<proj-slug>/conversation-history/<id>/<id>.jsonl
		cacheProjRoot := filepath.Join(dDir, "cache", "projects")
		cProjs, err := os.ReadDir(cacheProjRoot)
		if err != nil {
			continue
		}
		for _, cp := range cProjs {
			if !cp.IsDir() {
				continue
			}
			histRoot := filepath.Join(cacheProjRoot, cp.Name(), "conversation-history")
			hDirs, err := os.ReadDir(histRoot)
			if err != nil {
				continue
			}
			for _, hd := range hDirs {
				if !hd.IsDir() {
					continue
				}
				shortID := hd.Name()
				jsonlPath := filepath.Join(histRoot, shortID, shortID+".jsonl")
				fi, err := os.Stat(jsonlPath)
				if err != nil || fi.Size() < 16 {
					continue
				}
				fullID := resolveFullQoderUUID(shortID, dotDirs, byID)
				firstPrompt, lastReply := quickParseQoderCacheJSONL(jsonlPath)
				if existing, ok := byID[fullID]; ok {
					existing.cacheJSONLPath = jsonlPath
					if existing.summary.Summary == "" {
						existing.summary.Summary = truncateRunes(firstNonEmpty(firstPrompt, lastReply), 240)
					}
					if strings.HasPrefix(existing.summary.Title, "Qoder Session ") && firstPrompt != "" {
						existing.summary.Title = truncateRunes(firstLineText(firstPrompt), 80)
					}
				} else {
					title := truncateRunes(firstLineText(firstPrompt), 80)
					if title == "" {
						title = "Qoder Session " + fullID[:minInt(8, len(fullID))]
					}
					byID[fullID] = &qoderSessionCandidate{
						cacheJSONLPath: jsonlPath,
						summary: ExternalSessionSummary{
							ID:          fullID,
							Source:      ExternalToolQoder,
							SourceLabel: "Qoder",
							Title:       title,
							Summary:     truncateRunes(firstNonEmpty(firstPrompt, lastReply), 240),
							Model:       defModel,
							CreatedAt:   fi.ModTime().UnixMilli(),
							UpdatedAt:   fi.ModTime().UnixMilli(),
						},
					}
				}
			}
		}
	}

	return byID, installed
}

func scanQoderSessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	byID, installed := collectQoderCandidates(targetDir)
	stats := ExternalSourceStats{
		Installed:    installed,
		SessionCount: len(byID),
	}
	if len(byID) == 0 {
		return nil, stats
	}

	qLower := strings.ToLower(strings.TrimSpace(query))
	results := make([]ExternalSessionSummary, 0, len(byID))
	for _, cand := range byID {
		sum := cand.summary
		if qLower != "" {
			haystack := strings.ToLower(sum.Title + " " + sum.Summary + " " + sum.CWD + " " + sum.GitBranch)
			if !strings.Contains(haystack, qLower) {
				continue
			}
		}
		results = append(results, sum)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].MatchesCWD != results[j].MatchesCWD {
			return results[i].MatchesCWD
		}
		return results[i].UpdatedAt > results[j].UpdatedAt
	})
	return results, stats
}

func quickParseQoderCacheJSONL(filePath string) (firstUser, lastAssistant string) {
	turns := parseQoderCacheJSONL(filePath, 0, "")
	for _, t := range turns {
		if t.Role == "user" && firstUser == "" && t.Text != "" {
			firstUser = t.Text
		}
		if t.Role == "assistant" && t.Text != "" {
			lastAssistant = t.Text
		}
	}
	return firstUser, lastAssistant
}

func parseQoderCacheJSONL(filePath string, baseTs int64, defaultModel string) []NormalizedTurn {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	if baseTs <= 0 {
		if fi, err := f.Stat(); err == nil {
			baseTs = fi.ModTime().UnixMilli()
		} else {
			baseTs = time.Now().UnixMilli()
		}
	}

	var turns []NormalizedTurn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
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
		role := strings.TrimSpace(asString(item["role"]))
		msgObj, _ := item["message"].(map[string]interface{})
		if role == "" && msgObj != nil {
			role = strings.TrimSpace(asString(msgObj["role"]))
		}
		if role != "user" && role != "assistant" {
			continue
		}
		var rawContent interface{}
		if msgObj != nil {
			rawContent = msgObj["content"]
		} else {
			rawContent = item["content"]
		}
		text, reasoning, calls, _ := extractClaudeAssistantContent(rawContent)
		if role == "user" {
			text = cleanUserQueryTags(text)
			if strings.HasPrefix(text, "<system-reminder>") || text == "" {
				continue
			}
		}
		if text == "" && reasoning == "" && len(calls) == 0 {
			continue
		}
		step++
		ts := baseTs + step*1000
		if role == "assistant" && len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
			last := &turns[len(turns)-1]
			if reasoning != "" {
				last.Reasoning = strings.TrimSpace(last.Reasoning + "\n\n" + reasoning)
			}
			if text != "" {
				last.Text = strings.TrimSpace(last.Text + "\n\n" + text)
			}
			last.Tools = append(last.Tools, calls...)
			continue
		}
		turns = append(turns, NormalizedTurn{
			Role:      role,
			Timestamp: ts,
			Text:      text,
			Reasoning: reasoning,
			Tools:     calls,
			ModelID:   defaultModel,
		})
	}
	return turns
}

func loadQoderProjectMemories(cwd, sessionID, localDBPath string) string {
	var sections []string

	// 1. Check SQLite agent_memory if localDBPath exists
	if localDBPath != "" && sessionID != "" {
		safeID := strings.ReplaceAll(strings.ReplaceAll(sessionID, "\x00", ""), "'", "''")
		sqlQuery := fmt.Sprintf("SELECT COALESCE(title,'') AS title, COALESCE(content,'') AS content FROM agent_memory WHERE session_id = '%s' ORDER BY gmt_modified DESC LIMIT 8;", safeID)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", "-json", localDBPath, sqlQuery)
		cmd.WaitDelay = time.Second
		rawOut, err := cmd.Output()
		cancel()
		if err == nil && len(bytes.TrimSpace(rawOut)) > 0 {
			var rows []struct {
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			if json.Unmarshal(rawOut, &rows) == nil {
				for _, r := range rows {
					if strings.TrimSpace(r.Content) != "" {
						sections = append(sections, strings.TrimSpace(r.Title+": "+r.Content))
					}
				}
			}
		}
	}

	// 2. Check ~/.qoder*/memories/*/projects/<encoded-cwd>/task_summary_experience/*.md
	if cwd != "" {
		wantHint := strings.Trim(encodeClaudeProjectHint(cwd), "-")
		for _, dDir := range qoderDotDirs() {
			memRoot := filepath.Join(dDir, "memories")
			uDirs, err := os.ReadDir(memRoot)
			if err != nil {
				continue
			}
			for _, u := range uDirs {
				if !u.IsDir() {
					continue
				}
				taskDir := filepath.Join(memRoot, u.Name(), "projects", wantHint, "task_summary_experience")
				files, err := os.ReadDir(taskDir)
				if err != nil {
					continue
				}
				for i, f := range files {
					if i >= 3 || f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
						continue
					}
					if raw, err := os.ReadFile(filepath.Join(taskDir, f.Name())); err == nil {
						sections = append(sections, truncateRunes(strings.TrimSpace(string(raw)), 500))
					}
				}
			}
		}
	}
	return strings.Join(sections, "\n\n")
}

func loadQoderSessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	targetDir := firstNonEmpty(hintCWD, activeRoot)
	byID, _ := collectQoderCandidates(targetDir)
	cand, ok := byID[sessionID]
	if !ok {
		for id, c := range byID {
			if strings.HasPrefix(id, sessionID+"-") || strings.HasPrefix(sessionID, id+"-") {
				cand = c
				ok = true
				break
			}
		}
	}
	if !ok || cand == nil {
		return nil, fmt.Errorf("qoder session not found: %s", sessionID)
	}

	var turns []NormalizedTurn
	var plans []ExternalPlanStep

	if cand.transcriptPath != "" {
		if d, err := parseClaudeJSONL(cand.transcriptPath, cand.summary.ID, activeRoot); err == nil && d != nil {
			turns = d.Turns
			plans = d.PlanSteps
			if cand.summary.CWD == "" && d.Summary.CWD != "" {
				cand.summary.CWD = d.Summary.CWD
			}
			if cand.summary.Model == "" && d.Summary.Model != "" {
				cand.summary.Model = d.Summary.Model
			}
		}
	}
	if len(turns) == 0 && cand.cacheJSONLPath != "" {
		turns = parseQoderCacheJSONL(cand.cacheJSONLPath, cand.summary.CreatedAt, cand.summary.Model)
	}

	recap := loadQoderProjectMemories(cand.summary.CWD, cand.summary.ID, cand.localDBPath)
	if len(turns) == 0 {
		turns = []NormalizedTurn{
			{
				Role:      "user",
				Timestamp: cand.summary.CreatedAt,
				Text:      cand.summary.Title,
			},
			{
				Role:      "assistant",
				Timestamp: cand.summary.UpdatedAt,
				Text:      firstNonEmpty(recap, cand.summary.Summary, "Imported Qoder session: "+cand.summary.Title),
				ModelID:   cand.summary.Model,
			},
		}
	}

	changedFiles := extractChangedFilesFromTurns(turns)
	var lastTurnSummary string
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" && turns[i].Text != "" {
			lastTurnSummary = truncateRunes(turns[i].Text, 360)
			break
		}
	}

	sum := cand.summary
	if sum.CWD == "" {
		sum.CWD = hintCWD
	}
	sum.TurnCount = len(turns)
	sum.MatchesCWD = sameOrSubPath(sum.CWD, activeRoot)

	return &ExternalSessionDetail{
		Summary:         sum,
		Turns:           turns,
		PlanSteps:       plans,
		ChangedFiles:    changedFiles,
		LastTurnSummary: lastTurnSummary,
		LastRecap:       truncateRunes(recap, 1200),
	}, nil
}

func detectQoderCLIStatus(hoxiMap map[string]interface{}) LocalCLIStatusItem {
	sessions, stats := scanQoderSessions("", "")
	bin := agentcli.ResolveQoderBinary()
	var models []string
	var defModel string
	for _, m := range agentcli.DiscoverQoderModels() {
		if m.ID != "" {
			models = appendUniqueString(models, m.ID)
			if defModel == "" {
				defModel = m.ID
			}
		}
	}
	if dm := readQoderDefaultModel(); dm != "" {
		models = appendUniqueString(models, dm)
		if defModel == "" {
			defModel = dm
		}
	}
	for _, s := range sessions {
		if s.Model != "" {
			models = appendUniqueString(models, s.Model)
			if defModel == "" {
				defModel = s.Model
			}
		}
	}
	_, customConn := hoxiMap["qoder"]
	authMode := "session-handoff"
	if bin != "" {
		authMode = "qoder-runner"
	} else if customConn {
		authMode = "hoxi-custom"
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolQoder,
		Name:          "Qoder",
		Installed:     stats.Installed || bin != "",
		Authenticated: bin != "" || customConn,
		Connected:     bin != "" || customConn,
		DefaultModel:  defModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      authMode,
		ProviderID:    "qoder",
	}
}
