package worktree

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sessionTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitRaw(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sessionTestWrite(t *testing.T, dir, path, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func sessionTestDiff(path, before, after, status string) SessionDiff {
	return SessionDiff{File: path, Before: &before, After: &after, Status: status}
}

func sessionFixture(t *testing.T) (string, *Manager, string) {
	t.Helper()
	dir := initTempGitRepo(t)
	base := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n"
	sessionTestWrite(t, dir, "file.txt", base)
	sessionTestWrite(t, dir, "other.txt", "original\n")
	sessionTestGit(t, dir, "add", ".")
	sessionTestGit(t, dir, "commit", "-m", "fixture")
	return dir, NewManager(dir), base
}

func TestSessionCommitContentIsolationAndCumulative(t *testing.T) {
	t.Parallel()
	dir, manager, base := sessionFixture(t)
	after := strings.Replace(base, "one\n", "SESSION\n", 1)
	current := strings.Replace(after, "nine\n", "UNRELATED\n", 1)
	sessionTestWrite(t, dir, "file.txt", current)
	sessionTestWrite(t, dir, "other.txt", "staged elsewhere\n")
	sessionTestGit(t, dir, "add", "other.txt")
	staged := sessionTestGit(t, dir, "diff", "--cached", "--binary")
	diff := sessionTestDiff(filepath.Join(dir, "file.txt"), base, after, "modified")
	result, err := manager.CommitSessionChanges("session", "session only", false, []SessionDiff{diff})
	if err != nil || !result.OK {
		t.Fatalf("commit: %+v %v", result, err)
	}
	if got := sessionTestGit(t, dir, "show", "HEAD:file.txt"); got != after {
		t.Fatalf("wrong committed content: %q", got)
	}
	if got := sessionTestGit(t, dir, "diff", "--cached", "--binary"); got != staged {
		t.Fatalf("unrelated staging changed: %s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "file.txt")); string(got) != current {
		t.Fatal("workspace changed")
	}
	if msg := sessionTestGit(t, dir, "log", "-1", "--format=%B"); !strings.Contains(msg, HoxiCoAuthorTrailer) {
		t.Fatal("missing attribution")
	}
	head := sessionTestGit(t, dir, "rev-parse", "HEAD")
	if _, err := manager.CommitSessionChanges("session", "repeat", false, []SessionDiff{diff}); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Fatalf("repeat: %v", err)
	}
	if sessionTestGit(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("repeat created a commit")
	}
	next := strings.Replace(after, "five\n", "SECOND\n", 1)
	sessionTestWrite(t, dir, "file.txt", strings.Replace(next, "nine\n", "UNRELATED\n", 1))
	if _, err := manager.CommitSessionChanges("session", "cumulative", false, []SessionDiff{sessionTestDiff("file.txt", base, next, "modified")}); err != nil {
		t.Fatal(err)
	}
	if sessionTestGit(t, dir, "show", "HEAD:file.txt") != next {
		t.Fatal("cumulative commit incorrect")
	}
}

func TestSessionCommitRejectsUnsafeChanges(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing-before", "missing-after", "stale", "conflict", "staged", "intent-to-add", "traversal", "outside", "git", "symlink", "binary", "mode", "status", "duplicate", "index-lock", "missing-current", "missing-head"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, base := sessionFixture(t)
			after := strings.Replace(base, "one\n", "SESSION\n", 1)
			sessionTestWrite(t, dir, "file.txt", after)
			diff := sessionTestDiff("file.txt", base, after, "modified")
			switch kind {
			case "missing-before":
				diff.Before = nil
			case "missing-after":
				diff.After = nil
			case "stale":
				sessionTestWrite(t, dir, "file.txt", base)
			case "conflict":
				sessionTestWrite(t, dir, "file.txt", strings.Replace(base, "one\n", "OTHER\n", 1))
				sessionTestGit(t, dir, "add", "file.txt")
				sessionTestGit(t, dir, "commit", "-m", "external")
				sessionTestWrite(t, dir, "file.txt", after)
			case "staged":
				sessionTestGit(t, dir, "add", "file.txt")
			case "intent-to-add":
				sessionTestWrite(t, dir, "new.txt", after)
				sessionTestGit(t, dir, "add", "-N", "new.txt")
				diff = sessionTestDiff("new.txt", "", after, "added")
			case "traversal":
				diff.File = "sub/../file.txt"
			case "outside":
				diff.File = filepath.Join(t.TempDir(), "outside")
			case "git":
				diff.File = ".git/config"
			case "symlink":
				if err := os.Symlink(dir, filepath.Join(dir, "link")); err != nil {
					t.Fatal(err)
				}
				diff.File = "link/file.txt"
			case "binary":
				data := "binary\x00data"
				diff.After = &data
			case "mode":
				if err := os.Chmod(filepath.Join(dir, "file.txt"), 0755); err != nil {
					t.Fatal(err)
				}
			case "status":
				diff.Status = "renamed"
			case "index-lock":
				sessionTestWrite(t, dir, ".git/index.lock", "busy")
			case "missing-current":
				if err := os.Remove(filepath.Join(dir, "file.txt")); err != nil {
					t.Fatal(err)
				}
			case "missing-head":
				diff.File = "new.txt"
				sessionTestWrite(t, dir, "new.txt", after)
			}
			diffs := []SessionDiff{diff}
			if kind == "duplicate" {
				diffs = append(diffs, diff)
			}
			head := sessionTestGit(t, dir, "rev-parse", "HEAD")
			index, err := os.ReadFile(filepath.Join(dir, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.CommitSessionChanges("session", "unsafe", false, diffs); err == nil {
				t.Fatal("unsafe commit accepted")
			}
			if sessionTestGit(t, dir, "rev-parse", "HEAD") != head {
				t.Fatal("HEAD changed on failure")
			}
			got, _ := os.ReadFile(filepath.Join(dir, ".git/index"))
			if !bytes.Equal(index, got) {
				t.Fatal("index changed on failure")
			}
		})
	}
}

func TestSessionCommitAddDelete(t *testing.T) {
	t.Parallel()
	dir, manager, base := sessionFixture(t)
	sessionTestWrite(t, dir, "new.txt", "new\n")
	if err := os.Remove(filepath.Join(dir, "file.txt")); err != nil {
		t.Fatal(err)
	}
	diffs := []SessionDiff{sessionTestDiff("new.txt", "", "new\n", "added"), sessionTestDiff("file.txt", base, "", "deleted")}
	if _, err := manager.CommitSessionChanges("session", "add and delete", false, diffs); err != nil {
		t.Fatal(err)
	}
	if sessionTestGit(t, dir, "show", "HEAD:new.txt") != "new\n" || sessionTestGit(t, dir, "status", "--porcelain") != "" {
		t.Fatal("add/delete did not synchronize index")
	}
	if _, err := manager.CommitSessionChanges("session", "repeat", false, diffs); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Fatalf("repeat add/delete: %v", err)
	}
}

