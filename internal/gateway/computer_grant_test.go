package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/computer"
	"github.com/hoxiai/hoxi/internal/runner"
)

type grantDesktop struct {
	app    computer.App
	inputs []computer.Action
}

func (d *grantDesktop) Permissions(context.Context, string) (computer.Permissions, error) {
	return computer.Permissions{Supported: true, Accessibility: true, ScreenRecording: true}, nil
}
func (d *grantDesktop) Apps(context.Context) ([]computer.App, error) {
	return []computer.App{d.app}, nil
}
func (d *grantDesktop) Inspect(_ context.Context, _ computer.App, _ string, image bool) (computer.Snapshot, error) {
	img := ""
	if image {
		img = "fresh-jpeg"
	}
	return computer.Snapshot{
		WindowID: "win-1",
		Windows:  []computer.Window{{ID: "win-1", Title: "Main", Bounds: computer.Rect{X: 10, Y: 10, Width: 500, Height: 400}}},
		Elements: []computer.Element{{ID: 1, Role: "AXTextField", Name: "Search", Bounds: computer.Rect{X: 30, Y: 40, Width: 100, Height: 24}}},
		Image:    img,
		Width:    500,
		Height:   400,
	}, nil
}
func (d *grantDesktop) Input(_ context.Context, _ computer.App, _ computer.Window, a computer.Action) error {
	d.inputs = append(d.inputs, a)
	return nil
}

// An app is granted only after the native dialog accepts it, for the panel and
// the agent alike; headless mode (no dialog) never grants.
func TestComputerGrantRequiresNativeConfirmation(t *testing.T) {
	app := computer.App{ID: "1:1", PID: 1, Name: "Fixture", BundleID: "test.fixture", Path: "/fixture", Started: "1"}
	scope := computer.Scope{SessionID: "ses_one", Directory: "/repo"}
	ctx := context.Background()

	g := &Gateway{computer: computer.NewWithBackend(&grantDesktop{app: app})}
	if _, status, err := g.grantComputerApp(ctx, scope, app, "", true); err == nil || status != 503 || g.computer.Current(scope) != nil {
		t.Fatal("agent granted an app without a native dialog", status, err)
	}

	var seen []ComputerAppConfirmation
	accept := false
	g.SetComputerAppConfirmation(func(_ context.Context, c ComputerAppConfirmation) (bool, error) {
		seen = append(seen, c)
		return accept, nil
	})
	if _, status, err := g.grantComputerApp(ctx, scope, app, "", true); err == nil || status != 403 || g.computer.Current(scope) != nil {
		t.Fatal("declined dialog still granted the app", status, err)
	}
	if len(seen) != 1 || !seen[0].RequestedByAgent {
		t.Fatal("agent request not marked in the dialog", seen)
	}

	// The panel's locale is remembered for later agent dialogs.
	accept = true
	if _, status, err := g.grantComputerApp(ctx, scope, app, "zh-CN", false); err != nil || status != 200 {
		t.Fatal("confirmed panel grant failed", status, err)
	}
	g.computer.Revoke(scope)
	if _, _, err := g.grantComputerApp(ctx, scope, app, "", true); err != nil || g.computer.Current(scope) == nil {
		t.Fatal("confirmed agent grant failed", err)
	}
	if last := seen[len(seen)-1]; !last.RequestedByAgent || last.Locale != "zh-CN" {
		t.Fatal("agent dialog lost the remembered locale", last)
	}

	// A second agent dialog cannot stack on top of one still open.
	g.computerConfirming.Store(true)
	if _, status, err := g.grantComputerApp(ctx, scope, app, "", true); err == nil || status != 409 {
		t.Fatal("stacked agent dialog allowed", status, err)
	}
}

