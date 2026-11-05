package gateway

import (
	"encoding/json"
	"fmt"
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
			{
				Info: pulseTokenMessage("msg_h1", "ses_agy_hist", "gemini-4-argon-high", now.UnixMilli(), 1000, 100, 0, 0),
				Parts: []runner.MessagePart{
					{Type: "tool", State: &runner.ToolState{Status: "completed"}},
					{Type: "tool", State: &runner.ToolState{Status: "error"}},
				},
			},
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
	if todayRow.ToolCallsTotal != 2 || todayRow.ToolCallsFailed != 1 {
		t.Fatalf("tool calls = %d total / %d failed, want 2 / 1", todayRow.ToolCallsTotal, todayRow.ToolCallsFailed)
	}

	// Each native turn is created→completed in 1000ms; today has two.
	var gemini *historyModelStat
	for i := range todayRow.Models {
		if todayRow.Models[i].ModelID == "gemini-4-argon-high" {
			gemini = &todayRow.Models[i]
		}
	}
	if gemini == nil || gemini.DurationSamples != 2 || gemini.DurationSumMs != 2000 {
		t.Fatalf("model duration not aggregated: %+v", gemini)
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

func TestDevPulseHistoryCustomWindow(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repo := t.TempDir()
	now := time.Now()
	at := func(daysAgo int) string {
		d := now.AddDate(0, 0, -daysAgo)
		return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, d.Location()).Format(time.RFC3339)
	}
	gitAt(t, repo, at(0), "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, at(5), "add", ".")
	gitAt(t, repo, at(5), "commit", "-q", "-m", "old")

	// One commit inside the window (yesterday) and one outside it (today).
	if err := os.WriteFile(filepath.Join(repo, "in.go"), []byte("package main\nvar inWindow = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, at(1), "add", "in.go")
	gitAt(t, repo, at(1), "commit", "-q", "-m", "yesterday")
	if err := os.WriteFile(filepath.Join(repo, "out.go"), []byte("package main\nvar outOfWindow = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, at(0), "add", "out.go")
	gitAt(t, repo, at(0), "commit", "-q", "-m", "today")

	gw := New(0, "pass", repo, nil, nil)
	start := now.AddDate(0, 0, -5).Format("2006-01-02")
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?directory="+repo+"&start="+start+"&end="+end, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res devPulseHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(res.Days) != 5 {
		t.Fatalf("days = %d, want 5", len(res.Days))
	}
	if res.Days[0].Date != start || res.Days[4].Date != end {
		t.Fatalf("window %s..%s, want %s..%s", res.Days[0].Date, res.Days[4].Date, start, end)
	}
	if res.EndDate != end {
		t.Fatalf("endDate = %s, want %s", res.EndDate, end)
	}
	if historyDayByDate(res.Days, now.Format("2006-01-02")) != nil {
		t.Fatal("today must not appear in a window ending yesterday")
	}
	yRow := historyDayByDate(res.Days, now.AddDate(0, 0, -1).Format("2006-01-02"))
	if yRow == nil || yRow.Commits != 1 {
		t.Fatalf("yesterday commit not counted: %+v", yRow)
	}
}

func TestDevPulseHistoryRejectsBadDates(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	gw := New(0, "pass", t.TempDir(), nil, nil)
	for _, q := range []string{"start=2026-13-40", "start=notadate", "start=2026-01-01&end=nope"} {
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", q, rec.Code)
		}
	}
}

func TestDevPulseHistoryClampsWindow(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
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

func TestDevPulseHistoryCustomWindowClampsInclusiveDayCount(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	gw := New(0, "pass", t.TempDir(), nil, nil)
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := end.AddDate(0, 0, -historyMaxDays)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?start="+start.Format("2006-01-02")+"&end="+end.Format("2006-01-02"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res devPulseHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Days) != historyMaxDays {
		t.Fatalf("days = %d, want inclusive clamp to %d", len(res.Days), historyMaxDays)
	}
}

func TestDevPulseHistoryCountsNewTurnsInImportedSessionWhileSkippingImportedTurns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hoxi.db")
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", dbPath)

	now := time.Now()
	sessCreated := now.UnixMilli() - 5000
	importedTurnTs := sessCreated - 2000
	newTurnTs := sessCreated + 1000
	schema := fmt.Sprintf(`
CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, time_created INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT);
INSERT INTO session VALUES ('ses_imp', '[Antigravity (Gemini)] Imported Task', %d);
INSERT INTO message VALUES ('m_old', 'ses_imp', %d, '{"role":"assistant","providerID":"google","modelID":"gemini-3.8-flash","imported":true,"tokens":{"input":9000,"output":900}}');
INSERT INTO message VALUES ('m_new', 'ses_imp', %d, '{"role":"assistant","providerID":"google","modelID":"gemini-3.8-flash","tokens":{"input":300,"output":40}}');
`, sessCreated, importedTurnTs, newTurnTs)
	if out, err := exec.Command("sqlite3", dbPath, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 init: %v %s", err, out)
	}

	gw := New(0, "pass", t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res devPulseHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	todayRow := historyDayByDate(res.Days, now.Format("2006-01-02"))
	if todayRow == nil || todayRow.Turns != 1 || todayRow.InputTokens != 300 || todayRow.OutputTokens != 40 {
		t.Fatalf("expected only post-import turn counted (in=300, out=40), got %+v", todayRow)
	}
}

func TestDevPulseAndHistoryAgreeOnTodayUncommittedCommitterDateAndBuildFiles(t *testing.T) {
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repo := t.TempDir()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayNoon := todayStart.AddDate(0, 0, -1).Add(12 * time.Hour).Format(time.RFC3339)
	todayTime := todayStart.Add(1 * time.Second)
	if now.After(todayTime) {
		todayTime = now
	}
	todayStr := todayTime.Format(time.RFC3339)

	gitAt(t, repo, yesterdayNoon, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\nline1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, yesterdayNoon, "add", ".")
	gitAt(t, repo, yesterdayNoon, "commit", "-q", "-m", "yesterday seed")

	// Commit authored yesterday but committed today, touching seed.go (+2/-1),
	// tracked Wails installer script build/windows/installer/project.nsi (+4/0),
	// and compiled output build/bin/app (+10/0, must be ignored).
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\nline1_mod\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "build", "windows", "installer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "build", "windows", "installer", "project.nsi"), []byte("Section\nDetailPrint 1\nDetailPrint 2\nSectionEnd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "build", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "build", "bin", "out.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, yesterdayNoon, "add", ".")
	cmd := exec.Command("git", "-C", repo, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "rebased today")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=hoxi-test", "GIT_AUTHOR_EMAIL=test@hoxi.local",
		"GIT_COMMITTER_NAME=hoxi-test", "GIT_COMMITTER_EMAIL=test@hoxi.local",
		"GIT_AUTHOR_DATE="+yesterdayNoon, "GIT_COMMITTER_DATE="+todayStr,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Uncommitted tracked change to seed.go (+1/-1) and untracked nested dir newpkg/sub.go (+3/0).
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\nline1_mod\nline2_dirty\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "newpkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "newpkg", "sub.go"), []byte("package newpkg\nvar X = 1\nvar Y = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repo, nil, nil)

	recPulse := httptest.NewRecorder()
	gw.ServeHTTP(recPulse, localRequest(http.MethodGet, "/api/hoxi/dev-pulse?directory="+repo, nil))
	if recPulse.Code != http.StatusOK {
		t.Fatalf("dev-pulse status = %d: %s", recPulse.Code, recPulse.Body.String())
	}
	var pulse gitDailyPulseResponse
	if err := json.Unmarshal(recPulse.Body.Bytes(), &pulse); err != nil {
		t.Fatal(err)
	}
	// Committed today: seed.go (+2/-1) + build/windows/installer/project.nsi (+4/0) = +6/-1 across 2 files, 1 commit.
	if pulse.TodayCommits != 1 || pulse.CommittedAdditions != 6 || pulse.CommittedDeletions != 1 || pulse.CommittedFiles != 2 {
		t.Fatalf("unexpected pulse committed stats: %+v", pulse)
	}
	// Uncommitted today: seed.go (+1/-1) + newpkg/sub.go (+3/0) = +4/-1 across 2 files; union ChangedFiles = 3.
	if pulse.UncommittedAdditions != 4 || pulse.UncommittedDeletions != 1 || pulse.UncommittedFiles != 2 || pulse.ChangedFiles != 3 {
		t.Fatalf("unexpected pulse uncommitted stats: %+v", pulse)
	}

	recHist := httptest.NewRecorder()
	gw.ServeHTTP(recHist, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=2&directory="+repo, nil))
	if recHist.Code != http.StatusOK {
		t.Fatalf("dev-pulse/history status = %d: %s", recHist.Code, recHist.Body.String())
	}
	var hist devPulseHistoryResponse
	if err := json.Unmarshal(recHist.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	todayRow := historyDayByDate(hist.Days, now.Format("2006-01-02"))
	if todayRow == nil {
		t.Fatal("missing today row")
	}
	wantAdd := pulse.CommittedAdditions + pulse.UncommittedAdditions
	wantDel := pulse.CommittedDeletions + pulse.UncommittedDeletions
	if todayRow.Commits != pulse.TodayCommits || todayRow.Additions != wantAdd || todayRow.Deletions != wantDel || todayRow.Files != pulse.ChangedFiles {
		t.Fatalf("history today row %+v does not match dev-pulse (commits=%d add=%d del=%d files=%d)",
			todayRow, pulse.TodayCommits, wantAdd, wantDel, pulse.ChangedFiles)
	}
}

func TestDevPulseCombinesProjectRootAndActiveWorktreeWithoutCrossProjectLeak(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	nowTime := time.Now()
	now := nowTime.Format(time.RFC3339)
	repoA := t.TempDir()
	gitAt(t, repoA, now, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoA, "base.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repoA, now, "add", ".")
	gitAt(t, repoA, now, "commit", "-q", "-m", "base")

	wtDir := filepath.Join(repoA, ".hoxi", "worktrees", "ses_wt")
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repoA, now, "worktree", "add", "-q", "-b", "hoxi/ses_wt", wtDir, "HEAD")

	// Root repo gets a new commit on main after ses_wt branched off (+2/0).
	if err := os.WriteFile(filepath.Join(repoA, "main_feature.go"), []byte("package a\nvar M = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repoA, now, "add", "main_feature.go")
	gitAt(t, repoA, now, "commit", "-q", "-m", "main feature")

	// Worktree gets one real commit (+3/0), one internal apply snapshot commit (must be skipped),
	// and one uncommitted file (+2/0).
	if err := os.WriteFile(filepath.Join(wtDir, "wt_feature.go"), []byte("package a\nvar W1 = 1\nvar W2 = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, wtDir, now, "add", "wt_feature.go")
	gitAt(t, wtDir, now, "commit", "-q", "-m", "worktree feature")

	if err := os.WriteFile(filepath.Join(wtDir, "snap.go"), []byte("package a\nvar S = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, wtDir, now, "add", "snap.go")
	gitAt(t, wtDir, now, "commit", "-q", "-m", "chore(hoxi): snapshot before apply (ses_wt)")

	if err := os.WriteFile(filepath.Join(wtDir, "wt_dirty.go"), []byte("package a\nvar D = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := runner.NewService(repoA, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	if err := svc.Store().Put(runner.SessionRecord{
		ID:           "ses_wt",
		Runtime:      "opencode",
		ProjectDir:   repoA,
		WorktreePath: wtDir,
		SandboxMode:  "worktree",
	}); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repoA, nil, nil)
	gw.runner = svc

	recPulse := httptest.NewRecorder()
	gw.ServeHTTP(recPulse, localRequest(http.MethodGet, "/api/hoxi/dev-pulse?directory="+repoA+"&sessionID=ses_wt", nil))
	if recPulse.Code != http.StatusOK {
		t.Fatalf("dev-pulse status = %d: %s", recPulse.Code, recPulse.Body.String())
	}
	var pulse gitDailyPulseResponse
	if err := json.Unmarshal(recPulse.Body.Bytes(), &pulse); err != nil {
		t.Fatal(err)
	}
	// 3 real commits (base +1, main_feature +2, wt_feature +3 = +6), 1 uncommitted file (wt_dirty +2).
	if pulse.TodayCommits != 3 || pulse.CommittedAdditions != 6 || pulse.UncommittedAdditions != 2 {
		t.Fatalf("expected combined root + worktree stats without snapshot commit, got %+v", pulse)
	}

	// Second project repoB with same-named base.go (+4/0 committed, +1/0 uncommitted).
	repoB := t.TempDir()
	gitAt(t, repoB, now, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoB, "base.go"), []byte("package b\nvar B1 = 1\nvar B2 = 2\nvar B3 = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repoB, now, "add", ".")
	gitAt(t, repoB, now, "commit", "-q", "-m", "init b")
	if err := os.WriteFile(filepath.Join(repoB, "base.go"), []byte("package b\nvar B1 = 1\nvar B2 = 2\nvar B3 = 3\nvar B4 = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recB := httptest.NewRecorder()
	gw.ServeHTTP(recB, localRequest(http.MethodGet, "/api/hoxi/dev-pulse?directory="+repoB+"&sessionID=ses_wt", nil))
	if recB.Code != http.StatusOK {
		t.Fatalf("dev-pulse repoB status = %d: %s", recB.Code, recB.Body.String())
	}
	var pulseB gitDailyPulseResponse
	if err := json.Unmarshal(recB.Body.Bytes(), &pulseB); err != nil {
		t.Fatal(err)
	}
	if pulseB.TodayCommits != 1 || pulseB.CommittedAdditions != 4 || pulseB.UncommittedAdditions != 1 {
		t.Fatalf("repoB stats leaked from ses_wt: %+v", pulseB)
	}

	// Register repoB in workspaces: global dev-pulse and history (no ?directory=)
	// must sum repoA + wtDir + repoB without colliding on same-named base.go.
	if _, err := gw.workspaces.EnsureProjectForDirectory(repoB); err != nil {
		t.Fatal(err)
	}
	recGlobal := httptest.NewRecorder()
	gw.ServeHTTP(recGlobal, localRequest(http.MethodGet, "/api/hoxi/dev-pulse?refresh=1", nil))
	if recGlobal.Code != http.StatusOK {
		t.Fatalf("global dev-pulse status = %d: %s", recGlobal.Code, recGlobal.Body.String())
	}
	var globalPulse gitDailyPulseResponse
	if err := json.Unmarshal(recGlobal.Body.Bytes(), &globalPulse); err != nil {
		t.Fatal(err)
	}
	// Total across repoA (3 commits, +6 committed, +2 uncommitted, 4 files) + repoB (1 commit, +4 committed, +1 uncommitted, 1 file).
	if globalPulse.TodayCommits != 4 || globalPulse.CommittedAdditions != 10 || globalPulse.UncommittedAdditions != 3 || globalPulse.ChangedFiles != 5 {
		t.Fatalf("expected global pulse across repoA + wtDir + repoB (commits=4, commAdd=10, uncommAdd=3, files=5), got %+v", globalPulse)
	}

	recGlobalHist := httptest.NewRecorder()
	gw.ServeHTTP(recGlobalHist, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=2", nil))
	if recGlobalHist.Code != http.StatusOK {
		t.Fatalf("global history status = %d: %s", recGlobalHist.Code, recGlobalHist.Body.String())
	}
	var globalHist devPulseHistoryResponse
	if err := json.Unmarshal(recGlobalHist.Body.Bytes(), &globalHist); err != nil {
		t.Fatal(err)
	}
	todayRow := historyDayByDate(globalHist.Days, nowTime.Format("2006-01-02"))
	if todayRow == nil || todayRow.Commits != 4 || todayRow.Additions != 13 || todayRow.Files != 5 {
		t.Fatalf("expected global history today row to match global pulse (commits=4, add=13, files=5), got %+v", todayRow)
	}
}
