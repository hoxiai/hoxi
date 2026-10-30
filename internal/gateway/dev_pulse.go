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
		"build/",
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

type devPulseCache struct {
	mu  sync.Mutex
	dir string
	at  time.Time
	res gitDailyPulseResponse
}

const devPulseCacheTTL = 15 * time.Second

func (g *Gateway) handleDevPulse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
	workDir := g.rootDir
	if g.worktrees != nil {
		if rt := g.worktrees.RootDir(); rt != "" {
			workDir = rt
		}
	}
	if dir != "" {
		workDir = dir
	}
	// A sandboxed session commits inside its own worktree, so the pulse must
	// read code velocity from that worktree rather than the project root.
	if sessionID != "" && g.sessionPulseDirectory(sessionID) != "" {
		workDir = g.sessionPulseDirectory(sessionID)
	}

	forced := strings.TrimSpace(r.URL.Query().Get("refresh")) != ""

	now := time.Now()
	g.pulseCache.mu.Lock()
	if !forced && g.pulseCache.dir == workDir && !g.pulseCache.at.IsZero() && now.Sub(g.pulseCache.at) < devPulseCacheTTL {
		cached := g.pulseCache.res
		g.pulseCache.mu.Unlock()
		writeJSON(w, http.StatusOK, cached)
		return
	}
	g.pulseCache.mu.Unlock()

	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfTodayMs := startOfToday.UnixMilli()

	res := gitDailyPulseResponse{
		DbModels: []dailyModelStat{},
	}

	// 1. Query authoritative OpenCode SQLite DB for today's exact token & cost usage
	queryOpenCodeDailyDB(startOfTodayMs, &res)

	// 2. Native CLI runtimes (Claude Code, Antigravity, Qoder, CodeBuddy) never
	// write to hoxi.db, so their usage is only in the runner transcript store.
	g.collectRunnerDailyUsage(startOfTodayMs, &res)

	// 3. Query Git repository for today's source code additions & deletions
	if workDir != "" {
		collectGitDailyStats(workDir, startOfToday, &res)
	}

	g.pulseCache.mu.Lock()
	g.pulseCache.dir = workDir
	g.pulseCache.at = time.Now()
	g.pulseCache.res = res
	g.pulseCache.mu.Unlock()

	writeJSON(w, http.StatusOK, res)
}

// sessionPulseDirectory returns the directory a session actually works in, or
// "" when the session is unknown or its directory is unavailable.
func (g *Gateway) sessionPulseDirectory(sessionID string) string {
	if g.runner == nil {
		return ""
	}
	rec, ok := g.runner.Store().Meta(sessionID)
	if !ok {
		return ""
	}
	dir := strings.TrimSpace(rec.WorktreePath)
	if dir == "" {
		dir = strings.TrimSpace(rec.ProjectDir)
	}
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
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
		store.VisitTurnsTransient(rec.ID, func(full runner.SessionRecord) {
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

func collectGitDailyStats(workDir string, startOfToday time.Time, res *gitDailyPulseResponse) {
	committedFileSet := make(map[string]struct{})
	sinceArg := "--since=" + startOfToday.Format(time.RFC3339)
	if logOut, err := worktree.RunGitRaw(workDir, "log", sinceArg, "--no-merges", "--numstat", "--pretty=format:COMMIT:%H"); err == nil {
		for _, rawLine := range strings.Split(logOut, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "COMMIT:") {
				res.TodayCommits++
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
			add, errAdd := strconv.Atoi(fields[0])
			del, errDel := strconv.Atoi(fields[1])
			if errAdd == nil {
				res.CommittedAdditions += add
			}
			if errDel == nil {
				res.CommittedDeletions += del
			}
			committedFileSet[file] = struct{}{}
		}
		res.CommittedFiles = len(committedFileSet)
	}

	uncommittedFileSet := make(map[string]struct{})
	if numstat, err := worktree.RunGit(workDir, "diff", "--numstat", "HEAD"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			file := fields[len(fields)-1]
			if isGeneratedOrLockFile(file) {
				continue
			}
			if a, err := strconv.Atoi(fields[0]); err == nil {
				res.UncommittedAdditions += a
			}
			if d, err := strconv.Atoi(fields[1]); err == nil {
				res.UncommittedDeletions += d
			}
			uncommittedFileSet[file] = struct{}{}
		}
	}

	untrackedReadCount := 0
	untrackedReadBytes := int64(0)
	untrackedTruncated := false
	if porcelain, err := worktree.RunGitRaw(workDir, "status", "--porcelain", "-b"); err == nil {
		for _, rawLine := range strings.Split(porcelain, "\n") {
			if strings.HasPrefix(rawLine, "## ") {
				if br := worktree.ParseBranchFromPorcelainHeader(rawLine); br != "" {
					res.Branch = br
				}
				continue
			}
			if !strings.HasPrefix(rawLine, "?? ") {
				continue
			}
			rel := strings.TrimSpace(rawLine[3:])
			if isGeneratedOrLockFile(rel) {
				continue
			}
			uncommittedFileSet[rel] = struct{}{}
			if untrackedTruncated || untrackedReadCount >= maxUntrackedFilesToCount {
				untrackedTruncated = true
				continue
			}
			full := filepath.Join(workDir, rel)
			info, err := os.Stat(full)
			if err != nil || info.IsDir() || info.Size() > maxUntrackedFileSize {
				continue
			}
			if untrackedReadBytes+info.Size() > maxUntrackedTotalBytes {
				untrackedTruncated = true
				continue
			}
			data, err := os.ReadFile(full)
			if err != nil || len(data) == 0 || bytes.IndexByte(data, 0) != -1 {
				continue
			}
			untrackedReadCount++
			untrackedReadBytes += int64(len(data))
			cnt := bytes.Count(data, []byte{'\n'})
			if data[len(data)-1] != '\n' {
				cnt++
			}
			res.UncommittedAdditions += cnt
		}
	}
	res.UncommittedFiles = len(uncommittedFileSet)
	res.UncommittedTruncated = untrackedTruncated

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
        FROM message m
        JOIN session s ON s.id = m.session_id
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
    JOIN message m ON m.session_id = s.id
    WHERE m.time_created >= %d
      AND %s
  ),
  'toolsTotal', (
    SELECT COUNT(*) FROM (
      SELECT 1 FROM part p
      JOIN session s ON s.id = p.session_id
      WHERE p.time_created >= %d
        AND json_extract(p.data, '$.type') = 'tool'
        AND %s
      GROUP BY COALESCE(json_extract(p.data, '$.callID'), p.id)
    )
  ),
  'toolsFailed', (
    SELECT COUNT(*) FROM (
      SELECT 1 FROM part p
      JOIN session s ON s.id = p.session_id
      WHERE p.time_created >= %d
        AND json_extract(p.data, '$.type') = 'tool'
        AND json_extract(p.data, '$.state.status') = 'error'
        AND %s
      GROUP BY COALESCE(json_extract(p.data, '$.callID'), p.id)
    )
  )
);`,
		startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "m.data", "m.time_created", "s.time_created"),
		startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "m.data", "m.time_created", "s.time_created"),
		startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "p.data", "p.time_created", "s.time_created"),
		startOfTodayMs, handoff.SQLNotImportedTurnClause("s.title", "p.data", "p.time_created", "s.time_created"))

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
