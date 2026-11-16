package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/orchestration"
	"github.com/hoxiai/hoxi/internal/runner"
)

func scopingComparePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

// TestConcurrentSessionCreationKeepsWorkspacesSeparate covers the "new thread"
// path: many sessions are provisioned at the same time for two unrelated git
// projects plus a non-git folder. Every session must get its own directory
// binding and none may borrow another project's sandbox.
func TestConcurrentSessionCreationKeepsWorkspacesSeparate(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	plain := t.TempDir()
	for _, repo := range []string{repoA, repoB} {
		convoyTestGit(t, repo, "init", "-q", "-b", "main")
		if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		convoyTestGit(t, repo, "add", ".")
		convoyTestGit(t, repo, "commit", "-q", "-m", "base")
	}

	var engineIDs atomic.Int32
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/session" && r.Method == http.MethodPost {
			fmt.Fprintf(w, `{"id":"ses_engine_%d"}`, engineIDs.Add(1))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer engine.Close()

	svc := runner.NewService(repoA, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(engine.URL, "secret")
	backend := convoySessions{runner: svc}

	type target struct {
		directory string
		isolated  bool
	}
	type created struct {
		id       string
		dir      string
		isolated bool
	}
	const perProject = 6
	targets := []target{
		{repoA, true}, {repoB, true}, {plain, false},
	}
	sandboxPath := func(tg target, id string) string {
		if tg.isolated {
			return filepath.Join(tg.directory, ".hoxi", "worktrees", id)
		}
		return tg.directory
	}

	var mu sync.Mutex
	var got []created
	var wg sync.WaitGroup
	failures := make(chan string, 64)
	ctx := context.Background()
	for _, tg := range targets {
		for i := 0; i < perProject; i++ {
			wg.Add(1)
			go func(tg target, i int) {
				defer wg.Done()
				id, err := backend.Create(ctx, orchestration.SessionRequest{
					Directory: tg.directory,
					Title:     fmt.Sprintf("Convoy · %s · %d", filepath.Base(tg.directory), i),
					Provider:  "provider",
					Model:     "model",
					Isolated:  tg.isolated,
				})
				if err != nil {
					failures <- fmt.Sprintf("create %s #%d: %v", tg.directory, i, err)
					return
				}
				mu.Lock()
				got = append(got, created{id: id, dir: sandboxPath(tg, id), isolated: tg.isolated})
				mu.Unlock()
			}(tg, i)
		}
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
	if t.Failed() {
		t.Fatal("concurrent creation failed")
	}

	seen := make(map[string]string, len(got))
	for _, c := range got {
		_, effectiveDir, projectDir, ok, err := svc.ResolveSessionContext(ctx, c.id, "")
		if err != nil || !ok {
			t.Fatalf("session %s has no context: %v %v", c.id, ok, err)
		}
		if got, want := scopingComparePath(t, effectiveDir), scopingComparePath(t, c.dir); got != want {
			t.Fatalf("session %s bound sandbox %q, want %q", c.id, got, want)
		}
		if projectDir == "" {
			t.Fatalf("session %s has no project directory", c.id)
		}
		if c.isolated {
			if prev, dup := seen[scopingComparePath(t, effectiveDir)]; dup {
				t.Fatalf("isolated sessions %s and %s share sandbox %q", prev, c.id, effectiveDir)
			}
			seen[scopingComparePath(t, effectiveDir)] = c.id
		}
	}

	for _, directory := range []string{repoA, repoB, plain} {
		infos := svc.Store().List(directory)
		if len(infos) != perProject {
			t.Fatalf("directory %s lists %d sessions, want %d", directory, len(infos), perProject)
		}
		for _, info := range infos {
			_, _, projectDir, _, _ := svc.ResolveSessionContext(ctx, info.ID, "")
			if got, want := scopingComparePath(t, projectDir), scopingComparePath(t, directory); got != want {
				t.Fatalf("directory %s lists foreign session %s from %s", directory, info.ID, got)
			}
			for _, foreign := range []string{repoA, repoB, plain} {
				if foreign == directory {
					continue
				}
				// Scoping a session request to another workspace must fail.
				if _, _, _, _, err := svc.ResolveSessionContext(ctx, info.ID, foreign); err == nil {
					t.Fatalf("session %s accepted foreign directory selector %s", info.ID, foreign)
				}
			}
		}
	}
}
