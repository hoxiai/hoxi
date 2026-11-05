package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	// Untracked files are counted by reading them, so the scan is bounded by a
	// byte budget rather than a small file count: a handful of generated files
	// must not silently hide the rest of an agent's day.
	maxUntrackedFilesToCount = 500
	maxUntrackedFileSize     = 64 * 1024
	maxUntrackedTotalBytes   = 4 << 20
)

type dailyModelStat struct {
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

type gitDailyPulseResponse struct {
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
	DbModels             []dailyModelStat `json:"dbModels"`
}

func isGeneratedOrLockFile(relPath string) bool {
	p := filepath.ToSlash(strings.TrimSpace(relPath))
	if p == "" {
		return true
	}
	ignoredPrefixes := []string{
		".hoxi/",
		"web/dist/",
		"dist/",
		"node_modules/",
		"vendor/",
		".next/",
		"build/bin/",
	}
	for _, pref := range ignoredPrefixes {
		if strings.HasPrefix(p, pref) || strings.Contains(p, "/"+pref) {
			return true
		}
	}
	base := filepath.Base(p)
	ignoredNames := []string{
		"package-lock.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"bun.lockb",
		"go.sum",
		"Cargo.lock",
	}
	for _, name := range ignoredNames {
		if base == name {
			return true
		}
	}
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") || strings.HasSuffix(base, ".map") {
		return true
	}
	return false
}

type devPulseEntry struct {
	at  time.Time
	res gitDailyPulseResponse
}

type devPulseFlight struct {
	wg  sync.WaitGroup
	res gitDailyPulseResponse
}

type devPulseCache struct {
	mu           sync.Mutex
	entries      map[string]devPulseEntry
	inflight     map[string]*devPulseFlight
	usageAt      time.Time
	usageStartMs int64
	usageKey     requestLogKey
	usageRes     gitDailyPulseResponse
}

const (
	devPulseCacheTTL        = 15 * time.Second
	maxDevPulseCacheEntries = 16
	partMessageLookbackMs   = 24 * 60 * 60 * 1000
)

func (g *Gateway) handleDevPulse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
	workDir, extraWorktreeDir := g.resolvePulseGitDirs(dir, sessionID)
	cacheKey := workDir
	if extraWorktreeDir != "" {
		cacheKey = workDir + "\x00" + extraWorktreeDir
	}

	forced := strings.TrimSpace(r.URL.Query().Get("refresh")) != ""

	now := time.Now()
	g.pulseCache.mu.Lock()
	if g.pulseCache.entries == nil {
		g.pulseCache.entries = make(map[string]devPulseEntry)
	}
	if g.pulseCache.inflight == nil {
		g.pulseCache.inflight = make(map[string]*devPulseFlight)
	}
	if !forced {
		if entry, ok := g.pulseCache.entries[cacheKey]; ok && !entry.at.IsZero() && now.Sub(entry.at) < devPulseCacheTTL {
			g.pulseCache.mu.Unlock()
			writeJSON(w, http.StatusOK, entry.res)
			return
		}
		if flight, ok := g.pulseCache.inflight[cacheKey]; ok {
			g.pulseCache.mu.Unlock()
			flight.wg.Wait()
			writeJSON(w, http.StatusOK, flight.res)
			return
		}
	}
	var flight *devPulseFlight
	if !forced {
		flight = &devPulseFlight{}
		flight.wg.Add(1)
		g.pulseCache.inflight[cacheKey] = flight
	}
	g.pulseCache.mu.Unlock()

	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfTodayMs := startOfToday.UnixMilli()

	res := gitDailyPulseResponse{
		DbModels: []dailyModelStat{},
	}

	// 1 & 2. Global daily token/tool usage from OpenCode SQLite DB and native
	// CLI runtimes (independent of workDir; cached across worktree switches).
	g.collectDailyUsage(now, startOfTodayMs, forced, &res)

	// 3. Query Git repository for today's source code additions & deletions
	if workDir != "" {
		collectGitDailyStats(workDir, startOfToday, &res, extraWorktreeDir)
	}

	g.pulseCache.mu.Lock()
	if len(g.pulseCache.entries) >= maxDevPulseCacheEntries {
		var oldestKey string
		var oldestAt time.Time
		for k, v := range g.pulseCache.entries {
			if now.Sub(v.at) >= devPulseCacheTTL {
				delete(g.pulseCache.entries, k)
				continue
			}
			if oldestKey == "" || v.at.Before(oldestAt) {
				oldestKey = k
				oldestAt = v.at
			}
		}
		if len(g.pulseCache.entries) >= maxDevPulseCacheEntries && oldestKey != "" {
			delete(g.pulseCache.entries, oldestKey)
		}
	}
	g.pulseCache.entries[cacheKey] = devPulseEntry{at: time.Now(), res: res}
	if flight != nil {
		flight.res = res
		delete(g.pulseCache.inflight, cacheKey)
		flight.wg.Done()
	}
	g.pulseCache.mu.Unlock()

	writeJSON(w, http.StatusOK, res)
}

