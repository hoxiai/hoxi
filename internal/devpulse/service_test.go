package devpulse

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

type stubPulseSource struct {
	sessWorkDir   string
	sessProjDir   string
	candidates    []string
	sourceKey     SourceKey
	visitSessions func(minUpdatedMs int64, fn func(sessionID string, visitTurns func(func(TurnUsage))))
	runGit        func(workDir string, args ...string) (string, error)
	runGitRaw     func(workDir string, args ...string) (string, error)
}

func (s *stubPulseSource) SessionDirectories(string) (string, string) {
	return s.sessWorkDir, s.sessProjDir
}
func (s *stubPulseSource) CandidateDirectories() []string { return s.candidates }
func (s *stubPulseSource) SourceKey() SourceKey           { return s.sourceKey }
func (s *stubPulseSource) VisitSessions(minUpdatedMs int64, fn func(sessionID string, visitTurns func(func(TurnUsage)))) {
	if s.visitSessions != nil {
		s.visitSessions(minUpdatedMs, fn)
	}
}
func (s *stubPulseSource) NotImportedTurnSQLClause(string, string, string, string) string {
	return "1=1"
}
func (s *stubPulseSource) RunGit(workDir string, args ...string) (string, error) {
	if s.runGit != nil {
		return s.runGit(workDir, args...)
	}
	return "", nil
}
func (s *stubPulseSource) RunGitRaw(workDir string, args ...string) (string, error) {
	if s.runGitRaw != nil {
		return s.runGitRaw(workDir, args...)
	}
	return "", nil
}
func (s *stubPulseSource) ParseBranchFromPorcelainHeader(string) string {
	return ""
}

func TestResolveGitTargetsDiscoversProjectsSessionDirAndWorktrees(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repoA := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoA, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	wtA := filepath.Join(repoA, ".hoxi", "worktrees", "ses_a")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtA, ".git"), []byte("gitdir: ../../.git/worktrees/ses_a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoB := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoB, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	src := &stubPulseSource{
		sessWorkDir: wtA,
		sessProjDir: repoA,
		candidates:  []string{repoA, repoB},
	}
	svc := NewService(src)

	// Scoped to repoA: includes repoA and its worktree wtA, excludes repoB.
	scopedA := svc.ResolveGitTargets(repoA, "ses_a")
	if len(scopedA) != 2 {
		t.Fatalf("scopedA targets = %d, want 2 (repoA + wtA): %+v", len(scopedA), scopedA)
	}
	if scopedA[0].IsWorktree || !scopedA[1].IsWorktree {
		t.Fatalf("expected root first then worktree, got %+v", scopedA)
	}

	// Scoped to repoB with session in repoA: must not leak wtA into repoB.
	scopedB := svc.ResolveGitTargets(repoB, "ses_a")
	if len(scopedB) != 1 {
		t.Fatalf("scopedB targets = %d, want 1 (repoB only): %+v", len(scopedB), scopedB)
	}

	// Global discovery (empty directory): discovers repoA, repoB, and scans wtA.
	global := svc.ResolveGitTargets("", "ses_a")
	if len(global) != 3 {
		t.Fatalf("global targets = %d, want 3 (repoA, repoB, wtA): %+v", len(global), global)
	}
}

func TestCollectGitDailyStatsCountsNonASCIIUntrackedFiles(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "中文.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		porcelain string
	}{
		{name: "nul-separated", porcelain: "## main\x00?? 中文.go\x00"},
		{name: "octal-quoted", porcelain: "## main\n?? \"\\344\\270\\255\\346\\226\\207.go\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &stubPulseSource{
				runGitRaw: func(_ string, args ...string) (string, error) {
					if len(args) > 0 && args[0] == "status" {
						return tc.porcelain, nil
					}
					return "", nil
				},
			}
			svc := NewService(src)
			var stats DailyPulseResponse
			svc.CollectGitDailyStats([]GitTarget{{Dir: repo, RepoRoot: repo}}, time.Now().Add(-time.Hour), &stats)
			if stats.UncommittedFiles != 1 || stats.UncommittedAdditions != 2 {
				t.Fatalf("stats = %+v, want UncommittedFiles=1 UncommittedAdditions=2", stats)
			}
		})
	}
}

func TestDailyPulseAndHistoryInProcessSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hoxi.db")
	t.Setenv("HOXI_DB_PATH", dbPath)
	t.Setenv("OPENCODE_DB", "")

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	ts := startOfToday.Add(2 * time.Hour).UnixMilli()

	err := sqlitecli.WithReadWrite(context.Background(), dbPath, 3*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, `
			CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created INTEGER);
			CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
			CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT);
			INSERT INTO session VALUES ('ses_1', '/tmp/proj', 'Pulse Test', ?);
			INSERT INTO message VALUES ('msg_1', 'ses_1', ?, '{"role":"assistant","providerID":"openai","modelID":"gpt-5","cost":0.05,"time":{"completed":1999999999999},"tokens":{"input":1200,"output":300,"reasoning":100,"cache":{"read":400,"write":50}}}');
			INSERT INTO part VALUES ('prt_1', 'msg_1', 'ses_1', ?, '{"type":"tool","callID":"c1","state":{"status":"completed"}}');
			INSERT INTO part VALUES ('prt_2', 'msg_1', 'ses_1', ?, '{"type":"tool","callID":"c2","state":{"status":"error"}}');
		`, ts, ts, ts, ts)
		return execErr
	})
	if err != nil {
		t.Fatalf("seed pulse db: %v", err)
	}

	svc := NewService(&stubPulseSource{})
	pulse := svc.DailyPulse("", "", true)
	if pulse.DbSessionsCount != 1 || pulse.DbTurnsCount != 1 || pulse.DbTotalTokens != 2050 {
		t.Fatalf("unexpected pulse counts: %+v", pulse)
	}
	if pulse.DbToolCallsTotal != 2 || pulse.DbToolCallsFailed != 1 {
		t.Fatalf("unexpected tool counts: total=%d failed=%d", pulse.DbToolCallsTotal, pulse.DbToolCallsFailed)
	}

	byDate := map[string]*HistoryDay{}
	dayFn := func(k string) *HistoryDay {
		if d, ok := byDate[k]; ok {
			return d
		}
		d := &HistoryDay{Date: k}
		byDate[k] = d
		return d
	}
	svc.queryHistoryFromDB(startOfToday.UnixMilli(), startOfToday.AddDate(0, 0, 1).UnixMilli(), dayFn)
	todayKey := startOfToday.Format("2006-01-02")
	gotDay := byDate[todayKey]
	if gotDay == nil || gotDay.Turns != 1 || gotDay.Sessions != 1 || gotDay.InputTokens != 1200 || gotDay.ToolCallsTotal != 2 || gotDay.ToolCallsFailed != 1 {
		t.Fatalf("unexpected history day: %+v", gotDay)
	}

	// tools=0 skips the heavy SQLite part table scan while keeping message totals.
	recNoTools := httptest.NewRecorder()
	svc.HandleHistory(recNoTools, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=1&tools=0", nil))
	if recNoTools.Code != http.StatusOK {
		t.Fatalf("HandleHistory tools=0 status = %d", recNoTools.Code)
	}
	var noToolsRes HistoryResponse
	if err := json.Unmarshal(recNoTools.Body.Bytes(), &noToolsRes); err != nil {
		t.Fatal(err)
	}
	if len(noToolsRes.Days) != 1 || noToolsRes.Days[0].InputTokens != 1200 || noToolsRes.Days[0].ToolCallsTotal != 0 {
		t.Fatalf("tools=0 should keep token stats and skip SQLite part scan, got %+v", noToolsRes.Days)
	}
}

