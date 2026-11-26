package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func fetchPulseAndTodayHistory(t *testing.T, gw *Gateway, query string) (gitDailyPulseResponse, historyDay) {
	t.Helper()
	sep := "?"
	if query != "" {
		sep = "?" + query + "&"
	}
	recPulse := httptest.NewRecorder()
	gw.ServeHTTP(recPulse, localRequest(http.MethodGet, "/api/hoxi/dev-pulse"+sep+"refresh=1", nil))
	if recPulse.Code != http.StatusOK {
		t.Fatalf("dev-pulse status = %d: %s", recPulse.Code, recPulse.Body.String())
	}
	var pulse gitDailyPulseResponse
	if err := json.Unmarshal(recPulse.Body.Bytes(), &pulse); err != nil {
		t.Fatalf("unmarshal dev-pulse: %v", err)
	}

	recHist := httptest.NewRecorder()
	gw.ServeHTTP(recHist, localRequest(http.MethodGet, "/api/hoxi/dev-pulse/history"+sep+"days=2&refresh=1", nil))
	if recHist.Code != http.StatusOK {
		t.Fatalf("dev-pulse/history status = %d: %s", recHist.Code, recHist.Body.String())
	}
	var hist devPulseHistoryResponse
	if err := json.Unmarshal(recHist.Body.Bytes(), &hist); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	today := time.Now().Format("2006-01-02")
	row := historyDayByDate(hist.Days, today)
	if row == nil {
		t.Fatalf("missing today row %s in history: %+v", today, hist.Days)
	}
	return pulse, *row
}

func seedYesterdayRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := todayStart.Add(-12 * time.Hour).Format(time.RFC3339)

	gitAt(t, repo, yesterday, "init", "-q", "-b", "main")
	gitAt(t, repo, yesterday, "config", "user.name", "hoxi-test")
	gitAt(t, repo, yesterday, "config", "user.email", "test@hoxi.local")
	gitAt(t, repo, yesterday, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, yesterday, "add", ".")
	gitAt(t, repo, yesterday, "commit", "-q", "-m", "seed yesterday")
	return repo
}