func copyDailyUsage(dst *gitDailyPulseResponse, src gitDailyPulseResponse) {
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
	dst.DbModels = make([]dailyModelStat, len(src.DbModels))
	copy(dst.DbModels, src.DbModels)
}

func (g *Gateway) collectDailyUsage(now time.Time, startOfTodayMs int64, forced bool, res *gitDailyPulseResponse) {
	sourceKey := g.requestLogSourceKey()
	if !forced {
		g.pulseCache.mu.Lock()
		if !g.pulseCache.usageAt.IsZero() &&
			g.pulseCache.usageStartMs == startOfTodayMs &&
			(now.Sub(g.pulseCache.usageAt) < devPulseCacheTTL || g.pulseCache.usageKey == sourceKey) {
			copyDailyUsage(res, g.pulseCache.usageRes)
			g.pulseCache.mu.Unlock()
			return
		}
		g.pulseCache.mu.Unlock()
	}

	usage := gitDailyPulseResponse{
		DbModels: []dailyModelStat{},
	}
	queryOpenCodeDailyDB(startOfTodayMs, &usage)
	g.collectRunnerDailyUsage(startOfTodayMs, &usage)

	g.pulseCache.mu.Lock()
	g.pulseCache.usageAt = time.Now()
	g.pulseCache.usageStartMs = startOfTodayMs
	g.pulseCache.usageKey = sourceKey
	g.pulseCache.usageRes = usage
	g.pulseCache.mu.Unlock()

	copyDailyUsage(res, usage)
}

// sessionPulseDirectory returns the directory a session actually works in, or
// "" when the session is unknown or its directory is unavailable.
func (g *Gateway) sessionPulseDirectory(sessionID string) string {
	dir, _ := g.sessionPulseDirectories(sessionID)
	return dir
}

func (g *Gateway) sessionPulseDirectories(sessionID string) (workDir, projectDir string) {
	if sessionID == "" {
		return "", ""
	}
	if g.runner != nil {
		if rec, ok := g.runner.Store().Meta(sessionID); ok {
			proj := strings.TrimSpace(rec.ProjectDir)
			if proj != "" {
				if info, err := os.Stat(proj); err == nil && info.IsDir() {
					projectDir = proj
				}
			}
			dir := strings.TrimSpace(rec.WorktreePath)
			if dir == "" {
				dir = proj
			}
			if dir != "" {
				if info, err := os.Stat(dir); err == nil && info.IsDir() {
					return dir, projectDir
				}
				return projectDir, projectDir
			}
		}
	}
	if g.worktrees != nil {
		if b, ok, err := g.worktrees.BindingForSession(sessionID); err == nil && ok && b.Directory != "" {
			if info, err := os.Stat(b.Directory); err == nil && info.IsDir() {
				return b.Directory, hoxipaths.ProjectRootForWorktree(b.Directory)
			}
		}
	}
	return "", ""
}

