package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	historyDefaultDays = 30
	historyMaxDays     = 365
)

type historyModelStat struct {
	ProviderID       string `json:"providerID"`
	ModelID          string `json:"modelID"`
	Turns            int    `json:"turns"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
}

type historyDay struct {
	Date             string             `json:"date"`
	Turns            int                `json:"turns"`
	Sessions         int                `json:"sessions"`
	InputTokens      int64              `json:"inputTokens"`
	OutputTokens     int64              `json:"outputTokens"`
	ReasoningTokens  int64              `json:"reasoningTokens"`
	CacheReadTokens  int64              `json:"cacheReadTokens"`
	CacheWriteTokens int64              `json:"cacheWriteTokens"`
	ToolCallsTotal   int                `json:"toolCallsTotal"`
	ToolCallsFailed  int                `json:"toolCallsFailed"`
	Commits          int                `json:"commits"`
	Additions        int                `json:"additions"`
	Deletions        int                `json:"deletions"`
	Files            int                `json:"files"`
	Models           []historyModelStat `json:"models"`
}

type devPulseHistoryResponse struct {
	Days      []historyDay `json:"days"`
	StartDate string       `json:"startDate"`
	EndDate   string       `json:"endDate"`
	Timezone  string       `json:"timezone"`
}

// handleDevPulseHistory returns per-day usage for the requested window. Cost is
// deliberately not computed here: the pricing catalog and currency conversion
// live in the frontend, so this endpoint emits raw token counts per model and
// the client prices them with the same resolver as today's pulse.
func (g *Gateway) handleDevPulseHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	days := historyDefaultDays
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "days must be a positive integer")
			return
		}
		if n > historyMaxDays {
			n = historyMaxDays
		}
		days = n
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := todayStart.AddDate(0, 0, -(days - 1))
	end := todayStart

	// An explicit start (YYYY-MM-DD) selects a custom window, optionally bounded
	// by an end date. The window is right-open in the queries via endExclusive so
	// the whole end day is included.
	if raw := strings.TrimSpace(r.URL.Query().Get("start")); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, now.Location())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "start must be YYYY-MM-DD")
			return
		}
		start = parsed
		if rawEnd := strings.TrimSpace(r.URL.Query().Get("end")); rawEnd != "" {
			parsedEnd, err := time.ParseInLocation("2006-01-02", rawEnd, now.Location())
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "end must be YYYY-MM-DD")
				return
			}
			end = parsedEnd
		}
		if end.Before(start) {
			start, end = end, start
		}
		// The window never reaches past today.
		if end.After(todayStart) {
			end = todayStart
		}
		// Clamp an over-long custom window to the most recent max days.
		if end.Sub(start) > time.Duration(historyMaxDays)*24*time.Hour {
			start = end.AddDate(0, 0, -(historyMaxDays - 1))
		}
		days = 0
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			days++
		}
	}
	endExclusive := end.AddDate(0, 0, 1)

	res := devPulseHistoryResponse{
		StartDate: start.Format("2006-01-02"),
		EndDate:   end.Format("2006-01-02"),
		Timezone:  now.Location().String(),
	}

	byDate := make(map[string]*historyDay, days)
	day := func(key string) *historyDay {
		if d, ok := byDate[key]; ok {
			return d
		}
		d := &historyDay{Date: key, Models: []historyModelStat{}}
		byDate[key] = d
		return d
	}

	queryHistoryFromDB(start.UnixMilli(), endExclusive.UnixMilli(), day)
	g.collectRunnerHistory(start.UnixMilli(), endExclusive.UnixMilli(), day)

	workDir := g.rootDir
	if g.worktrees != nil {
		if rt := g.worktrees.RootDir(); rt != "" {
			workDir = rt
		}
	}
	if dir := strings.TrimSpace(r.URL.Query().Get("directory")); dir != "" {
		workDir = dir
	}
	if workDir != "" {
		collectGitHistory(workDir, start, endExclusive, day)
	}

	sortHistoryModels(byDate)
	// Emit a dense series so the chart can show zero-usage days instead of
	// silently collapsing them.
	res.Days = make([]historyDay, 0, days)
	for i := 0; i < days; i++ {
		key := start.AddDate(0, 0, i).Format("2006-01-02")
		res.Days = append(res.Days, *day(key))
	}
	writeJSON(w, http.StatusOK, res)
}

// historyDayKey keeps SQLite and Go agreeing on "which local day is this",
// including across DST boundaries.
func historyDayKey(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("2006-01-02")
}

func queryHistoryFromDB(startMs, endMs int64, day func(string) *historyDay) {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}

	// One flat row per (day, session, model); Go does the rollup. Nesting
	// json_group_array here would emit a JSON *string* and silently break the
	// typed decode.
	sqlQuery := fmt.Sprintf(`
SELECT
  date(m.time_created / 1000, 'unixepoch', 'localtime') AS day,
  m.session_id,
  COALESCE(json_extract(m.data, '$.providerID'), 'unknown') AS provider_id,
  COALESCE(json_extract(m.data, '$.modelID'), 'unknown') AS model_id,
  COUNT(*) AS turns,
  SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER)) AS input,
  SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER)) AS output,
  SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.reasoning'), 0) AS INTEGER)) AS reasoning,
  SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER)) AS cache_read,
  SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.cache.write'), 0) AS INTEGER)) AS cache_write
