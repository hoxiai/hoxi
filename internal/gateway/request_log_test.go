package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
)

func TestRequestLogListsNativeCallsNewestFirst(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "missing.db"))

	repo := t.TempDir()
	svc := runner.NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UnixMilli()
	older := pulseTokenMessage("msg_old", "ses_log", "claude-sonnet", now-10_000, 100, 20, 5, 30)
	older.ProviderID = "anthropic"
	older.Time.Completed = older.Time.Created + 2500
	newer := pulseTokenMessage("msg_new", "ses_log", "claude-sonnet", now, 0, 0, 0, 0)
	newer.Error = &runner.MessageError{Name: "APIError", Data: &runner.MessageErrorData{Message: "upstream unavailable", StatusCode: 503}}
	// A call that is still running: no completion time, no tokens yet.
	running := runner.MessageInfo{
		ID: "msg_run", SessionID: "ses_log", Role: "assistant", ProviderID: "anthropic",
		ModelID: "claude-sonnet", Time: runner.MessageTime{Created: now + 10},
	}
	// An unfinished turn older than the pending window is stale, not running.
	stale := runner.MessageInfo{
		ID: "msg_stale", SessionID: "ses_log", Role: "assistant",
		Time: runner.MessageTime{Created: now - 2*int64(requestLogPendingWindow/time.Millisecond)},
	}
	olderTurn := runner.MessageTurn{
		Info: older,
		Parts: []runner.MessagePart{
			{Type: "tool", Time: &runner.PartTime{Start: older.Time.Created + 50}},
			{Type: "reasoning", Time: &runner.PartTime{Start: older.Time.Created + 700}},
			{Type: "text", Time: &runner.PartTime{Start: older.Time.Created + 900}},
		},
	}
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_log", Runtime: string(runner.RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo,
		Title: "log", CreatedAt: now - 10_000, UpdatedAt: now,
		Turns: []runner.MessageTurn{olderTurn, {Info: newer}, {Info: running}, {Info: stale}},
	}); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repo, nil, nil)
	gw.runner = svc
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/request-log?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var res requestLogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Local || len(res.Entries) != 3 || res.Entries[0].ID != "msg_run" {
		t.Fatalf("unexpected log: %+v", res)
	}
	if run := res.Entries[0]; run.Level != "pending" || run.ModelID != "claude-sonnet" {
		t.Fatalf("running call: %+v", run)
	}
	for _, entry := range res.Entries {
		if entry.ID == "msg_stale" {
			t.Fatalf("stale unfinished call should be hidden: %+v", entry)
		}
	}
	failed := res.Entries[1]
	if failed.Status != 503 || failed.Level != "error" || failed.Error != "upstream unavailable" || failed.Client != "local" {
		t.Fatalf("failed call: %+v", failed)
	}
	ok := res.Entries[2]
	if ok.DurationMs != 2500 || ok.InputTokens != 100 || ok.CacheReadTokens != 30 || ok.ProviderID != "anthropic" {
		t.Fatalf("successful call: %+v", ok)
	}
	if ok.FirstTokenMs != 700 {
		t.Fatalf("time to first token = %d, want 700", ok.FirstTokenMs)
	}
	if failed.FirstTokenMs != 0 {
		t.Fatalf("failed call should carry no first-token timing: %+v", failed)
	}
}

func TestRequestLogRejectsBadLimit(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "missing.db"))
	gw := New(0, "pass", t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/request-log?limit=0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/request-log", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rec.Code)
	}
}
