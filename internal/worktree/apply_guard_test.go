package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyStopsWhenSnapshotCommitFails(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)
	info, err := mgr.Create("ses_apply_fail")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	configGitUser(info.Directory)

	hook := filepath.Join(repoDir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Directory, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	if err := mgr.Apply("ses_apply_fail", "patch"); err == nil {
		t.Fatal("expected Apply to fail when snapshot commit fails")
	}
	mainReadme, err := os.ReadFile(filepath.Join(repoDir, "README.md"))
	if err != nil {
		t.Fatalf("read main README: %v", err)
	}
	if string(mainReadme) != "# Hoxi Test\n" {
		t.Fatalf("main workspace changed after failed snapshot: %q", mainReadme)
	}
	if err := mgr.Apply("ses_apply_fail", "merge"); err == nil {
		t.Fatal("expected merge Apply to fail when snapshot commit fails")
	}
}

func TestRemoveKeepsBranchWhenWorktreeRemoveFails(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)
	info, err := mgr.Create("ses_rm_fail")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "lock", info.Directory).CombinedOutput(); err != nil {
		t.Fatalf("lock worktree: %v\n%s", err, out)
	}

	if err := mgr.Remove("ses_rm_fail", true); err == nil {
		t.Fatal("expected Remove to fail for a locked worktree")
	}
	if _, err := os.Stat(info.Directory); err != nil {
		t.Fatalf("worktree directory removed despite failure: %v", err)
	}
	out, err := exec.Command("git", "-C", repoDir, "branch", "--list", "hoxi/ses_rm_fail").CombinedOutput()
	if err != nil {
		t.Fatalf("list branch: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "hoxi/ses_rm_fail") {
		t.Fatalf("branch deleted after failed remove: %q", out)
	}
}

func TestReusedWorktreeBranchCannotUndoNewProjectCommits(t *testing.T) {
	t.Parallel()
	root := initTempGitRepo(t)
	mgr := NewManager(root)
	first, err := mgr.Create("ses_reuse")
	if err != nil {
		t.Fatal(err)
	}
	configGitUser(first.Directory)
	sessionTestWrite(t, first.Directory, "session.txt", "session edit\n")
	sessionTestGit(t, first.Directory, "add", "session.txt")
	sessionTestGit(t, first.Directory, "commit", "-m", appendHoxiCoAuthor("session edit"))
	if err := mgr.RemoveWithOptions("ses_reuse", false, true); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UseLocalDirectory("ses_reuse"); err != nil {
		t.Fatal(err)
	}
	sessionTestWrite(t, root, "main-only.txt", "unrelated project commit\n")
	sessionTestGit(t, root, "add", "main-only.txt")
	sessionTestGit(t, root, "commit", "-m", appendHoxiCoAuthor("project edit"))
	newHead := sessionTestGit(t, root, "rev-parse", "HEAD")
	reused, err := mgr.Create("ses_reuse")
	if err != nil {
		t.Fatal(err)
	}
	if reused.BaseCommit != first.BaseCommit {
		t.Fatalf("old branch was rebased to wrong baseline: %s", reused.BaseCommit)
	}
	// Also repair the apply behavior of metadata produced before this fix.
	reused.BaseCommit = newHead
	mgr.storeMeta(reused)
	if err := mgr.Apply("ses_reuse", "patch"); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"main-only.txt": "unrelated project commit\n", "session.txt": "session edit\n"} {
		got, err := os.ReadFile(filepath.Join(root, file))
		if err != nil || string(got) != want {
			t.Fatalf("apply corrupted %s: %q %v", file, got, err)
		}
	}
}
