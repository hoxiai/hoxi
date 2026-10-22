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
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_log", Runtime: string(runner.RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo,
		Title: "log", CreatedAt: now - 10_000, UpdatedAt: now,
		Turns: []runner.MessageTurn{{Info: older}, {Info: newer}},
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
	if !res.Local || len(res.Entries) != 2 || res.Entries[0].ID != "msg_new" {
		t.Fatalf("unexpected log: %+v", res)
	}
	failed := res.Entries[0]
	if failed.Status != 503 || failed.Level != "error" || failed.Error != "upstream unavailable" || failed.Client != "local" {
		t.Fatalf("failed call: %+v", failed)
	}
	ok := res.Entries[1]
	if ok.DurationMs != 2500 || ok.InputTokens != 100 || ok.CacheReadTokens != 30 || ok.ProviderID != "anthropic" {
		t.Fatalf("successful call: %+v", ok)
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
