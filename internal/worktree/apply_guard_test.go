package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyStopsWhenSnapshotCommitFails(t *testing.T) {
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
