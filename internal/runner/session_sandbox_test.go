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
	"strings"
	"testing"
	"time"
)

func createSandboxSession(t *testing.T, svc *Service, runtime, directory, mode string) SessionInfo {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"runtime": runtime, "directory": directory,
		"sandboxMode": mode, "modelID": "test-model"})
	w := httptest.NewRecorder()
	svc.handleCreateSession(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var info SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestRunnerSandboxSelectionAndProcessDirectory(t *testing.T) {
	for _, runtime := range []string{string(RuntimeClaudeCode), string(RuntimeAntigravity)} {
		for _, mode := range []string{"local", "worktree"} {
			t.Run(runtime+"/"+mode, func(t *testing.T) {
				root := initTestGitRepo(t)
				storePath := filepath.Join(t.TempDir(), "sessions.json")
				svc := NewService(root, nil, nil, storePath)
				defer svc.CloseAll()
				info := createSandboxSession(t, svc, runtime, root, mode)
				if info.SandboxMode != mode {
					t.Fatalf("mode=%q", info.SandboxMode)
				}
				if mode == "local" {
					if normalizeComparePath(info.Directory) != normalizeComparePath(root) {
						t.Fatal("local session moved outside project")
					}
					if _, err := os.Stat(filepath.Join(root, ".hoxi", "worktrees")); !os.IsNotExist(err) {
						t.Fatal("local mode created worktrees")
					}
				} else if !strings.Contains(filepath.ToSlash(info.Directory), "/.hoxi/worktrees/"+info.ID) || info.Branch == "" {
					t.Fatalf("missing isolated directory/branch: %+v", info)
				}
				reloaded, ok := NewStore(storePath).Get(info.ID)
				if !ok || reloaded.ToSessionInfo().SandboxMode != mode {
					t.Fatal("sandbox choice was not persisted")
				}

				binRoot := t.TempDir()
				binDir := filepath.Join(binRoot, "bin")
				if err := os.MkdirAll(binDir, 0700); err != nil {
					t.Fatal(err)
				}
				cwdLog := filepath.Join(t.TempDir(), "cwd")
				t.Setenv("GEMINI_HOME", binRoot)
				t.Setenv("PATH", binDir)
				t.Setenv("TEST_RUNNER_CWD", cwdLog)
				t.Setenv("TEST_RUNNER_RUNTIME", runtime)
				script := `#!/bin/sh
pwd > "$TEST_RUNNER_CWD"
while IFS= read -r line; do :; done
if [ "$TEST_RUNNER_RUNTIME" = "claude-code" ]; then
  printf '%s\n' '{"type":"result","result":"OK","is_error":false}'
else
  printf '%s\n' '{"event":"result","result":{"response":"OK","status":"DONE"}}'
fi
`
				for _, name := range []string{"claude", "agy"} {
					if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
				}
				ch, unsub := svc.Subscribe(info.ID)
				defer unsub()
				w := httptest.NewRecorder()
				svc.handleSessionPrompt(w, httptest.NewRequest(http.MethodPost, "/prompt", strings.NewReader(`{"text":"test","agent":"plan"}`)), info.ID)
				if w.Code != http.StatusOK {
					t.Fatalf("prompt: %d %s", w.Code, w.Body.String())
				}
				deadline := time.After(5 * time.Second)
				for {
					select {
					case evt := <-ch:
						if evt.Type == "session.idle" {
							goto finished
						}
					case <-deadline:
						t.Fatal("process did not complete")
					}
				}
			finished:
				cwd, err := os.ReadFile(cwdLog)
				if err != nil {
					t.Fatal(err)
				}
				expected, err := existingDirectory(info.Directory)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := existingDirectory(strings.TrimSpace(string(cwd)))
				if err != nil {
					t.Fatal(err)
				}
				if actual != expected {
					t.Fatalf("CLI ran in %q instead of %q", cwd, info.Directory)
				}
			})
		}
	}
}

func TestRunnerLocalModeWorksWithoutGitAndWorktreeModeFailsClosed(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	info := createSandboxSession(t, svc, string(RuntimeClaudeCode), root, "")
	if info.SandboxMode != "local" {
		t.Fatal("omitted mode should default to local")
	}
	for _, mode := range []string{"worktree", "invalid"} {
		body, _ := json.Marshal(map[string]string{"runtime": "antigravity", "directory": root, "sandboxMode": mode, "modelID": "test"})
		w := httptest.NewRecorder()
		svc.handleCreateSession(w, httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s should reject: %d %s", mode, w.Code, w.Body.String())
		}
	}
	if len(svc.Store().List(root)) != 1 {
		t.Fatal("failed isolation request left a session behind")
	}
}

func TestRunnerModeSwitchPreservesChangesAndUpdatesExecutionDirectory(t *testing.T) {
	root := initTestGitRepo(t)
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	info := createSandboxSession(t, svc, "claude-code", root, "local")
	isolated, err := svc.changeSessionSandbox(info.ID, "worktree")
	if err != nil {
		t.Fatal(err)
	}
	if isolated.SandboxMode != "worktree" || isolated.Directory == info.Directory {
		t.Fatal("switch did not provision worktree")
	}
	file := filepath.Join(isolated.Directory, "pending.txt")
	if err := os.WriteFile(file, []byte("keep this edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.changeSessionSandbox(info.ID, "local"); err == nil {
		t.Fatal("dirty worktree was discarded")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("dirty file was lost")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	local, err := svc.changeSessionSandbox(info.ID, "local")
	if err != nil {
		t.Fatal(err)
	}
	if local.SandboxMode != "local" || local.Directory != info.Directory {
		t.Fatal("local directory was not restored")
	}
	if _, err := os.Stat(isolated.Directory); !os.IsNotExist(err) {
		t.Fatal("old worktree still shadows local status")
	}
	// Re-entering the same preserved branch remains supported.
	if _, err := svc.changeSessionSandbox(info.ID, "worktree"); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.procs[info.ID] = &activeProcess{}
	svc.mu.Unlock()
	if _, err := svc.changeSessionSandbox(info.ID, "local"); err == nil {
		t.Fatal("running session switched directory")
	}
	svc.mu.Lock()
	delete(svc.procs, info.ID)
	svc.mu.Unlock()
}

func TestRunnerDirectoryValidationAndLegacyMode(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".hoxi", "worktrees", "legacy")
	if err := os.MkdirAll(wt, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := SessionRecord{ProjectDir: root, WorktreePath: wt}
	if legacy.ToSessionInfo().SandboxMode != "worktree" {
		t.Fatal("legacy worktree changed to local")
	}
	if err := validateSessionDirectory(legacy); err != nil {
		t.Fatal(err)
	}
	local := SessionRecord{ProjectDir: root, WorktreePath: t.TempDir(), SandboxMode: "local"}
	if err := validateSessionDirectory(local); err == nil {
		t.Fatal("local session escaped its selected project")
	}
}

func TestLocalRunnerGitOperationsKeepTheirProjectAfterReload(t *testing.T) {
	root, project := t.TempDir(), initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	svc := NewService(root, nil, nil, storePath)
	info := createSandboxSession(t, svc, "claude-code", project, "local")
	for _, service := range []*Service{svc, NewService(root, nil, nil, storePath)} {
		mux := http.NewServeMux()
		service.worktrees.RegisterRoutes(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/worktree/status?sessionID="+info.ID, nil))
		var status struct {
			Directory string `json:"directory"`
			Isolated  bool   `json:"isolated"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || status.Directory != project || status.Isolated {
			t.Fatalf("local session Git operations switched projects: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestExplicitRunnerDiscardHonorsBranchChoice(t *testing.T) {
	for _, keep := range []bool{true, false} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			root := initTestGitRepo(t)
			svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
			info := createSandboxSession(t, svc, "antigravity", root, "worktree")
			if err := os.WriteFile(filepath.Join(info.Directory, "dirty.txt"), []byte("discard this"), 0600); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]interface{}{"sandboxMode": "local", "force": true, "keepBranch": keep})
			w := httptest.NewRecorder()
			svc.handleSingleSession(w, httptest.NewRequest(http.MethodPatch, "/session", bytes.NewReader(body)), info.ID)
			if w.Code != http.StatusOK {
				t.Fatalf("discard: %d %s", w.Code, w.Body.String())
			}
			if _, err := os.Stat(info.Directory); !os.IsNotExist(err) {
				t.Fatal("discard left the worktree")
			}
			out, err := exec.Command("git", "-C", root, "branch", "--list", info.Branch).CombinedOutput()
			if err != nil || (strings.TrimSpace(string(out)) != "") != keep {
				t.Fatalf("keepBranch=%v: %s %v", keep, out, err)
			}
			rec, _ := svc.Store().Get(info.ID)
			if rec.sandboxMode() != "local" || rec.WorktreePath != root {
				t.Fatal("discard did not update runner execution directory")
			}
		})
	}
}

func TestPromoteLocalSessionToWorktreeTransfersWorkingStateAndContext(t *testing.T) {
	root := initTestGitRepo(t)
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	info := createSandboxSession(t, svc, "claude-code", root, "local")

	_, _, _ = svc.store.Update(info.ID, func(r *SessionRecord) {
		r.Turns = []MessageTurn{{
			Info:  MessageInfo{ID: "msg_1", SessionID: info.ID, Role: "user"},
			Parts: []MessagePart{{Type: "text", Text: "implement draft"}},
		}}
	})

	if err := os.WriteFile(filepath.Join(root, "draft.txt"), []byte("untracked draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	isolated, err := svc.changeSessionSandbox(info.ID, "worktree")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(isolated.Directory, "draft.txt"))
	if err != nil || string(got) != "untracked draft\n" {
		t.Fatalf("expected untracked draft transferred to worktree, got %q (err=%v)", string(got), err)
	}

	rec, _ := svc.Store().Get(info.ID)
	ctx := runtimeContext(rec)
	if !strings.Contains(ctx, "<system-reminder>") || !strings.Contains(ctx, isolated.Directory) || !strings.Contains(ctx, isolated.Branch) {
		t.Fatalf("expected system-reminder with worktree directory and branch in runtimeContext, got %q", ctx)
	}
}

