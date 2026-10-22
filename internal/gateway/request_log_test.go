package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestRequestLogContentReturnsInputAndOutput(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "missing.db"))

	repo := t.TempDir()
	svc := runner.NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UnixMilli()
	user := pulseTokenMessage("msg_user", "ses_content", "claude-sonnet", now-1000, 0, 0, 0, 0)
	user.Role = "user"
	assistant := pulseTokenMessage("msg_asst", "ses_content", "claude-sonnet", now, 10, 5, 0, 0)
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_content", Runtime: string(runner.RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo,
		Title: "content", CreatedAt: now - 1000, UpdatedAt: now,
		Turns: []runner.MessageTurn{
			{Info: user, Parts: []runner.MessagePart{{Type: "text", Text: "build the store"}}},
			{Info: assistant, Parts: []runner.MessagePart{
				{Type: "reasoning", Text: "weighing options"},
				{Type: "text", Text: "Done."},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repo, nil, nil)
	gw.runner = svc
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/request-log/content?id=msg_asst&sessionID=ses_content", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var res requestContent
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Found || len(res.Input) != 1 || res.Input[0].Role != "user" || res.Input[0].Text != "build the store" {
		t.Fatalf("input: %+v", res)
	}
	if len(res.Output) != 2 || res.Output[0].Kind != "reasoning" || res.Output[1].Text != "Done." {
		t.Fatalf("output: %+v", res)
	}

	missing := httptest.NewRecorder()
	gw.ServeHTTP(missing, localRequest(http.MethodGet, "/api/hoxi/request-log/content", nil))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing id status %d", missing.Code)
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

func TestRequestLogRedactsSecretsAndSkipsAbortedOrAncientSessions(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "missing.db"))

	if got := trimLogText("auth failed: Bearer secret-tok-123 and sk-abc123456789 and api_key=mysecret"); strings.Contains(got, "secret-tok-123") || strings.Contains(got, "sk-abc123456789") || strings.Contains(got, "mysecret") {
		t.Fatalf("trimLogText leaked secret: %q", got)
	}

	repo := t.TempDir()
	svc := runner.NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UnixMilli()

	// An unfinished turn superseded by a newer turn or marked aborted must not stay pending.
	superseded := runner.MessageInfo{
		ID: "msg_superseded", SessionID: "ses_active", Role: "assistant",
		Time: runner.MessageTime{Created: now - 5000},
	}
	aborted := runner.MessageInfo{
		ID: "msg_aborted", SessionID: "ses_active", Role: "assistant", Finish: "aborted",
		Time: runner.MessageTime{Created: now - 2000},
	}
	latest := pulseTokenMessage("msg_done", "ses_active", "claude-sonnet", now, 50, 10, 0, 0)
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_active", Runtime: string(runner.RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo,
		Title: "active", CreatedAt: now - 5000, UpdatedAt: now,
		Turns: []runner.MessageTurn{{Info: superseded}, {Info: aborted}, {Info: latest}},
	}); err != nil {
		t.Fatal(err)
	}

	// Ancient session older than requestLogMaxAge must be skipped.
	ancientTs := time.Now().Add(-requestLogMaxAge - 24*time.Hour).UnixMilli()
	ancientTurn := pulseTokenMessage("msg_ancient", "ses_ancient", "claude-sonnet", ancientTs, 500, 100, 0, 0)
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_ancient", Runtime: string(runner.RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo,
		Title: "ancient", CreatedAt: ancientTs, UpdatedAt: ancientTs,
		Turns: []runner.MessageTurn{{Info: ancientTurn}},
	}); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repo, nil, nil)
	gw.runner = svc
	entries := gw.collectRequestLog()
	if len(entries) != 1 || entries[0].ID != "msg_done" {
		t.Fatalf("expected only msg_done, got %+v", entries)
	}
}

func TestOpenCodeRequestContentWithoutSessionIDAndMissingMessage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hoxi.db")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", dbPath)

	schema := `
CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT);
INSERT INTO session VALUES ('ses_oc', 'OpenCode Session');
INSERT INTO message VALUES ('m_user', 'ses_oc', 1000, '{"role":"user"}');
INSERT INTO message VALUES ('m_asst', 'ses_oc', 2000, '{"role":"assistant"}');
INSERT INTO part VALUES ('p_u1', 'm_user', 'ses_oc', 1000, '{"type":"text","text":"hello from user"}');
INSERT INTO part VALUES ('p_u_blob', 'm_user', 'ses_oc', 1001, '{"type":"file","url":"data:image/png;base64,AAAA"}');
INSERT INTO part VALUES ('p_a1', 'm_asst', 'ses_oc', 2000, '{"type":"text","text":"hello from assistant"}');
`
	if out, err := exec.Command("sqlite3", dbPath, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 init: %v %s", err, out)
	}

	// Empty sessionID resolves from the target message row.
	got := openCodeRequestContent("", "m_asst")
	if !got.Found || len(got.Input) != 1 || got.Input[0].Text != "hello from user" || len(got.Output) != 1 || got.Output[0].Text != "hello from assistant" {
		t.Fatalf("unexpected openCodeRequestContent without sessionID: %+v", got)
	}

	// Non-existent messageID returns Found: false.
	missing := openCodeRequestContent("", "m_does_not_exist")
	if missing.Found {
		t.Fatalf("expected Found=false for missing messageID, got %+v", missing)
	}
}
