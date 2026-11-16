package handoff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
)

func traeDotDirs() []string {
	if env := strings.TrimSpace(os.Getenv("TRAE_HOME")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var out []string
	for _, sub := range []string{".trae", ".trae-cn"} {
		p := filepath.Join(home, sub)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func traeAppSupportDirs() []string {
	if env := strings.TrimSpace(os.Getenv("TRAE_DATA_DIR")); env != "" {
		return []string{filepath.Clean(env)}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Library", "Application Support", "Trae CN"),
		filepath.Join(home, "Library", "Application Support", "Trae"),
		filepath.Join(home, ".config", "Trae CN"),
		filepath.Join(home, ".config", "Trae"),
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates,
			filepath.Join(appData, "Trae CN"),
			filepath.Join(appData, "Trae"),
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

func cleanTraeModelID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, prefix := range []string{"solo_agent_1__", "solo_coder_1__", "solo_agent__", "solo_coder__", "1_-_", "custom_responses_compatible//"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimSuffix(s, "_null")
	return strings.TrimSpace(s)
}

func readTraeSessionModels() (bySession map[string]string, defaultModel string) {
	bySession = map[string]string{}
	for _, appDir := range traeAppSupportDirs() {
		vscdb := filepath.Join(appDir, "User", "globalStorage", "state.vscdb")
		if _, err := os.Stat(vscdb); err != nil {
			continue
		}
		sqlQuery := `SELECT key, value FROM ItemTable WHERE key LIKE '%AI.agent.model.session_selected_model' OR key LIKE '%AI.agent.model.recent_user_selection_by_agent_label' OR key LIKE '%ai-chat:sessionRelation:globalModelMap' LIMIT 20;`
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 3000", "-json", vscdb, sqlQuery)
		cmd.WaitDelay = time.Second
		rawOut, err := cmd.Output()
		cancel()
		if err != nil || len(bytes.TrimSpace(rawOut)) == 0 {
			continue
		}
		var rows []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if json.Unmarshal(rawOut, &rows) != nil {
			continue
		}
		for _, r := range rows {
			switch {
			case strings.HasSuffix(r.Key, "AI.agent.model.session_selected_model"):
				var sessMap map[string]map[string]struct {
					ModelID string `json:"modelId"`
				}
				if json.Unmarshal([]byte(r.Value), &sessMap) == nil {
					for sID, agentMap := range sessMap {
						for _, entry := range agentMap {
							if m := cleanTraeModelID(entry.ModelID); m != "" {
								bySession[sID] = m
								if defaultModel == "" {
									defaultModel = m
								}
							}
						}
					}
				}
			case strings.HasSuffix(r.Key, "recent_user_selection_by_agent_label"):
				var agentMap map[string]struct {
					ModelID string `json:"modelId"`
				}
				if json.Unmarshal([]byte(r.Value), &agentMap) == nil {
					for _, entry := range agentMap {
						if m := cleanTraeModelID(entry.ModelID); m != "" && defaultModel == "" {
							defaultModel = m
						}
					}
				}
			case strings.HasSuffix(r.Key, "globalModelMap"):
				var gMap map[string]string
				if json.Unmarshal([]byte(r.Value), &gMap) == nil {
					for _, rawM := range gMap {
						if m := cleanTraeModelID(rawM); m != "" && defaultModel == "" {
							defaultModel = m
						}
					}
				}
			}
		}
	}
	return bySession, defaultModel
}

type traeTopicEntry struct {
	SessionID string
	Timestamp int64
	Text      string
}

type traeSessionCandidate struct {
	summary           ExternalSessionSummary
	memoryFiles       []string
	topics            []traeTopicEntry
	projectMemoryPath string
}

type traeMemoryLine struct {
	Intent             string   `json:"intent"`
	Actions            []string `json:"actions"`
	Outcome            string   `json:"outcome"`
	Learned            []string `json:"learned"`
	MessageSummaryTime string   `json:"message_summary_time"`
	MessageID          string   `json:"message_id"`
	CompactSummaryMeta struct {
		CreatedAtMs int64 `json:"created_at_ms"`
	} `json:"compact_summary_meta"`
}

func parseTraeTopicsFile(filePath string) []traeTopicEntry {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []traeTopicEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "[session_id:") {
			continue
		}
		closeIdx := strings.Index(line, "]")
		if closeIdx < 0 {
			continue
		}
		header := line[1:closeIdx]
		body := strings.TrimSpace(line[closeIdx+1:])
		if body == "" {
			continue
		}
		var sID, timeStr string
		for _, part := range strings.Split(header, "|") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "session_id:") {
				sID = strings.TrimSpace(strings.TrimPrefix(part, "session_id:"))
			} else if strings.HasPrefix(part, "topic_summary_time:") {
				timeStr = strings.TrimSpace(strings.TrimPrefix(part, "topic_summary_time:"))
			}
		}
		if sID != "" {
			out = append(out, traeTopicEntry{
				SessionID: sID,
				Timestamp: parseTimeMs(timeStr),
				Text:      body,
			})
		}
	}
	return out
}

func quickInspectTraeMemoryJSONL(filePath string) (firstIntent, lastOutcome string, createdMs, updatedMs int64) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec traeMemoryLine
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if firstIntent == "" && strings.TrimSpace(rec.Intent) != "" {
			firstIntent = strings.TrimSpace(rec.Intent)
		}
		if strings.TrimSpace(rec.Outcome) != "" {
			lastOutcome = strings.TrimSpace(rec.Outcome)
		}
		ts := rec.CompactSummaryMeta.CreatedAtMs
		if ts == 0 {
			ts = parseTimeMs(rec.MessageSummaryTime)
		}
		if ts > 0 {
			if createdMs == 0 || ts < createdMs {
				createdMs = ts
			}
			if ts > updatedMs {
				updatedMs = ts
			}
		}
	}
	return firstIntent, lastOutcome, createdMs, updatedMs
}

