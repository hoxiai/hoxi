package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestIsolatedRunnerRestoresSandboxSelector(t *testing.T) {
	project := initTestGitRepo(t)
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "sessions.json")
	svc := NewService(root, nil, nil, path)
	info := createSandboxSession(t, svc, "claude-code", project, "worktree")
	// Persisted branch metadata can lag a rename performed via the worktree API.
	if _, err := svc.worktrees.ForDirectory(project).RenameBranch(info.ID, "renamed/restored"); err != nil {
		t.Fatal(err)
	}
	svc = NewService(root, nil, nil, path)
	mux := http.NewServeMux()
	svc.worktrees.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/api/hoxi/worktree/status?sessionID="+info.ID+"&directory="+url.QueryEscape(info.Directory), nil))
	var status struct {
		Directory, Branch, BaseCommit string
		Isolated                      bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || !status.Isolated || status.Directory != info.Directory || status.Branch != "renamed/restored" || status.BaseCommit == "" {
		t.Fatalf("restored selector lost isolation: %d %s", w.Code, w.Body.String())
	}
}

func TestRenamedRunnerBranchSurvivesLocalReentryAndRestart(t *testing.T) {
	root := initTestGitRepo(t)
	path := filepath.Join(t.TempDir(), "sessions.json")
	svc := NewService(root, nil, nil, path)
	info := createSandboxSession(t, svc, "antigravity", root, "worktree")
	if _, err := svc.worktrees.RenameBranch(info.ID, "feature/retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Directory, "retained.txt"), []byte("preserve me\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.RunGit(info.Directory, "add", "retained.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.RunGit(info.Directory, "commit", "-m", "retained\n\nCo-authored-by: Hoxi <code@hoxi.ai>"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.changeSessionSandbox(info.ID, "local"); err != nil {
		t.Fatal(err)
	}
	svc = NewService(root, nil, nil, path)
	reentered, err := svc.changeSessionSandbox(info.ID, "worktree")
	if err != nil {
		t.Fatal(err)
	}
	if reentered.Branch != "feature/retained" {
		t.Fatalf("reattached wrong branch: %+v", reentered)
	}
	if data, err := os.ReadFile(filepath.Join(reentered.Directory, "retained.txt")); err != nil || string(data) != "preserve me\n" {
		t.Fatalf("lost retained commit: %q %v", data, err)
	}
}

func TestRunnerExitPersistenceFailurePreservesWorktree(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "discard"}[force], func(t *testing.T) {
			root := initTestGitRepo(t)
			path := filepath.Join(t.TempDir(), "sessions.json")
			svc := NewService(root, nil, nil, path)
			info := createSandboxSession(t, svc, "claude-code", root, "worktree")
			if force {
				if err := os.WriteFile(filepath.Join(info.Directory, "dirty.txt"), []byte("keep\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// A directory at the atomic-write path fails even when tests run as root.
			if err := os.Mkdir(filepath.Join(path, info.ID, sessionMetaFile+".tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.changeSessionSandboxWithOptions(info.ID, "local", force, false); err == nil {
				t.Fatal("exit accepted a failed store save")
			}
			for _, store := range []*Store{svc.store, NewStore(path)} {
				rec, _ := store.Get(info.ID)
				if rec.sandboxMode() != "worktree" || rec.WorktreePath != info.Directory {
					t.Fatalf("failed save changed session: %+v", rec)
				}
			}
			if _, err := os.Stat(info.Directory); err != nil {
				t.Fatal("failed save removed worktree", err)
			}
			if force {
				if _, err := os.Stat(filepath.Join(info.Directory, "dirty.txt")); err != nil {
					t.Fatal("failed save discarded edits", err)
				}
			}
			binding, known, err := svc.worktrees.BindingForSession(info.ID)
			if err != nil || !known || !binding.Isolated || binding.Directory != info.Directory {
				t.Fatalf("failed save changed binding: %+v %v", binding, err)
			}
		})
	}
}

func TestRunningRunnerRejectsGenericApplyBeforeSnapshot(t *testing.T) {
	root := initTestGitRepo(t)
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	info := createSandboxSession(t, svc, "claude-code", root, "worktree")
	if err := os.WriteFile(filepath.Join(info.Directory, "dirty.txt"), []byte("not ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	head, _ := worktree.RunGit(info.Directory, "rev-parse", "HEAD")
	svc.mu.Lock()
	svc.procs[info.ID] = &activeProcess{}
	svc.mu.Unlock()
	mux := http.NewServeMux()
	svc.worktrees.RegisterRoutes(mux)
	for _, selector := range []string{root, info.Directory} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/apply?directory="+url.QueryEscape(selector),
			strings.NewReader(`{"sessionID":"`+info.ID+`","mode":"patch"}`)))
		if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "running") {
			t.Fatalf("running apply accepted: %d %s", w.Code, w.Body.String())
		}
	}
	after, _ := worktree.RunGit(info.Directory, "rev-parse", "HEAD")
	if after != head {
		t.Fatal("rejected apply snapshotted running edits")
	}
	if _, err := os.Stat(filepath.Join(root, "dirty.txt")); !os.IsNotExist(err) {
		t.Fatal("rejected apply modified project")
	}
}

func TestRunnerExitRemovalFailureRollsBackPersistedMode(t *testing.T) {
	root := initTestGitRepo(t)
	path := filepath.Join(t.TempDir(), "sessions.json")
	svc := NewService(root, nil, nil, path)
	info := createSandboxSession(t, svc, "claude-code", root, "worktree")
	if err := os.WriteFile(filepath.Join(info.Directory, "dirty.txt"), []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.changeSessionSandbox(info.ID, "local"); err == nil {
		t.Fatal("dirty worktree exit succeeded")
	}
	for _, store := range []*Store{svc.store, NewStore(path)} {
		rec, _ := store.Get(info.ID)
		if rec.sandboxMode() != "worktree" || rec.WorktreePath != info.Directory {
			t.Fatalf("removal failure did not roll back mode: %+v", rec)
		}
	}
}

func TestRunnerApplyGuardSerializesWithTurnStart(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{ID: "test"}); err != nil {
		t.Fatal(err)
	}
	// Prompt startup holds turnMu until its process has been registered.
	svc.turnMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- svc.guardWorktreeApply("test", func() error { return nil })
	}()
	select {
	case <-done:
		t.Fatal("apply bypassed turn-start lock")
	case <-time.After(20 * time.Millisecond):
	}
	svc.mu.Lock()
	svc.procs["test"] = &activeProcess{}
	svc.mu.Unlock()
	svc.turnMu.Unlock()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "running") {
			t.Fatalf("apply missed process published during startup: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("apply deadlocked")
	}
}
