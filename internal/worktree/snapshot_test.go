package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotPreservesTrackedClaudeEditsAndStagedIndex(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t)

	// Commit a tracked .claude/settings.json file at v1.
	claudeDir := filepath.Join(repoDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeSettings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(claudeSettings, []byte(`{"version":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "add", "-f", ".claude/settings.json"); err != nil {
		t.Fatalf("git add .claude/settings.json: %v", err)
	}
	if _, err := runGit(repoDir, "commit", "-m", "chore: track .claude/settings.json"); err != nil {
		t.Fatalf("git commit .claude/settings.json: %v", err)
	}

	// User makes an uncommitted working-tree edit to .claude/settings.json (v2),
	// creates an untracked .claude/runtime.log, and stages a change to README.md.
	if err := os.WriteFile(claudeSettings, []byte(`{"version":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeRuntimeLog := filepath.Join(claudeDir, "runtime.log")
	if err := os.WriteFile(claudeRuntimeLog, []byte("runtime trace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Hoxi Test\nstaged user line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "add", "README.md"); err != nil {
		t.Fatalf("git add README.md: %v", err)
	}

	// Capture snapshot before agent turn.
	snap, err := CreateSnapshot(repoDir)
	if err != nil || snap == "" {
		t.Fatalf("CreateSnapshot failed: snap=%q err=%v", snap, err)
	}

	// Verify .claude/ was NOT added to .git/info/exclude since .claude has tracked files.
	excludeBytes, err := os.ReadFile(filepath.Join(repoDir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(excludeBytes), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".claude" || trimmed == ".claude/" {
			t.Fatalf("expected .claude/ not to be added to .git/info/exclude when tracked, got:\n%s", string(excludeBytes))
		}
	}

	// Verify untracked .claude/runtime.log was not included in the snapshot tree.
	treeOut, err := runGit(repoDir, "ls-tree", "-r", "--name-only", snap)
	if err != nil {
		t.Fatalf("ls-tree snap: %v", err)
	}
	if strings.Contains(treeOut, "runtime.log") {
		t.Fatalf("expected untracked .claude/runtime.log excluded from snapshot tree, got:\n%s", treeOut)
	}

	// Simulate agent turn modifying .claude/settings.json, README.md, and creating agent.txt.
	if err := os.WriteFile(claudeSettings, []byte(`{"version":999}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readmePath, []byte("# Hoxi Test\nagent overwrite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentFile := filepath.Join(repoDir, "agent.txt")
	if err := os.WriteFile(agentFile, []byte("agent artifact\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restore snapshot.
	if err := RestoreSnapshot(repoDir, snap); err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	// 1. User's uncommitted edit to tracked .claude/settings.json (v2) must survive!
	gotClaude, err := os.ReadFile(claudeSettings)
	if err != nil {
		t.Fatalf("read .claude/settings.json: %v", err)
	}
	if string(gotClaude) != `{"version":2}`+"\n" {
		t.Fatalf(".claude/settings.json = %q, want %q", string(gotClaude), `{"version":2}`+"\n")
	}

	// 2. Untracked .claude/runtime.log must remain intact.
	if gotLog, err := os.ReadFile(claudeRuntimeLog); err != nil || string(gotLog) != "runtime trace\n" {
		t.Fatalf(".claude/runtime.log = %q (err=%v), want %q", string(gotLog), err, "runtime trace\n")
	}

	// 3. Agent-created untracked file outside .claude must be removed.
	if _, err := os.Stat(agentFile); !os.IsNotExist(err) {
		t.Fatalf("expected agent.txt to be removed by RestoreSnapshot, err=%v", err)
	}

	// 4. User's staged changes in the real git index must be preserved.
	cachedDiff, err := runGitRaw(repoDir, "diff", "--cached")
	if err != nil {
		t.Fatalf("git diff --cached: %v", err)
	}
	if !strings.Contains(cachedDiff, "+staged user line") {
		t.Fatalf("expected user staged change in README.md to survive RestoreSnapshot, got cached diff:\n%s", cachedDiff)
	}
}

func TestEnsureGitExcludeExactLineMatch(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t)
	excludePath := filepath.Join(repoDir, ".git", "info", "exclude")

	// Write a comment and a subpath that contain ".claude/" as a substring.
	initial := "# comment mentioning .claude/\n.claude/local.json\n"
	if err := os.WriteFile(excludePath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	ensureGitExclude(repoDir, ".hoxi/", ".claude/")
	afterFirst, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(afterFirst)), "\n")
	var hoxiCount, claudeCount int
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case ".hoxi", ".hoxi/":
			hoxiCount++
		case ".claude", ".claude/":
			claudeCount++
		}
	}
	if hoxiCount != 1 || claudeCount != 1 {
		t.Fatalf("expected 1 .hoxi/ and 1 .claude/ line, got hoxi=%d claude=%d in:\n%s", hoxiCount, claudeCount, string(afterFirst))
	}

	// Calling ensureGitExclude again with or without trailing slash must not duplicate entries.
	ensureGitExclude(repoDir, ".hoxi", ".hoxi/", ".claude", ".claude/")
	afterSecond, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSecond) != string(afterFirst) {
		t.Fatalf("expected idempotent exclude file, got:\n%s\nwant:\n%s", string(afterSecond), string(afterFirst))
	}
}