func collectTraeCandidates(targetDir string) (map[string]*traeSessionCandidate, bool) {
	dotDirs := traeDotDirs()
	appDirs := traeAppSupportDirs()
	installed := len(dotDirs) > 0 || len(appDirs) > 0
	byID := map[string]*traeSessionCandidate{}
	if !installed {
		return byID, false
	}

	modelBySession, defaultModel := readTraeSessionModels()

	for _, dDir := range dotDirs {
		projRoot := filepath.Join(dDir, "memory", "projects")
		projDirs, err := os.ReadDir(projRoot)
		if err != nil {
			continue
		}
		for _, pEntry := range projDirs {
			if !pEntry.IsDir() {
				continue
			}
			pPath := filepath.Join(projRoot, pEntry.Name())
			cwd := decodeHyphenatedProjectPath(pEntry.Name(), targetDir)
			projMemPath := filepath.Join(pPath, "project_memory.md")
			if _, err := os.Stat(projMemPath); err != nil {
				projMemPath = ""
			}

			dateDirs, err := os.ReadDir(pPath)
			if err != nil {
				continue
			}
			sort.Slice(dateDirs, func(i, j int) bool {
				return dateDirs[i].Name() < dateDirs[j].Name()
			})

			for _, dEntry := range dateDirs {
				if !dEntry.IsDir() {
					continue
				}
				dPath := filepath.Join(pPath, dEntry.Name())
				files, err := os.ReadDir(dPath)
				if err != nil {
					continue
				}
				for _, f := range files {
					if f.IsDir() {
						continue
					}
					fName := f.Name()
					fPath := filepath.Join(dPath, fName)

					if fName == "topics.md" {
						topics := parseTraeTopicsFile(fPath)
						for _, tp := range topics {
							cand, ok := byID[tp.SessionID]
							if !ok {
								model := firstNonEmpty(modelBySession[tp.SessionID], defaultModel)
								ts := tp.Timestamp
								if ts == 0 {
									if fi, err := f.Info(); err == nil {
										ts = fi.ModTime().UnixMilli()
									}
								}
								cand = &traeSessionCandidate{
									projectMemoryPath: projMemPath,
									summary: ExternalSessionSummary{
										ID:          tp.SessionID,
										Source:      ExternalToolTrae,
										SourceLabel: "Trae",
										CWD:         cwd,
										Title:       truncateRunes(firstLineText(tp.Text), 80),
										Summary:     truncateRunes(tp.Text, 240),
										Model:       model,
										CreatedAt:   ts,
										UpdatedAt:   ts,
										MatchesCWD:  sameOrSubPath(cwd, targetDir),
									},
								}
								byID[tp.SessionID] = cand
							}
							cand.topics = append(cand.topics, tp)
							if tp.Timestamp > cand.summary.UpdatedAt {
								cand.summary.UpdatedAt = tp.Timestamp
							}
						}
						continue
					}

					if strings.HasPrefix(fName, "session_memory_") && strings.HasSuffix(fName, ".jsonl") {
						sID := strings.TrimSuffix(strings.TrimPrefix(fName, "session_memory_"), ".jsonl")
						if sID == "" {
							continue
						}
						firstIntent, lastOutcome, createdMs, updatedMs := quickInspectTraeMemoryJSONL(fPath)
						if updatedMs == 0 {
							if fi, err := f.Info(); err == nil {
								updatedMs = fi.ModTime().UnixMilli()
							}
						}
						if createdMs == 0 {
							createdMs = updatedMs
						}
						model := firstNonEmpty(modelBySession[sID], defaultModel)

						cand, ok := byID[sID]
						if !ok {
							title := truncateRunes(firstLineText(firstIntent), 80)
							if title == "" {
								title = "Trae Session " + sID[:minInt(8, len(sID))]
							}
							cand = &traeSessionCandidate{
								projectMemoryPath: projMemPath,
								summary: ExternalSessionSummary{
									ID:          sID,
									Source:      ExternalToolTrae,
									SourceLabel: "Trae",
									CWD:         cwd,
									Title:       title,
									Summary:     truncateRunes(firstNonEmpty(lastOutcome, firstIntent), 240),
									Model:       model,
									CreatedAt:   createdMs,
									UpdatedAt:   updatedMs,
									MatchesCWD:  sameOrSubPath(cwd, targetDir),
								},
							}
							byID[sID] = cand
						} else {
							if firstIntent != "" && (cand.summary.Title == "" || strings.HasPrefix(cand.summary.Title, "Trae Session ") || len([]rune(cand.summary.Title)) > 60) {
								cand.summary.Title = truncateRunes(firstLineText(firstIntent), 80)
							}
							if lastOutcome != "" {
								cand.summary.Summary = truncateRunes(lastOutcome, 240)
							}
							if updatedMs > cand.summary.UpdatedAt {
								cand.summary.UpdatedAt = updatedMs
							}
						}
						cand.memoryFiles = append(cand.memoryFiles, fPath)
					}
				}
			}
		}
	}

	return byID, installed
}

