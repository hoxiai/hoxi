package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/workspace"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestSessionScopeSelectorsAndCompatibility(t *testing.T) {
	root := t.TempDir()
	mgr := worktree.NewManager(root)
	if err := makeSandbox(root, "ses_guard"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{worktrees: mgr}
	bound := filepath.Join(root, ".hoxi", "worktrees", "ses_guard")
	wrong := t.TempDir()
	link := filepath.Join(root, "sandbox-link")
	if err := os.Symlink(bound, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, target, header string
		status               int
		wantDir              string
	}{
		{"missing injects", "/session/ses_guard", "", http.StatusOK, bound},
		{"main mismatch", "/session/ses_guard?directory=" + url.QueryEscape(root), "", http.StatusForbidden, ""},
		{"sibling mismatch", "/api/session/ses_guard?directory=" + url.QueryEscape(wrong), "", http.StatusForbidden, ""},
		{"symlink canonical match", "/api/session/ses_guard?directory=" + url.QueryEscape(link), "", http.StatusOK, ""},
		{"header mismatch", "/session/ses_guard", wrong, http.StatusForbidden, ""},
		{"unknown passthrough", "/session/legacy", "", http.StatusOK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.header != "" {
				r.Header.Set(sessionDirectoryHeader, tc.header)
			}
			w := httptest.NewRecorder()
			if !g.guardSessionScope(w, r) {
				if w.Code != tc.status {
					t.Fatalf("status = %d, want %d", w.Code, tc.status)
				}
				return
			}
			if tc.status != http.StatusOK {
				t.Fatalf("guard allowed request, want %d", tc.status)
			}
			if tc.wantDir != "" {
				canonicalDir, err := filepath.EvalSymlinks(tc.wantDir)
				if err != nil {
					t.Fatal(err)
				}
				if r.URL.Query().Get("directory") != canonicalDir {
					t.Fatalf("directory = %q, want %q", r.URL.Query().Get("directory"), canonicalDir)
				}
			}
		})
	}

	for _, target := range []string{
		"/session/ses_guard?directory=" + url.QueryEscape(bound) + "&directory=" + url.QueryEscape(bound),
		"/session/ses_guard?directory=%zz",
	} {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		if g.guardSessionScope(w, r) || w.Code != http.StatusBadRequest {
			t.Fatalf("target %q should be rejected: allowed=%v status=%d", target, w.Code == 0, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/session/ses_guard", nil)
	r.Header[sessionDirectoryHeader] = []string{bound, bound}
	w := httptest.NewRecorder()
	if g.guardSessionScope(w, r) || w.Code != http.StatusBadRequest {
		t.Fatal("duplicate directory headers should be rejected")
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/hoxi/runner/sessions/ses_guard?directory="+url.QueryEscape(root), nil)
	patchRec := httptest.NewRecorder()
	if !g.guardSessionScope(patchRec, patchReq) {
		t.Fatalf("runner session PATCH with parent project root should be allowed, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
}

func TestSessionScopeHealsDeletedWorkspaceDirectorySelector(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldRoot := filepath.Join(home, "old-repo")
	newRoot := filepath.Join(home, "new-repo")
	unrelatedExisting := filepath.Join(home, "other-repo")
	for _, dir := range []string{oldRoot, newRoot, unrelatedExisting} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	wsStore := workspace.NewStore(oldRoot)
	projectID, err := wsStore.EnsureProjectForDirectory(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	folderID := wsStore.List()[0].Folders[0].ID
	wtMgr := worktree.NewManager(oldRoot)
	runnerSvc := runner.NewService(oldRoot, wtMgr, nil, filepath.Join(home, "sessions"))
	runnerSvc.SetProjectResolver(wsStore)
	if err := runnerSvc.Store().Put(runner.SessionRecord{
		ID: "ses_healed", ProjectID: projectID, Runtime: "claude-code",
		ProjectDir: oldRoot, WorktreePath: oldRoot, SandboxMode: "local",
		CreatedAt: 1, UpdatedAt: 2,
	}); err != nil {
		t.Fatal(err)
	}
	_ = wtMgr.ForDirectory(oldRoot).UseLocalDirectory("ses_healed")

	// Simulate workspace folder replacement and deletion of the old directory on disk.
	wsMux := http.NewServeMux()
	wsStore.RegisterRoutes(wsMux)
	updateRec := httptest.NewRecorder()
	updateBody := `{"folders":[{"id":` + string(mustJSON(t, folderID)) + `,"path":` + string(mustJSON(t, newRoot)) + `,"isPrimary":true}]}`
	wsMux.ServeHTTP(updateRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/workspaces/"+projectID, strings.NewReader(updateBody)))
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update workspace status = %d: %s", updateRec.Code, updateRec.Body.String())
	}
	if err := os.RemoveAll(oldRoot); err != nil {
		t.Fatal(err)
	}

	gw := &Gateway{worktrees: wtMgr, workspaces: wsStore, runner: runnerSvc}
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_healed/prompt_async?directory="+url.QueryEscape(oldRoot), nil)
	rec := httptest.NewRecorder()
	if !gw.guardSessionScope(rec, req) {
		t.Fatalf("guardSessionScope rejected stale deleted directory selector: %d %s", rec.Code, rec.Body.String())
	}
	canonicalNew, _ := filepath.EvalSymlinks(newRoot)
	if got := req.URL.Query().Get("directory"); got != canonicalNew && got != newRoot {
		t.Fatalf("rewritten directory = %q, want %q", got, canonicalNew)
	}

	// An unrelated existing directory must still be rejected with 403 Forbidden.
	badReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_healed/prompt_async?directory="+url.QueryEscape(unrelatedExisting), nil)
	badRec := httptest.NewRecorder()
	if gw.guardSessionScope(badRec, badReq) || badRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unrelated existing directory, got allowed=%v code=%d", badRec.Code == 0, badRec.Code)
	}
}

func makeSandbox(root, sessionID string) error {
	return os.MkdirAll(filepath.Join(root, ".hoxi", "worktrees", sessionID), 0o755)
}
