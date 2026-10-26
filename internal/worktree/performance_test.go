package worktree

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWorkspaceRoutes(t *testing.T) {
	t.Parallel()
	a, b := initTempGitRepo(t), initTempGitRepo(t)
	m := NewManager(a)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	var wg sync.WaitGroup
	for _, dir := range []string{a, b} {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/worktree/status?sessionID=__root__&directory="+url.QueryEscape(dir), nil))
				var st worktreeStatus
				if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Directory != dir {
					t.Errorf("status for %s: %s (%v)", dir, w.Body, err)
				}
			}
		}(dir)
	}
	wg.Wait()
	if m.RootDir() != a {
		t.Fatal("request changed default workspace")
	}
	meta, err := m.ForDirectory(b).Create("ses_b")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.scoped("", "ses_b"); got.RootDir() != b {
		t.Fatal("session-only request lost owning workspace")
	}
	if got := m.scoped(meta.Directory, "ses_b").resolveSessionMeta("ses_b"); got.BaseCommit != meta.BaseCommit || !got.Isolated {
		t.Fatal("sandbox-directory request lost session metadata")
	}
	if err := m.scoped("", "ses_b").Remove("ses_b", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(meta.Directory); !os.IsNotExist(err) {
		t.Fatal("session-only removal did not remove owning worktree")
	}
}

func TestMetadataReadsDuringGitMutation(t *testing.T) {
	t.Parallel()
	dir := initTempGitRepo(t)
	m := NewManager(dir)
	// A blocked hook models a slow Git operation without blocking metadata readers.
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	marker := filepath.Join(t.TempDir(), "started")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.Create("slow"); done <- err }()
	defer func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hook never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	read := make(chan struct{})
	go func() { m.resolveSessionMeta("other"); close(read) }()
	select {
	case <-read:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("metadata read blocked by Git mutation")
	}
}

func TestDiffBatchedPatches(t *testing.T) {
	t.Parallel()
	dir := initTempGitRepo(t)
	for _, name := range []string{"space name.txt", "binary.dat", "deleted.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runGit(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "commit", "-m", "fixtures"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "space name.txt"), []byte("new\nextra\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "binary.dat"), []byte{0, 1, 2}, 0o644)
	_ = os.Remove(filepath.Join(dir, "deleted.txt"))
	_ = os.WriteFile(filepath.Join(dir, "added.txt"), []byte("added\n"), 0o644)
	_, _ = runGit(dir, "add", "added.txt")
	_ = os.WriteFile(filepath.Join(dir, "untracked space.txt"), []byte("untracked\n"), 0o644)
	diffs, err := NewManager(dir).Diff("__root__")
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]worktreeFileDiff)
	for _, d := range diffs {
		byName[d.File] = d
	}
	if len(byName) != 5 {
		t.Fatalf("unexpected diffs: %+v", diffs)
	}
	if d := byName["space name.txt"]; d.Additions != 2 || d.Deletions != 1 || !strings.Contains(d.Patch, "+extra") {
		t.Fatalf("bad spaced filename diff: %+v", d)
	}
	if byName["deleted.txt"].Status != "deleted" || byName["added.txt"].Status != "added" || byName["untracked space.txt"].Status != "added" {
		t.Fatalf("bad statuses: %+v", diffs)
	}
	if !strings.Contains(byName["binary.dat"].Patch, "Binary files") {
		t.Fatal("lost binary patch")
	}
}

