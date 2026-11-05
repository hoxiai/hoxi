package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelinkProjectMovesLocalSessionWithoutTouchingActivity(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRelinkProjectToNonGitDirectorySwitchesWorktreeSessionToLocal(t *testing.T) {
	t.Parallel()
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	svc := NewService(oldRoot, nil, nil, t.TempDir())
	oldWorktree := filepath.Join(oldRoot, ".hoxi", "worktrees", "ses_relink_nongit")
	if err := svc.store.Put(SessionRecord{
		ID: "ses_relink_nongit", ProjectID: "prj_relink", Runtime: "claude-code",
		ProjectDir: oldRoot, WorktreePath: oldWorktree, Branch: "hoxi/ses_relink_nongit", BaseCommit: "abc1234",
		SandboxMode: "worktree", CreatedAt: 11, UpdatedAt: 22,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.RelinkProject("prj_relink", oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	rec, ok := svc.store.Get("ses_relink_nongit")
	if !ok {
		t.Fatal("relinked session disappeared")
	}
	if rec.SandboxMode != "local" {
		t.Fatalf("SandboxMode = %q, want local", rec.SandboxMode)
	}
	if normalizeComparePath(rec.ProjectDir) != normalizeComparePath(newRoot) ||
		normalizeComparePath(rec.WorktreePath) != normalizeComparePath(newRoot) {
		t.Fatalf("relinked paths = %q / %q, want %q", rec.ProjectDir, rec.WorktreePath, newRoot)
	}
	if rec.Branch != "" || rec.BaseCommit != "" {
		t.Fatalf("expected branch/baseCommit cleared on non-git fallback, got %q / %q", rec.Branch, rec.BaseCommit)
	}
	binding, known, err := svc.worktrees.BindingForSession("ses_relink_nongit")
	if err != nil || !known || binding.Isolated ||
		normalizeComparePath(binding.Directory) != normalizeComparePath(newRoot) {
		t.Fatalf("binding = %+v, %v, %v", binding, known, err)
	}
}

func TestRelinkProjectSkipsSessionWithActiveTurnProcess(t *testing.T) {
	t.Parallel()
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	svc := NewService(oldRoot, nil, nil, t.TempDir())
	if err := svc.worktrees.ForDirectory(oldRoot).UseLocalDirectory("ses_relink_busy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.Put(SessionRecord{
		ID: "ses_relink_busy", ProjectID: "prj_relink", Runtime: "claude-code",
		ProjectDir: oldRoot, WorktreePath: oldRoot, SandboxMode: "local",
		CreatedAt: 11, UpdatedAt: 22,
	}); err != nil {
		t.Fatal(err)
	}

	svc.mu.Lock()
	svc.procs["ses_relink_busy"] = &activeProcess{}
	svc.mu.Unlock()
	t.Cleanup(func() {
		svc.mu.Lock()
		delete(svc.procs, "ses_relink_busy")
		svc.mu.Unlock()
	})

	err := svc.RelinkProject("prj_relink", oldRoot, newRoot)
	if err == nil || !strings.Contains(err.Error(), "session is running") {
		t.Fatalf("RelinkProject err = %v, want running session error", err)
	}
	rec, ok := svc.store.Get("ses_relink_busy")
	if !ok || normalizeComparePath(rec.ProjectDir) != normalizeComparePath(oldRoot) ||
		normalizeComparePath(rec.WorktreePath) != normalizeComparePath(oldRoot) {
		t.Fatalf("busy session was modified: %+v, %v", rec, ok)
	}
	binding, known, bindErr := svc.worktrees.BindingForSession("ses_relink_busy")
	if bindErr != nil || !known || normalizeComparePath(binding.Directory) != normalizeComparePath(oldRoot) {
		t.Fatalf("busy session binding changed: %+v, %v, %v", binding, known, bindErr)
	}
}

type stubProjectResolver struct {
	roots map[string][]string
	byDir map[string]string
}

func (r *stubProjectResolver) ProjectIDForDirectory(dir string) (string, bool) {
	id, ok := r.byDir[normalizeComparePath(dir)]
	return id, ok
}

func (r *stubProjectResolver) EnsureProjectForDirectory(dir string) (string, error) {
	id, _ := r.ProjectIDForDirectory(dir)
	return id, nil
}

func (r *stubProjectResolver) EnsureProjectForDirectoryWithSurface(dir, _ string) (string, error) {
	return r.EnsureProjectForDirectory(dir)
}

func (r *stubProjectResolver) CanonicalProjectID(id string) (string, bool) {
	_, ok := r.roots[id]
	return id, ok
}

func (r *stubProjectResolver) ProjectRoots(id string) []string {
	return r.roots[id]
}

func TestHealSessionDirectoryRelinksStaleProjectSession(t *testing.T) {
	t.Parallel()
	oldRoot := filepath.Join(t.TempDir(), "deleted-old-root")
	newRoot := t.TempDir()
	svc := NewService(newRoot, nil, nil, t.TempDir())
	svc.SetProjectResolver(&stubProjectResolver{
		roots: map[string][]string{"prj_1": {newRoot}},
		byDir: map[string]string{normalizeComparePath(newRoot): "prj_1"},
	})
	if err := svc.store.Put(SessionRecord{
		ID: "ses_stale_1", ProjectID: "prj_1", Runtime: "claude-code",
		ProjectDir: oldRoot, WorktreePath: oldRoot, SandboxMode: "local",
		CreatedAt: 10, UpdatedAt: 20,
	}); err != nil {
		t.Fatal(err)
	}

	if !svc.HealSessionDirectory("ses_stale_1") {
		t.Fatal("expected HealSessionDirectory to relink stale session")
	}
	rec, ok := svc.store.Get("ses_stale_1")
	if !ok || normalizeComparePath(rec.ProjectDir) != normalizeComparePath(newRoot) ||
		normalizeComparePath(rec.WorktreePath) != normalizeComparePath(newRoot) {
		t.Fatalf("healed session = %+v, %v", rec, ok)
	}
}