func TestHandleHistoryCachesAndSkipsRedundantWorktreeGitLog(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	t.Setenv("OPENCODE_DB", "")

	repo := t.TempDir()
	wtGitDir := filepath.Join(repo, ".git", "worktrees", "ses_wt")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/hoxi/ses_wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtDir := filepath.Join(repo, ".hoxi", "worktrees", "ses_wt")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var rootLogs, wtLogs, diffCalls, visitCalls atomic.Int32
	src := &stubPulseSource{
		candidates: []string{repo},
		visitSessions: func(_ int64, _ func(string, func(func(TurnUsage)))) {
			visitCalls.Add(1)
		},
		runGit: func(_ string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "diff" {
				diffCalls.Add(1)
			}
			return "", nil
		},
		runGitRaw: func(workDir string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "status" {
				return "## main\x00", nil
			}
			if len(args) > 0 && args[0] == "log" {
				if samePulseDir(workDir, wtDir) {
					wtLogs.Add(1)
				} else {
					rootLogs.Add(1)
				}
			}
			return "", nil
		},
	}
	svc := NewService(src)

	rec1 := httptest.NewRecorder()
	svc.HandleHistory(rec1, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=7&tools=0", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first HandleHistory status = %d", rec1.Code)
	}
	if rootLogs.Load() != 1 || wtLogs.Load() != 0 {
		t.Fatalf("expected 1 root git log and 0 redundant worktree git logs, got root=%d wt=%d", rootLogs.Load(), wtLogs.Load())
	}
	if diffCalls.Load() != 0 {
		t.Fatalf("expected clean git status to skip git diff, got %d diff calls", diffCalls.Load())
	}

	// Second request hits the in-memory cache with zero extra git/runner work.
	rec2 := httptest.NewRecorder()
	svc.HandleHistory(rec2, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=7&tools=0", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("cached HandleHistory status = %d", rec2.Code)
	}
	if rootLogs.Load() != 1 || visitCalls.Load() != 1 {
		t.Fatalf("expected cached response to avoid re-scanning, got rootLogs=%d visits=%d", rootLogs.Load(), visitCalls.Load())
	}

	// Explicit refresh=1 bypasses both response and git-history caches.
	rec3 := httptest.NewRecorder()
	svc.HandleHistory(rec3, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=7&tools=0&refresh=1", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("refresh HandleHistory status = %d", rec3.Code)
	}
	if rootLogs.Load() != 2 || visitCalls.Load() != 2 {
		t.Fatalf("expected refresh=1 to recompute, got rootLogs=%d visits=%d", rootLogs.Load(), visitCalls.Load())
	}
}