func TestDiffBoundsMassiveUntrackedDirsAndResolvesExplicitEditedFiles(t *testing.T) {
	t.Parallel()
	dir := initTempGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z_direct_untracked.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	smallDir := filepath.Join(dir, "z_small_pkg")
	if err := os.MkdirAll(smallDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smallDir, "helper.go"), []byte("package small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An alphabetically earlier untracked directory with > maxUntrackedFilesPerDir files
	// must not crowd out direct untracked files or small untracked directories.
	hugeDir := filepath.Join(dir, "a_huge_dump")
	if err := os.MkdirAll(hugeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxUntrackedFilesPerDir+10; i++ {
		name := filepath.Join(hugeDir, fmt.Sprintf("page_%03d.html", i))
		if err := os.WriteFile(name, []byte("<html>dump</html>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewManager(dir)
	diffs, err := mgr.Diff("__root__")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	byFile := make(map[string]worktreeFileDiff, len(diffs))
	for _, d := range diffs {
		if strings.HasPrefix(d.File, "a_huge_dump/") {
			t.Fatalf("expected massive untracked dir a_huge_dump/ to be skipped in bulk diff, got %s", d.File)
		}
		byFile[d.File] = d
	}
	if _, ok := byFile["README.md"]; !ok {
		t.Fatalf("expected modified tracked README.md in diffs, got %+v", diffs)
	}
	if d, ok := byFile["z_direct_untracked.go"]; !ok || !strings.Contains(d.Patch, "+package main") {
		t.Fatalf("expected direct untracked file with inline patch, got %+v", d)
	}
	if d, ok := byFile["z_small_pkg/helper.go"]; !ok || !strings.Contains(d.Patch, "+package small") {
		t.Fatalf("expected small untracked dir file with inline patch, got %+v", d)
	}

	// Explicitly editing a file inside the skipped huge untracked dir must still resolve via DiffForFiles.
	targeted, err := mgr.DiffForFiles("__root__", map[string]struct{}{
		"a_huge_dump/page_001.html": {},
	})
	if err != nil {
		t.Fatalf("DiffForFiles failed: %v", err)
	}
	foundTargeted := false
	for _, d := range targeted {
		if d.File == "a_huge_dump/page_001.html" && strings.Contains(d.Patch, "+<html>dump</html>") {
			foundTargeted = true
			break
		}
	}
	if !foundTargeted {
		t.Fatalf("expected DiffForFiles to resolve explicit file in huge untracked dir, got %+v", targeted)
	}
}

func TestFingerprintRemainsNonEmptyAboveMaxFingerprintFiles(t *testing.T) {
	t.Parallel()
	dir := initTempGitRepo(t)
	for i := 0; i < maxFingerprintFiles+15; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("untracked_%03d.txt", i)), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fp1 := computeRepoStateFingerprint(dir, "HEAD")
	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint when porcelain entries exceed maxFingerprintFiles")
	}
	fp2 := computeRepoStateFingerprint(dir, "HEAD")
	if fp1 != fp2 {
		t.Fatalf("expected deterministic fingerprint, got %q vs %q", fp1, fp2)
	}

	mgr := NewManager(dir)
	diffs, err := mgr.Diff("__root__")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	inlineCount := 0
	deferredLargeCount := 0
	for _, d := range diffs {
		if d.Patch != "" {
			inlineCount++
		} else if d.Large {
			deferredLargeCount++
		}
	}
	if inlineCount > maxDiffUntrackedInlineFiles {
		t.Fatalf("expected inline untracked patches capped at %d, got %d", maxDiffUntrackedInlineFiles, inlineCount)
	}
	if deferredLargeCount == 0 {
		t.Fatal("expected untracked files beyond maxDiffUntrackedInlineFiles to be marked Large with deferred patch")
	}
}

func TestSnapshotSkipsMassiveUntrackedDirsWithoutDeletingOnRestore(t *testing.T) {
	t.Parallel()
	dir := initTempGitRepo(t)
	hugeDir := filepath.Join(dir, "huge_static")
	if err := os.MkdirAll(hugeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxUntrackedFilesPerDir+5; i++ {
		if err := os.WriteFile(filepath.Join(hugeDir, fmt.Sprintf("f_%03d.html", i)), []byte("static\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := CreateSnapshot(dir)
	if err != nil || snap == "" {
		t.Fatalf("CreateSnapshot failed: snap=%q err=%v", snap, err)
	}
	treeOut, err := runGit(dir, "ls-tree", "-r", "--name-only", snap)
	if err != nil {
		t.Fatalf("ls-tree snap: %v", err)
	}
	if strings.Contains(treeOut, "huge_static/") {
		t.Fatalf("expected huge_static/ excluded from snapshot tree, got:\n%s", treeOut)
	}

	agentFile := filepath.Join(dir, "agent_created.txt")
	if err := os.WriteFile(agentFile, []byte("temporary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnapshot(dir, snap); err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}
	if _, err := os.Stat(agentFile); !os.IsNotExist(err) {
		t.Fatalf("expected agent_created.txt removed on RestoreSnapshot, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(hugeDir, "f_000.html")); err != nil {
		t.Fatalf("expected skipped huge_static/ files preserved on RestoreSnapshot, err=%v", err)
	}
}
