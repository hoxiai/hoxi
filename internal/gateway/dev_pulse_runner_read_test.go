package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/devpulse"
	"github.com/hoxiai/hoxi/internal/runner"
)

func TestDevPulseRetriesUnreadableRunnerUsage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOXI_HOME", root)
	t.Setenv("OPENCODE_DB", filepath.Join(root, "absent.db"))
	dir := filepath.Join(root, "sessions")
	const id = "ses_cld_usage"
	now := time.Now().UnixMilli()
	msg := pulseTokenMessage("msg_usage", id, "test-model", now, 123, 10, 0, 0)
	seed := runner.NewService(root, nil, nil, dir)
	if err := seed.Store().Put(runner.SessionRecord{ID: id, ProjectDir: root, WorktreePath: root, Runtime: "claude-code", CreatedAt: now, UpdatedAt: now,
		Turns: []runner.MessageTurn{{Info: msg}}}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{runner: runner.NewService(root, nil, nil, dir)}
	path := filepath.Join(seed.Store().SessionDir(id), "turns.jsonl")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	request := func(history bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		if history {
			g.handleDevPulseHistory(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=1", nil))
		} else {
			g.handleDevPulse(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse", nil))
		}
		return w
	}
	for _, history := range []bool{false, true} {
		if history {
			// Exercise the path where the user already tried to open the transcript.
			g.runner.Store().Get(id)
		}
		if w := request(history); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("unreadable log (history=%v): %d %s", history, w.Code, w.Body.String())
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	daily := request(false)
	var usage gitDailyPulseResponse
	if err := json.Unmarshal(daily.Body.Bytes(), &usage); err != nil || daily.Code != 200 || usage.DbInputTokens != 123 {
		t.Fatalf("daily usage after repair: %d %s (%v)", daily.Code, daily.Body.String(), err)
	}
	history := request(true)
	var series devpulse.HistoryResponse
	if err := json.Unmarshal(history.Body.Bytes(), &series); err != nil || history.Code != 200 || len(series.Days) != 1 || series.Days[0].InputTokens != 123 {
		t.Fatalf("usage history after repair: %d %s (%v)", history.Code, history.Body.String(), err)
	}
}
