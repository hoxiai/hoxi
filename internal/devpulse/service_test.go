package devpulse

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