// collectRunnerDailyUsage folds native-runtime token usage into the pulse.
// OpenCode sessions are skipped: their turns mirror hoxi.db, which the SQLite
// step already counted, so adding them here would double count.
func (g *Gateway) collectRunnerDailyUsage(startOfTodayMs int64, res *gitDailyPulseResponse) {
	if g.runner == nil {
		return
	}
	store := g.runner.Store()
	// key -> index into res.DbModels; append may reallocate, so never keep raw
	// pointers into the slice.
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
	for _, info := range store.List("") {
		if info.Time.Updated < startOfTodayMs {
			continue
		}
		rec, ok := store.Meta(info.ID)
		if !ok || rec.Subagent {
			continue
		}
		hasNativeTurnToday := false
		hasOpenCodeTurnToday := false
		store.VisitTurnsSummary(rec.ID, func(full runner.SessionRecord) {
			for i := range full.Turns {
				turn := full.Turns[i]
				if turn.Info.Role != "assistant" || turn.Info.Time.Created < startOfTodayMs {
					continue
				}
				providerID, isNative := nativeTurnProvider(turn, full)
				if !isNative {
					hasOpenCodeTurnToday = true
					continue
				}
				hasNativeTurnToday = true
				for _, part := range turn.Parts {
					if part.Type != "tool" {
						continue
					}
					toolsTotal++
					if part.State != nil && part.State.Status == "error" {
						toolsFailed++
					}
				}
				tokens := turn.Info.Tokens
				if tokens == nil {
					continue
				}
				if tokens.Input == 0 && tokens.Output == 0 && tokens.Reasoning == 0 && tokens.Cache.Read == 0 && tokens.Cache.Write == 0 {
					continue
				}
				turns++
				turnTs := turn.Info.Time.Created
				if turnTs > latestTurnTs {
					latestTurnTs = turnTs
				}
				input += int64(tokens.Input)
				output += int64(tokens.Output)
				reasoning += int64(tokens.Reasoning)
				cacheRead += float64(tokens.Cache.Read)
				cacheWrite += float64(tokens.Cache.Write)
				cost += turn.Info.Cost

				mergeDailyModel(res, index, dailyModelUsage{
					ProviderID: providerID,
					ModelID:    firstNonEmptyString(turn.Info.ModelID, full.ModelID, "unknown"),
					Tokens:     *tokens,
					Cost:       turn.Info.Cost,
				})
			}
		})
		if hasNativeTurnToday && !hasOpenCodeTurnToday {
			sessions++
		}
	}
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

// nativeTurnProvider reports whether turn was executed by a native CLI runtime
// (as opposed to OpenCode, whose turns already live in hoxi.db) and returns its
// runtime/provider identifier.
func nativeTurnProvider(turn runner.MessageTurn, rec runner.SessionRecord) (string, bool) {
	prov := strings.TrimSpace(turn.Info.ProviderID)
	rt := strings.TrimSpace(rec.Runtime)
	if runner.IsValidRuntime(prov) {
		return prov, true
	}
	if strings.HasPrefix(turn.Info.ID, "msg_a_") {
		if prov != "" && prov != "opencode" {
			return prov, true
		}
		if runner.IsValidRuntime(rt) {
			return rt, true
		}
		if p := strings.TrimSpace(rec.ProviderID); runner.IsValidRuntime(p) {
			return p, true
		}
		return firstNonEmptyString(prov, "native"), true
	}
	if len(rec.OpenCodeIDs) == 0 && runner.IsValidRuntime(rt) {
		return firstNonEmptyString(prov, rt), true
	}
	return "", false
}

type dailyModelUsage struct {
	ProviderID string
	ModelID    string
	Tokens     runner.MessageTokens
	Cost       float64
}

func mergeDailyModel(res *gitDailyPulseResponse, index map[string]int, u dailyModelUsage) {
	key := u.ProviderID + "/" + u.ModelID
	at, ok := index[key]
	if !ok {
		res.DbModels = append(res.DbModels, dailyModelStat{
			ProviderID: u.ProviderID,
			ModelID:    u.ModelID,
		})
		at = len(res.DbModels) - 1
		index[key] = at
	}
	m := &res.DbModels[at]
	m.Turns++
	m.InputTokens += int64(u.Tokens.Input)
	m.OutputTokens += int64(u.Tokens.Output)
	m.ReasoningTokens += int64(u.Tokens.Reasoning)
	m.CacheReadTokens += int64(u.Tokens.Cache.Read)
	m.CacheWriteTokens += int64(u.Tokens.Cache.Write)
	m.TotalTokens += int64(u.Tokens.Input + u.Tokens.Output + u.Tokens.Reasoning + u.Tokens.Cache.Read + u.Tokens.Cache.Write)
	m.Cost += u.Cost
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseGitNumstatLine(rawLine string) (int, int, string, bool) {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return 0, 0, "", false
	}
	var addStr, delStr, file string
	if parts := strings.SplitN(line, "\t", 3); len(parts) == 3 {
		addStr = strings.TrimSpace(parts[0])
		delStr = strings.TrimSpace(parts[1])
		file = strings.TrimSpace(parts[2])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return 0, 0, "", false
		}
		addStr = fields[0]
		delStr = fields[1]
		file = fields[len(fields)-1]
	}
	file = strings.Trim(file, "\"")
	if file == "" || isGeneratedOrLockFile(file) {
		return 0, 0, "", false
	}
	add, _ := strconv.Atoi(addStr)
	del, _ := strconv.Atoi(delStr)
	return add, del, file, true
}

