package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkspacePathTildeExpansionAndBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	worktree := filepath.Join(home, "workspace")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	skillRef := filepath.Join(home, ".hoxi", "skills", "work-app-builder", "references", "contract.md")
	if err := os.MkdirAll(filepath.Dir(skillRef), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillRef, []byte("# Contract\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(secretFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWorkspacePath(worktree, "~/.hoxi/skills/work-app-builder/references/contract.md", false)
	if err != nil {
		t.Fatalf("resolveWorkspacePath tilde failed: %v", err)
	}
	if got != skillRef {
		t.Fatalf("resolveWorkspacePath tilde = %q, want %q", got, skillRef)
	}

	// mustStayInside=true rejects tilde paths outside the worktree.
	if _, err := resolveWorkspacePath(worktree, "~/.hoxi/skills/work-app-builder/references/contract.md", true); err == nil {
		t.Fatalf("expected mustStayInside=true to reject external tilde path")
	}

	// ~otheruser and ~\ forms must fail closed so resolveWorkspacePath stays 1:1 with runner resolveHookToolPath.
	for _, bad := range []string{"~otheruser/file.txt", `~\Documents\file.txt`} {
		if _, err := resolveWorkspacePath(worktree, bad, false); err == nil {
			t.Fatalf("expected %q path to be rejected", bad)
		}
	}

	// Unvetted readTool still rejects external tilde paths and sensitive paths.
	rt := &readTool{}
	rawInput, _ := json.Marshal(map[string]any{"filePath": "~/.hoxi/skills/work-app-builder/references/contract.md"})
	if out, err := rt.Execute(context.Background(), ToolContext{WorktreeDir: worktree, PermissionVetted: false}, rawInput); err == nil || !out.IsError {
		t.Fatalf("expected unvetted readTool to block external tilde path, got out=%+v err=%v", out, err)
	}
	secretInput, _ := json.Marshal(map[string]any{"filePath": "~/.ssh/id_rsa"})
	if out, err := rt.Execute(context.Background(), ToolContext{WorktreeDir: worktree, PermissionVetted: false}, secretInput); err == nil || !out.IsError {
		t.Fatalf("expected unvetted readTool to block ~/.ssh/id_rsa, got out=%+v err=%v", out, err)
	}

	// PermissionVetted readTool (after runner approval gate) reads expanded ~/ cleanly.
	out, err := rt.Execute(context.Background(), ToolContext{WorktreeDir: worktree, PermissionVetted: true}, rawInput)
	if err != nil || out.IsError {
		t.Fatalf("expected vetted readTool to read ~/ reference file, got out=%+v err=%v", out, err)
	}
	if !strings.Contains(out.Output, "# Contract") {
		t.Fatalf("expected contract contents, got:\n%s", out.Output)
	}
}
