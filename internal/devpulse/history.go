package devpulse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	HistoryDefaultDays = 30
	HistoryMaxDays     = 365
)

type HistoryModelStat struct {
	ProviderID       string `json:"providerID"`
	ModelID          string `json:"modelID"`
	Turns            int    `json:"turns"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	// DurationSumMs/DurationSamples aggregate created→completed time so the
	// client can show an average per model without a separate endpoint.
	DurationSumMs   int64 `json:"durationSumMs"`
	DurationSamples int   `json:"durationSamples"`
}

type HistoryDay struct {
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
	Models           []HistoryModelStat `json:"models"`
}

type HistoryResponse struct {
	Days      []HistoryDay `json:"days"`
	StartDate string       `json:"startDate"`
	EndDate   string       `json:"endDate"`
	Timezone  string       `json:"timezone"`
}

// HandleHistory returns per-day usage for the requested window. Cost is
// deliberately not computed here: the pricing catalog and currency conversion
// live in the frontend, so this endpoint emits raw token counts per model and
// the client prices them with the same resolver as today's pulse.
func (s *Service) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	days := HistoryDefaultDays
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "days must be a positive integer")
			return
		}
		if n > HistoryMaxDays {
			n = HistoryMaxDays
		}
		days = n
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := todayStart.AddDate(0, 0, -(days - 1))
	end := todayStart

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
		if end.After(todayStart) {
			end = todayStart
		}
		oldestAllowed := end.AddDate(0, 0, -(HistoryMaxDays - 1))
		if start.Before(oldestAllowed) {
			start = oldestAllowed
		}
		days = 0
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			days++
		}
	}
	endExclusive := end.AddDate(0, 0, 1)

	res := HistoryResponse{
		StartDate: start.Format("2006-01-02"),
		EndDate:   end.Format("2006-01-02"),
		Timezone:  now.Location().String(),
	}

	byDate := make(map[string]*HistoryDay, days)
	day := func(key string) *HistoryDay {
		if d, ok := byDate[key]; ok {
			return d
		}
		d := &HistoryDay{Date: key, Models: []HistoryModelStat{}}
		byDate[key] = d
		return d
	}

	s.queryHistoryFromDB(start.UnixMilli(), endExclusive.UnixMilli(), day)
	s.CollectRunnerHistory(start.UnixMilli(), endExclusive.UnixMilli(), day)

	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
	if targets := s.ResolveGitTargets(dir, sessionID); len(targets) > 0 {
		s.CollectGitHistory(targets, start, endExclusive, todayStart, day)
	}

	sortHistoryModels(byDate)
	res.Days = make([]HistoryDay, 0, days)
	for i := 0; i < days; i++ {
		key := start.AddDate(0, 0, i).Format("2006-01-02")
		res.Days = append(res.Days, *day(key))
	}
	writeJSON(w, http.StatusOK, res)
}

func historyDayKey(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("2006-01-02")
}

func (s *Service) queryHistoryFromDB(startMs, endMs int64, day func(string) *HistoryDay) {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}

	msgClause := s.notImportedClause("s.title", "m.data", "m.time_created", "s.time_created")
	partClause := s.notImportedClause("s.title", "p.data", "p.time_created", "s.time_created")
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
  SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.cache.write'), 0) AS INTEGER)) AS cache_write,
  SUM(CASE WHEN CAST(COALESCE(json_extract(m.data, '$.time.completed'), 0) AS INTEGER) > m.time_created
        THEN CAST(COALESCE(json_extract(m.data, '$.time.completed'), 0) AS INTEGER) - m.time_created ELSE 0 END) AS dur_sum,
  SUM(CASE WHEN CAST(COALESCE(json_extract(m.data, '$.time.completed'), 0) AS INTEGER) > m.time_created
        THEN 1 ELSE 0 END) AS dur_n
FROM session s
CROSS JOIN message m ON m.session_id = s.id
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
ORDER BY day;`, startMs, endMs, msgClause)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, sqlitecli.Executable(), "-json", "-readonly", "-cmd", ".timeout 5000", dbPath, sqlQuery)
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
		DurSum     int64  `json:"dur_sum"`
		DurN       int    `json:"dur_n"`
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
		mergeHistoryModel(target, HistoryModelStat{
			ProviderID:       row.ProviderID,
			ModelID:          row.ModelID,
			Turns:            row.Turns,
			InputTokens:      row.Input,
			OutputTokens:     row.Output,
			ReasoningTokens:  row.Reasoning,
			CacheReadTokens:  row.CacheRead,
			CacheWriteTokens: row.CacheWrite,
			DurationSumMs:    row.DurSum,
			DurationSamples:  row.DurN,
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
  FROM session s
  CROSS JOIN message m ON m.session_id = s.id
  CROSS JOIN part p ON p.message_id = m.id
  WHERE m.time_created >= %d
    AND m.time_created < %d
    AND p.time_created >= %d
    AND p.time_created < %d
    AND json_extract(p.data, '$.type') = 'tool'
    AND %s
  GROUP BY date(p.time_created / 1000, 'unixepoch', 'localtime'), COALESCE(json_extract(p.data, '$.callID'), p.id)
)
GROUP BY day
ORDER BY day;`, startMs-partMessageLookbackMs, endMs, startMs, endMs, partClause)

	tCtx, tCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer tCancel()
	tCmd := subproc.CommandContext(tCtx, sqlitecli.Executable(), "-json", "-readonly", "-cmd", ".timeout 5000", dbPath, toolSQL)
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

func mergeHistoryModel(target *HistoryDay, incoming HistoryModelStat) {
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
		m.DurationSumMs += incoming.DurationSumMs
		m.DurationSamples += incoming.DurationSamples
		return
	}
	target.Models = append(target.Models, incoming)
}

// CollectRunnerHistory folds native-runtime usage (Claude Code, Antigravity,
// Qoder, CodeBuddy) into the per-day series. OpenCode turns mirror hoxi.db and
// are already counted, so they are skipped here.
func (s *Service) CollectRunnerHistory(startMs, endMs int64, day func(string) *HistoryDay) {
	if s.source == nil {
		return
	}
	loc := time.Now().Location()

	type sessionKey struct{ date, sessionID string }
	seenNative := make(map[string]map[sessionKey]int)
	seenOpenCode := make(map[string]map[string]bool)

	s.source.VisitSessions(startMs, func(sessionID string, visitTurns func(func(TurnUsage))) {
		visitTurns(func(turn TurnUsage) {
			if turn.Role != "assistant" || turn.Created < startMs || turn.Created >= endMs {
				return
			}
			key := historyDayKey(turn.Created, loc)
			if !turn.IsNative {
				if seenOpenCode[key] == nil {
					seenOpenCode[key] = make(map[string]bool)
				}
				seenOpenCode[key][sessionID] = true
				return
			}
			if turn.ToolCallsTotal > 0 || turn.ToolCallsFailed > 0 {
				target := day(key)
				target.ToolCallsTotal += turn.ToolCallsTotal
				target.ToolCallsFailed += turn.ToolCallsFailed
			}
			if !turn.HasTokens {
				return
			}
			target := day(key)
			target.Turns++
			target.InputTokens += turn.InputTokens
			target.OutputTokens += turn.OutputTokens
			target.ReasoningTokens += turn.ReasoningTokens
			target.CacheReadTokens += turn.CacheReadTokens
			target.CacheWriteTokens += turn.CacheWriteTokens
			var durationSum int64
			var durationSamples int
			if turn.Completed > turn.Created && turn.Created > 0 {
				durationSum = turn.Completed - turn.Created
				durationSamples = 1
			}
			mergeHistoryModel(target, HistoryModelStat{
				ProviderID:       turn.ProviderID,
				ModelID:          turn.ModelID,
				Turns:            1,
				InputTokens:      turn.InputTokens,
				OutputTokens:     turn.OutputTokens,
				ReasoningTokens:  turn.ReasoningTokens,
				CacheReadTokens:  turn.CacheReadTokens,
				CacheWriteTokens: turn.CacheWriteTokens,
				DurationSumMs:    durationSum,
				DurationSamples:  durationSamples,
			})
			if seenNative[key] == nil {
				seenNative[key] = make(map[sessionKey]int)
			}
			seenNative[key][sessionKey{key, sessionID}]++
		})
	})
	for key, sessions := range seenNative {
		for sk := range sessions {
			if !seenOpenCode[key][sk.sessionID] {
				day(key).Sessions++
			}
		}
	}
}

func sortHistoryModels(byDate map[string]*HistoryDay) {
	for _, d := range byDate {
		models := d.Models
		sort.SliceStable(models, func(a, b int) bool {
			return models[a].Turns > models[b].Turns
		})
	}
}