func TestSessionCommitHooksAndExternalHEAD(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"reject", "mutate-index", "move-head"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, base := sessionFixture(t)
			after := strings.Replace(base, "one\n", "SESSION\n", 1)
			sessionTestWrite(t, dir, "file.txt", after)
			head := strings.TrimSpace(sessionTestGit(t, dir, "rev-parse", "HEAD"))
			script := "#!/bin/sh\nexit 1\n"
			if kind == "mutate-index" {
				script = "#!/bin/sh\nprintf 'hook content\\n' > other.txt\ngit add other.txt\n"
			}
			if kind == "move-head" {
				script = "#!/bin/sh\ngit update-ref refs/heads/main " + head + "^\n"
			}
			if err := os.WriteFile(filepath.Join(dir, ".git/hooks/pre-commit"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.CommitSessionChanges("session", "hooked", false, []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")}); err == nil {
				t.Fatal("hook safety check did not reject")
			}
			want := head
			if kind == "move-head" {
				want = strings.TrimSpace(sessionTestGit(t, dir, "rev-parse", head+"^"))
			}
			if strings.TrimSpace(sessionTestGit(t, dir, "rev-parse", "HEAD")) != want {
				t.Fatal("unexpected branch publication")
			}
		})
	}
}

func TestSessionCommitRoute(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"scoped", "empty", "legacy", "missing-snapshot"} {
		t.Run(kind, func(t *testing.T) {
			dir, _, base := sessionFixture(t)
			manager := NewManager(t.TempDir())
			after := strings.Replace(base, "one\n", "SESSION\n", 1)
			sessionTestWrite(t, dir, "file.txt", after)
			sessionTestWrite(t, dir, "other.txt", "unrelated\n")
			body := map[string]any{"sessionID": "session", "message": "route", "directory": dir}
			switch kind {
			case "scoped":
				body["sessionDiffs"] = []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")}
			case "empty":
				body["sessionDiffs"] = []SessionDiff{}
			case "missing-snapshot":
				body["sessionDiffs"] = []SessionDiff{{File: "file.txt", Status: "modified"}}
			}
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			manager.RegisterRoutes(mux)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", bytes.NewReader(data)))
			want := http.StatusOK
			if kind == "empty" || kind == "missing-snapshot" {
				want = http.StatusBadRequest
			}
			if rec.Code != want {
				t.Fatalf("route: %d %s", rec.Code, rec.Body.String())
			}
			other := sessionTestGit(t, dir, "show", "HEAD:other.txt")
			if (kind == "legacy" && other != "unrelated\n") || (kind != "legacy" && other != "original\n") {
				t.Fatalf("wrong legacy/scoped behavior: %q", other)
			}
		})
	}
}

