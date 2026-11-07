package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reviewTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// Windows installs Git with core.autocrlf=true; pin it off so the fixture
	// blobs, working files and patches share one line ending and --3way applies.
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Env = nonInteractiveGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func reviewTestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newMergeFixture creates a repo with a committed seven-line file and the
// given sibling worktrees, all on the same base commit.
func newMergeFixture(t *testing.T, names ...string) map[string]string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	reviewTestGit(t, repo, "init", "-q", "-b", "main")
	reviewTestWrite(t, filepath.Join(repo, "f.txt"), "a\nb\nc\nd\ne\nf\ng\n")
	reviewTestGit(t, repo, "add", ".")
	reviewTestGit(t, repo, "commit", "-q", "-m", "base")
	dirs := map[string]string{"repo": repo}
	for _, name := range names {
		dir := filepath.Join(root, name)
		reviewTestGit(t, repo, "worktree", "add", "-q", "-b", name, dir)
		dirs[name] = dir
	}
	return dirs
}

func readMergeFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMergeWorkingStateCombinesTransitiveChanges(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t, "a", "b", "c")
	// b depends on a, so its working tree already carries a's edit.
	reviewTestWrite(t, filepath.Join(dirs["a"], "f.txt"), "a\nB-from-a\nc\nd\ne\nf\ng\n")
	reviewTestWrite(t, filepath.Join(dirs["b"], "f.txt"), "a\nB-from-a\nc\nd\ne\nF-from-b\ng\n")
	reviewTestWrite(t, filepath.Join(dirs["b"], "pkg", "new.go"), "package pkg\n")

	if err := MergeWorkingState(dirs["c"], dirs["a"], dirs["b"]); err != nil {
		t.Fatalf("MergeWorkingState: %v", err)
	}
	if got := readMergeFile(t, filepath.Join(dirs["c"], "f.txt")); got != "a\nB-from-a\nc\nd\ne\nF-from-b\ng\n" {
		t.Fatalf("merged file = %q", got)
	}
	if got := readMergeFile(t, filepath.Join(dirs["c"], "pkg", "new.go")); got != "package pkg\n" {
		t.Fatalf("new file = %q", got)
	}
}

func TestMergeWorkingStateRestoresTargetOnConflict(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t, "a", "b", "d")
	reviewTestWrite(t, filepath.Join(dirs["a"], "f.txt"), "a\nb\nc\nd\ne\nf\nG-from-a\n")
	reviewTestWrite(t, filepath.Join(dirs["b"], "f.txt"), "a\nB-from-b\nc\nd\ne\nf\ng\n")
	original := "a\nB-from-d\nc\nd\ne\nf\ng\n"
	reviewTestWrite(t, filepath.Join(dirs["d"], "f.txt"), original)
	reviewTestWrite(t, filepath.Join(dirs["d"], "own.txt"), "kept\n")

	err := MergeWorkingState(dirs["d"], dirs["a"], dirs["b"])
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict, got %v", err)
	}
	if got := readMergeFile(t, filepath.Join(dirs["d"], "f.txt")); got != original {
		t.Fatalf("target not restored after conflict: %q", got)
	}
	if got := readMergeFile(t, filepath.Join(dirs["d"], "own.txt")); got != "kept\n" {
		t.Fatalf("target's own untracked file lost: %q", got)
	}
	if status := reviewTestGit(t, dirs["d"], "status", "--porcelain"); strings.Contains(status, "UU") {
		t.Fatalf("conflict markers left in index:\n%s", status)
	}
}

func TestMergeWorkingStateRejectsDivergentNewFiles(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t, "a", "b")
	reviewTestWrite(t, filepath.Join(dirs["a"], "same.txt"), "from a\n")
	reviewTestWrite(t, filepath.Join(dirs["b"], "same.txt"), "from b\n")

	if err := MergeWorkingState(dirs["b"], dirs["a"]); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict for divergent new files, got %v", err)
	}
	if got := readMergeFile(t, filepath.Join(dirs["b"], "same.txt")); got != "from b\n" {
		t.Fatalf("target file overwritten: %q", got)
	}
}

func TestMergeWorkingStateIncludesCommittedSourceChanges(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t, "a", "b")
	reviewTestWrite(t, filepath.Join(dirs["a"], "f.txt"), "a\nB-committed-in-a\nc\nd\ne\nf\ng\n")
	reviewTestWrite(t, filepath.Join(dirs["a"], "committed.go"), "package main\n")
	reviewTestGit(t, dirs["a"], "add", ".")
	reviewTestGit(t, dirs["a"], "commit", "-q", "-m", "feat: subtask a commit")

	if err := MergeWorkingState(dirs["b"], dirs["a"]); err != nil {
		t.Fatalf("MergeWorkingState with committed source changes: %v", err)
	}
	if got := readMergeFile(t, filepath.Join(dirs["b"], "f.txt")); got != "a\nB-committed-in-a\nc\nd\ne\nf\ng\n" {
		t.Fatalf("merged f.txt = %q", got)
	}
	if got := readMergeFile(t, filepath.Join(dirs["b"], "committed.go")); got != "package main\n" {
		t.Fatalf("merged committed.go = %q", got)
	}
}

