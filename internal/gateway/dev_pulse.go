package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	maxUntrackedFilesToCount = 24
	maxUntrackedFileSize     = 64 * 1024
)

type dailyModelStat struct {
	ProviderID  string  `json:"providerID"`
	ModelID     string  `json:"modelID"`
	Turns       int     `json:"turns"`
	TotalTokens int64   `json:"totalTokens"`
	Cost        float64 `json:"cost"`
}

type gitDailyPulseResponse struct {
	TodayCommits         int              `json:"todayCommits"`
	CommittedAdditions   int              `json:"committedAdditions"`
	CommittedDeletions   int              `json:"committedDeletions"`
	CommittedFiles       int              `json:"committedFiles"`
	UncommittedAdditions int              `json:"uncommittedAdditions"`
	UncommittedDeletions int              `json:"uncommittedDeletions"`
	UncommittedFiles     int              `json:"uncommittedFiles"`
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

var (
	pulseCacheMu   sync.Mutex
	cachedPulseDir string
	cachedPulseAt  time.Time
	cachedPulseRes gitDailyPulseResponse
)

const devPulseCacheTTL = 15 * time.Second

func (g *Gateway) handleDevPulse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	workDir := g.rootDir
	if g.worktrees != nil {
		if rt := g.worktrees.RootDir(); rt != "" {
			workDir = rt
		}
	}
	if dir != "" {
		workDir = dir
	}

	now := time.Now()
	pulseCacheMu.Lock()
	if cachedPulseDir == workDir && !cachedPulseAt.IsZero() && now.Sub(cachedPulseAt) < devPulseCacheTTL {
		cached := cachedPulseRes
		pulseCacheMu.Unlock()
		writeJSON(w, http.StatusOK, cached)
		return
	}
	pulseCacheMu.Unlock()

	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfTodayMs := startOfToday.UnixMilli()

	res := gitDailyPulseResponse{
		DbModels: []dailyModelStat{},
	}

	// 1. Query authoritative OpenCode SQLite DB for today's exact token & cost usage
	queryOpenCodeDailyDB(startOfTodayMs, &res)

	// 2. Query Git repository for today's source code additions & deletions
	if workDir != "" {
		collectGitDailyStats(workDir, startOfToday, &res)
	}

	pulseCacheMu.Lock()
	cachedPulseDir = workDir
	cachedPulseAt = time.Now()
	cachedPulseRes = res
	pulseCacheMu.Unlock()

	writeJSON(w, http.StatusOK, res)
}

func collectGitDailyStats(workDir string, startOfToday time.Time, res *gitDailyPulseResponse) {
	sinceArg := "--since=" + startOfToday.Format(time.RFC3339)
	if logOut, err := worktree.RunGitRaw(workDir, "log", sinceArg, "--no-merges", "--numstat", "--pretty=format:COMMIT:%H"); err == nil {
		committedFileSet := make(map[string]struct{})
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
			if untrackedReadCount >= maxUntrackedFilesToCount {
				continue
			}
			full := filepath.Join(workDir, rel)
			if info, err := os.Stat(full); err == nil && !info.IsDir() && info.Size() <= maxUntrackedFileSize {
				untrackedReadCount++
				if data, err := os.ReadFile(full); err == nil && len(data) > 0 && bytes.IndexByte(data, 0) == -1 {
					cnt := bytes.Count(data, []byte{'\n'})
					if data[len(data)-1] != '\n' {
						cnt++
					}
					res.UncommittedAdditions += cnt
				}
			}
		}
	}
	res.UncommittedFiles = len(uncommittedFileSet)
}

func queryOpenCodeDailyDB(startOfTodayMs int64, res *gitDailyPulseResponse) {
	dbPath := handoff.OpenCodeDataDBPath()
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
          AND s.title NOT LIKE '[Grok CLI]%%'
          AND s.title NOT LIKE '[Claude Code]%%'
          AND s.title NOT LIKE '[Codex%%'
          AND (
            CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) > 0
            OR CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) > 0
            OR CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) > 0
          )
        GROUP BY
          m.time_created,
          COALESCE(json_extract(m.data, '$.modelID'), ''),
          CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER),
          CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER)
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
      AND s.title NOT LIKE '[Grok CLI]%%'
      AND s.title NOT LIKE '[Claude Code]%%'
      AND s.title NOT LIKE '[Codex%%'
      AND s.title NOT LIKE '%%(fork #%%'
  )
);`, startOfTodayMs, startOfTodayMs)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-cmd", ".timeout 5000", dbPath, sqlQuery)
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
			ProviderID:  m.ProviderID,
			ModelID:     m.ModelID,
			Turns:       m.Turns,
			TotalTokens: m.TotalTokens,
			Cost:        m.Cost,
		})
	}
}
