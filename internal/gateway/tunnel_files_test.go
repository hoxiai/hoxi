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
