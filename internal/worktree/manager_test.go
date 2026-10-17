package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@hoxi.local")

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Hoxi Test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "chore: initial commit")
	return dir
}

func TestSanitizeSessionID(t *testing.T) {
	cases := map[string]string{
		"ses_123-abc":          "ses_123-abc",
		"../evil/../../passwd": "evilpasswd",
		"   ":                  "",
		"ses!@#$%^&*()99":      "ses99",
	}
	for input, want := range cases {
		if got := sanitizeSessionID(input); got != want {
			t.Errorf("sanitizeSessionID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWorktreeLifecycleCreateDiffRevertApplyRemove(t *testing.T) {
	repoDir := initTempGitRepo(t)
	mgr := newWorktreeManager(repoDir)

	// 1. Create worktree for session
	info, err := mgr.Create("ses_unit_01")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !info.Isolated || info.Branch != "hoxi/ses_unit_01" {
		t.Fatalf("unexpected worktree info: %+v", info)
	}

	// Ensure git user config inside worktree too
	for _, kv := range [][2]string{{"user.name", "Test User"}, {"user.email", "test@hoxi.local"}} {
		cmd := exec.Command("git", "-C", info.Directory, "config", kv[0], kv[1])
		_ = cmd.Run()
	}

	// 2. Modify tracked file and create untracked file in worktree
	trackedPath := filepath.Join(info.Directory, "README.md")
	if err := os.WriteFile(trackedPath, []byte("# Hoxi Test\nAdded line\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	untrackedPath := filepath.Join(info.Directory, "notes.txt")
	if err := os.WriteFile(untrackedPath, []byte("temp notes\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	status, err := mgr.Status("ses_unit_01")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !status.Dirty || status.ChangedFiles != 2 {
		t.Fatalf("expected 2 changed files (tracked + untracked), got %+v", status)
	}

	// 3. Revert untracked file
	if err := mgr.RevertFile("ses_unit_01", "notes.txt"); err != nil {
		t.Fatalf("RevertFile failed: %v", err)
	}
	if _, err := os.Stat(untrackedPath); !os.IsNotExist(err) {
		t.Fatalf("expected untracked file to be removed after RevertFile")
	}

	// 4. Commit inside worktree and apply change back to main workspace
	hash, err := mgr.CommitWorktree("ses_unit_01", "feat: update readme")
	if err != nil || hash == "" {
		t.Fatalf("CommitWorktree failed: err=%v, hash=%q", err, hash)
	}
	if err := mgr.Apply("ses_unit_01", "patch"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	mainReadme, err := os.ReadFile(filepath.Join(repoDir, "README.md"))
	if err != nil {
		t.Fatalf("read main README: %v", err)
	}
	if string(mainReadme) != "# Hoxi Test\nAdded line\n" {
		t.Fatalf("expected main README to have applied change, got %q", string(mainReadme))
	}

	// 5. Remove worktree
	if err := mgr.Remove("ses_unit_01", true); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	afterRemove, err := mgr.Status("ses_unit_01")
	if err != nil {
		t.Fatalf("Status after remove failed: %v", err)
	}
	if afterRemove.Isolated {
		t.Fatalf("expected worktree Isolated=false after Remove")
	}
}