FROM message m
JOIN session s ON s.id = m.session_id
WHERE m.time_created >= %d
  AND m.time_created < %d
  AND json_extract(m.data, '$.role') = 'assistant'
  AND %s
  AND (
    CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) > 0
    OR CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) > 0
    OR CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) > 0
  )
GROUP BY day, m.session_id, provider_id, model_id
ORDER BY day;`, startMs, endMs, handoff.SQLNotImportedTitleClause("s.title"))

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-json", "-readonly", "-cmd", ".timeout 5000", dbPath, sqlQuery)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return
	}

	var rows []struct {
		Day        string `json:"day"`
		SessionID  string `json:"session_id"`
		ProviderID string `json:"provider_id"`
		ModelID    string `json:"model_id"`
		Turns      int    `json:"turns"`
		Input      int64  `json:"input"`
		Output     int64  `json:"output"`
		Reasoning  int64  `json:"reasoning"`
		CacheRead  int64  `json:"cache_read"`
		CacheWrite int64  `json:"cache_write"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &rows); err != nil {
		return
	}

	sessionsByDay := make(map[string]map[string]struct{})
	for _, row := range rows {
		if row.Day == "" {
			continue
		}
		target := day(row.Day)
		target.Turns += row.Turns
		target.InputTokens += row.Input
		target.OutputTokens += row.Output
		target.ReasoningTokens += row.Reasoning
		target.CacheReadTokens += row.CacheRead
		target.CacheWriteTokens += row.CacheWrite
		mergeHistoryModel(target, historyModelStat{
			ProviderID:       row.ProviderID,
			ModelID:          row.ModelID,
			Turns:            row.Turns,
			InputTokens:      row.Input,
			OutputTokens:     row.Output,
			ReasoningTokens:  row.Reasoning,
			CacheReadTokens:  row.CacheRead,
			CacheWriteTokens: row.CacheWrite,
		})
		if sessionsByDay[row.Day] == nil {
			sessionsByDay[row.Day] = make(map[string]struct{})
		}
		sessionsByDay[row.Day][row.SessionID] = struct{}{}
	}
	for key, sessions := range sessionsByDay {
		day(key).Sessions += len(sessions)
	}

	toolSQL := fmt.Sprintf(`
SELECT
  day,
  COUNT(*) AS tools_total,
  SUM(is_error) AS tools_failed
FROM (
  SELECT
    date(p.time_created / 1000, 'unixepoch', 'localtime') AS day,
    MAX(CASE WHEN json_extract(p.data, '$.state.status') = 'error' THEN 1 ELSE 0 END) AS is_error
  FROM part p
  JOIN session s ON s.id = p.session_id
  WHERE p.time_created >= %d
    AND p.time_created < %d
    AND json_extract(p.data, '$.type') = 'tool'
    AND %s
  GROUP BY date(p.time_created / 1000, 'unixepoch', 'localtime'), COALESCE(json_extract(p.data, '$.callID'), p.id)
)
GROUP BY day
ORDER BY day;`, startMs, endMs, handoff.SQLNotImportedTitleClause("s.title"))

	tCtx, tCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer tCancel()
	tCmd := exec.CommandContext(tCtx, "sqlite3", "-json", "-readonly", "-cmd", ".timeout 5000", dbPath, toolSQL)
	if tOut, err := tCmd.Output(); err == nil && len(bytes.TrimSpace(tOut)) > 0 {
		var toolRows []struct {
			Day         string `json:"day"`
			ToolsTotal  int    `json:"tools_total"`
			ToolsFailed int    `json:"tools_failed"`
		}
		if json.Unmarshal(bytes.TrimSpace(tOut), &toolRows) == nil {
			for _, tr := range toolRows {
				if tr.Day == "" {
					continue
				}
				target := day(tr.Day)
				target.ToolCallsTotal += tr.ToolsTotal
				target.ToolCallsFailed += tr.ToolsFailed
			}
		}
	}
}

func mergeHistoryModel(target *historyDay, incoming historyModelStat) {
	for i := range target.Models {
		m := &target.Models[i]
		if m.ProviderID != incoming.ProviderID || m.ModelID != incoming.ModelID {
			continue
		}
		m.Turns += incoming.Turns
		m.InputTokens += incoming.InputTokens
		m.OutputTokens += incoming.OutputTokens
		m.ReasoningTokens += incoming.ReasoningTokens
		m.CacheReadTokens += incoming.CacheReadTokens
		m.CacheWriteTokens += incoming.CacheWriteTokens
		return
	}
	target.Models = append(target.Models, incoming)
}

