package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	requestLogDefaultLimit = 200
	requestLogMaxLimit     = 500
	// requestLogPendingWindow bounds how long an unfinished call is treated as
	// in progress. A turn killed mid-flight never records a completion time, so
	// only recent ones are shown as running instead of stale forever.
	requestLogPendingWindow = 30 * time.Minute
	// requestLogMaxAge bounds how far back runner session transcripts are scanned.
	requestLogMaxAge = 7 * 24 * time.Hour
)

// requestLogEntry is one local model call. It carries timing and token counts
// only: prompts, completions and provider credentials stay out of this view.
type requestLogEntry struct {
	ID               string `json:"id"`
	SessionID        string `json:"sessionID"`
	Time             int64  `json:"time"`
	Completed        int64  `json:"completed,omitempty"`
	DurationMs       int64  `json:"durationMs"`
	FirstTokenMs     int64  `json:"firstTokenMs,omitempty"`
	ProviderID       string `json:"providerID"`
	Account          string `json:"account,omitempty"`
	ModelID          string `json:"modelID"`
	Client           string `json:"client"`
	Status           int    `json:"status,omitempty"`
	Level            string `json:"level"`
	Retry            int    `json:"retry,omitempty"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	// Cost is what the runtime recorded, if anything. The frontend prices from
	// the catalog when it is zero, so this is only a reported figure.
	Cost  float64 `json:"cost,omitempty"`
	Error string  `json:"error,omitempty"`
}

type requestLogResponse struct {
	Entries  []requestLogEntry `json:"entries"`
	Total    int               `json:"total"`
	Local    bool              `json:"local"`
	Timezone string            `json:"timezone"`
}

// handleRequestLog lists recent model calls made on this computer. The phone
// never sees it: the route is absent from the tunnel allow-list.
func (g *Gateway) handleRequestLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	limit := requestLogDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > requestLogMaxLimit {
			n = requestLogMaxLimit
		}
		limit = n
	}

	entries := g.collectRequestLog()
	total := len(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	writeJSON(w, http.StatusOK, requestLogResponse{
		Entries:  entries,
		Total:    total,
		Local:    true,
		Timezone: time.Now().Location().String(),
	})
}

func (g *Gateway) collectRequestLog() []requestLogEntry {
	now := time.Now()
	cutoffMs := now.Add(-requestLogPendingWindow).UnixMilli()
	earliestMs := now.Add(-requestLogMaxAge).UnixMilli()
	entries := queryOpenCodeRequestLog(cutoffMs)
	if g.runner == nil {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time > entries[j].Time })
		return entries
	}
	store := g.runner.Store()
	for _, info := range store.List("") {
		if info.Time.Updated > 0 && info.Time.Updated < earliestMs {
			continue
		}
		if len(entries) >= requestLogMaxLimit {
			sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time > entries[j].Time })
			entries = entries[:requestLogMaxLimit]
			if info.Time.Updated > 0 && info.Time.Updated < entries[len(entries)-1].Time && info.Time.Updated < cutoffMs {
				break
			}
		}
		rec, ok := store.Meta(info.ID)
		if !ok || rec.Subagent {
			continue
		}
		if rec.UpdatedAt > 0 && rec.UpdatedAt < earliestMs {
			continue
		}
		runtime := strings.TrimSpace(rec.Runtime)
		if runtime == "" || runtime == "opencode" {
			continue
		}
		store.VisitTurns(rec.ID, func(full runner.SessionRecord) {
			var maxCreated int64
			for i := range full.Turns {
				if full.Turns[i].Info.Time.Created > maxCreated {
					maxCreated = full.Turns[i].Info.Time.Created
				}
			}
			for i := range full.Turns {
				tInfo := full.Turns[i].Info
				isLatest := tInfo.Time.Created >= maxCreated && strings.TrimSpace(tInfo.Finish) == ""
				if entry, ok := requestLogFromTurn(full.Turns[i], rec, runtime, cutoffMs, isLatest); ok {
					entries = append(entries, entry)
				}
			}
		})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time > entries[j].Time })
	return entries
}

func requestLogFromTurn(turn runner.MessageTurn, rec runner.SessionRecord, runtime string, cutoffMs int64, isLatest bool) (requestLogEntry, bool) {
	info := turn.Info
	if info.Role != "assistant" {
		return requestLogEntry{}, false
	}
	tokens := info.Tokens
	hasTokens := tokens != nil && (tokens.Input > 0 || tokens.Output > 0 || tokens.Cache.Read > 0)
	failed := info.Error != nil
	inProgress := isLatest && info.Time.Completed == 0 && info.Time.Created >= cutoffMs
	if !hasTokens && !failed && !inProgress {
		return requestLogEntry{}, false
	}
	entry := requestLogEntry{
		ID:         info.ID,
		SessionID:  firstNonEmptyString(info.SessionID, rec.ID),
		Time:       info.Time.Created,
		Completed:  info.Time.Completed,
		DurationMs: requestDuration(info.Time),
		ProviderID: firstNonEmptyString(info.ProviderID, runtime),
		ModelID:    firstNonEmptyString(info.ModelID, rec.ModelID, "unknown"),
		Client:     "local",
		Level:      "info",
	}
	if tokens != nil {
		entry.InputTokens = int64(tokens.Input)
		entry.OutputTokens = int64(tokens.Output)
		entry.ReasoningTokens = int64(tokens.Reasoning)
		entry.CacheReadTokens = int64(tokens.Cache.Read)
		entry.CacheWriteTokens = int64(tokens.Cache.Write)
	}
	entry.Cost = info.Cost
	entry.FirstTokenMs = turnFirstTokenMs(turn, info.Time.Created)
	switch {
	case failed:
		entry.Level = "error"
		entry.Status = requestErrorStatus(info.Error)
		entry.Error = requestErrorText(info.Error)
	case inProgress:
		entry.Level = "pending"
	}
	return entry, true
}

// turnFirstTokenMs measures time to first visible token: the earliest text or
// reasoning part start after the assistant turn was created.
func turnFirstTokenMs(turn runner.MessageTurn, created int64) int64 {
	if created <= 0 {
		return 0
	}
	first := int64(0)
	for i := range turn.Parts {
		part := turn.Parts[i]
		if part.Type != "text" && part.Type != "reasoning" {
			continue
		}
		start := part.Time.Start
		if start <= 0 || start <= created {
			continue
		}
		if first == 0 || start < first {
			first = start
		}
	}
	if first == 0 {
		return 0
	}
	return first - created
}

func requestErrorStatus(err *runner.MessageError) int {
	if err == nil || err.Data == nil {
		return 0
	}
	return err.Data.StatusCode
}

func requestErrorText(err *runner.MessageError) string {
	if err == nil {
		return ""
	}
	if err.Data != nil && strings.TrimSpace(err.Data.Message) != "" {
		return trimLogText(err.Data.Message)
	}
	return trimLogText(err.Name)
}

var requestLogSecretPattern = regexp.MustCompile(`(?i)(bearer\s+\S+|(?:sk-|hk-|ghp_|github_pat_|sk-ant-)[a-z0-9_\-]+|(?:api[_-]?key|token|password|secret|authorization)\s*[=:]\s*[^\s,;]+)`)

func trimLogText(v string) string {
	if len(v) > 4096 {
		v = v[:4096]
	}
	v = requestLogSecretPattern.ReplaceAllString(v, "[redacted]")
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > 240 {
		v = v[:240]
	}
	return v
}

func queryOpenCodeRequestLog(cutoffMs int64) []requestLogEntry {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	sqlQuery := fmt.Sprintf(`
SELECT
  m.id,
  m.session_id,
  m.time_created,
  CAST(COALESCE(json_extract(m.data, '$.time.completed'), 0) AS INTEGER) AS completed,
  COALESCE(json_extract(m.data, '$.providerID'), 'opencode') AS provider_id,
  COALESCE(json_extract(m.data, '$.modelID'), 'unknown') AS model_id,
  CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) AS input,
  CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) AS output,
  CAST(COALESCE(json_extract(m.data, '$.tokens.reasoning'), 0) AS INTEGER) AS reasoning,
  CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) AS cache_read,
  CAST(COALESCE(json_extract(m.data, '$.tokens.cache.write'), 0) AS INTEGER) AS cache_write,
  CAST(COALESCE(json_extract(m.data, '$.cost'), 0) AS REAL) AS cost,
  CAST(COALESCE(json_extract(m.data, '$.error.data.statusCode'), 0) AS INTEGER) AS status,
  COALESCE(json_extract(m.data, '$.error.data.message'), json_extract(m.data, '$.error.name'), '') AS error,
  COALESCE((
    SELECT MIN(p.time_created) FROM part p
    WHERE p.message_id = m.id AND json_extract(p.data, '$.type') IN ('text', 'reasoning')
  ), 0) AS first_part
