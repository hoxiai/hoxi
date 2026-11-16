package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A project with an untracked .opencode/ directory gets a .opencode symlink in
// each session worktree. The link must never be counted or committed: once
// committed, merging the branch back fails because Git refuses to replace the
// project's untracked .opencode/ directory with a symlink.
func TestSharedOpenCodeLinkIsNeverCommittedOrMerged(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git(repoDir, "init", "-b", "main")
	git(repoDir, "config", "core.autocrlf", "false")
	configGitUser(repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Hoxi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "README.md")
	git(repoDir, "commit", "-m", "init")
	// Untracked project .opencode/, as OpenCode leaves behind (no exclude rule).
	if err := os.MkdirAll(filepath.Join(repoDir, ".opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".opencode", "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(repoDir)
	info, err := mgr.Create("ses_opencode_link")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	configGitUser(info.Directory)
	link, err := os.Lstat(filepath.Join(info.Directory, ".opencode"))
	if err != nil || link.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected .opencode symlink in worktree, err=%v", err)
	}
	if status := strings.TrimSpace(git(info.Directory, "status", "--porcelain")); status != "" {
		t.Fatalf("fresh worktree must look clean, got %q", status)
	}
	if status := git(repoDir, "status", "--porcelain"); !strings.Contains(status, ".opencode/") {
		t.Fatalf("project's own untracked .opencode/ must stay visible, got %q", status)
	}

	if err := os.WriteFile(filepath.Join(info.Directory, "feature.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ApplyWithMessage("ses_opencode_link", "merge", "feat: add feature"); err != nil {
		t.Fatalf("merge back to main: %v", err)
	}
	if files := git(repoDir, "ls-tree", "--name-only", "HEAD"); strings.Contains(files, ".opencode") ||
		!strings.Contains(files, "feature.txt") {
		t.Fatalf("main tree must contain feature.txt and no .opencode, got %q", files)
	}
	if fi, err := os.Lstat(filepath.Join(repoDir, ".opencode")); err != nil || !fi.IsDir() {
		t.Fatalf("project .opencode/ directory must survive the merge, err=%v", err)
	}
}

// A user rule that already ignores the project's .opencode/ must not be undone.
func TestExcludeSharedOpenCodeLinkKeepsUserIgnore(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t) // writes ".opencode" to info/exclude
	if _, err := NewManager(repoDir).Create("ses_opencode_ignored"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repoDir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "!/.opencode/") {
		t.Fatalf("must not re-include an ignored .opencode/, exclude=%q", data)
	}
}