func scanTraeSessions(targetDir, query string) ([]ExternalSessionSummary, ExternalSourceStats) {
	byID, installed := collectTraeCandidates(targetDir)
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
			haystack := strings.ToLower(sum.Title + " " + sum.Summary + " " + sum.CWD + " " + sum.Model)
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

func loadTraeSessionDetail(sessionID, hintCWD, activeRoot string) (*ExternalSessionDetail, error) {
	targetDir := firstNonEmpty(hintCWD, activeRoot)
	byID, _ := collectTraeCandidates(targetDir)
	cand, ok := byID[sessionID]
	if !ok || cand == nil {
		return nil, fmt.Errorf("trae session not found: %s", sessionID)
	}

	var turns []NormalizedTurn
	var plans []ExternalPlanStep
	step := int64(0)

	for _, memPath := range cand.memoryFiles {
		f, err := os.Open(memPath)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var rec traeMemoryLine
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			step++
			ts := rec.CompactSummaryMeta.CreatedAtMs
			if ts == 0 {
				ts = parseTimeMs(rec.MessageSummaryTime)
			}
			if ts == 0 {
				ts = cand.summary.CreatedAt + step*1000
			}

			intent := strings.TrimSpace(rec.Intent)
			if intent != "" {
				turns = append(turns, NormalizedTurn{
					Role:      "user",
					Timestamp: ts,
					Text:      intent,
					ModelID:   cand.summary.Model,
				})
			}

			var replyBuilder strings.Builder
			if strings.TrimSpace(rec.Outcome) != "" {
				replyBuilder.WriteString(strings.TrimSpace(rec.Outcome))
			}
			if len(rec.Learned) > 0 {
				if replyBuilder.Len() > 0 {
					replyBuilder.WriteString("\n\n### Key Findings & Learned Context\n")
				}
				for _, l := range rec.Learned {
					if strings.TrimSpace(l) != "" {
						replyBuilder.WriteString("- " + strings.TrimSpace(l) + "\n")
					}
				}
			}

			var reasoningParts []string
			for _, act := range rec.Actions {
				act = strings.TrimSpace(act)
				if act != "" {
					reasoningParts = append(reasoningParts, "- "+act)
					plans = append(plans, ExternalPlanStep{
						Title:  act,
						Status: "completed",
					})
				}
			}

			replyText := strings.TrimSpace(replyBuilder.String())
			if replyText != "" || len(reasoningParts) > 0 {
				turns = append(turns, NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts + 500,
					Text:      replyText,
					Reasoning: strings.Join(reasoningParts, "\n"),
					ModelID:   cand.summary.Model,
				})
			}
		}
		_ = f.Close()
	}

	if len(turns) == 0 && len(cand.topics) > 0 {
		for idx, tp := range cand.topics {
			ts := tp.Timestamp
			if ts == 0 {
				ts = cand.summary.CreatedAt + int64(idx+1)*1000
			}
			turns = append(turns,
				NormalizedTurn{
					Role:      "user",
					Timestamp: ts,
					Text:      cand.summary.Title,
					ModelID:   cand.summary.Model,
				},
				NormalizedTurn{
					Role:      "assistant",
					Timestamp: ts + 500,
					Text:      tp.Text,
					ModelID:   cand.summary.Model,
				},
			)
		}
	}

	var projRecap string
	if cand.projectMemoryPath != "" {
		if raw, err := os.ReadFile(cand.projectMemoryPath); err == nil {
			projRecap = truncateRunes(strings.TrimSpace(string(raw)), 1500)
		}
	}
	if len(cand.topics) > 0 {
		var topicLines []string
		for _, tp := range cand.topics {
			topicLines = append(topicLines, "- "+tp.Text)
		}
		joinedTopics := strings.Join(topicLines, "\n")
		if projRecap != "" {
			projRecap = joinedTopics + "\n\n" + projRecap
		} else {
			projRecap = joinedTopics
		}
	}

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
		ChangedFiles:    extractChangedFilesFromTurns(turns),
		LastTurnSummary: lastTurnSummary,
		LastRecap:       truncateRunes(projRecap, 1500),
	}, nil
}

func detectTraeCLIStatus() LocalCLIStatusItem {
	sessions, stats := scanTraeSessions("", "")
	_, defModel := readTraeSessionModels()
	var models []string
	if defModel != "" {
		models = appendUniqueString(models, defModel)
	}
	for _, s := range sessions {
		if s.Model != "" {
			models = appendUniqueString(models, s.Model)
			if defModel == "" {
				defModel = s.Model
			}
		}
	}
	return LocalCLIStatusItem{
		ID:            ExternalToolTrae,
		Name:          "Trae",
		Installed:     stats.Installed,
		Authenticated: false,
		Connected:     false,
		DefaultModel:  defModel,
		Models:        models,
		SessionCount:  stats.SessionCount,
		AuthMode:      "session-handoff",
		ProviderID:    "trae",
	}
}