func TestSessionCommitEmptyFilesAndPush(t *testing.T) {
	t.Parallel()
	dir, manager, _ := sessionFixture(t)
	remote := t.TempDir()
	sessionTestGit(t, remote, "init", "--bare")
	sessionTestGit(t, dir, "remote", "add", "origin", remote)
	sessionTestWrite(t, dir, "empty.txt", "")
	added := sessionTestDiff("empty.txt", "", "", "added")
	if _, err := manager.CommitSessionChanges("session", "empty addition", true, []SessionDiff{added}); err != nil {
		t.Fatal(err)
	}
	head := sessionTestGit(t, dir, "rev-parse", "HEAD")
	if result, err := manager.CommitSessionChanges("session", "", true, []SessionDiff{added}); err != nil || !result.Pushed {
		t.Fatalf("repeat push: %+v %v", result, err)
	}
	if result, err := manager.CommitSessionChanges("session", "", true, nil); err != nil || !result.Pushed {
		t.Fatalf("empty push: %+v %v", result, err)
	}
	if sessionTestGit(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("push-only created a commit")
	}
	if err := os.Remove(filepath.Join(dir, "empty.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitSessionChanges("session", "empty deletion", false, []SessionDiff{sessionTestDiff("empty.txt", "", "", "deleted")}); err != nil {
		t.Fatal(err)
	}
	if sessionTestGit(t, dir, "status", "--porcelain") != "" {
		t.Fatal("empty file deletion left staged changes")
	}
}

func TestSessionCommitLinkedWorktree(t *testing.T) {
	t.Parallel()
	dir, manager, base := sessionFixture(t)
	meta, err := manager.Create("ses_commit")
	if err != nil {
		t.Fatal(err)
	}
	configGitUser(meta.Directory)
	after := strings.Replace(base, "one\n", "SANDBOX\n", 1)
	sessionTestWrite(t, meta.Directory, "file.txt", after)
	rootHead := sessionTestGit(t, dir, "rev-parse", "HEAD")
	if _, err := manager.CommitSessionChanges("ses_commit", "sandbox", false, []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")}); err != nil {
		t.Fatal(err)
	}
	if sessionTestGit(t, dir, "rev-parse", "HEAD") != rootHead || sessionTestGit(t, meta.Directory, "show", "HEAD:file.txt") != after {
		t.Fatal("commit escaped sandbox")
	}
	if sessionTestGit(t, meta.Directory, "status", "--porcelain") != "" {
		t.Fatal("sandbox index was not synchronized")
	}
}

func TestSessionCommitRejectsUnsafeAddDelete(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"added-collision", "deleted-recreated", "deleted-conflict", "invalid-added-before", "invalid-deleted-after", "tracked-symlink", "symlink-alias", "non-utf8"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, base := sessionFixture(t)
			diff := sessionTestDiff("file.txt", base, "", "deleted")
			switch kind {
			case "added-collision":
				diff = sessionTestDiff("file.txt", "", "different\n", "added")
			case "deleted-recreated":
				// Even an empty recreated file is not an absent file.
				sessionTestWrite(t, dir, "file.txt", "")
			case "deleted-conflict":
				sessionTestWrite(t, dir, "file.txt", "external\n")
				sessionTestGit(t, dir, "add", "file.txt")
				sessionTestGit(t, dir, "commit", "-m", "external")
				if err := os.Remove(filepath.Join(dir, "file.txt")); err != nil {
					t.Fatal(err)
				}
			case "invalid-added-before":
				diff.Status = "added"
			case "invalid-deleted-after":
				diff.After = &base
			case "tracked-symlink":
				if err := os.Symlink("file.txt", filepath.Join(dir, "link")); err != nil {
					t.Fatal(err)
				}
				sessionTestGit(t, dir, "add", "link")
				sessionTestGit(t, dir, "commit", "-m", "link")
				if err := os.Remove(filepath.Join(dir, "link")); err != nil {
					t.Fatal(err)
				}
				diff = sessionTestDiff("link", "file.txt", "", "deleted")
			case "symlink-alias":
				if err := os.Symlink(dir, filepath.Join(dir, "alias")); err != nil {
					t.Fatal(err)
				}
				diff.File = filepath.Join(dir, "alias", "file.txt")
			case "non-utf8":
				value := "\xff\xfe"
				diff = sessionTestDiff("file.txt", base, value, "modified")
			}
			if _, err := manager.CommitSessionChanges("session", "unsafe", false, []SessionDiff{diff}); err == nil {
				t.Fatal("unsafe add/delete accepted")
			}
		})
	}
}