func collectGitDailyStats(workDir string, startOfToday time.Time, res *gitDailyPulseResponse, extraDirs ...string) {
	committedFileSet := make(map[string]struct{})
	seenCommits := make(map[string]struct{})
	sinceArg := "--since=" + startOfToday.Format(time.RFC3339)

	dirs := make([]string, 0, 1+len(extraDirs))
	if strings.TrimSpace(workDir) != "" {
		dirs = append(dirs, workDir)
	}
	for _, d := range extraDirs {
		if strings.TrimSpace(d) != "" && !samePulseDir(workDir, d) {
			dirs = append(dirs, d)
		}
	}

	for idx, dir := range dirs {
		logOut, err := worktree.RunGitRaw(dir, "log", sinceArg, "--no-merges", "--no-renames", "--numstat", "--pretty=format:COMMIT:%H\t%s")
		if err != nil {
			continue
		}
		skipCurrent := false
		for _, rawLine := range strings.Split(logOut, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "COMMIT:") {
				rest := strings.TrimPrefix(line, "COMMIT:")
				parts := strings.SplitN(rest, "\t", 2)
				sha := strings.TrimSpace(parts[0])
				var subject string
				if len(parts) > 1 {
					subject = strings.TrimSpace(parts[1])
				}
				if sha != "" {
					if _, exists := seenCommits[sha]; exists {
						skipCurrent = true
						continue
					}
					seenCommits[sha] = struct{}{}
				}
				if idx > 0 && isInternalSnapshotCommitSubject(subject) {
					skipCurrent = true
					continue
				}
				skipCurrent = false
				res.TodayCommits++
				continue
			}
			if skipCurrent {
				continue
			}
			add, del, file, ok := parseGitNumstatLine(rawLine)
			if !ok {
				continue
			}
			res.CommittedAdditions += add
			res.CommittedDeletions += del
			committedFileSet[file] = struct{}{}
		}
	}
	res.CommittedFiles = len(committedFileSet)

	uncommittedFileSet := make(map[string]struct{})
	var budget untrackedBudget
	for _, dir := range dirs {
		add, del, branch := collectGitUncommittedInto(dir, uncommittedFileSet, &budget)
		res.UncommittedAdditions += add
		res.UncommittedDeletions += del
		if branch != "" {
			res.Branch = branch
		}
	}
	res.UncommittedFiles = len(uncommittedFileSet)
	res.UncommittedTruncated = budget.truncated

	changed := make(map[string]struct{}, len(committedFileSet)+len(uncommittedFileSet))
	for file := range committedFileSet {
		changed[file] = struct{}{}
	}
	for file := range uncommittedFileSet {
		changed[file] = struct{}{}
	}
	res.ChangedFiles = len(changed)
}

