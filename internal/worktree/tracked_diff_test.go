package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTrackedPatchesDeferBeforeMaterializingAndPreserveDetails(t *testing.T) {
	t.Parallel()
	dir := initTempGitRepo(t)
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ordinaryFiles := []string{"space name.txt", "中文.txt"}
	if runtime.GOOS != "windows" {
		ordinaryFiles = append(ordinaryFiles, ":(glob)*.txt")
	}
	write("package-lock.json", strings.Repeat("old lock entry\n", 1000))
	write("deleted.generated.ts", strings.Repeat("old generated entry\n", 1000))
	for _, name := range ordinaryFiles {
		write(name, "old\n")
	}
	if _, err := runGit(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "commit", "-m", "diff fixtures"); err != nil {
		t.Fatal(err)
	}
	write("package-lock.json", strings.Repeat("new lock entry\n", 1000))
	write("added.generated.ts", strings.Repeat("new generated entry\n", 1000))
	write("large.txt", strings.Repeat("large content\n", 30000))
	if err := os.Remove(filepath.Join(dir, "deleted.generated.ts")); err != nil {
		t.Fatal(err)
	}
	for _, name := range ordinaryFiles {
		write(name, "new\nextra\n")
	}
	if _, err := runGit(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	numstat, err := runGitRaw(dir, "diff", "--no-renames", "--numstat", "-z", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	inline, deferred := planTrackedPatches(dir, numstat)
	if len(inline) != len(ordinaryFiles) || len(deferred) != 4 {
		t.Fatalf("unexpected patch plan: inline=%q deferred=%q", inline, deferred)
	}
	raw, err := runGitRaw(dir, trackedPatchArgs("HEAD", false, inline, false)...)
	if err != nil || strings.Contains(raw, "new lock entry") || strings.Contains(raw, "large content") {
		t.Fatalf("deferred content reached patch generation: len=%d err=%v", len(raw), err)
	}
	mgr := NewManager(dir)
	diffs, err := mgr.Diff("__root__")
	if err != nil {
		t.Fatal(err)
	}
	byFile := make(map[string]worktreeFileDiff)
	for _, diff := range diffs {
		byFile[diff.File] = diff
	}
	for _, name := range ordinaryFiles {
		if d := byFile[name]; d.Additions != 2 || !strings.Contains(d.Patch, "+extra") {
			t.Errorf("lost literal path patch %q: %+v", name, d)
		}
	}
	for name, status := range map[string]string{"package-lock.json": "modified", "deleted.generated.ts": "deleted", "added.generated.ts": "added", "large.txt": "added"} {
		d := byFile[name]
		if !d.Large || d.Patch != "" || d.Status != status {
			t.Errorf("bad deferred summary %q: %+v", name, d)
		}
	}
	selected, err := mgr.DiffForFiles("__root__", map[string]struct{}{"large.txt": {}})
	if err != nil {
		t.Fatal(err)
	}
	for _, diff := range selected {
		if diff.File == "large.txt" && diff.Patch != "" {
			t.Fatal("session refresh eagerly hydrated a deferred patch")
		}
	}
	detail, err := mgr.DiffFile("__root__", "package-lock.json")
	if err != nil || !strings.Contains(detail.Patch, "+new lock entry") {
		t.Fatalf("explicit detail lost full patch: len=%d err=%v", len(detail.Patch), err)
	}
}

func TestTrackedPatchPlanBoundsCountAndHandlesQuotedHeaders(t *testing.T) {
	t.Parallel()
	var numstat strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&numstat, "1\t0\tfile_%04d.txt\x00", i)
	}
	inline, deferred := planTrackedPatches(t.TempDir(), numstat.String())
	if len(inline) != 64 || len(deferred) != 936 {
		t.Fatalf("unbounded inline plan: %d inline, %d deferred", len(inline), len(deferred))
	}
	if file := patchHeaderFile("ignored header\n--- \"a/tab\\tname.txt\"\n+++ \"b/tab\\tname.txt\"\n"); file != "tab\tname.txt" {
		t.Fatalf("quoted path lost: %q", file)
	}
	if file := patchHeaderFile("\"a/tab\\tname.bin\" \"b/tab\\tname.bin\"\nBinary files differ\n"); file != "tab\tname.bin" {
		t.Fatalf("quoted binary path lost: %q", file)
	}
}
