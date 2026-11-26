package devpulse

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type realGitPulseSource struct {
	stubPulseSource
	dismissed map[string]bool
}

func (s *realGitPulseSource) RunGit(workDir string, args ...string) (string, error) {
	out, err := s.RunGitRaw(workDir, args...)
	return strings.TrimSpace(out), err
}

func (s *realGitPulseSource) RunGitRaw(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.autocrlf=false", "-C", workDir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func (s *realGitPulseSource) IsDismissedDirectory(dir string) bool {
	if len(s.dismissed) == 0 {
		return false
	}
	cd := canonicalPulseDir(dir)
	for raw := range s.dismissed {
		if samePulseDir(cd, raw) {
			return true
		}
	}
	return false
}

func runTestGit(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=hoxi-test", "GIT_AUTHOR_EMAIL=test@hoxi.local",
		"GIT_COMMITTER_NAME=hoxi-test", "GIT_COMMITTER_EMAIL=test@hoxi.local",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCollectGitDailyStatsPureRenameAndEditedRenameNotInflated(t *testing.T) {
	repo := t.TempDir()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := todayStart.Add(-12 * time.Hour).Format(time.RFC3339)
	today := now.Format(time.RFC3339)

	runTestGit(t, repo, yesterday, "init", "-q", "-b", "main")
	var pureLines, editLines strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&pureLines, "const p%d = %d\n", i, i)
		fmt.Fprintf(&editLines, "const e%d = %d\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(repo, "pure.go"), []byte(pureLines.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "edit.go"), []byte(editLines.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, yesterday, "add", ".")
	runTestGit(t, repo, yesterday, "commit", "-q", "-m", "seed yesterday")

	// Commit a pure rename today: must add 0 / delete 0 (not +20 / -20).
	runTestGit(t, repo, today, "mv", "pure.go", "pure_moved.go")
	runTestGit(t, repo, today, "commit", "-q", "-m", "refactor: move pure.go")

	// Leave an uncommitted rename + 1 modified line: must report +1 / -1 (not +20 / -20).
	runTestGit(t, repo, today, "mv", "edit.go", "edit_moved.go")
	modified := strings.Replace(editLines.String(), "const e10 = 10\n", "const e10 = 999\n", 1)
	if err := os.WriteFile(filepath.Join(repo, "edit_moved.go"), []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(&realGitPulseSource{})
	var stats DailyPulseResponse
	svc.CollectGitDailyStats([]GitTarget{{Dir: repo, RepoRoot: repo}}, todayStart, &stats)

	if stats.CommittedAdditions != 0 || stats.CommittedDeletions != 0 {
		t.Fatalf("pure committed rename inflated stats: %+v", stats)
	}
	if stats.UncommittedAdditions != 1 || stats.UncommittedDeletions != 1 {
		t.Fatalf("edited uncommitted rename expected +1/-1, got %+v", stats)
	}
}

func TestCollectGitDailyStatsUnbornRepoCountsStagedAndModifiedFiles(t *testing.T) {
	repo := t.TempDir()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	runTestGit(t, repo, now.Format(time.RFC3339), "init", "-q", "-b", "main")
	// Stage 2 lines, then append 2 more unstaged lines (AM status) plus an untracked file (+3).
	if err := os.WriteFile(filepath.Join(repo, "staged.go"), []byte("package main\nvar a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, now.Format(time.RFC3339), "add", "staged.go")
	if err := os.WriteFile(filepath.Join(repo, "staged.go"), []byte("package main\nvar a = 1\nvar b = 2\nvar c = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("package main\nvar u1 = 1\nvar u2 = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(&realGitPulseSource{})
	var stats DailyPulseResponse
	svc.CollectGitDailyStats([]GitTarget{{Dir: repo, RepoRoot: repo}}, todayStart, &stats)

	if stats.UncommittedAdditions != 7 || stats.UncommittedFiles != 2 {
		t.Fatalf("unborn repo stats = %+v, want UncommittedAdditions=7 UncommittedFiles=2", stats)
	}
}

func TestCollectGitDailyStatsLargeUntrackedFileAndSymlink(t *testing.T) {
	repo := t.TempDir()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	runTestGit(t, repo, now.Format(time.RFC3339), "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "seed.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, now.Format(time.RFC3339), "add", ".")
	runTestGit(t, repo, now.Format(time.RFC3339), "commit", "-q", "-m", "seed")

	// Create a ~90KB untracked text file (> old 64KB cap) with 1500 lines.
	var large strings.Builder
	for i := 0; i < 1500; i++ {
		large.WriteString("var generatedLineWithPadding0123456789abcdef0123456789 = 1\n")
	}
	if large.Len() <= 64*1024 {
		t.Fatalf("expected > 64KB fixture, got %d bytes", large.Len())
	}
	if err := os.WriteFile(filepath.Join(repo, "large.go"), []byte(large.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// External file pointed to by an untracked symlink must NOT be read; symlink counts as 1 line.
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}

	svc := NewService(&realGitPulseSource{})
	var stats DailyPulseResponse
	svc.CollectGitDailyStats([]GitTarget{{Dir: repo, RepoRoot: repo}}, todayStart, &stats)

	if stats.UncommittedAdditions != 1501 || stats.UncommittedFiles != 2 || stats.UncommittedTruncated {
		t.Fatalf("large untracked + symlink stats = %+v, want UncommittedAdditions=1501 UncommittedFiles=2 Truncated=false", stats)
	}
}

func TestCollectGitDailyStatsDeduplicatesTransferredUncommittedAcrossWorktrees(t *testing.T) {
	repo := t.TempDir()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := todayStart.Add(-12 * time.Hour).Format(time.RFC3339)

	runTestGit(t, repo, yesterday, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "shared.go"), []byte("package main\nvar a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, yesterday, "add", ".")
	runTestGit(t, repo, yesterday, "commit", "-q", "-m", "seed")

	// Ignore .hoxi in git exclude just like Hoxi does.
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte(".hoxi/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt1 := filepath.Join(repo, ".hoxi", "worktrees", "ses_1")
	wt2 := filepath.Join(repo, ".hoxi", "worktrees", "ses_2")
	if err := os.MkdirAll(filepath.Dir(wt1), 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, yesterday, "worktree", "add", "-q", "-b", "hoxi/ses_1", wt1, "HEAD")
	runTestGit(t, repo, yesterday, "worktree", "add", "-q", "-b", "hoxi/ses_2", wt2, "HEAD")

	// Identical tracked edit (+2/0) + identical untracked file (+3/0) in BOTH root and wt1
	// (simulating TransferWorkingState). Must count ONCE (+5 total), NOT 2x (+10).
	for _, dir := range []string{repo, wt1} {
		if err := os.WriteFile(filepath.Join(dir, "shared.go"), []byte("package main\nvar a = 1\nvar b = 2\nvar c = 3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "new_copied.go"), []byte("package main\nvar n1 = 1\nvar n2 = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Distinct tracked edit (+1/0) on the same file in wt2 must still be counted!
	if err := os.WriteFile(filepath.Join(wt2, "shared.go"), []byte("package main\nvar a = 1\nvar distinct = 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(&realGitPulseSource{})
	targets := []GitTarget{
		{Dir: repo, RepoRoot: repo, IsWorktree: false},
		{Dir: wt1, RepoRoot: repo, IsWorktree: true},
		{Dir: wt2, RepoRoot: repo, IsWorktree: true},
	}
	var stats DailyPulseResponse
	svc.CollectGitDailyStats(targets, todayStart, &stats)

	// shared.go in root+wt1 (+2 once) + new_copied.go in root+wt1 (+3 once) + shared.go in wt2 (+1) = +6.
	if stats.UncommittedAdditions != 6 || stats.UncommittedDeletions != 0 || stats.UncommittedFiles != 2 {
		t.Fatalf("deduplicated cross-worktree stats = %+v, want UncommittedAdditions=6 UncommittedDeletions=0 UncommittedFiles=2", stats)
	}

	// Verify deduplication stays accurate even when user gitconfig enables
	// diff.mnemonicPrefix / diff.noprefix and files use CRLF line endings.
	runTestGit(t, repo, yesterday, "config", "diff.mnemonicPrefix", "true")
	runTestGit(t, repo, yesterday, "config", "diff.noprefix", "true")
	if err := os.WriteFile(filepath.Join(repo, "crlf.txt"), []byte("line1\r\nline2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt1, "crlf.txt"), []byte("line1\r\nline2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, wt1, yesterday, "add", "crlf.txt")
	var statsCustomCfg DailyPulseResponse
	svc.CollectGitDailyStats(targets, todayStart, &statsCustomCfg)
	// Previous +6 plus crlf.txt (+2 once across untracked root and staged wt1) = +8 across 3 files.
	if statsCustomCfg.UncommittedAdditions != 8 || statsCustomCfg.UncommittedFiles != 3 {
		t.Fatalf("dedup with mnemonicPrefix/noprefix + CRLF = %+v, want UncommittedAdditions=8 UncommittedFiles=3", statsCustomCfg)
	}
}

func TestResolveGitTargetsSkipsDismissedDirectoriesAndMapsLinkedWorktree(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "hoxi.db"))

	repoKeep := t.TempDir()
	now := time.Now().Format(time.RFC3339)
	runTestGit(t, repoKeep, now, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoKeep, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoKeep, now, "add", ".")
	runTestGit(t, repoKeep, now, "commit", "-q", "-m", "seed")

	// External linked worktree outside .hoxi/worktrees (e.g. .claude/worktrees/ext).
	extWT := filepath.Join(repoKeep, ".claude", "worktrees", "ext")
	if err := os.MkdirAll(filepath.Dir(extWT), 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoKeep, now, "worktree", "add", "-q", "-b", "claude/ext", extWT, "HEAD")

	repoDismissed := t.TempDir()
	runTestGit(t, repoDismissed, now, "init", "-q", "-b", "main")

	src := &realGitPulseSource{
		stubPulseSource: stubPulseSource{
			candidates: []string{extWT, repoDismissed},
		},
		dismissed: map[string]bool{repoDismissed: true},
	}
	svc := NewService(src)

	global := svc.ResolveGitTargets("", "")
	if len(global) != 2 {
		t.Fatalf("global targets = %+v, want 2 (repoKeep root + extWT worktree; repoDismissed skipped)", global)
	}
	if global[0].IsWorktree || !samePulseDir(global[0].Dir, repoKeep) {
		t.Fatalf("expected repoKeep as root target, got %+v", global[0])
	}
	if !global[1].IsWorktree || !samePulseDir(global[1].Dir, extWT) || !samePulseDir(global[1].RepoRoot, repoKeep) {
		t.Fatalf("expected external linked worktree mapped to repoKeep, got %+v", global[1])
	}

	// Explicit directory scope still resolves a dismissed directory when directly queried.
	explicit := svc.ResolveGitTargets(repoDismissed, "")
	if len(explicit) != 1 || !samePulseDir(explicit[0].Dir, repoDismissed) {
		t.Fatalf("explicit query for dismissed repo should still resolve, got %+v", explicit)
	}
}
