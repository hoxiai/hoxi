package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
)

type summaryExtension struct {
	Extension
	items  []WorkAppRegistryEntry
	err    error
	scopes map[string]WorkPackageSessionScope
	sErr   error
}

func (s summaryExtension) Close() {}
func (s summaryExtension) ListWorkAppRegistryEntries() ([]WorkAppRegistryEntry, error) {
	return s.items, s.err
}
func (s summaryExtension) WorkPackageSessionScope(appID string) (WorkPackageSessionScope, error) {
	if s.sErr != nil {
		return WorkPackageSessionScope{}, s.sErr
	}
	return s.scopes[appID], nil
}

func TestWorkAppRegistryEntryRemoteContract(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	g.SetGatewayToken("desktop")
	for _, tc := range []struct {
		ext  Extension
		code int
		json string
	}{
		{nil, 501, ""},
		{summaryExtension{}, 200, `{"items":[]}`},
		{summaryExtension{items: []WorkAppRegistryEntry{{ID: "demo", Name: "Demo", Status: "installed"}}}, 200, `{"items":[{"id":"demo","name":"Demo","status":"installed"}]}`},
		{summaryExtension{err: errors.New("private store path")}, 503, ""},
	} {
		g.extensions = nil
		if tc.ext != nil {
			g.extensions = []Extension{tc.ext}
		}
		w := httptest.NewRecorder()
		g.serveTunnelRequest(w, localRequest(http.MethodGet, workAppRegistryPath, nil))
		if w.Code != tc.code || (tc.json != "" && w.Body.String() != tc.json+"\n") {
			t.Fatalf("response %d: %s", w.Code, w.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := localRequest(method, workAppRegistryPath, nil)
		r.Header.Set("X-Hoxi-Remote-Source", "lan")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("unpaired %s: %d", method, w.Code)
		}
	}
}

func TestWorkPackageSessionsRemoteContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	root := t.TempDir()
	appDir := filepath.Join(home, ".hoxi", "workspaces", "default", "shopify")
	otherDir := filepath.Join(home, ".hoxi", "workspaces", "default", "other")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}

	g := New(0, "test", root, nil, nil)
	g.SetGatewayToken("desktop")
	defer g.CloseBrowser()

	// Create 3 sessions: one in appDir, one bound by sessionID in root, one unrelated in otherDir.
	sAppDir, code, err := g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "App dir task",
		Directory:   appDir,
		Runtime:     string(runner.RuntimeNative),
		SandboxMode: "local",
	}, appDir)
	if err != nil || code != http.StatusOK {
		t.Fatalf("create sAppDir: %d %v", code, err)
	}
	sBound, code, err := g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "Bound task",
		Directory:   root,
		Runtime:     string(runner.RuntimeNative),
		SandboxMode: "local",
	}, root)
	if err != nil || code != http.StatusOK {
		t.Fatalf("create sBound: %d %v", code, err)
	}
	_, code, err = g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "Other app task",
		Directory:   otherDir,
		Runtime:     string(runner.RuntimeNative),
		SandboxMode: "local",
	}, otherDir)
	if err != nil || code != http.StatusOK {
		t.Fatalf("create other: %d %v", code, err)
	}

	// 501 without extension.
	w := httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, "/api/hoxi/work/app-sessions/shopify", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("no extension = %d, want 501", w.Code)
	}

	// 404 when app is not installed.
	g.extensions = []Extension{summaryExtension{
		scopes: map[string]WorkPackageSessionScope{
			"shopify": {
				Installed:   true,
				SessionIDs:  []string{sBound.ID},
				Directories: []string{appDir},
			},
		},
	}}
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, "/api/hoxi/work/app-sessions/unknown", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("not installed = %d, want 404", w.Code)
	}

	// 503 on extension error.
	g.extensions = []Extension{summaryExtension{sErr: errors.New("boom")}}
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, "/api/hoxi/work/app-sessions/shopify", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("store err = %d, want 503", w.Code)
	}

	// 200 returns only sessions matching shopify's directory or bound session IDs.
	g.extensions = []Extension{summaryExtension{
		scopes: map[string]WorkPackageSessionScope{
			"shopify": {
				Installed:   true,
				SessionIDs:  []string{sBound.ID},
				Directories: []string{appDir},
			},
		},
	}}
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, "/api/hoxi/work/app-sessions/shopify", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("installed = %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		Items    []runner.SessionInfo `json:"items"`
		Statuses map[string]string    `json:"statuses"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 sessions for shopify, got %d: %+v", len(payload.Items), payload.Items)
	}
	gotIDs := map[string]bool{payload.Items[0].ID: true, payload.Items[1].ID: true}
	if !gotIDs[sAppDir.ID] || !gotIDs[sBound.ID] {
		t.Fatalf("unexpected session IDs: %+v", gotIDs)
	}

	// Opening a Scheme C per-app default workspace session via /api/hoxi/sessions/*/message is allowed over tunnel.
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, "/api/hoxi/sessions/"+sAppDir.ID+"/message?directory="+appDir, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("read app default dir session message over tunnel = %d: %s", w.Code, w.Body.String())
	}

	// Unpaired request is rejected with 403.
	w = httptest.NewRecorder()
	g.ServeHTTP(w, localRequest(http.MethodGet, "/api/hoxi/work/app-sessions/shopify", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unpaired = %d, want 403", w.Code)
	}
}