// collectRunnerHistory folds native-runtime usage (Claude Code, Antigravity,
// Qoder, CodeBuddy) into the per-day series. OpenCode turns mirror hoxi.db and
// are already counted, so they are skipped here.
func (g *Gateway) collectRunnerHistory(startMs, endMs int64, day func(string) *historyDay) {
	if g.runner == nil {
		return
	}
	store := g.runner.Store()
	loc := time.Now().Location()

	type modelKey struct{ date, provider, model string }
	seen := make(map[string]map[modelKey]int)

	for _, info := range store.List("") {
		if info.Time.Updated < startMs {
			continue
		}
		rec, ok := store.Meta(info.ID)
		if !ok || rec.Subagent {
			continue
		}
		runtime := strings.TrimSpace(rec.Runtime)
		if runtime == "" || runtime == "opencode" {
			continue
		}
		store.VisitTurns(rec.ID, func(full runner.SessionRecord) {
			for i := range full.Turns {
				turn := full.Turns[i]
				if turn.Info.Role != "assistant" || turn.Info.Time.Created < startMs || turn.Info.Time.Created >= endMs {
					continue
				}
				key := historyDayKey(turn.Info.Time.Created, loc)
				for _, part := range turn.Parts {
					if part.Type != "tool" {
						continue
					}
					target := day(key)
					target.ToolCallsTotal++
					if part.State != nil && part.State.Status == "error" {
						target.ToolCallsFailed++
					}
				}
				tokens := turn.Info.Tokens
				if tokens == nil {
					continue
				}
				if tokens.Input == 0 && tokens.Output == 0 && tokens.Cache.Read == 0 {
					continue
				}
				target := day(key)
				target.Turns++
				target.InputTokens += int64(tokens.Input)
				target.OutputTokens += int64(tokens.Output)
				target.ReasoningTokens += int64(tokens.Reasoning)
				target.CacheReadTokens += int64(tokens.Cache.Read)
				target.CacheWriteTokens += int64(tokens.Cache.Write)
				mergeHistoryModel(target, historyModelStat{
					ProviderID:       runtime,
					ModelID:          firstNonEmptyString(turn.Info.ModelID, rec.ModelID, "unknown"),
					Turns:            1,
					InputTokens:      int64(tokens.Input),
					OutputTokens:     int64(tokens.Output),
					ReasoningTokens:  int64(tokens.Reasoning),
					CacheReadTokens:  int64(tokens.Cache.Read),
					CacheWriteTokens: int64(tokens.Cache.Write),
				})
				if seen[key] == nil {
					seen[key] = make(map[modelKey]int)
				}
				seen[key][modelKey{key, runtime, rec.ID}]++
			}
		})
	}
	// Distinct sessions per day for native runtimes.
	for key, sessions := range seen {
		day(key).Sessions += len(sessions)
	}
}

// collectGitHistory buckets today's commit numstat into per-day totals with a
// single git invocation instead of one call per day.
func collectGitHistory(workDir string, start, end time.Time, day func(string) *historyDay) {
	sinceArg := "--since=" + start.Format(time.RFC3339)
	untilArg := "--until=" + end.Format(time.RFC3339)
	logOut, err := worktree.RunGitRaw(workDir,
		"log", sinceArg, untilArg, "--no-merges", "--numstat",
		"--pretty=format:COMMIT:%at")
	if err != nil {
		return
	}

	loc := time.Now().Location()
	fileSets := make(map[string]map[string]struct{})
	var current string
	for _, rawLine := range strings.Split(logOut, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "COMMIT:") {
			sec, convErr := strconv.ParseInt(strings.TrimPrefix(line, "COMMIT:"), 10, 64)
			if convErr != nil {
				current = ""
				continue
			}
			current = historyDayKey(sec*1000, loc)
			target := day(current)
			target.Commits++
			if fileSets[current] == nil {
				fileSets[current] = make(map[string]struct{})
			}
			continue
		}
		if current == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		file := fields[len(fields)-1]
		if isGeneratedOrLockFile(file) {
			continue
		}
		target := day(current)
		if n, convErr := strconv.Atoi(fields[0]); convErr == nil {
			target.Additions += n
		}
		if n, convErr := strconv.Atoi(fields[1]); convErr == nil {
			target.Deletions += n
		}
		fileSets[current][file] = struct{}{}
	}
	for key, set := range fileSets {
		day(key).Files = len(set)
	}
}

// sortHistoryModels keeps the per-day model list stable for the UI.
func sortHistoryModels(byDate map[string]*historyDay) {
	for _, d := range byDate {
		models := d.Models
		sort.SliceStable(models, func(a, b int) bool {
			return models[a].Turns > models[b].Turns
		})
	}
}
