package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, p, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func tunnelGet(g *Gateway, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, target, nil))
	return rec
}

func TestTunnelFileReadsStayInsideWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(root, "inside.go"), "package inside\n")
	writeTestFile(t, filepath.Join(root, "sub", "deep.go"), "package deep\n")
	secret := writeTestFile(t, filepath.Join(outside, "secret.txt"), "top-secret\n")
	g := New(0, "test", root, nil, nil)

	q := func(kv ...string) string {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v.Encode()
	}

	for _, route := range []string{"/file/content", "/api/file/content", "/api/hoxi/file/content"} {
		if rec := tunnelGet(g, route+"?"+q("path", "inside.go", "directory", root)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "package inside") {
			t.Fatalf("%s inside workspace = %d %s", route, rec.Code, rec.Body.String())
		}
		denied := []string{
			q("path", secret, "directory", root),
			q("path", "secret.txt", "directory", outside),
			q("path", "../"+filepath.Base(outside)+"/secret.txt", "directory", root),
			q("path", filepath.Join(outside, "missing.txt"), "directory", root),
		}
		for _, query := range denied {
			rec := tunnelGet(g, route+"?"+query)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "top-secret") {
				t.Fatalf("%s?%s over tunnel = %d %s, want 403", route, query, rec.Code, rec.Body.String())
			}
		}
	}

	// The desktop keeps reading files outside the workspace.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content?"+q("path", secret, "directory", root), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("desktop read outside workspace = %d", rec.Code)
	}

	if rec := tunnelGet(g, "/api/hoxi/file/list?"+q("path", "sub", "directory", root)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deep.go") {
		t.Fatalf("list inside workspace = %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{q("path", outside, "directory", root), q("directory", outside), q("path", home)} {
		if rec := tunnelGet(g, "/api/hoxi/file/list?"+query); rec.Code != http.StatusForbidden {
			t.Fatalf("list %s over tunnel = %d %s, want 403", query, rec.Code, rec.Body.String())
		}
	}

	if rec := tunnelGet(g, "/api/hoxi/file/search?"+q("q", "deep", "directory", root)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deep.go") {
		t.Fatalf("search inside workspace = %d %s", rec.Code, rec.Body.String())
	}
	if rec := tunnelGet(g, "/api/hoxi/file/search?"+q("q", "secret", "directory", outside)); rec.Code != http.StatusForbidden {
		t.Fatalf("search outside workspace = %d %s, want 403", rec.Code, rec.Body.String())
	}

	// A symlink inside the workspace pointing outside is still refused.
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if rec := tunnelGet(g, "/file/content?"+q("path", "link.txt", "directory", root)); rec.Code != http.StatusForbidden {
		t.Fatalf("symlink escape over tunnel = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestSensitiveCredentialPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateDir := filepath.Join(t.TempDir(), "custom-state")
	t.Setenv("HOXI_HOME", stateDir)
	t.Setenv("OPENCODE_DB", "")
	db := writeTestFile(t, filepath.Join(t.TempDir(), "engine.db"), "sqlite")
	t.Setenv("HOXI_DB_PATH", db)

	sensitive := []string{
		filepath.Join(stateDir, "models.json"),
		filepath.Join(stateDir, "tunnel.json"),
		db,
		filepath.Join(home, ".codex", "auth.json"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".gemini", "oauth_creds.json"),
		filepath.Join(home, ".claude", ".credentials.json"),
		filepath.Join(home, ".config", "gh", "hosts.yml"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".npmrc"),
		filepath.Join(home, ".docker", "config.json"),
		filepath.Join(home, ".git-credentials"),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, "project", ".hoxi", "models.json"),
	}
	for _, p := range sensitive {
		writeTestFile(t, p+".probe", "x") // make parents exist so symlink resolution matches
		if !isSensitiveCredentialPath(p) {
			t.Errorf("%s must be treated as a credential path", p)
		}
	}
	ordinary := []string{
		filepath.Join(stateDir, "notes.md"),
		filepath.Join(home, "project", "models.json"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".gemini", "antigravity", "brain", "c1", "plan.md"),
		filepath.Join(home, ".config", "ghostty", "config"),
		filepath.Join(home, ".npmrc.d", "x"),
	}
	for _, p := range ordinary {
		if isSensitiveCredentialPath(p) {
			t.Errorf("%s should stay readable", p)
		}
	}
}

func initGatewayGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	convoyTestGit(t, dir, "init", "-q", "-b", "main")
	writeTestFile(t, filepath.Join(dir, "base.txt"), "base\n")
	convoyTestGit(t, dir, "add", ".")
	convoyTestGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func TestTunnelDiffDoesNotFollowSecretSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root := initGatewayGitRepo(t)
	outside := writeTestFile(t, filepath.Join(t.TempDir(), ".ssh", "id_audit"), "SYNTHETIC_AUDIT_SECRET\n")
	if err := os.Symlink(outside, filepath.Join(root, "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()
	query := url.Values{"directory": {root}, "path": {"innocent.txt"}}
	denied := tunnelGet(g, "/api/hoxi/file/content?"+query.Encode())
	if denied.Code != http.StatusForbidden {
		t.Fatalf("control file read was not denied: %d", denied.Code)
	}
	got := tunnelGet(g, "/api/hoxi/worktree/diff?"+url.Values{"directory": {root}}.Encode())
	if strings.Contains(got.Body.String(), "SYNTHETIC_AUDIT_SECRET") {
		t.Fatalf("remote diff leaked symlink target content: HTTP %d %s", got.Code, got.Body.String())
	}
}

func TestTunnelWorktreeRoutesStayInsideRegisteredRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root, outside := initGatewayGitRepo(t), initGatewayGitRepo(t)
	writeTestFile(t, filepath.Join(outside, "outside.txt"), "OUTSIDE_WORKSPACE_AUDIT\n")
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()
	queries := []url.Values{
		{"directory": {outside}, "sessionID": {"ses_unknown"}},
		{"directory": {root}, "extraDir": {outside}},
	}
	for _, q := range queries {
		got := tunnelGet(g, "/api/hoxi/worktree/diff?"+q.Encode())
		if got.Code != http.StatusForbidden || strings.Contains(got.Body.String(), "OUTSIDE_WORKSPACE_AUDIT") {
			t.Fatalf("unregistered directory accepted for %v: HTTP %d %s", q, got.Code, got.Body.String())
		}
	}
}

func TestFileReadInWorktreeDoesNotFallBackToMainRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "deleted.go"), "MAIN_WORKSPACE_ONLY\n")
	sandbox := filepath.Join(root, ".hoxi", "worktrees", "ses_isolated")
	if err := os.MkdirAll(sandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()
	if err := g.worktrees.RestoreSession("ses_isolated", sandbox, "", ""); err != nil {
		t.Fatal(err)
	}
	got := tunnelGet(g, "/api/hoxi/file/content?"+url.Values{"directory": {sandbox}, "sessionID": {"ses_isolated"}, "path": {"deleted.go"}}.Encode())
	if strings.Contains(got.Body.String(), "MAIN_WORKSPACE_ONLY") {
		t.Fatalf("session file read returned main workspace content: HTTP %d %s", got.Code, got.Body.String())
	}
}
