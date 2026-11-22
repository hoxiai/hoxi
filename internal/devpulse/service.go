// Package devpulse aggregates daily and historical Git activity and AI model
// token/tool usage across OpenCode SQLite state, native CLI runtimes, and
// workspace Git worktrees.
package devpulse

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

const (
	// Untracked files are counted by reading them, so the scan is bounded by a
	// byte budget rather than a small file count: a handful of generated files
	// must not silently hide the rest of an agent's day.
	maxUntrackedFilesToCount = 500
	maxUntrackedFileSize     = 64 * 1024
	maxUntrackedTotalBytes   = 4 << 20

	devPulseCacheTTL        = 15 * time.Second
	maxDevPulseCacheEntries = 16
	partMessageLookbackMs   = 24 * 60 * 60 * 1000
)

type DailyModelStat struct {
	ProviderID       string  `json:"providerID"`
	ModelID          string  `json:"modelID"`
	Turns            int     `json:"turns"`
	TotalTokens      int64   `json:"totalTokens"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	Cost             float64 `json:"cost"`
}

type DailyPulseResponse struct {
	TodayCommits       int `json:"todayCommits"`
	CommittedAdditions int `json:"committedAdditions"`
	CommittedDeletions int `json:"committedDeletions"`
	CommittedFiles     int `json:"committedFiles"`
	// ChangedFiles is the union of committed and uncommitted paths, so a file
	// both committed and still dirty today is counted once.
	ChangedFiles         int `json:"changedFiles,omitempty"`
	UncommittedAdditions int `json:"uncommittedAdditions"`
	UncommittedDeletions int `json:"uncommittedDeletions"`
	UncommittedFiles     int `json:"uncommittedFiles"`
	// UncommittedTruncated reports that untracked-file line counting stopped at
	// its read budget, so additions are a lower bound rather than a total.
	UncommittedTruncated bool             `json:"uncommittedTruncated,omitempty"`
	Branch               string           `json:"branch,omitempty"`
	HeadCommit           string           `json:"headCommit,omitempty"`
	HeadSubject          string           `json:"headSubject,omitempty"`
	DbTotalTokens        int64            `json:"dbTotalTokens"`
	DbInputTokens        int64            `json:"dbInputTokens"`
	DbOutputTokens       int64            `json:"dbOutputTokens"`
	DbReasoningTokens    int64            `json:"dbReasoningTokens"`
	DbCacheReadTokens    int64            `json:"dbCacheReadTokens"`
	DbCacheWriteTokens   int64            `json:"dbCacheWriteTokens"`
	DbTotalCost          float64          `json:"dbTotalCost"`
	DbTurnsCount         int              `json:"dbTurnsCount"`
	DbSessionsCount      int              `json:"dbSessionsCount"`
	DbToolCallsTotal     int              `json:"dbToolCallsTotal"`
	DbToolCallsFailed    int              `json:"dbToolCallsFailed"`
	DbLatestTurnTs       int64            `json:"dbLatestTurnTs"`
	DbModels             []DailyModelStat `json:"dbModels"`
}

// CandidateDirectories returns workspace directories supplied by the pulse Source.
func (s *Service) CandidateDirectories() []string {
	if s == nil || s.source == nil {
		return nil
	}
	return s.source.CandidateDirectories()
}

// CanonicalDir normalizes a directory path for pulse comparisons.
func CanonicalDir(raw string) string {
	return canonicalPulseDir(raw)
}

// SourceKey fingerprints the underlying SQLite and runner stores so cached
// daily usage can be reused while nothing has changed.
type SourceKey struct {
	DBSize, DBMod, WALSize, WALMod int64
	RunnerRev                      uint64
}

// TurnUsage summarizes a single assistant turn from the runner session store.
type TurnUsage struct {
	Role             string
	Created          int64
	Completed        int64
	ProviderID       string
	ModelID          string
	IsNative         bool
	ToolCallsTotal   int
	ToolCallsFailed  int
	HasTokens        bool
	InputTokens      int64
	OutputTokens     int64
	ReasoningTokens  int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	Cost             float64
}

// Source supplies workspace, session, runner, and git adapters from the host
// gateway without coupling devpulse to other domain packages.
type Source interface {
	SessionDirectories(sessionID string) (workDir, projectDir string)
	CandidateDirectories() []string
	SourceKey() SourceKey
	VisitSessions(minUpdatedMs int64, fn func(sessionID string, visitTurns func(func(TurnUsage))))
	NotImportedTurnSQLClause(titleCol, dataCol, rowTimeCol, sessTimeCol string) string
	RunGit(workDir string, args ...string) (string, error)
	RunGitRaw(workDir string, args ...string) (string, error)
	ParseBranchFromPorcelainHeader(headerLine string) string
}

type pulseEntry struct {
	at  time.Time
	res DailyPulseResponse
}

type pulseFlight struct {
	wg  sync.WaitGroup
	res DailyPulseResponse
}

type Service struct {
	source       Source
	mu           sync.Mutex
	entries      map[string]pulseEntry
	inflight     map[string]*pulseFlight
	usageAt      time.Time
	usageStartMs int64
	usageKey     SourceKey
	usageRes     DailyPulseResponse
}

func NewService(source Source) *Service {
	return &Service{
		source:   source,
		entries:  make(map[string]pulseEntry),
		inflight: make(map[string]*pulseFlight),
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/dev-pulse", s.HandleDailyPulse)
	mux.HandleFunc("/api/hoxi/dev-pulse/history", s.HandleHistory)
}

func (s *Service) HandleDailyPulse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
	forced := strings.TrimSpace(r.URL.Query().Get("refresh")) != ""
	res := s.DailyPulse(dir, sessionID, forced)
	writeJSON(w, http.StatusOK, res)
}

func (s *Service) DailyPulse(dir, sessionID string, forced bool) DailyPulseResponse {
	targets := s.ResolveGitTargets(dir, sessionID)
	cacheKey := TargetsCacheKey(targets)

	now := time.Now()
	s.mu.Lock()
	if s.entries == nil {
		s.entries = make(map[string]pulseEntry)
	}
	if s.inflight == nil {
		s.inflight = make(map[string]*pulseFlight)
	}
	if !forced {
		if entry, ok := s.entries[cacheKey]; ok && !entry.at.IsZero() && now.Sub(entry.at) < devPulseCacheTTL {
			s.mu.Unlock()
			return entry.res
		}
		if flight, ok := s.inflight[cacheKey]; ok {
			s.mu.Unlock()
			flight.wg.Wait()
			return flight.res
		}
	}
	var flight *pulseFlight
	if !forced {
		flight = &pulseFlight{}
		flight.wg.Add(1)
		s.inflight[cacheKey] = flight
	}
	s.mu.Unlock()

	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfTodayMs := startOfToday.UnixMilli()

	res := DailyPulseResponse{
		DbModels: []DailyModelStat{},
	}

	s.collectDailyUsage(now, startOfTodayMs, forced, &res)
	if len(targets) > 0 {
		s.CollectGitDailyStats(targets, startOfToday, &res)
	}

	s.mu.Lock()
	if len(s.entries) >= maxDevPulseCacheEntries {
		var oldestKey string
		var oldestAt time.Time
		for k, v := range s.entries {
			if now.Sub(v.at) >= devPulseCacheTTL {
				delete(s.entries, k)
				continue
			}
			if oldestKey == "" || v.at.Before(oldestAt) {
				oldestKey = k
				oldestAt = v.at
			}
		}
		if len(s.entries) >= maxDevPulseCacheEntries && oldestKey != "" {
			delete(s.entries, oldestKey)
		}
	}
	s.entries[cacheKey] = pulseEntry{at: time.Now(), res: res}
	if flight != nil {
		flight.res = res
		delete(s.inflight, cacheKey)
		flight.wg.Done()
	}
	s.mu.Unlock()

	return res
}

func copyDailyUsage(dst *DailyPulseResponse, src DailyPulseResponse) {
	dst.DbTurnsCount = src.DbTurnsCount
	dst.DbSessionsCount = src.DbSessionsCount
	dst.DbToolCallsTotal = src.DbToolCallsTotal
	dst.DbToolCallsFailed = src.DbToolCallsFailed
	dst.DbLatestTurnTs = src.DbLatestTurnTs
	dst.DbTotalTokens = src.DbTotalTokens
	dst.DbInputTokens = src.DbInputTokens
	dst.DbOutputTokens = src.DbOutputTokens
	dst.DbReasoningTokens = src.DbReasoningTokens
	dst.DbCacheReadTokens = src.DbCacheReadTokens
	dst.DbCacheWriteTokens = src.DbCacheWriteTokens
	dst.DbTotalCost = src.DbTotalCost
	dst.DbModels = make([]DailyModelStat, len(src.DbModels))
	copy(dst.DbModels, src.DbModels)
}

func (s *Service) collectDailyUsage(now time.Time, startOfTodayMs int64, forced bool, res *DailyPulseResponse) {
	var sourceKey SourceKey
	if s.source != nil {
		sourceKey = s.source.SourceKey()
	}
	if !forced {
		s.mu.Lock()
		if !s.usageAt.IsZero() &&
			s.usageStartMs == startOfTodayMs &&
			(now.Sub(s.usageAt) < devPulseCacheTTL || s.usageKey == sourceKey) {
			copyDailyUsage(res, s.usageRes)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}

	usage := DailyPulseResponse{
		DbModels: []DailyModelStat{},
	}
	s.queryOpenCodeDailyDB(startOfTodayMs, &usage)
	s.CollectRunnerDailyUsage(startOfTodayMs, &usage)

	s.mu.Lock()
	s.usageAt = time.Now()
	s.usageStartMs = startOfTodayMs
	s.usageKey = sourceKey
	s.usageRes = usage
	s.mu.Unlock()

	copyDailyUsage(res, usage)
}

// CollectRunnerDailyUsage folds native-runtime token usage into the pulse.
// OpenCode sessions are skipped: their turns mirror hoxi.db, which the SQLite
// step already counted, so adding them here would double count.
func (s *Service) CollectRunnerDailyUsage(startOfTodayMs int64, res *DailyPulseResponse) {
	if s.source == nil {
		return
	}
	index := make(map[string]int, len(res.DbModels))
	for i := range res.DbModels {
		index[res.DbModels[i].ProviderID+"/"+res.DbModels[i].ModelID] = i
	}

	var (
		turns                       int
		sessions                    int
		toolsTotal, toolsFailed     int
		latestTurnTs                int64
		input, output, reasoning    int64
		cacheRead, cacheWrite, cost float64
	)
	s.source.VisitSessions(startOfTodayMs, func(_ string, visitTurns func(func(TurnUsage))) {
		hasNativeTurnToday := false
		hasOpenCodeTurnToday := false
		visitTurns(func(turn TurnUsage) {
			if turn.Role != "assistant" || turn.Created < startOfTodayMs {
				return
			}
			if !turn.IsNative {
				hasOpenCodeTurnToday = true
				return
			}
			hasNativeTurnToday = true
			toolsTotal += turn.ToolCallsTotal
			toolsFailed += turn.ToolCallsFailed
			if !turn.HasTokens {
				return
			}
			turns++
			if turn.Created > latestTurnTs {
				latestTurnTs = turn.Created
			}
			input += turn.InputTokens
			output += turn.OutputTokens
			reasoning += turn.ReasoningTokens
			cacheRead += float64(turn.CacheReadTokens)
			cacheWrite += float64(turn.CacheWriteTokens)
			cost += turn.Cost

			mergeDailyModel(res, index, turn)
		})
		if hasNativeTurnToday && !hasOpenCodeTurnToday {
			sessions++
		}
	})
	res.DbSessionsCount += sessions
	res.DbToolCallsTotal += toolsTotal
	res.DbToolCallsFailed += toolsFailed
	if latestTurnTs > res.DbLatestTurnTs {
		res.DbLatestTurnTs = latestTurnTs
	}
	if turns == 0 {
		return
	}
	res.DbTurnsCount += turns
	res.DbInputTokens += input
	res.DbOutputTokens += output
	res.DbReasoningTokens += reasoning
	res.DbCacheReadTokens += int64(cacheRead)
	res.DbCacheWriteTokens += int64(cacheWrite)
	res.DbTotalTokens += input + output + reasoning + int64(cacheRead) + int64(cacheWrite)
	res.DbTotalCost += cost
}

func mergeDailyModel(res *DailyPulseResponse, index map[string]int, u TurnUsage) {
	key := u.ProviderID + "/" + u.ModelID
	at, ok := index[key]
	if !ok {
		res.DbModels = append(res.DbModels, DailyModelStat{
			ProviderID: u.ProviderID,
			ModelID:    u.ModelID,
		})
		at = len(res.DbModels) - 1
		index[key] = at
	}
	m := &res.DbModels[at]
	m.Turns++
	m.InputTokens += u.InputTokens
	m.OutputTokens += u.OutputTokens
	m.ReasoningTokens += u.ReasoningTokens
	m.CacheReadTokens += u.CacheReadTokens
	m.CacheWriteTokens += u.CacheWriteTokens
	m.TotalTokens += u.InputTokens + u.OutputTokens + u.ReasoningTokens + u.CacheReadTokens + u.CacheWriteTokens
	m.Cost += u.Cost
}

func (s *Service) notImportedClause(titleCol, dataCol, rowTimeCol, sessTimeCol string) string {
	if s.source != nil {
		if clause := strings.TrimSpace(s.source.NotImportedTurnSQLClause(titleCol, dataCol, rowTimeCol, sessTimeCol)); clause != "" {
			return clause
		}
	}
	return "1=1"
}

func (s *Service) queryOpenCodeDailyDB(startOfTodayMs int64, res *DailyPulseResponse) {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}

	msgClause := s.notImportedClause("s.title", "m.data", "m.time_created", "s.time_created")
	partClause := s.notImportedClause("s.title", "p.data", "p.time_created", "s.time_created")
	modelsSQL := fmt.Sprintf(`
SELECT
  provider_id,
  model_id,
  COUNT(*) AS turns,
  COALESCE(SUM(tok_in + tok_out + tok_reason + cache_read + cache_write), 0) AS total_tokens,
  COALESCE(SUM(tok_in), 0) AS input_tokens,
  COALESCE(SUM(tok_out), 0) AS output_tokens,
  COALESCE(SUM(tok_reason), 0) AS reasoning_tokens,
  COALESCE(SUM(cache_read), 0) AS cache_read_tokens,
  COALESCE(SUM(cache_write), 0) AS cache_write_tokens,
  COALESCE(SUM(cost), 0) AS total_cost,
  COALESCE(MAX(msg_ts), 0) AS max_ts
FROM (
  SELECT
    COALESCE(json_extract(m.data, '$.providerID'), 'unknown') AS provider_id,
    COALESCE(json_extract(m.data, '$.modelID'), 'unknown') AS model_id,
    m.time_created AS msg_ts,
    CAST(COALESCE(json_extract(m.data, '$.cost'), 0) AS REAL) AS cost,
    CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) AS tok_in,
    CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) AS tok_out,
    CAST(COALESCE(json_extract(m.data, '$.tokens.reasoning'), 0) AS INTEGER) AS tok_reason,
    CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) AS cache_read,
    CAST(COALESCE(json_extract(m.data, '$.tokens.cache.write'), 0) AS INTEGER) AS cache_write
  FROM session s
  CROSS JOIN message m ON m.session_id = s.id
  WHERE m.time_created >= ?
    AND json_extract(m.data, '$.role') = 'assistant'
    AND %s
    AND (
      CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) > 0
      OR CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) > 0
      OR CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) > 0
    )
)
GROUP BY provider_id, model_id
ORDER BY total_tokens DESC;`, msgClause)

	sessionsSQL := fmt.Sprintf(`