func TestDevPulseAndHistoryPreserveWorktreeApplyMergeWithEmptyMessage(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repo := seedYesterdayRepo(t)
	mgr := worktree.NewManager(repo)
	meta, err := mgr.Create("ses_merge_empty")
	if err != nil {
		t.Fatalf("Create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta.Directory, "feature.go"), []byte("package main\nvar a = 1\nvar b = 2\nvar c = 3\nvar d = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Apply in merge mode with empty message (exactly what SandboxLifecycleSection does).
	if err := mgr.ApplyWithMessage("ses_merge_empty", "merge", ""); err != nil {
		t.Fatalf("ApplyWithMessage(merge, empty): %v", err)
	}

	gw := New(0, "pass", repo, nil, nil)
	pulse, todayRow := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
	if pulse.CommittedAdditions != 5 || pulse.UncommittedAdditions != 0 {
		t.Fatalf("merge apply with empty message lost lines: pulse=%+v", pulse)
	}
	if todayRow.Additions != 5 || todayRow.Commits != 1 {
		t.Fatalf("history today row disagrees with pulse: row=%+v pulse=%+v", todayRow, pulse)
	}
}

func TestDevPulseAndHistoryPreserveLegacyMergedInternalSnapshotCommit(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repo := seedYesterdayRepo(t)
	now := time.Now().Format(time.RFC3339)

	// 1) Legacy snapshot commit merged via --no-ff into main -> MUST be counted (+4).
	wtMerged := filepath.Join(repo, ".hoxi", "worktrees", "ses_legacy_merged")
	if err := os.MkdirAll(filepath.Dir(wtMerged), 0o755); err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, now, "worktree", "add", "-q", "-b", "hoxi/ses_legacy_merged", wtMerged, "HEAD")
	if err := os.WriteFile(filepath.Join(wtMerged, "merged.go"), []byte("package main\nvar m1 = 1\nvar m2 = 2\nvar m3 = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, wtMerged, now, "add", "-A")
	gitAt(t, wtMerged, now, "commit", "-q", "-m", "chore(hoxi): snapshot before apply (ses_legacy_merged)")
	gitAt(t, repo, now, "merge", "--no-ff", "-q", "-m", "Merge hoxi session ses_legacy_merged", "hoxi/ses_legacy_merged")

	// 2) Unmerged snapshot commit sitting only on hoxi/* -> MUST stay skipped.
	wtUnmerged := filepath.Join(repo, ".hoxi", "worktrees", "ses_unmerged")
	gitAt(t, repo, now, "worktree", "add", "-q", "-b", "hoxi/ses_unmerged", wtUnmerged, "HEAD")
	if err := os.WriteFile(filepath.Join(wtUnmerged, "snap_only.go"), []byte("package main\nvar u1 = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, wtUnmerged, now, "add", "-A")
	gitAt(t, wtUnmerged, now, "commit", "-q", "-m", "chore(hoxi): snapshot before apply (ses_unmerged)")

	gw := New(0, "pass", repo, nil, nil)
	pulse, todayRow := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
	if pulse.CommittedAdditions != 4 || pulse.TodayCommits != 1 {
		t.Fatalf("expected merged legacy snapshot (+4, 1 commit) and skipped unmerged snapshot, got %+v", pulse)
	}
	if todayRow.Additions != 4 || todayRow.Commits != 1 {
		t.Fatalf("history disagrees on legacy merged snapshot: %+v", todayRow)
	}
}

func TestDevPulseAndHistoryDoNotDoubleCountWorktreeApplyPatch(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	t.Run("single committed worktree applied via patch before and after commit on main", func(t *testing.T) {
		repo := seedYesterdayRepo(t)
		now := time.Now().Format(time.RFC3339)
		mgr := worktree.NewManager(repo)
		meta, err := mgr.Create("ses_p1")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(meta.Directory, "feat1.go"), []byte("package main\nvar f1 = 1\nvar f2 = 2\nvar f3 = 3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitAt(t, meta.Directory, now, "add", "-A")
		gitAt(t, meta.Directory, now, "commit", "-q", "-m", "feat: add feat1")

		if err := mgr.ApplyWithMessage("ses_p1", "patch", ""); err != nil {
			t.Fatalf("Apply patch: %v", err)
		}

		gw := New(0, "pass", repo, nil, nil)
		// Before committing on main: must count +4 once (not +8).
		pulseBefore, histBefore := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseBefore.CommittedAdditions+pulseBefore.UncommittedAdditions != 4 {
			t.Fatalf("before commit on main expected 4 total additions (1x), got %+v", pulseBefore)
		}
		if histBefore.Additions != 4 {
			t.Fatalf("history before commit expected 4 additions, got %+v", histBefore)
		}

		// Commit applied patch on main while hoxi/ses_p1 branch still exists: must stay +4 (not +8).
		gitAt(t, repo, now, "add", "-A")
		gitAt(t, repo, now, "commit", "-q", "-m", "feat: land feat1 on main")
		pulseAfter, histAfter := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseAfter.CommittedAdditions != 4 || pulseAfter.UncommittedAdditions != 0 || pulseAfter.TodayCommits != 1 {
			t.Fatalf("after commit on main expected 4 committed / 0 uncommitted / 1 commit, got %+v", pulseAfter)
		}
		if histAfter.Additions != 4 || histAfter.Commits != 1 {
			t.Fatalf("history after commit expected 4 additions / 1 commit, got %+v", histAfter)
		}
	})

	t.Run("multi-commit worktree squashed via apply patch and committed on main", func(t *testing.T) {
		repo := seedYesterdayRepo(t)
		now := time.Now().Format(time.RFC3339)
		mgr := worktree.NewManager(repo)
		meta, err := mgr.Create("ses_p2")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(meta.Directory, "multi.go"), []byte("package main\nvar m1 = 1\nvar m2 = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitAt(t, meta.Directory, now, "add", "-A")
		gitAt(t, meta.Directory, now, "commit", "-q", "-m", "feat: part 1")

		if err := os.WriteFile(filepath.Join(meta.Directory, "multi.go"), []byte("package main\nvar m1 = 1\nvar m2 = 2\nvar m3 = 3\nvar m4 = 4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitAt(t, meta.Directory, now, "add", "-A")
		gitAt(t, meta.Directory, now, "commit", "-q", "-m", "feat: part 2")

		if err := mgr.ApplyWithMessage("ses_p2", "patch", ""); err != nil {
			t.Fatalf("Apply patch: %v", err)
		}

		gw := New(0, "pass", repo, nil, nil)
		pulseBefore, histBefore := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseBefore.CommittedAdditions+pulseBefore.UncommittedAdditions != 5 {
			t.Fatalf("multi-commit before main commit expected 5 total additions, got %+v", pulseBefore)
		}
		if histBefore.Additions != 5 {
			t.Fatalf("history before main commit expected 5 additions, got %+v", histBefore)
		}

		gitAt(t, repo, now, "add", "-A")
		gitAt(t, repo, now, "commit", "-q", "-m", "feat: squash multi.go on main")
		pulseAfter, histAfter := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseAfter.CommittedAdditions != 5 || pulseAfter.UncommittedAdditions != 0 {
			t.Fatalf("multi-commit squashed on main expected 5 committed / 0 uncommitted, got %+v", pulseAfter)
		}
		if histAfter.Additions != 5 {
			t.Fatalf("history after squashed commit expected 5 additions, got %+v", histAfter)
		}
	})

	t.Run("dirty worktree applied via patch with custom message does not double count", func(t *testing.T) {
		repo := seedYesterdayRepo(t)
		now := time.Now().Format(time.RFC3339)
		mgr := worktree.NewManager(repo)
		meta, err := mgr.Create("ses_p3")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(meta.Directory, "dirty.go"), []byte("package main\nvar d1 = 1\nvar d2 = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := mgr.ApplyWithMessage("ses_p3", "patch", "custom patch message"); err != nil {
			t.Fatalf("Apply patch with custom msg: %v", err)
		}

		gw := New(0, "pass", repo, nil, nil)
		pulseBefore, _ := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseBefore.CommittedAdditions != 0 || pulseBefore.UncommittedAdditions != 3 {
			t.Fatalf("patch apply with custom msg must not double-count snapshot, got %+v", pulseBefore)
		}

		gitAt(t, repo, now, "add", "-A")
		gitAt(t, repo, now, "commit", "-q", "-m", "feat: commit dirty.go")
		pulseAfter, histAfter := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseAfter.CommittedAdditions != 3 || pulseAfter.UncommittedAdditions != 0 || histAfter.Additions != 3 {
			t.Fatalf("after committing patch expected 3 committed / 0 uncommitted, got pulse=%+v hist=%+v", pulseAfter, histAfter)
		}
	})

	t.Run("mixed committed and dirty worktree applied via patch counts both once", func(t *testing.T) {
		repo := seedYesterdayRepo(t)
		now := time.Now().Format(time.RFC3339)
		mgr := worktree.NewManager(repo)
		meta, err := mgr.Create("ses_p4")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(meta.Directory, "committed.go"), []byte("package main\nvar c1 = 1\nvar c2 = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitAt(t, meta.Directory, now, "add", "-A")
		gitAt(t, meta.Directory, now, "commit", "-q", "-m", "feat: committed part")

		if err := os.WriteFile(filepath.Join(meta.Directory, "dirty.go"), []byte("package main\nvar d1 = 1\nvar d2 = 2\nvar d3 = 3\nvar d4 = 4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := mgr.ApplyWithMessage("ses_p4", "patch", ""); err != nil {
			t.Fatalf("Apply patch mixed: %v", err)
		}

		gw := New(0, "pass", repo, nil, nil)
		pulseBefore, histBefore := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseBefore.CommittedAdditions != 3 || pulseBefore.UncommittedAdditions != 5 || histBefore.Additions != 8 {
			t.Fatalf("mixed patch before main commit expected +3 committed / +5 uncommitted (8 total), got pulse=%+v hist=%+v", pulseBefore, histBefore)
		}

		gitAt(t, repo, now, "add", "-A")
		gitAt(t, repo, now, "commit", "-q", "-m", "feat: land both")
		pulseAfter, histAfter := fetchPulseAndTodayHistory(t, gw, "directory="+repo)
		if pulseAfter.CommittedAdditions != 8 || pulseAfter.UncommittedAdditions != 0 || histAfter.Additions != 8 {
			t.Fatalf("mixed patch after main commit expected +8 committed / 0 uncommitted, got pulse=%+v hist=%+v", pulseAfter, histAfter)
		}
	})
}

func TestDevPulseGlobalSkipsDismissedProjectSessions(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repoKeep := seedYesterdayRepo(t)
	repoDismissed := seedYesterdayRepo(t)

	if err := os.WriteFile(filepath.Join(repoKeep, "keep_dirty.go"), []byte("package main\nvar k = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDismissed, "gone_dirty.go"), []byte("package main\nvar g1 = 1\nvar g2 = 2\nvar g3 = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := runner.NewService(repoKeep, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	if err := svc.Store().Put(runner.SessionRecord{
		ID:           "ses_old_dismissed",
		Runtime:      "native",
		ProjectDir:   repoDismissed,
		WorktreePath: repoDismissed,
		SandboxMode:  "local",
	}); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "pass", repoKeep, nil, nil)
	gw.runner = svc
	dismissedID, err := gw.workspaces.EnsureProjectForDirectory(repoDismissed)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := gw.workspaces.Remove(dismissedID); err != nil || !removed {
		t.Fatalf("Remove dismissed project: removed=%v err=%v", removed, err)
	}

	pulse, todayRow := fetchPulseAndTodayHistory(t, gw, "")
	if pulse.UncommittedAdditions != 2 || pulse.UncommittedFiles != 1 {
		t.Fatalf("dismissed project leaked into global pulse: %+v", pulse)
	}
	if todayRow.Additions != 2 || todayRow.Files != 1 {
		t.Fatalf("dismissed project leaked into global history: %+v", todayRow)
	}
}
