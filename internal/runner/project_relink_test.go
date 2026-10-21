package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRelinkProjectMovesLocalSessionWithoutTouchingActivity(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	svc := NewService(oldRoot, nil, nil, t.TempDir())
	if err := svc.store.Put(SessionRecord{
		ID: "ses_relink_local", ProjectID: "prj_relink", Runtime: "claude-code",
		ProjectDir: oldRoot, WorktreePath: oldRoot, SandboxMode: "local",
		CreatedAt: 11, UpdatedAt: 22,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.RelinkProject("prj_relink", oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	rec, ok := svc.store.Get("ses_relink_local")
	if !ok || rec.ProjectDir != newRoot || rec.WorktreePath != newRoot {
		t.Fatalf("relinked record = %+v, %v", rec, ok)
	}
	if rec.UpdatedAt != 22 {
		t.Fatalf("relink changed UpdatedAt = %d, want 22", rec.UpdatedAt)
	}
	binding, known, err := svc.worktrees.BindingForSession("ses_relink_local")
	if err != nil || !known || binding.Isolated ||
		normalizeComparePath(binding.Directory) != normalizeComparePath(newRoot) {
		t.Fatalf("binding = %+v, %v, %v", binding, known, err)
	}
}

func TestRelinkProjectMovesWorktreeAndRepairsGitPointer(t *testing.T) {
	oldRoot := initTestGitRepo(t)
	newRoot := oldRoot + "-moved"
	svc := NewService(oldRoot, nil, nil, t.TempDir())
	meta, err := svc.worktrees.ForDirectory(oldRoot).CreateOnBranch("ses_relink_worktree", "")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Isolated {
		t.Fatal("expected an isolated worktree")
	}
	if err := svc.store.Put(SessionRecord{
		ID: "ses_relink_worktree", ProjectID: "prj_relink", Runtime: "claude-code",
		ProjectDir: oldRoot, WorktreePath: meta.Directory, Branch: meta.Branch, BaseCommit: meta.BaseCommit,
		SandboxMode: "worktree", CreatedAt: 11, UpdatedAt: 22,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(newRoot) })

	if err := svc.RelinkProject("prj_relink", oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	rec, ok := svc.store.Get("ses_relink_worktree")
	if !ok {
		t.Fatal("relinked session disappeared")
	}
	wantWorktree := filepath.Join(newRoot, ".hoxi", "worktrees", "ses_relink_worktree")
	if normalizeComparePath(rec.ProjectDir) != normalizeComparePath(newRoot) ||
		normalizeComparePath(rec.WorktreePath) != normalizeComparePath(wantWorktree) {
		t.Fatalf("relinked paths = %q / %q", rec.ProjectDir, rec.WorktreePath)
	}
	if rec.UpdatedAt != 22 {
		t.Fatalf("relink changed UpdatedAt = %d, want 22", rec.UpdatedAt)
	}
	if _, err := exec.Command("git", "-C", wantWorktree, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("git pointer was not repaired: %v", err)
	}
}
