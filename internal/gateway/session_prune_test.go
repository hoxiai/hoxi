package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
)

type pruneFixture struct {
	gw   *Gateway
	svc  *runner.Service
	repo string
}

func newPruneFixture(t *testing.T) pruneFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	// Never let a developer shell point the test at the live hoxi.db.
	t.Setenv("OPENCODE_DB", filepath.Join(home, "hoxi.db"))
	t.Setenv("HOXI_DB_PATH", filepath.Join(home, "hoxi.db"))
	repo := t.TempDir()
	svc := runner.NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	gw := New(0, "pass", repo, nil, nil)
	gw.runner = svc
	return pruneFixture{gw: gw, svc: svc, repo: repo}
}

func (f pruneFixture) put(t *testing.T, rec runner.SessionRecord) {
	t.Helper()
	if rec.Runtime == "" {
		rec.Runtime = string(runner.RuntimeClaudeCode)
	}
	if rec.ProjectDir == "" {
		rec.ProjectDir = f.repo
	}
	if rec.WorktreePath == "" {
		rec.WorktreePath = rec.ProjectDir
	}
	if err := f.svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}
}

func (f pruneFixture) preview(t *testing.T, days string) (int, sessionPrunePreview) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.gw.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/system/storage/old-sessions?olderThanDays="+days, nil))
	var res sessionPrunePreview
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, res
}

func (f pruneFixture) delete(t *testing.T, body string) (int, sessionPruneResult) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.gw.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/system/storage/old-sessions/delete", strings.NewReader(body)))
	var res sessionPruneResult
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, res
}

func daysAgo(n int) int64 { return time.Now().Add(-time.Duration(n) * 24 * time.Hour).UnixMilli() }

func skipReasons(res sessionPruneResult) map[string]string {
	out := map[string]string{}
	for _, s := range res.Skipped {
		out[s.ID] = s.Reason
	}
	return out
}

