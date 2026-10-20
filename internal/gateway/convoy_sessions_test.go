package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/orchestration"
	"github.com/hoxiai/hoxi/internal/runner"
)

func convoyTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=hoxi-test", "GIT_AUTHOR_EMAIL=test@hoxi.local",
		"GIT_COMMITTER_NAME=hoxi-test", "GIT_COMMITTER_EMAIL=test@hoxi.local",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestConvoySessionsIsolateTasksAndMergePrerequisites drives the gateway's
// orchestration backend against a real runner and git repository.
func TestConvoySessionsIsolateTasksAndMergePrerequisites(t *testing.T) {
	repo := t.TempDir()
	convoyTestGit(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	convoyTestGit(t, repo, "add", ".")
	convoyTestGit(t, repo, "commit", "-q", "-m", "base")

	var engineIDs atomic.Int32
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"ses_engine_%d"}`, engineIDs.Add(1))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer engine.Close()

	svc := runner.NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(engine.URL, "secret")
	backend := convoySessions{runner: svc}

	if !backend.CanIsolate(repo) {
		t.Fatal("a committed git repository should support isolation")
	}
	if backend.CanIsolate(t.TempDir()) {
		t.Fatal("a plain directory must not claim isolation")
	}

	ctx := context.Background()
	req := orchestration.SessionRequest{Directory: repo, Title: "Convoy · a", Provider: "provider", Model: "model", Isolated: true}
	first, err := backend.Create(ctx, req)
	if err != nil {
		t.Fatalf("create first task session: %v", err)
	}
	req.Title = "Convoy · b"
	second, err := backend.Create(ctx, req)
	if err != nil {
		t.Fatalf("create second task session: %v", err)
	}
	firstRec, _ := svc.Store().Get(first)
	secondRec, _ := svc.Store().Get(second)
	if firstRec.WorktreePath == repo || secondRec.WorktreePath == repo || firstRec.WorktreePath == secondRec.WorktreePath {
		t.Fatalf("tasks must run in distinct worktrees: %q %q", firstRec.WorktreePath, secondRec.WorktreePath)
	}

	if err := os.WriteFile(filepath.Join(firstRec.WorktreePath, "app.txt"), []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := backend.Merge(second, []string{first}); err != nil {
		t.Fatalf("merge prerequisite: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(secondRec.WorktreePath, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\nTWO\nthree\n" {
		t.Fatalf("dependent worktree did not receive prerequisite changes: %q", got)
	}
	if main, _ := os.ReadFile(filepath.Join(repo, "app.txt")); string(main) != "one\ntwo\nthree\n" {
		t.Fatalf("convoy changes leaked into the main checkout: %q", main)
	}
}