func TestSessionCommitRejectsContextDependentConfig(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"worktree-hooks", "worktree-signing", "worktree-benign", "onbranch-hooks", "onbranch-signing", "inactive-include", "nested-include", "gitdir-include"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, base := sessionFixture(t)
			realDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			after := strings.Replace(base, "one\n", "SESSION\n", 1)
			sessionTestWrite(t, dir, "file.txt", after)
			hooks := t.TempDir()
			if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			configFile := filepath.Join(t.TempDir(), "policy.config")
			key, value := "core.hooksPath", hooks
			if strings.Contains(kind, "signing") {
				key, value = "commit.gpgSign", "true"
			}
			if kind == "worktree-benign" {
				key, value = "core.untrackedCache", "true"
			}
			if strings.HasPrefix(kind, "worktree-") {
				sessionTestGit(t, dir, "config", "extensions.worktreeConfig", "true")
				sessionTestGit(t, dir, "config", "--worktree", key, value)
			} else {
				sessionTestGit(t, dir, "config", "--file", configFile, key, value)
				condition := "onbranch:main"
				if kind == "inactive-include" {
					condition = "onbranch:other"
				}
				if kind == "gitdir-include" {
					condition = "gitdir:" + filepath.Join(realDir, ".git") + "/"
				}
				if kind == "nested-include" {
					nested := filepath.Join(t.TempDir(), "nested.config")
					sessionTestGit(t, dir, "config", "--file", nested, "includeIf."+condition+".path", configFile)
					sessionTestGit(t, dir, "config", "include.path", nested)
				} else {
					sessionTestGit(t, dir, "config", "includeIf."+condition+".path", configFile)
				}
			}
			head := sessionTestGit(t, dir, "rev-parse", "HEAD")
			index, err := os.ReadFile(filepath.Join(dir, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.CommitSessionChanges("session", "must not bypass policy", false, []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")})
			if kind == "worktree-benign" || kind == "inactive-include" {
				if err != nil {
					t.Fatalf("expected benign config to succeed, got %v", err)
				}
				if sessionTestGit(t, dir, "show", "HEAD:file.txt") != after {
					t.Fatal("expected session commit to be published")
				}
				return
			}
			if err == nil {
				t.Fatal("expected policy enforcement or configuration rejection, got nil")
			}
			if strings.HasPrefix(kind, "onbranch-") || kind == "nested-include" {
				if !strings.Contains(err.Error(), "cannot safely preserve") {
					t.Fatalf("expected configuration rejection, got %v", err)
				}
			}
			if sessionTestGit(t, dir, "rev-parse", "HEAD") != head {
				t.Fatal("configuration policy was bypassed")
			}
			got, _ := os.ReadFile(filepath.Join(dir, ".git/index"))
			if !bytes.Equal(index, got) {
				t.Fatal("index changed")
			}
		})
	}
}

