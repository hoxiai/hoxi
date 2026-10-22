package worktree

import (
	"encoding/json"
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