SELECT COUNT(DISTINCT s.id)
FROM session s
CROSS JOIN message m ON m.session_id = s.id
WHERE m.time_created >= ?
  AND %s;`, msgClause)

	toolsSQL := fmt.Sprintf(`
SELECT COUNT(*), COALESCE(SUM(is_error), 0)
FROM (
  SELECT MAX(CASE WHEN json_extract(p.data, '$.state.status') = 'error' THEN 1 ELSE 0 END) AS is_error
  FROM session s
  CROSS JOIN message m ON m.session_id = s.id
  CROSS JOIN part p ON p.message_id = m.id
  WHERE m.time_created >= ?
    AND p.time_created >= ?
    AND json_extract(p.data, '$.type') = 'tool'
    AND %s
  GROUP BY COALESCE(json_extract(p.data, '$.callID'), p.id)
);`, partClause)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = sqlitecli.WithReadOnly(ctx, dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, modelsSQL, startOfTodayMs)
		if err != nil {
			return err
		}

		var models []DailyModelStat
		var turnsCount int
		var totalTok, inTok, outTok, reasonTok, cacheReadTok, cacheWriteTok, latestTs int64
		var totalCost float64
		for rows.Next() {
			var m DailyModelStat
			var maxTs int64
			if err := rows.Scan(&m.ProviderID, &m.ModelID, &m.Turns, &m.TotalTokens, &m.InputTokens, &m.OutputTokens, &m.ReasoningTokens, &m.CacheReadTokens, &m.CacheWriteTokens, &m.Cost, &maxTs); err != nil {
				_ = rows.Close()
				return err
			}
			turnsCount += m.Turns
			totalTok += m.TotalTokens
			inTok += m.InputTokens
			outTok += m.OutputTokens
			reasonTok += m.ReasoningTokens
			cacheReadTok += m.CacheReadTokens
			cacheWriteTok += m.CacheWriteTokens
			totalCost += m.Cost
			if maxTs > latestTs {
				latestTs = maxTs
			}
			models = append(models, m)
		}
		rErr := rows.Err()
		_ = rows.Close()
		if rErr != nil {
			return rErr
		}

		var sessCount, toolsTotal, toolsFailed int
		if err := db.QueryRowContext(ctx, sessionsSQL, startOfTodayMs).Scan(&sessCount); err != nil {
			return err
		}
		_ = db.QueryRowContext(ctx, toolsSQL, startOfTodayMs-partMessageLookbackMs, startOfTodayMs).Scan(&toolsTotal, &toolsFailed)

		res.DbSessionsCount = sessCount
		res.DbToolCallsTotal = toolsTotal
		res.DbToolCallsFailed = toolsFailed
		res.DbTurnsCount += turnsCount
		res.DbTotalTokens += totalTok
		res.DbInputTokens += inTok
		res.DbOutputTokens += outTok
		res.DbReasoningTokens += reasonTok
		res.DbCacheReadTokens += cacheReadTok
		res.DbCacheWriteTokens += cacheWriteTok
		res.DbTotalCost += totalCost
		if latestTs > res.DbLatestTurnTs {
			res.DbLatestTurnTs = latestTs
		}
		res.DbModels = append(res.DbModels, models...)
		return nil
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