func TestSessionCommitRouteBinding(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"omitted", "sandbox", "body-root", "query-root", "hidden-query-root", "invalid-directory", "duplicate-query", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, base := sessionFixture(t)
			meta, err := manager.Create("ses_bound")
			if err != nil {
				t.Fatal(err)
			}
			configGitUser(meta.Directory)
			after := strings.Replace(base, "one\n", "SESSION\n", 1)
			// Both workspaces contain the diff: a wrong selection would commit root.
			sessionTestWrite(t, dir, "file.txt", after)
			sessionTestWrite(t, meta.Directory, "file.txt", after)
			body := map[string]any{"sessionID": "ses_bound", "message": "bound", "sessionDiffs": []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")}}
			endpoint := "/api/hoxi/worktree/commit"
			want := http.StatusForbidden
			switch kind {
			case "omitted":
				want = http.StatusOK
			case "sandbox":
				body["directory"] = meta.Directory
				endpoint += "?directory=" + url.QueryEscape(meta.Directory)
				want = http.StatusOK
			case "body-root":
				body["directory"] = dir
			case "query-root":
				endpoint += "?directory=" + url.QueryEscape(dir)
			case "hidden-query-root":
				body["directory"] = meta.Directory
				endpoint += "?directory=" + url.QueryEscape(dir)
			case "invalid-directory":
				body["directory"] = filepath.Join(dir, "missing")
				want = http.StatusBadRequest
			case "duplicate-query":
				endpoint += "?directory=" + url.QueryEscape(meta.Directory) + "&directory=" + url.QueryEscape(dir)
				want = http.StatusBadRequest
			case "ambiguous":
				other := initTempGitRepo(t)
				if _, err := manager.ForDirectory(other).Create("ses_bound"); err != nil {
					t.Fatal(err)
				}
			}
			head := sessionTestGit(t, dir, "rev-parse", "HEAD")
			sandboxHead := sessionTestGit(t, meta.Directory, "rev-parse", "HEAD")
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			manager.RegisterRoutes(mux)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data)))
			if rec.Code != want {
				t.Fatalf("route: %d %s", rec.Code, rec.Body.String())
			}
			if sessionTestGit(t, dir, "rev-parse", "HEAD") != head {
				t.Fatal("route committed to root instead of bound sandbox")
			}
			got := sessionTestGit(t, meta.Directory, "rev-parse", "HEAD")
			if (want == http.StatusOK && got == sandboxHead) || (want != http.StatusOK && got != sandboxHead) {
				t.Fatal("unexpected sandbox HEAD change")
			}
		})
	}
}

