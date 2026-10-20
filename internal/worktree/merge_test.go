package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