func TestApplyAbortsCleanlyOnMergeOrPatchConflict(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t)
	repo := dirs["repo"]
	mgr := NewManager(repo)

	meta, err := mgr.Create("ses_conflict")
	if err != nil {
		t.Fatalf("Create worktree: %v", err)
	}
	// Edit line 2 in the isolated worktree
	reviewTestWrite(t, filepath.Join(meta.Directory, "f.txt"), "a\nB-sandbox\nc\nd\ne\nf\ng\n")
	// Diverge line 2 on main in the primary repo and commit
	reviewTestWrite(t, filepath.Join(repo, "f.txt"), "a\nB-main\nc\nd\ne\nf\ng\n")
	reviewTestGit(t, repo, "add", "f.txt")
	reviewTestGit(t, repo, "commit", "-q", "-m", "main edit")

	if err := mgr.Apply("ses_conflict", "merge"); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict from merge mode, got %v", err)
	}
	if status := reviewTestGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean repo after aborted merge, got:\n%s", status)
	}
	if got := readMergeFile(t, filepath.Join(repo, "f.txt")); got != "a\nB-main\nc\nd\ne\nf\ng\n" {
		t.Fatalf("repo file altered after failed merge: %q", got)
	}

	if err := mgr.Apply("ses_conflict", "patch"); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict from patch mode, got %v", err)
	}
	if status := reviewTestGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean repo after aborted patch apply, got:\n%s", status)
	}
	if got := readMergeFile(t, filepath.Join(repo, "f.txt")); got != "a\nB-main\nc\nd\ne\nf\ng\n" {
		t.Fatalf("repo file altered after failed patch apply: %q", got)
	}
}

func TestPatchConflictPreservesLargeStagedAdditionAndExistingStaging(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t)
	repo := dirs["repo"]
	mgr := NewManager(repo)

	meta, err := mgr.Create("ses_patch_large_staged")
	if err != nil {
		t.Fatal(err)
	}
	reviewTestWrite(t, filepath.Join(meta.Directory, "f.txt"), "a\nB-sandbox\nc\nd\ne\nf\ng\n")
	reviewTestWrite(t, filepath.Join(repo, "f.txt"), "a\nB-main\nc\nd\ne\nf\ng\n")
	reviewTestGit(t, repo, "add", "f.txt")
	reviewTestGit(t, repo, "commit", "-q", "-m", "main divergence")

	large := filepath.Join(repo, "user-staged-large.bin")
	largeContent := strings.Repeat("x", maxSnapshotUntrackedFileSize+1)
	reviewTestWrite(t, large, largeContent)
	reviewTestWrite(t, filepath.Join(repo, "user-staged.txt"), "user work\n")
	reviewTestGit(t, repo, "add", "user-staged-large.bin", "user-staged.txt")

	beforeIndex := reviewTestGit(t, repo, "diff", "--cached", "--name-only")
	if err := mgr.Apply("ses_patch_large_staged", "patch"); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict, got %v", err)
	}
	info, err := os.Stat(large)
	if err != nil {
		t.Fatalf("large staged user file lost after failed patch apply: %v", err)
	}
	if info.Size() != int64(len(largeContent)) {
		t.Fatalf("large staged user file size = %d, want %d", info.Size(), len(largeContent))
	}
	if got := readMergeFile(t, filepath.Join(repo, "user-staged.txt")); got != "user work\n" {
		t.Fatalf("user-staged.txt content = %q", got)
	}
	if after := reviewTestGit(t, repo, "diff", "--cached", "--name-only"); after != beforeIndex {
		t.Fatalf("user staged index lost after failed patch apply: before=%q after=%q", beforeIndex, after)
	}
}

func TestPatchConflictRollbackRestoresAllGitChangedPaths(t *testing.T) {
	t.Parallel()
	for _, name := range []string{".opencode/config.js", " spaced.txt", "trailing.txt "} {
		name := name
		t.Run(strings.TrimSpace(name), func(t *testing.T) {
			t.Parallel()
			dirs := newMergeFixture(t)
			repo := dirs["repo"]
			p := filepath.Join(repo, name)
			reviewTestWrite(t, p, "user original config\n")
			reviewTestGit(t, repo, "add", "--", name)
			reviewTestGit(t, repo, "commit", "-q", "-m", "config base")

			mgr := NewManager(repo)
			meta, err := mgr.Create("ses_audit_tx")
			if err != nil {
				t.Fatal(err)
			}
			reviewTestWrite(t, filepath.Join(meta.Directory, "f.txt"), "a\nB-sandbox\nc\nd\ne\nf\ng\n")
			if err := os.Remove(filepath.Join(meta.Directory, name)); err != nil {
				t.Fatal(err)
			}
			reviewTestWrite(t, filepath.Join(repo, "f.txt"), "a\nB-main\nc\nd\ne\nf\ng\n")
			reviewTestGit(t, repo, "add", "f.txt")
			reviewTestGit(t, repo, "commit", "-q", "-m", "main divergence")

			if err := mgr.Apply("ses_audit_tx", "patch"); !errors.Is(err, ErrMergeConflict) {
				t.Fatalf("expected ErrMergeConflict, got %v", err)
			}
			got, err := os.ReadFile(p)
			if err != nil || string(got) != "user original config\n" {
				t.Fatalf("rollback lost %q: content=%q err=%v", name, got, err)
			}
		})
	}
}

func TestBeginWorkingTreeTransactionRejectsEscapingOrDotGitPath(t *testing.T) {
	t.Parallel()
	dirs := newMergeFixture(t)
	repo := dirs["repo"]
	for _, bad := range []string{"../outside.txt", ".git/config", "sub/.git/hooks/pre-commit"} {
		if _, err := beginWorkingTreeTransaction(repo, []string{"f.txt", bad}); err == nil {
			t.Fatalf("expected beginWorkingTreeTransaction to reject %q", bad)
		}
	}
}