func TestSessionCommitCumulativeAdditionCheckpoint(t *testing.T) {
	t.Parallel()
	dir, manager, _ := sessionFixture(t)
	sessionTestWrite(t, dir, "new.txt", "initial\n")
	if _, err := manager.CommitSessionChanges("session", "initial addition", false, []SessionDiff{sessionTestDiff("new.txt", "", "initial\n", "added")}); err != nil {
		t.Fatal(err)
	}
	head := sessionTestGit(t, dir, "rev-parse", "HEAD")
	sessionTestWrite(t, dir, "new.txt", "newer\n")
	_, err := manager.CommitSessionChanges("other-session", "later addition", false, []SessionDiff{sessionTestDiff("new.txt", "", "newer\n", "added")})
	if err == nil || !strings.Contains(err.Error(), "already exists in HEAD") {
		t.Fatalf("expected conservative cumulative addition rejection: %v", err)
	}
	if sessionTestGit(t, dir, "rev-parse", "HEAD") != head || sessionTestGit(t, dir, "diff", "--cached") != "" {
		t.Fatal("rejected cumulative addition changed HEAD or staging")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "new.txt")); string(got) != "newer\n" {
		t.Fatal("workspace changed")
	}
	// A fresh manager must recover the same session's durable checkpoint.
	manager = NewManager(dir)
	for _, after := range []string{"newer\n", "third\n", "fourth\n"} {
		sessionTestWrite(t, dir, "new.txt", after)
		if _, err := manager.CommitSessionChanges("session", "later addition", false, []SessionDiff{sessionTestDiff("new.txt", "", after, "added")}); err != nil {
			t.Fatal(err)
		}
		if sessionTestGit(t, dir, "show", "HEAD:new.txt") != after || sessionTestGit(t, dir, "status", "--porcelain") != "" {
			t.Fatal("cumulative addition content or index incorrect")
		}
	}
}