FROM message m
JOIN session s ON s.id = m.session_id
WHERE json_extract(m.data, '$.role') = 'assistant'
  AND (
    CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) > 0
    OR CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) > 0
    OR CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) > 0
    OR json_extract(m.data, '$.error') IS NOT NULL
    OR (json_extract(m.data, '$.time.completed') IS NULL AND m.time_created >= %d)
  )
ORDER BY m.time_created DESC
LIMIT %d;`, cutoffMs, requestLogMaxLimit)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-json", "-readonly", "-cmd", ".timeout 5000", dbPath, sqlQuery)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var rows []struct {
		ID         string  `json:"id"`
		SessionID  string  `json:"session_id"`
		Time       int64   `json:"time_created"`
		Completed  int64   `json:"completed"`
		ProviderID string  `json:"provider_id"`
		ModelID    string  `json:"model_id"`
		Input      int64   `json:"input"`
		Output     int64   `json:"output"`
		Reasoning  int64   `json:"reasoning"`
		CacheRead  int64   `json:"cache_read"`
		CacheWrite int64   `json:"cache_write"`
		Cost       float64 `json:"cost"`
		Status     int     `json:"status"`
		Error      string  `json:"error"`
		FirstPart  int64   `json:"first_part"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &rows); err != nil {
		return nil
	}
	entries := make([]requestLogEntry, 0, len(rows))
	for _, row := range rows {
		level := "info"
		switch {
		case strings.TrimSpace(row.Error) != "" || row.Status >= 400:
			level = "error"
		case row.Completed == 0:
			level = "pending"
		}
		entries = append(entries, requestLogEntry{
			ID: row.ID, SessionID: row.SessionID, Time: row.Time, Completed: row.Completed,
			DurationMs: durationFromMillis(row.Time, row.Completed), ProviderID: row.ProviderID,
			ModelID: row.ModelID, Client: "local", Status: row.Status, Level: level,
			InputTokens: row.Input, OutputTokens: row.Output, ReasoningTokens: row.Reasoning,
			CacheReadTokens: row.CacheRead, CacheWriteTokens: row.CacheWrite, Cost: row.Cost, Error: trimLogText(row.Error),
			FirstTokenMs: durationFromMillis(row.Time, row.FirstPart),
		})
	}
	return entries
}

func durationFromMillis(created, completed int64) int64 {
	if completed > created && created > 0 {
		return completed - created
	}
	return 0
}

func requestDuration(t runner.MessageTime) int64 {
	if t.Completed > t.Created && t.Created > 0 {
		return t.Completed - t.Created
	}
	return 0
}
