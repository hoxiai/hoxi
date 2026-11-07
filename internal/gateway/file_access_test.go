package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// decodeSensitiveRequest builds an absolute path inside a synthetic credential
// store so isSensitiveCredentialPath matches it.
func sensitiveFixture(t *testing.T) (home, root, secret string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root = t.TempDir()
	secret = writeTestFile(t, filepath.Join(home, ".gemini", "oauth_creds.json"), "SYNTHETIC_CREDENTIAL\n")
	return home, root, secret
}

func TestSensitiveFileReadAsksDesktopAndNeverRemembers(t *testing.T) {
	_, root, secret := sensitiveFixture(t)
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	query := "?" + url.Values{"path": {secret}, "directory": {root}}.Encode()

	// Headless / no dialog: fail closed.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("read without a dialog = %d %s, want 403", rec.Code, rec.Body.String())
	}

	// Declining the dialog denies the read.
	calls := 0
	g.SetFileAccessConfirmation(func(_ context.Context, c FileAccessConfirmation) (bool, error) {
		calls++
		if c.Action != "read" || c.Path != secret {
			t.Fatalf("unexpected confirmation %+v", c)
		}
		return false, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("declined read = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("confirmation calls = %d, want 1", calls)
	}

	// Accepting grants that one read only.
	g.SetFileAccessConfirmation(func(_ context.Context, _ FileAccessConfirmation) (bool, error) {
		calls++
		return true, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("accepted read = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("accepted read should ask again: calls = %d, want 2", calls)
	}
}

func TestSensitiveFileOpenAsksDesktop(t *testing.T) {
	_, root, secret := sensitiveFixture(t)
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	body := `{"path":` + jsonString(secret) + `,"directory":` + jsonString(root) + `,"action":"reveal"}`

	// No dialog: fail closed.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/open", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("open without a dialog = %d %s, want 403", rec.Code, rec.Body.String())
	}

	// Declining denies the open.
	g.SetFileAccessConfirmation(func(_ context.Context, c FileAccessConfirmation) (bool, error) {
		if c.Action != "reveal" || c.Path != secret {
			t.Fatalf("unexpected confirmation %+v", c)
		}
		return false, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/open", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("declined open = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestSensitiveFileAccessNeverOfferedToRemoteControllers(t *testing.T) {
	home, root, secret := sensitiveFixture(t)
	_ = home
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	calls := 0
	g.SetFileAccessConfirmation(func(_ context.Context, _ FileAccessConfirmation) (bool, error) {
		calls++
		return true, nil
	})

	query := "?" + url.Values{"path": {secret}, "directory": {root}}.Encode()
	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("remote sensitive read = %d %s, want 403 without content", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("remote request must never raise a desktop dialog, got %d", calls)
	}
}

func TestOutOfWorkspaceFileReadSkipsSensitiveDialog(t *testing.T) {
	home, root, _ := sensitiveFixture(t)
	ordinary := writeTestFile(t, filepath.Join(home, "notes.md"), "ordinary notes\n")
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	calls := 0
	g.SetFileAccessConfirmation(func(_ context.Context, _ FileAccessConfirmation) (bool, error) {
		calls++
		return false, nil
	})

	query := "?" + url.Values{"path": {ordinary}, "directory": {root}}.Encode()
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ordinary notes") {
		t.Fatalf("desktop non-sensitive read = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("non-sensitive path must not ask, got %d calls", calls)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