func TestSessionCommitCheckpointAncestorAndWriteFailure(t *testing.T) {
	t.Parallel()
	t.Run("non-ancestor", func(t *testing.T) {
		dir, manager, _ := sessionFixture(t)
		sessionTestWrite(t, dir, "new.txt", "initial\n")
		if _, err := manager.CommitSessionChanges("session", "addition", false, []SessionDiff{sessionTestDiff("new.txt", "", "initial\n", "added")}); err != nil {
			t.Fatal(err)
		}
		sessionTestGit(t, dir, "checkout", "-b", "unrelated", "HEAD~1")
		sessionTestWrite(t, dir, "new.txt", "other history\n")
		sessionTestGit(t, dir, "add", "new.txt")
		sessionTestGit(t, dir, "commit", "-m", "other history")
		sessionTestWrite(t, dir, "new.txt", "latest\n")
		_, err := manager.CommitSessionChanges("session", "must reject", false, []SessionDiff{sessionTestDiff("new.txt", "", "latest\n", "added")})
		if err == nil || !strings.Contains(err.Error(), "not an ancestor") {
			t.Fatalf("non-ancestor checkpoint accepted: %v", err)
		}
	})
	t.Run("save-failure", func(t *testing.T) {
		dir, manager, base := sessionFixture(t)
		after := strings.Replace(base, "one\n", "SESSION\n", 1)
		sessionTestWrite(t, dir, "file.txt", after)
		// Fail checkpoint creation only after initial checkpoint loading succeeded.
		script := "#!/bin/sh\nprintf blocked > \"$(git rev-parse --git-common-dir)/hoxi-session-commits\"\n"
		if err := os.WriteFile(filepath.Join(dir, ".git/hooks/pre-commit"), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		result, err := manager.CommitSessionChanges("session", "published", false, []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")})
		if err == nil || !strings.Contains(err.Error(), "created but session checkpoint save failed") || result.OK || result.Commit == "" {
			t.Fatalf("missing explicit published error: %+v %v", result, err)
		}
		if strings.TrimSpace(sessionTestGit(t, dir, "rev-parse", "HEAD")) != result.Commit || sessionTestGit(t, dir, "diff", "--cached") != "" {
			t.Fatal("published commit/index not preserved after checkpoint failure")
		}
	})
}

func TestSessionCommitCheckpointPreservesIndependentChanges(t *testing.T) {
	t.Parallel()
	dir, manager, base := sessionFixture(t)
	sessionTestWrite(t, dir, "new.txt", base)
	if _, err := manager.CommitSessionChanges("session", "addition", false, []SessionDiff{sessionTestDiff("new.txt", "", base, "added")}); err != nil {
		t.Fatal(err)
	}
	independent := strings.Replace(base, "nine\n", "EXTERNAL\n", 1)
	sessionTestWrite(t, dir, "new.txt", independent)
	sessionTestGit(t, dir, "add", "new.txt")
	sessionTestGit(t, dir, "commit", "-m", "independent")
	for _, first := range []string{"SECOND", "THIRD"} {
		after := strings.Replace(base, "one\n", first+"\n", 1)
		current := strings.Replace(after, "nine\n", "EXTERNAL\n", 1)
		sessionTestWrite(t, dir, "new.txt", current)
		if _, err := manager.CommitSessionChanges("session", "cumulative", false, []SessionDiff{sessionTestDiff("new.txt", "", after, "added")}); err != nil {
			t.Fatal(err)
		}
		if sessionTestGit(t, dir, "show", "HEAD:new.txt") != current {
			t.Fatal("independent HEAD change lost")
		}
	}
	path := filepath.Join(dir, ".git", "hoxi-session-commits", fmt.Sprintf("%x.json", sha256.Sum256([]byte("session"))))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint sessionCommitCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.SessionID != "session" || checkpoint.Files["new.txt"].After != strings.Replace(base, "one\n", "THIRD\n", 1) {
		t.Fatal("checkpoint does not record original session After")
	}
}

func TestSessionCommitRouteRequiresSessionID(t *testing.T) {
	t.Parallel()
	dir, manager, base := sessionFixture(t)
	sessionTestWrite(t, dir, "file.txt", "changed\n")
	for _, id := range []string{"", "   "} {
		body, err := json.Marshal(map[string]any{"sessionID": id, "message": "reject", "sessionDiffs": []SessionDiff{sessionTestDiff("file.txt", base, "changed\n", "modified")}})
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		manager.RegisterRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "sessionID is required") {
			t.Fatalf("empty sessionID accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestSessionCommitCheckpointRejectsUnsafeContinuation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"stale", "conflict", "corrupt", "wrong-session", "wrong-blob"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, _ := sessionFixture(t)
			sessionTestWrite(t, dir, "new.txt", "initial\n")
			if _, err := manager.CommitSessionChanges("session", "addition", false, []SessionDiff{sessionTestDiff("new.txt", "", "initial\n", "added")}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, ".git", "hoxi-session-commits", fmt.Sprintf("%x.json", sha256.Sum256([]byte("session"))))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sessionTestWrite(t, dir, "new.txt", "latest\n")
			switch kind {
			case "stale":
				sessionTestWrite(t, dir, "new.txt", "initial\n")
			case "conflict":
				sessionTestWrite(t, dir, "new.txt", "external\n")
				sessionTestGit(t, dir, "add", "new.txt")
				sessionTestGit(t, dir, "commit", "-m", "external conflicting edit")
				sessionTestWrite(t, dir, "new.txt", "latest\n")
			case "corrupt":
				data = []byte("not json")
			case "wrong-session", "wrong-blob":
				var checkpoint sessionCommitCheckpoint
				if err := json.Unmarshal(data, &checkpoint); err != nil {
					t.Fatal(err)
				}
				if kind == "wrong-session" {
					checkpoint.SessionID = "other-session"
				} else {
					snapshot := checkpoint.Files["new.txt"]
					snapshot.Blob = "invalid"
					checkpoint.Files["new.txt"] = snapshot
				}
				data, err = json.Marshal(checkpoint)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			head := sessionTestGit(t, dir, "rev-parse", "HEAD")
			if _, err := manager.CommitSessionChanges("session", "unsafe continuation", false, []SessionDiff{sessionTestDiff("new.txt", "", "latest\n", "added")}); err == nil {
				t.Fatal("unsafe continuation accepted")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, got) || sessionTestGit(t, dir, "rev-parse", "HEAD") != head || sessionTestGit(t, dir, "diff", "--cached") != "" {
				t.Fatal("failed continuation changed checkpoint, HEAD or staging")
			}
		})
	}
}

func TestSessionCommitModifiedSameLineCheckpoint(t *testing.T) {
	t.Parallel()
	dir, manager, base := sessionFixture(t)
	for i, first := range []string{"SESSION", "SECOND", "THIRD"} {
		after := strings.Replace(base, "one\n", first+"\n", 1)
		current := after
		if i > 0 {
			current = strings.Replace(after, "nine\n", "INDEPENDENT\n", 1)
		}
		sessionTestWrite(t, dir, "file.txt", current)
		diff := sessionTestDiff("file.txt", base, after, "modified")
		if i > 0 {
			head := sessionTestGit(t, dir, "rev-parse", "HEAD")
			if _, err := manager.CommitSessionChanges("other-session", "must not borrow checkpoint", false, []SessionDiff{diff}); err == nil {
				t.Fatal("other session reused checkpoint")
			}
			if sessionTestGit(t, dir, "rev-parse", "HEAD") != head {
				t.Fatal("rejected session changed HEAD")
			}
		}
		// Recovery must work without any in-memory history.
		manager = NewManager(dir)
		other := sessionTestGit(t, dir, "show", "HEAD:other.txt")
		if _, err := manager.CommitSessionChanges("session", "same line cumulative edit", false, []SessionDiff{diff}); err != nil {
			t.Fatal(err)
		}
		if sessionTestGit(t, dir, "show", "HEAD:file.txt") != current || sessionTestGit(t, dir, "status", "--porcelain") != "" {
			t.Fatal("wrong committed content or workspace/index changed")
		}
		if sessionTestGit(t, dir, "show", "HEAD:other.txt") != other {
			t.Fatal("unrelated committed file changed")
		}
		if _, err := manager.CommitSessionChanges("session", "repeat", false, []SessionDiff{diff}); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
			t.Fatalf("repeat cumulative modification: %v", err)
		}
		// Advance HEAD past the saved checkpoint, changing both another line
		// of this file and an unrelated file without touching the session line.
		independent := strings.Replace(current, "nine\n", "INDEPENDENT\n", 1)
		sessionTestWrite(t, dir, "file.txt", independent)
		sessionTestWrite(t, dir, "other.txt", first+" external\n")
		sessionTestGit(t, dir, "add", "file.txt", "other.txt")
		sessionTestGit(t, dir, "commit", "-m", "independent changes after checkpoint")
	}
	if sessionTestGit(t, dir, "show", "HEAD:other.txt") != "THIRD external\n" {
		t.Fatal("unrelated committed file lost")
	}
}

func TestSessionCommitModifiedCheckpointValidation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"non-ancestor", "wrong-blob"} {
		t.Run(kind, func(t *testing.T) {
			dir, manager, base := sessionFixture(t)
			first := strings.Replace(base, "one\n", "SESSION\n", 1)
			sessionTestWrite(t, dir, "file.txt", first)
			if _, err := manager.CommitSessionChanges("session", "first edit", false, []SessionDiff{sessionTestDiff("file.txt", base, first, "modified")}); err != nil {
				t.Fatal(err)
			}
			if kind == "non-ancestor" {
				sessionTestGit(t, dir, "checkout", "-b", "other-history", "HEAD~1")
			} else {
				path := filepath.Join(dir, ".git", "hoxi-session-commits", fmt.Sprintf("%x.json", sha256.Sum256([]byte("session"))))
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var checkpoint sessionCommitCheckpoint
				if err := json.Unmarshal(data, &checkpoint); err != nil {
					t.Fatal(err)
				}
				snapshot := checkpoint.Files["file.txt"]
				snapshot.Blob = "wrong"
				checkpoint.Files["file.txt"] = snapshot
				if err := writeSessionCheckpoint(path, checkpoint); err != nil {
					t.Fatal(err)
				}
			}
			after := strings.Replace(base, "one\n", "SECOND\n", 1)
			sessionTestWrite(t, dir, "file.txt", after)
			head := sessionTestGit(t, dir, "rev-parse", "HEAD")
			if _, err := manager.CommitSessionChanges("session", "reject invalid checkpoint", false, []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")}); err == nil {
				t.Fatal("invalid modified checkpoint accepted")
			}
			if sessionTestGit(t, dir, "rev-parse", "HEAD") != head || sessionTestGit(t, dir, "diff", "--cached") != "" {
				t.Fatal("failed validation changed HEAD or staging")
			}
		})
	}
}
