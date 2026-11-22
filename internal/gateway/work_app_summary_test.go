package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type summaryExtension struct {
	Extension
	items  []WorkAppRegistryEntry
	err    error
	scopes map[string]WorkPackageSessionScope
	sErr   error
}

func (s summaryExtension) Close()               {}
func (s summaryExtension) ForgetSession(string) {}
func (s summaryExtension) ListWorkAppRegistryEntries() ([]WorkAppRegistryEntry, error) {
	return s.items, s.err
}
func (s summaryExtension) WorkPackageSessionScope(appID string) (WorkPackageSessionScope, error) {
	if s.sErr != nil {
		return WorkPackageSessionScope{}, s.sErr
	}
	return s.scopes[appID], nil
}

type richSummaryExtension struct {
	summaryExtension
	details   []WorkAppCatalogDetail
	prepDirs  map[string]string
	prepErr   error
	bindErr   error
	boundApps map[string]string
}

func (r *richSummaryExtension) ListWorkAppCatalogDetails() ([]WorkAppCatalogDetail, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.details, nil
}

func (r *richSummaryExtension) WorkPackageRemoteDetail(appID string) (WorkAppCatalogDetail, bool, error) {
	if r.sErr != nil {
		return WorkAppCatalogDetail{}, false, r.sErr
	}
	for _, d := range r.details {
		if d.ID == appID {
			return d, true, nil
		}
	}
	return WorkAppCatalogDetail{}, false, nil
}

func (r *richSummaryExtension) PrepareWorkPackageSession(appID string) (string, bool, error) {
	if r.prepErr != nil {
		return "", true, r.prepErr
	}
	dir, ok := r.prepDirs[appID]
	if !ok {
		return "", false, nil
	}
	return dir, true, nil
}

