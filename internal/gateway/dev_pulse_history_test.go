package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
)

func gitAt(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=hoxi-test", "GIT_AUTHOR_EMAIL=test@hoxi.local",
		"GIT_COMMITTER_NAME=hoxi-test", "GIT_COMMITTER_EMAIL=test@hoxi.local",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func historyDayByDate(days []historyDay, date string) *historyDay {
	for i := range days {
		if days[i].Date == date {
			return &days[i]
		}
	}
	return nil
}

// The history endpoint must emit a dense, zero-filled series, fold native
// runtime usage in, and stay out of the tunnel route allow-list.
func TestDevPulseHistoryAggregatesGitAndRunner(t *testing.T) {
	// Isolate from the developer's real ~/.hoxi/hoxi.db.
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repo := t.TempDir()
	gitAt(t, repo, time.Now().Format(time.RFC3339), "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, time.Now().Format(time.RFC3339), "add", ".")
	gitAt(t, repo, time.Now().Format(time.RFC3339), "commit", "-q", "-m", "seed")

	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Two commits today (one ignored lockfile) and one yesterday.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package main\nvar a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, now.Format(time.RFC3339), "add", ".")
	gitAt(t, repo, now.Format(time.RFC3339), "commit", "-q", "-m", "today")
	if err := os.WriteFile(filepath.Join(repo, "b.go"), []byte("package main\nvar b = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, now.Format(time.RFC3339), "add", "b.go")
	gitAt(t, repo, now.Format(time.RFC3339), "commit", "-q", "-m", "today 2")

	svc := runner.NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	rec := runner.SessionRecord{
		ID: "ses_agy_hist", Runtime: string(runner.RuntimeAntigravity),
		ProjectDir: repo, WorktreePath: repo, SandboxMode: "local",
		Title: "hist", CreatedAt: startOfToday, UpdatedAt: now.UnixMilli(),
		Turns: []runner.MessageTurn{
			{Info: pulseTokenMessage("msg_h1", "ses_agy_hist", "gemini-4-argon-high", now.UnixMilli(), 1000, 100, 0, 0)},
			{Info: pulseTokenMessage("msg_h2", "ses_agy_hist", "gemini-4-argon-high", now.UnixMilli()+1, 500, 50, 0, 0)},
			// Yesterday's turn must land on its own day, not today.
			{Info: pulseTokenMessage("msg_h3", "ses_agy_hist", "gemini-4-argon-high", startOfToday-3600_000, 4000, 400, 0, 0)},
		},
	}
	if err := svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repo, nil, nil)
	gw.runner = svc

	rec2 := httptest.NewRecorder()
	gw.ServeHTTP(rec2, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=3&directory="+repo, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var res devPulseHistoryResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(res.Days) != 3 {
		t.Fatalf("expected a dense 3-day series, got %d: %+v", len(res.Days), res.Days)
	}
	if res.Days[2].Date != today {
		t.Fatalf("last day = %s, want %s", res.Days[2].Date, today)
	}

	todayRow := historyDayByDate(res.Days, today)
	if todayRow == nil {
		t.Fatal("today missing from series")
	}
	// Three commits land today: the seed plus the two above.
	if todayRow.Commits != 3 || todayRow.Additions < 5 {
		t.Fatalf("unexpected git totals: %+v", todayRow)
	}
	if todayRow.Files != 3 {
		t.Fatalf("files = %d, want 3 (seed.go, a.go, b.go; lockfile ignored)", todayRow.Files)
	}
	if todayRow.InputTokens != 1500 || todayRow.OutputTokens != 150 {
		t.Fatalf("native tokens not folded in: %+v", todayRow)
	}
	if todayRow.Turns != 2 {
		t.Fatalf("turns = %d, want 2", todayRow.Turns)
	}

	yRow := historyDayByDate(res.Days, yesterday)
	if yRow == nil || yRow.InputTokens != 4000 {
		t.Fatalf("yesterday native tokens misplaced: %+v", yRow)
	}
}

func TestDevPulseHistoryRejectsBadWindow(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	gw := New(0, "pass", t.TempDir(), nil, nil)

	for _, q := range []string{"days=abc", "days=0", "days=-3"} {
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", q, rec.Code)
		}
	}

	recPost := httptest.NewRecorder()
	gw.ServeHTTP(recPost, localRequest(http.MethodPost, "/api/hoxi/dev-pulse/history", nil))
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recPost.Code)
	}
}

func TestDevPulseHistoryClampsWindow(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	gw := New(0, "pass", t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=100000", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var res devPulseHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.Days) != historyMaxDays {
		t.Fatalf("days = %d, want clamp to %d", len(res.Days), historyMaxDays)
	}
}