func TestWorktreeGitLogSeparatesSharedDetachedFromIndependentRepo(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	t.Setenv("OPENCODE_DB", "")

	repo := t.TempDir()
	// 1) Shared linked worktree on detached HEAD -> must pass HEAD --not --branches.
	detachedGitDir := filepath.Join(repo, ".git", "worktrees", "ses_detached")
	if err := os.MkdirAll(detachedGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(detachedGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(detachedGitDir, "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detachedWT := filepath.Join(repo, ".hoxi", "worktrees", "ses_detached")
	if err := os.MkdirAll(detachedWT, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(detachedWT, ".git"), []byte("gitdir: "+detachedGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2) Independent nested repo under .hoxi/worktrees/* -> must run normal git log without --not --branches.
	independentWT := filepath.Join(repo, ".hoxi", "worktrees", "nested_repo")
	if err := os.MkdirAll(filepath.Join(independentWT, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var detachedArgs, independentArgs []string
	src := &stubPulseSource{
		candidates: []string{repo},
		runGitRaw: func(workDir string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "status" {
				return "## main\x00", nil
			}
			if len(args) > 0 && args[0] == "log" {
				mu.Lock()
				switch {
				case samePulseDir(workDir, detachedWT):
					detachedArgs = append([]string(nil), args...)
				case samePulseDir(workDir, independentWT):
					independentArgs = append([]string(nil), args...)
				}
				mu.Unlock()
			}
			return "", nil
		},
	}
	svc := NewService(src)
	rec := httptest.NewRecorder()
	svc.HandleHistory(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse/history?days=7&tools=0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleHistory status = %d", rec.Code)
	}
	if !slices.Contains(detachedArgs, "--not") || !slices.Contains(detachedArgs, "--branches") {
		t.Fatalf("detached shared worktree should pass HEAD --not --branches, got %v", detachedArgs)
	}
	if len(independentArgs) == 0 {
		t.Fatal("independent nested repo should still run git log")
	}
	if slices.Contains(independentArgs, "--not") {
		t.Fatalf("independent nested repo must not exclude its own branches with --not, got %v", independentArgs)
	}
}

func TestDailyPulseAndHistoryCountReasoningAndCacheWriteOnlySQLiteTurns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hoxi.db")
	t.Setenv("HOXI_DB_PATH", dbPath)
	t.Setenv("OPENCODE_DB", "")

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	ts := startOfToday.Add(time.Hour).UnixMilli()

	err := sqlitecli.WithReadWrite(context.Background(), dbPath, 3*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, `
			CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created INTEGER);
			CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
			CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT);
			INSERT INTO session VALUES ('ses_r', '/tmp/proj', 'Reasoning Only', ?);
			INSERT INTO message VALUES ('msg_r', 'ses_r', ?, '{"role":"assistant","providerID":"anthropic","modelID":"claude-opus-5","tokens":{"input":0,"output":0,"reasoning":150,"cache":{"read":0,"write":250}}}');
		`, ts, ts)
		return execErr
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(&stubPulseSource{})
	pulse := svc.DailyPulse("", "", true)
	if pulse.DbTurnsCount != 1 || pulse.DbReasoningTokens != 150 || pulse.DbCacheWriteTokens != 250 || pulse.DbTotalTokens != 400 {
		t.Fatalf("unexpected pulse for reasoning/cache_write-only turn: %+v", pulse)
	}
	byDate := map[string]*HistoryDay{}
	svc.queryHistoryFromDB(startOfToday.UnixMilli(), startOfToday.AddDate(0, 0, 1).UnixMilli(), func(k string) *HistoryDay {
		if d, ok := byDate[k]; ok {
			return d
		}
		d := &HistoryDay{Date: k}
		byDate[k] = d
		return d
	})
	day := byDate[startOfToday.Format("2006-01-02")]
	if day == nil || day.Turns != 1 || day.ReasoningTokens != 150 || day.CacheWriteTokens != 250 {
		t.Fatalf("unexpected history for reasoning/cache_write-only turn: %+v", day)
	}
}

func TestDailyPulseRefreshesUsageWhenSourceKeyChangesWithinGitTTL(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	t.Setenv("OPENCODE_DB", "")

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nowMs := time.Now().UnixMilli()
	var gitCalls atomic.Int32
	var tokens atomic.Int64
	tokens.Store(500)
	var rev atomic.Uint64
	rev.Store(1)

	src := &stubPulseSource{
		candidates: []string{repo},
		runGitRaw: func(_ string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "status" {
				gitCalls.Add(1)
				return "## main\x00", nil
			}
			return "", nil
		},
		visitSessions: func(_ int64, fn func(string, func(func(TurnUsage)))) {
			fn("ses_live", func(yield func(TurnUsage)) {
				yield(TurnUsage{
					ID:          "msg_a_1",
					Role:        "assistant",
					Created:     nowMs,
					ProviderID:  "native",
					ModelID:     "gpt-5.4",
					IsNative:    true,
					HasTokens:   true,
					InputTokens: tokens.Load(),
				})
			})
		},
	}
	svc := NewService(&dynamicKeyPulseSource{stubPulseSource: src, rev: &rev})

	p1 := svc.DailyPulse("", "", false)
	if p1.DbInputTokens != 500 || gitCalls.Load() != 1 {
		t.Fatalf("first pulse = %+v gitCalls=%d", p1, gitCalls.Load())
	}
	// Same sourceKey hits full cache.
	p2 := svc.DailyPulse("", "", false)
	if p2.DbInputTokens != 500 || gitCalls.Load() != 1 {
		t.Fatalf("cached pulse = %+v gitCalls=%d", p2, gitCalls.Load())
	}
	// Runner revision bumps within 15s TTL: usage must refresh immediately while reusing cached git stats.
	tokens.Store(1200)
	rev.Store(2)
	p3 := svc.DailyPulse("", "", false)
	if p3.DbInputTokens != 1200 || gitCalls.Load() != 1 {
		t.Fatalf("expected fresh tokens (1200) with reused git cache (gitCalls=1), got tokens=%d gitCalls=%d", p3.DbInputTokens, gitCalls.Load())
	}
}

type dynamicKeyPulseSource struct {
	*stubPulseSource
	rev *atomic.Uint64
}

func (d *dynamicKeyPulseSource) SourceKey() SourceKey {
	return SourceKey{RunnerRev: d.rev.Load()}
}

func TestDailyPulseUsageRefreshCoalescesAndNeverClobbersNewerForcedGitRefresh(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))
	t.Setenv("OPENCODE_DB", "")

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nowMs := time.Now().UnixMilli()
	var rev atomic.Uint64
	rev.Store(1)
	var visitCalls atomic.Int32
	var untrackedLines atomic.Int32
	untrackedLines.Store(1)

	inUsage := make(chan struct{})
	releaseUsage := make(chan struct{})
	var blockOnce sync.Once

	src := &stubPulseSource{
		candidates: []string{repo},
		runGitRaw: func(_ string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "status" {
				if untrackedLines.Load() == 2 {
					return "## main\x00?? fresh.go\x00", nil
				}
				return "## main\x00", nil
			}
			return "", nil
		},
		visitSessions: func(_ int64, fn func(string, func(func(TurnUsage)))) {
			n := visitCalls.Add(1)
			if n == 2 {
				blockOnce.Do(func() {
					close(inUsage)
					<-releaseUsage
				})
			}
			fn("ses_live", func(yield func(TurnUsage)) {
				yield(TurnUsage{
					ID:          "msg_a_1",
					Role:        "assistant",
					Created:     nowMs,
					ProviderID:  "native",
					ModelID:     "gpt-5.4",
					IsNative:    true,
					HasTokens:   true,
					InputTokens: int64(rev.Load() * 100),
				})
			})
		},
	}
	if err := os.WriteFile(filepath.Join(repo, "fresh.go"), []byte("package p\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&dynamicKeyPulseSource{stubPulseSource: src, rev: &rev})

	// Seed initial cached entry with clean git (0 additions) and rev=1.
	p1 := svc.DailyPulse("", "", false)
	if p1.UncommittedAdditions != 0 || p1.DbInputTokens != 100 {
		t.Fatalf("initial pulse = %+v", p1)
	}

	// Bump rev=2 and fire two concurrent non-forced refreshes; they must coalesce
	// into a single visitSessions scan (call #2) while blocked on releaseUsage.
	rev.Store(2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			svc.DailyPulse("", "", false)
		}()
	}
	<-inUsage
	// Give the second goroutine a brief moment to join usageInflight.
	time.Sleep(20 * time.Millisecond)

	// Concurrently run a forced refresh that sees the new dirty git file (2 additions).
	untrackedLines.Store(2)
	forcedRes := svc.DailyPulse("", "", true)
	if forcedRes.UncommittedAdditions != 2 {
		t.Fatalf("forced pulse expected 2 uncommitted additions, got %+v", forcedRes)
	}

	// Now let the earlier non-forced usage refresh finish: it must NOT overwrite
	// the newer forced git stats in cache.
	close(releaseUsage)
	wg.Wait()

	after := svc.DailyPulse("", "", false)
	if after.UncommittedAdditions != 2 {
		t.Fatalf("stale lightweight refresh clobbered newer forced git stats: got %+v", after)
	}
	// Visit calls: 1 (initial) + 1 (coalesced pair) + 1 (forced) = 3.
	if visitCalls.Load() != 3 {
		t.Fatalf("expected concurrent non-forced usage refreshes to coalesce (3 total visits), got %d", visitCalls.Load())
	}
}
