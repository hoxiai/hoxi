package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
)

func TestIdleMemoryReleaserRunsOncePerQuietPeriod(t *testing.T) {
	idle, releases := true, 0
	r := &idleMemoryReleaser{idle: func() bool { return idle }, release: func() { releases++ }}
	start := time.Now()
	r.tick(start)
	r.tick(start.Add(time.Minute))
	if releases != 0 {
		t.Fatal("must wait until the quiet period lasts long enough")
	}
	r.tick(start.Add(idleMemoryReleaseAfter))
	r.tick(start.Add(idleMemoryReleaseAfter + time.Minute))
	if releases != 1 {
		t.Fatalf("releases = %d, want exactly one per quiet period", releases)
	}
	idle = false
	r.tick(start.Add(10 * time.Minute))
	idle = true
	r.tick(start.Add(11 * time.Minute))
	r.tick(start.Add(11*time.Minute + idleMemoryReleaseAfter))
	if releases != 2 {
		t.Fatalf("releases = %d, want a new release after activity resumed and stopped", releases)
	}
}

func TestDeletingConversationRemovesItsBrowserProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	t.Setenv("OPENCODE_DB", filepath.Join(home, "hoxi.db"))
	t.Setenv("HOXI_DB_PATH", filepath.Join(home, "hoxi.db"))
	repo := t.TempDir()
	gw := New(0, "pass", repo, nil, nil)
	root := filepath.Join(home, "browser-profile")
	gw.browser.SetProfileRoot(root)
	own := filepath.Join(root+"-sessions", "ses_cld_gone", "Default", "Cookies")
	other := filepath.Join(root+"-sessions", "ses_cld_stay", "Default", "Cookies")
	for _, p := range []string{own, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"ses_cld_gone", "ses_cld_stay"} {
		if err := gw.runner.Store().Put(runner.SessionRecord{ID: id, Runtime: string(runner.RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo}); err != nil {
			t.Fatal(err)
		}
	}
	if err := gw.runner.DeleteConversation(context.Background(), "ses_cld_gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(own))); !os.IsNotExist(err) {
		t.Fatalf("deleted conversation's browser profile should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another conversation's profile must stay: %v", err)
	}
}

func TestSystemInfoReportsGoMemory(t *testing.T) {
	if s := readGoMemoryStats(); s.SysBytes == 0 || s.HeapInuseBytes == 0 {
		t.Fatalf("unexpected Go memory stats: %+v", s)
	}
}
