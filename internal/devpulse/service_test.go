package devpulse

import (
	"os"
	"path/filepath"
	"testing"
)

type stubPulseSource struct {
	sessWorkDir   string
	sessProjDir   string
	candidates    []string
	sourceKey     SourceKey
	visitSessions func(minUpdatedMs int64, fn func(sessionID string, visitTurns func(func(TurnUsage))))
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
func (s *stubPulseSource) RunGit(string, ...string) (string, error)    { return "", nil }
func (s *stubPulseSource) RunGitRaw(string, ...string) (string, error) { return "", nil }
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