func queryOpenCodeDailyDB(startOfTodayMs int64, res *gitDailyPulseResponse) {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}

	sqlQuery := fmt.Sprintf(`
SELECT json_object(
  'models', COALESCE((
    SELECT json_group_array(json_object(
      'providerID', provider_id,
      'modelID', model_id,
      'turns', turns,
      'totalTokens', total_tokens,
      'inputTokens', input_tokens,
      'outputTokens', output_tokens,
      'reasoningTokens', reasoning_tokens,
      'cacheReadTokens', cache_read_tokens,
      'cacheWriteTokens', cache_write_tokens,
      'cost', total_cost,
      'maxTs', max_ts
    ))
    FROM (
      SELECT
        provider_id,
        model_id,
        COUNT(*) as turns,
        SUM(tok_in + tok_out + tok_reason + cache_read + cache_write) as total_tokens,
        SUM(tok_in) as input_tokens,
        SUM(tok_out) as output_tokens,
        SUM(tok_reason) as reasoning_tokens,
        SUM(cache_read) as cache_read_tokens,
        SUM(cache_write) as cache_write_tokens,
        SUM(cost) as total_cost,
        MAX(msg_ts) as max_ts
      FROM (
        SELECT
          COALESCE(json_extract(m.data, '$.providerID'), 'unknown') as provider_id,
          COALESCE(json_extract(m.data, '$.modelID'), 'unknown') as model_id,
          m.time_created as msg_ts,
          CAST(COALESCE(json_extract(m.data, '$.cost'), 0) AS REAL) as cost,
          CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) as tok_in,
          CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) as tok_out,
          CAST(COALESCE(json_extract(m.data, '$.tokens.reasoning'), 0) AS INTEGER) as tok_reason,
          CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) as cache_read,
          CAST(COALESCE(json_extract(m.data, '$.tokens.cache.write'), 0) AS INTEGER) as cache_write
        FROM session s
        CROSS JOIN message m ON m.session_id = s.id
        WHERE m.time_created >= %d
          AND json_extract(m.data, '$.role') = 'assistant'
          AND %s
          AND (
            CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) > 0
            OR CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) > 0
            OR CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) > 0
          )
      )
      GROUP BY provider_id, model_id
      ORDER BY total_tokens DESC
    )
  ), json('[]')),
  'sessionsCount', (
    SELECT COUNT(DISTINCT s.id)
    FROM session s
    CROSS JOIN message m ON m.session_id = s.id
    WHERE m.time_created >= %d
      AND %s
  ),
  'toolsTotal', (
    SELECT COUNT(*) FROM (
      SELECT 1 FROM session s
      CROSS JOIN message m ON m.session_id = s.id
      CROSS JOIN part p ON p.message_id = m.id
      WHERE m.time_created >= %d
        AND p.time_created >= %d
        AND json_extract(p.data, '$.type') = 'tool'
        AND %s
      GROUP BY COALESCE(json_extract(p.data, '$.callID'), p.id)
    )
  ),
  'toolsFailed', (
    SELECT COUNT(*) FROM (
      SELECT 1 FROM session s
      CROSS JOIN message m ON m.session_id = s.id
      CROSS JOIN part p ON p.message_id = m.id
      WHERE m.time_created >= %d
        AND p.time_created >= %d
        AND json_extract(p.data, '$.type') = 'tool'
        AND json_extract(p.data, '$.state.status') = 'error'
        AND %s
      GROUP BY COALESCE(json_extract(p.data, '$.callID'), p.id)
    )
  )
);`,
		startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "m.data", "m.time_created", "s.time_created"),
		startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "m.data", "m.time_created", "s.time_created"),
		startOfTodayMs-partMessageLookbackMs, startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "p.data", "p.time_created", "s.time_created"),
		startOfTodayMs-partMessageLookbackMs, startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "p.data", "p.time_created", "s.time_created"))

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 5000", dbPath, sqlQuery)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return
	}

	var parsed struct {
		Models []struct {
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
			MaxTs            int64   `json:"maxTs"`
		} `json:"models"`
		SessionsCount int `json:"sessionsCount"`
		ToolsTotal    int `json:"toolsTotal"`
		ToolsFailed   int `json:"toolsFailed"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &parsed); err != nil {
		return
	}

	res.DbSessionsCount = parsed.SessionsCount
	res.DbToolCallsTotal = parsed.ToolsTotal
	res.DbToolCallsFailed = parsed.ToolsFailed

	for _, m := range parsed.Models {
		res.DbTurnsCount += m.Turns
		res.DbTotalTokens += m.TotalTokens
		res.DbInputTokens += m.InputTokens
		res.DbOutputTokens += m.OutputTokens
		res.DbReasoningTokens += m.ReasoningTokens
		res.DbCacheReadTokens += m.CacheReadTokens
		res.DbCacheWriteTokens += m.CacheWriteTokens
		res.DbTotalCost += m.Cost
		if m.MaxTs > res.DbLatestTurnTs {
			res.DbLatestTurnTs = m.MaxTs
		}
		res.DbModels = append(res.DbModels, dailyModelStat{
			ProviderID:       m.ProviderID,
			ModelID:          m.ModelID,
			Turns:            m.Turns,
			TotalTokens:      m.TotalTokens,
			InputTokens:      m.InputTokens,
			OutputTokens:     m.OutputTokens,
			ReasoningTokens:  m.ReasoningTokens,
			CacheReadTokens:  m.CacheReadTokens,
			CacheWriteTokens: m.CacheWriteTokens,
			Cost:             m.Cost,
		})
	}
}
