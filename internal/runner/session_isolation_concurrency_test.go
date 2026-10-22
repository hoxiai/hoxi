package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func initTestGitRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "test@hoxi.ai")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial\n\nCo-authored-by: Hoxi <code@hoxi.ai>")
	return dir
}

// TestConcurrentNestedProjectSessionsStayIsolated reproduces "new thread" in two
// workspaces where one folder is nested inside the other. Each session must be
// provisioned under its own project, never sharing the parent's sandbox, and
// listing either workspace must not return the other's sessions.
func TestConcurrentNestedProjectSessionsStayIsolated(t *testing.T) {
	t.Parallel()
	parent := initTestGitRepo(t)
	child := initTestGitRepoAt(t, filepath.Join(parent, "client"))

	svc := NewService(parent, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))

	type created struct {
		id  string
		dir string
	}
	var mu sync.Mutex
	var sessions []created
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for _, project := range []string{parent, child} {
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(project string) {
				defer wg.Done()
				body, _ := json.Marshal(map[string]string{
					"runtime": "claude-code", "directory": project,
					"sandboxMode": "worktree", "modelID": "test-model",
				})
				w := httptest.NewRecorder()
				svc.handleCreateSession(w, httptest.NewRequest(http.MethodPost,
					"/api/hoxi/runner/sessions", bytes.NewReader(body)))
				if w.Code != http.StatusOK {
					errs <- fmt.Errorf("create in %s: %d %s", project, w.Code, w.Body.String())
					return
				}
				var info SessionInfo
				if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
					errs <- err
					return
				}
				mu.Lock()
				sessions = append(sessions, created{id: info.ID, dir: info.Directory})
				mu.Unlock()
			}(project)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.Fatal("concurrent session creation failed")
	}

	seen := make(map[string]string, len(sessions))
	for _, s := range sessions {
		rec, ok := svc.Store().Get(s.id)
		if !ok {
			t.Fatalf("session %s missing from store", s.id)
		}
		var wantPath string
		switch normalizeComparePath(rec.ProjectDir) {
		case normalizeComparePath(parent):
			wantPath = filepath.Join(parent, ".hoxi", "worktrees", s.id)
		case normalizeComparePath(child):
			wantPath = filepath.Join(child, ".hoxi", "worktrees", s.id)
		default:
			t.Fatalf("session %s bound to unexpected project %q", s.id, rec.ProjectDir)
		}
		if normalizeComparePath(rec.WorktreePath) != normalizeComparePath(wantPath) ||
			normalizeComparePath(s.dir) != normalizeComparePath(wantPath) {
			t.Fatalf("session %s ran in %q / %q, want %q", s.id, rec.WorktreePath, s.dir, wantPath)
		}
		binding, known, err := svc.worktrees.BindingForSession(s.id)
		if err != nil || !known || !binding.Isolated ||
			normalizeComparePath(binding.Directory) != normalizeComparePath(wantPath) {
			t.Fatalf("session %s binding = %+v, %v, %v", s.id, binding, known, err)
		}
		if prev, dup := seen[normalizeComparePath(rec.WorktreePath)]; dup {
			t.Fatalf("sessions %s and %s share sandbox %q", prev, s.id, rec.WorktreePath)
		}
		seen[normalizeComparePath(rec.WorktreePath)] = s.id
	}

	for _, project := range []string{parent, child} {
		infos := svc.Store().List(project)
		if len(infos) != 5 {
			t.Fatalf("List(%s) = %d sessions, want 5", project, len(infos))
		}
		for _, info := range infos {
			rec, _ := svc.Store().Get(info.ID)
			if normalizeComparePath(rec.ProjectDir) != normalizeComparePath(project) {
				t.Fatalf("List(%s) leaked session %s from %s", project, info.ID, rec.ProjectDir)
			}
		}
	}
}