func TestComputerAutoObserveCompoundTypeAndAutoApproveBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gw := New(0, "pass", root, nil, nil)
	defer gw.CloseBrowser()
	app := computer.App{ID: "1:1", PID: 1, Name: "Fixture", BundleID: "test.fixture", Path: "/fixture", Started: "1"}
	backend := &grantDesktop{app: app}
	gw.computer = computer.NewWithBackend(backend)
	gw.SetComputerAgentToken("agent-token")
	gw.SetBrowserUIOrigins("http://127.0.0.1:5420")
	gw.SetComputerAppConfirmation(func(_ context.Context, _ ComputerAppConfirmation) (bool, error) {
		return true, nil
	})

	// Create a managed runner session in root
	recCreate := httptest.NewRecorder()
	reqCreate := localRequest(http.MethodPost, "/api/hoxi/sessions", strings.NewReader(`{"runtime":"claude-code","directory":`+string(mustJSON(t, root))+`}`))
	reqCreate.Header.Set("Content-Type", "application/json")
	gw.ServeHTTP(recCreate, reqCreate)
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session: %d %s", recCreate.Code, recCreate.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode session: %v %s", err, recCreate.Body.String())
	}
	query := "?sessionID=" + created.ID + "&directory=" + root

	// 1. Desktop UI grants app with autoApprove=true
	reqGrant := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:5420/api/hoxi/computer/grant"+query, strings.NewReader(`{"appID":"1:1","locale":"zh-CN","autoApprove":true}`))
	reqGrant.Host = "127.0.0.1:5420"
	reqGrant.Header.Set("Origin", "http://127.0.0.1:5420")
	recGrant := httptest.NewRecorder()
	gw.handleComputer(recGrant, reqGrant)
	if recGrant.Code != http.StatusOK || !strings.Contains(recGrant.Body.String(), `"autoApprove":true`) {
		t.Fatalf("grant with autoApprove failed: %d %s", recGrant.Code, recGrant.Body.String())
	}

	// 2. Agent observes initial state
	agentCall := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:5420/api/hoxi/computer/action"+query, strings.NewReader(body))
		r.Host = "127.0.0.1:5420"
		r.Header.Set("X-Hoxi-Agent", "true")
		r.Header.Set("Authorization", "Bearer agent-token")
		w := httptest.NewRecorder()
		gw.handleComputer(w, r)
		return w
	}
	recObs := agentCall(`{"action":"state"}`)
	if recObs.Code != http.StatusOK {
		t.Fatalf("initial state failed: %d %s", recObs.Code, recObs.Body.String())
	}
	var snap1 computer.Snapshot
	if err := json.Unmarshal(recObs.Body.Bytes(), &snap1); err != nil || snap1.ObservationID == "" {
		t.Fatalf("decode initial snapshot: %v", err)
	}

	// 3. Agent executes compound type+key with screenshot=true under autoApprove, receiving a fresh observationID + image
	recType := agentCall(`{"action":"type","observationID":"` + snap1.ObservationID + `","windowID":"win-1","elementID":1,"text":"query","key":"Enter","screenshot":true}`)
	if recType.Code != http.StatusOK {
		t.Fatalf("auto-approved compound type+key failed: %d %s", recType.Code, recType.Body.String())
	}
	var postSnap struct {
		Completed     bool               `json:"completed"`
		ObservationID string             `json:"observationID"`
		WindowID      string             `json:"windowID"`
		Image         string             `json:"image"`
		Elements      []computer.Element `json:"elements"`
	}
	if err := json.Unmarshal(recType.Body.Bytes(), &postSnap); err != nil {
		t.Fatalf("decode post-input snapshot: %v", err)
	}
	if !postSnap.Completed || postSnap.ObservationID == "" || postSnap.ObservationID == snap1.ObservationID || postSnap.Image != "fresh-jpeg" || len(postSnap.Elements) != 1 {
		t.Fatalf("expected fresh post-input observation with screenshot, got %+v", postSnap)
	}
	if len(backend.inputs) != 3 || backend.inputs[0].Action != "click" || backend.inputs[1].Action != "type" || backend.inputs[2].Action != "key" || backend.inputs[2].Key != "Enter" {
		t.Fatalf("expected click+type+key sequence, got %+v", backend.inputs)
	}

	// 4. Even with autoApprove=true on the grant, switching to ApprovalModeUnattended must reject desktop input
	if _, err := gw.runner.UpdateApprovalPolicy(runner.ApprovalPolicy{Mode: runner.ApprovalModeUnattended}); err != nil {
		t.Fatal(err)
	}
	recUnattended := agentCall(`{"action":"click","observationID":"` + postSnap.ObservationID + `","windowID":"win-1","elementID":1}`)
	if recUnattended.Code != http.StatusForbidden {
		t.Fatalf("unattended mode accepted computer input despite autoApprove: %d %s", recUnattended.Code, recUnattended.Body.String())
	}
	_, _ = gw.runner.UpdateApprovalPolicy(runner.ApprovalPolicy{Mode: runner.ApprovalModeSmart})

	// 5. Pausing resets autoApprove
	reqPause := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:5420/api/hoxi/computer/pause"+query, strings.NewReader(`{"paused":true}`))
	reqPause.Host = "127.0.0.1:5420"
	reqPause.Header.Set("Origin", "http://127.0.0.1:5420")
	recPause := httptest.NewRecorder()
	gw.handleComputer(recPause, reqPause)
	if recPause.Code != http.StatusOK || strings.Contains(recPause.Body.String(), `"autoApprove":true`) {
		t.Fatalf("pause did not clear autoApprove: %d %s", recPause.Code, recPause.Body.String())
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
