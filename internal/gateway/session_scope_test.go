package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

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

func makeSandbox(root, sessionID string) error {
	return os.MkdirAll(filepath.Join(root, ".hoxi", "worktrees", sessionID), 0o755)
}