func TestOldSessionsPreviewAndDeleteOnlyConfirmedIdleRoots(t *testing.T) {
	f := newPruneFixture(t)
	f.put(t, runner.SessionRecord{ID: "ses_cld_old", Title: "old", CreatedAt: daysAgo(60), UpdatedAt: daysAgo(40)})
	f.put(t, runner.SessionRecord{ID: "ses_cld_old_child", Title: "child", ParentID: "ses_cld_old", Subagent: true, CreatedAt: daysAgo(41), UpdatedAt: daysAgo(41)})
	f.put(t, runner.SessionRecord{ID: "ses_cld_recent", Title: "recent", CreatedAt: daysAgo(60), UpdatedAt: daysAgo(2)})
	// An old root whose subagent ran recently is still in use.
	f.put(t, runner.SessionRecord{ID: "ses_cld_active_tree", Title: "tree", CreatedAt: daysAgo(90), UpdatedAt: daysAgo(90)})
	f.put(t, runner.SessionRecord{ID: "ses_cld_active_child", ParentID: "ses_cld_active_tree", Subagent: true, CreatedAt: daysAgo(1), UpdatedAt: daysAgo(1)})

	code, preview := f.preview(t, "30")
	if code != http.StatusOK {
		t.Fatalf("preview status %d", code)
	}
	if len(preview.Sessions) != 1 || preview.Sessions[0].ID != "ses_cld_old" || preview.Sessions[0].Children != 1 {
		t.Fatalf("preview should list only the idle root with its child: %+v", preview.Sessions)
	}
	if preview.Sessions[0].Bytes <= 0 || preview.TotalBytes != preview.Sessions[0].Bytes {
		t.Fatalf("preview should estimate reclaimable bytes: %+v", preview)
	}

	code, res := f.delete(t, `{"olderThanDays":30,"sessionIDs":["ses_cld_old","ses_cld_recent","ses_cld_active_tree","ses_cld_old_child","ses_missing","ses_cld_old"]}`)
	if code != http.StatusOK {
		t.Fatalf("delete status %d", code)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != "ses_cld_old" {
		t.Fatalf("deleted = %v, want only ses_cld_old", res.Deleted)
	}
	reasons := skipReasons(res)
	for _, id := range []string{"ses_cld_recent", "ses_cld_active_tree", "ses_cld_old_child", "ses_missing"} {
		if reasons[id] != pruneSkipNotEligible {
			t.Fatalf("%s skip reason = %q, want %q (all: %+v)", id, reasons[id], pruneSkipNotEligible, res.Skipped)
		}
	}
	if len(res.Skipped) != 4 {
		t.Fatalf("each rejected ID must be reported exactly once: %+v", res.Skipped)
	}
	store := f.svc.Store()
	if _, ok := store.Meta("ses_cld_old"); ok {
		t.Fatal("confirmed idle session should be deleted")
	}
	if _, ok := store.Meta("ses_cld_old_child"); ok {
		t.Fatal("its subagent child should be deleted with it")
	}
	for _, id := range []string{"ses_cld_recent", "ses_cld_active_tree", "ses_cld_active_child"} {
		if _, ok := store.Meta(id); !ok {
			t.Fatalf("%s must survive cleanup", id)
		}
	}
}

func TestOldSessionsRejectsBadInputAndRemoteCallers(t *testing.T) {
	f := newPruneFixture(t)
	f.put(t, runner.SessionRecord{ID: "ses_cld_old", CreatedAt: daysAgo(60), UpdatedAt: daysAgo(60)})

	for _, days := range []string{"", "abc", "6", "0", "-30", "3651"} {
		if code, _ := f.preview(t, days); code != http.StatusBadRequest {
			t.Fatalf("preview olderThanDays=%q status %d, want 400", days, code)
		}
	}
	for _, body := range []string{
		`not json`,
		`{"olderThanDays":3,"sessionIDs":["ses_cld_old"]}`,
		`{"olderThanDays":30,"sessionIDs":[]}`,
		`{"olderThanDays":30}`,
	} {
		if code, _ := f.delete(t, body); code != http.StatusBadRequest {
			t.Fatalf("delete %s status %d, want 400", body, code)
		}
	}
	for _, target := range []string{
		"/api/hoxi/system/storage/old-sessions?olderThanDays=30",
		"/api/hoxi/system/storage/old-sessions/delete",
	} {
		method := http.MethodGet
		if strings.HasSuffix(target, "/delete") {
			method = http.MethodPost
		}
		body := strings.NewReader(`{"olderThanDays":30,"sessionIDs":["ses_cld_old"]}`)
		tunnelRec := httptest.NewRecorder()
		f.gw.serveTunnelRequest(tunnelRec, httptest.NewRequest(method, target, body))
		if tunnelRec.Code != http.StatusForbidden {
			t.Fatalf("tunnel %s %s status %d, want 403", method, target, tunnelRec.Code)
		}
		body = strings.NewReader(`{"olderThanDays":30,"sessionIDs":["ses_cld_old"]}`)
		remote := httptest.NewRequest(method, target, body)
		remote = remote.WithContext(context.WithValue(remote.Context(), remoteSourceKey{}, remoteSourceTunnel))
		remoteRec := httptest.NewRecorder()
		if method == http.MethodGet {
			f.gw.handleOldSessionsPreview(remoteRec, remote)
		} else {
			f.gw.handleOldSessionsDelete(remoteRec, remote)
		}
		if remoteRec.Code != http.StatusForbidden {
			t.Fatalf("remote %s %s status %d, want 403", method, target, remoteRec.Code)
		}
	}
	if _, ok := f.svc.Store().Meta("ses_cld_old"); !ok {
		t.Fatal("remote callers must not delete sessions")
	}
}

func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestOldSessionsKeepDirtyWorktreesAndBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := newPruneFixture(t)
	runGitT(t, f.repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, f.repo, "add", "README.md")
	runGitT(t, f.repo, "commit", "-q", "-m", "init")

	worktree := func(id string) string {
		dir := filepath.Join(f.repo, ".hoxi", "worktrees", id)
		runGitT(t, f.repo, "worktree", "add", "-q", "-b", "hoxi/"+id, dir)
		f.put(t, runner.SessionRecord{ID: id, ProjectDir: f.repo, WorktreePath: dir, SandboxMode: "isolated", CreatedAt: daysAgo(60), UpdatedAt: daysAgo(60)})
		return dir
	}
	cleanDir := worktree("ses_cld_clean")
	dirtyDir := worktree("ses_cld_dirty")
	if err := os.WriteFile(filepath.Join(dirtyDir, "wip.txt"), []byte("unsaved work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, preview := f.preview(t, "30")
	if code != http.StatusOK || len(preview.Sessions) != 2 || !preview.Sessions[0].Worktree {
		t.Fatalf("preview should flag both sandboxed sessions: %d %+v", code, preview.Sessions)
	}

	_, res := f.delete(t, `{"olderThanDays":30,"sessionIDs":["ses_cld_clean","ses_cld_dirty"]}`)
	if len(res.Deleted) != 1 || res.Deleted[0] != "ses_cld_clean" {
		t.Fatalf("deleted = %v, want only the clean sandbox", res.Deleted)
	}
	if reason := skipReasons(res)["ses_cld_dirty"]; reason != pruneSkipWorktreeDirty {
		t.Fatalf("dirty sandbox reason = %q, want %q", reason, pruneSkipWorktreeDirty)
	}
	if _, err := os.Stat(cleanDir); !os.IsNotExist(err) {
		t.Fatalf("clean worktree should be removed, stat err = %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(dirtyDir, "wip.txt")); err != nil || string(raw) != "unsaved work\n" {
		t.Fatalf("uncommitted work must be kept: %q %v", raw, err)
	}
	if _, ok := f.svc.Store().Meta("ses_cld_dirty"); !ok {
		t.Fatal("a session with uncommitted work must not be deleted")
	}
	out, err := exec.Command("git", "-C", f.repo, "branch", "--list", "hoxi/ses_cld_clean").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "hoxi/ses_cld_clean") {
		t.Fatalf("the removed sandbox's branch must be kept: %q %v", out, err)
	}
}