func (r *richSummaryExtension) BindWorkPackageSession(appID, sessionID string) error {
	if r.bindErr != nil {
		return r.bindErr
	}
	if r.boundApps == nil {
		r.boundApps = map[string]string{}
	}
	r.boundApps[sessionID] = appID
	return nil
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

func TestWorkAppOverviewAndCreateSessionRemoteContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	root := t.TempDir()
	appDir := filepath.Join(home, ".hoxi", "workspaces", "default", "demo")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tracker := statusbar.NewTracker()
	g := New(0, "test", root, nil, tracker)
	g.SetGatewayToken("desktop")
	defer g.CloseBrowser()

	// 501 without extension.
	w := httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, workAppOverviewPath, nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("overview without ext = %d, want 501", w.Code)
	}
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(`{}`))))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("create without ext = %d, want 501", w.Code)
	}

	// Create one existing session in appDir and mark a pending approval on it.
	existing, code, err := g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "Existing Demo Task",
		Directory:   appDir,
		Runtime:     string(runner.RuntimeNative),
		SandboxMode: "local",
	}, appDir)
	if err != nil || code != http.StatusOK {
		t.Fatalf("create existing: %d %v", code, err)
	}
	tracker.SetPendingApproval("per_demo_1", existing.ID, true)

	ext := &richSummaryExtension{
		summaryExtension: summaryExtension{
			items: []WorkAppRegistryEntry{{ID: "demo", Name: "Demo App", Status: "installed"}},
			scopes: map[string]WorkPackageSessionScope{
				"demo": {Installed: true, Directories: []string{appDir}},
			},
		},
		details: []WorkAppCatalogDetail{
			{
				ID:          "demo",
				Name:        "Demo App",
				Status:      "installed",
				Description: "Sanitized app summary",
				Launchpad: []WorkAppLaunchpadEntry{
					{ID: "lp_1", Label: "Quick Check", Prompt: "Run quick check", Skill: "demo-ops"},
				},
				HasAccountBinding:  true,
				ActiveAccountLabel: "US Store",
			},
		},
		prepDirs: map[string]string{"demo": appDir},
	}
	g.extensions = []Extension{ext}

	// 1. Overview includes description + session/pending counts.
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, workAppOverviewPath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("overview = %d: %s", w.Code, w.Body.String())
	}
	var overviewResp struct {
		Items []WorkAppOverviewEntry `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overviewResp); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	if len(overviewResp.Items) != 1 {
		t.Fatalf("expected 1 overview item, got %+v", overviewResp.Items)
	}
	got := overviewResp.Items[0]
	if got.ID != "demo" || got.Description != "Sanitized app summary" || got.SessionCount != 1 || got.PendingApprovalCount != 1 {
		t.Fatalf("unexpected overview item: %+v", got)
	}

	// 2. GET /api/hoxi/work/app-sessions/demo includes app launchpad + pendingApprovals.
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodGet, "/api/hoxi/work/app-sessions/demo", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get app sessions = %d: %s", w.Code, w.Body.String())
	}
	var sessResp struct {
		Items            []runner.SessionInfo  `json:"items"`
		PendingApprovals map[string]int        `json:"pendingApprovals"`
		App              workAppRemoteMetadata `json:"app"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sessResp); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if sessResp.PendingApprovals[existing.ID] != 1 {
		t.Fatalf("expected pending approval count 1, got %+v", sessResp.PendingApprovals)
	}
	if !sessResp.App.CanCreateSession || len(sessResp.App.Launchpad) != 1 || sessResp.App.Launchpad[0].ID != "lp_1" || !sessResp.App.HasAccountBinding || sessResp.App.ActiveAccountLabel != "US Store" {
		t.Fatalf("unexpected app metadata: %+v", sessResp.App)
	}

	// 3. POST rejects unknown fields (such as directory / storeId / sandboxMode) and query strings.
	for _, badBody := range []string{
		`{"directory":"/tmp"}`,
		`{"storeId":"store_1"}`,
		`{"sandboxMode":"unattended"}`,
		`{not-json`,
	} {
		w = httptest.NewRecorder()
		g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(badBody))))
		if w.Code != http.StatusBadRequest && w.Code != http.StatusForbidden {
			t.Fatalf("POST badBody %s = %d, want 400/403", badBody, w.Code)
		}
	}
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo?storeId=x", bytes.NewReader([]byte(`{}`))))
	if w.Code != http.StatusForbidden {
		t.Fatalf("POST with query = %d, want 403", w.Code)
	}

	// 4. POST returns 404 for uninstalled app.
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/missing", bytes.NewReader([]byte(`{}`))))
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST missing app = %d, want 404", w.Code)
	}

	// 5. POST creates app-bound session with idempotent replay and conflict protection.
	createPayload := `{"title":"Mobile Demo Task","clientRequestId":"req_demo_1"}`
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(createPayload))))
	if w.Code != http.StatusOK {
		t.Fatalf("POST create = %d: %s", w.Code, w.Body.String())
	}
	var created runner.SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.ID == "" || created.Directory != appDir || created.Title != "Mobile Demo Task" {
		t.Fatalf("unexpected created session: %+v", created)
	}
	if ext.boundApps[created.ID] != "demo" {
		t.Fatalf("expected session %s to be bound to demo, got %+v", created.ID, ext.boundApps)
	}
	// Replay with identical clientRequestId returns the same session without creating a duplicate.
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(createPayload))))
	if w.Code != http.StatusOK {
		t.Fatalf("POST replay = %d: %s", w.Code, w.Body.String())
	}
	var replayed runner.SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &replayed); err != nil || replayed.ID != created.ID {
		t.Fatalf("expected replay session %s, got %+v (%v)", created.ID, replayed, err)
	}
	// Reusing clientRequestId with different payload returns 409.
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(`{"title":"Other","clientRequestId":"req_demo_1"}`))))
	if w.Code != http.StatusConflict {
		t.Fatalf("POST conflicting clientRequestId = %d, want 409", w.Code)
	}

	// Concurrent duplicate POSTs with the same clientRequestId must create exactly one session.
	var wg sync.WaitGroup
	concIDs := make([]string, 4)
	for i := range concIDs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			g.serveTunnelRequest(rec, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(`{"title":"Concurrent Task","clientRequestId":"req_conc_1"}`))))
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent POST %d = %d: %s", idx, rec.Code, rec.Body.String())
				return
			}
			var info runner.SessionInfo
			if err := json.Unmarshal(rec.Body.Bytes(), &info); err == nil {
				concIDs[idx] = info.ID
			}
		}(i)
	}
	wg.Wait()
	if concIDs[0] == "" {
		t.Fatalf("concurrent POST produced empty session ID")
	}
	for i := 1; i < len(concIDs); i++ {
		if concIDs[i] != concIDs[0] {
			t.Fatalf("concurrent duplicate POSTs created distinct sessions: %+v", concIDs)
		}
	}

	// 6. Binding failure rolls back the newly created session and returns 503.
	ext.bindErr = errors.New("disk full")
	beforeItems, _ := g.runner.ListSessionsByScope(nil, []string{appDir})
	beforeCount := len(beforeItems)
	w = httptest.NewRecorder()
	g.serveTunnelRequest(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(`{"title":"Should Rollback"}`))))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST bind failure = %d, want 503", w.Code)
	}
	afterItems, _ := g.runner.ListSessionsByScope(nil, []string{appDir})
	afterCount := len(afterItems)
	if afterCount != beforeCount {
		t.Fatalf("expected failed bind to rollback session: before=%d after=%d", beforeCount, afterCount)
	}

	// 7. Unpaired requests are rejected with 403.
	w = httptest.NewRecorder()
	g.ServeHTTP(w, localRequest(http.MethodGet, workAppOverviewPath, nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unpaired overview = %d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, localRequest(http.MethodPost, "/api/hoxi/work/app-sessions/demo", bytes.NewReader([]byte(`{}`))))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unpaired create = %d, want 403", w.Code)
	}
}
