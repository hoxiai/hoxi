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
)

const (
	requestLogDefaultLimit = 200
	requestLogMaxLimit     = 500
)

// requestLogEntry is one local model call. It carries timing and token counts
// only: prompts, completions and provider credentials stay out of this view.
type requestLogEntry struct {
	ID               string `json:"id"`
	SessionID        string `json:"sessionID"`
	Time             int64  `json:"time"`
	Completed        int64  `json:"completed,omitempty"`
	DurationMs       int64  `json:"durationMs"`
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
	Error            string `json:"error,omitempty"`
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
	entries := queryOpenCodeRequestLog()
	if g.runner == nil {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time > entries[j].Time })
		return entries
	}
	store := g.runner.Store()
	for _, info := range store.List("") {
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
				if entry, ok := requestLogFromTurn(full.Turns[i], rec, runtime); ok {
					entries = append(entries, entry)
				}
			}
		})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time > entries[j].Time })
	return entries
}

func requestLogFromTurn(turn runner.MessageTurn, rec runner.SessionRecord, runtime string) (requestLogEntry, bool) {
	info := turn.Info
	if info.Role != "assistant" {
		return requestLogEntry{}, false
	}
	tokens := info.Tokens
	hasTokens := tokens != nil && (tokens.Input > 0 || tokens.Output > 0 || tokens.Cache.Read > 0)
	failed := info.Error != nil
	if !hasTokens && !failed {
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
	if failed {
		entry.Level = "error"
		entry.Status = requestErrorStatus(info.Error)
		entry.Error = requestErrorText(info.Error)
	} else if info.Time.Completed == 0 {
		entry.Level = "pending"
	}
	return entry, true
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

func trimLogText(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > 240 {
		v = v[:240]
	}
	return v
}

func queryOpenCodeRequestLog() []requestLogEntry {
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
  CAST(COALESCE(json_extract(m.data, '$.error.data.statusCode'), 0) AS INTEGER) AS status,
  COALESCE(json_extract(m.data, '$.error.data.message'), json_extract(m.data, '$.error.name'), '') AS error
FROM message m
JOIN session s ON s.id = m.session_id
WHERE json_extract(m.data, '$.role') = 'assistant'
  AND %s
  AND (
    CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER) > 0
    OR CAST(COALESCE(json_extract(m.data, '$.tokens.output'), 0) AS INTEGER) > 0
    OR CAST(COALESCE(json_extract(m.data, '$.tokens.cache.read'), 0) AS INTEGER) > 0
    OR json_extract(m.data, '$.error') IS NOT NULL
  )
ORDER BY m.time_created DESC
LIMIT %d;`, handoff.SQLNotImportedTitleClause("s.title"), requestLogMaxLimit)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-json", "-readonly", "-cmd", ".timeout 5000", dbPath, sqlQuery)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var rows []struct {
		ID         string `json:"id"`
		SessionID  string `json:"session_id"`
		Time       int64  `json:"time_created"`
		Completed  int64  `json:"completed"`
		ProviderID string `json:"provider_id"`
		ModelID    string `json:"model_id"`
		Input      int64  `json:"input"`
		Output     int64  `json:"output"`
		Reasoning  int64  `json:"reasoning"`
		CacheRead  int64  `json:"cache_read"`
		CacheWrite int64  `json:"cache_write"`
		Status     int    `json:"status"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &rows); err != nil {
		return nil
	}
	entries := make([]requestLogEntry, 0, len(rows))
	for _, row := range rows {
		level := "info"
		if strings.TrimSpace(row.Error) != "" || row.Status >= 400 {
			level = "error"
		} else if row.Completed == 0 {
			level = "pending"
		}
		entries = append(entries, requestLogEntry{
			ID: row.ID, SessionID: row.SessionID, Time: row.Time, Completed: row.Completed,
			DurationMs: durationFromMillis(row.Time, row.Completed), ProviderID: row.ProviderID,
			ModelID: row.ModelID, Client: "local", Status: row.Status, Level: level,
			InputTokens: row.Input, OutputTokens: row.Output, ReasoningTokens: row.Reasoning,
			CacheReadTokens: row.CacheRead, CacheWriteTokens: row.CacheWrite, Error: trimLogText(row.Error),
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
