package gateway

import (
	"context"
	"testing"

	"github.com/hoxiai/hoxi/internal/computer"
)

type grantDesktop struct{ app computer.App }

func (d grantDesktop) Permissions(context.Context, string) (computer.Permissions, error) {
	return computer.Permissions{Supported: true, Accessibility: true, ScreenRecording: true}, nil
}
func (d grantDesktop) Apps(context.Context) ([]computer.App, error) {
	return []computer.App{d.app}, nil
}
func (d grantDesktop) Inspect(context.Context, computer.App, string, bool) (computer.Snapshot, error) {
	return computer.Snapshot{}, nil
}
func (d grantDesktop) Input(context.Context, computer.App, computer.Window, computer.Action) error {
	return nil
}

// An app is granted only after the native dialog accepts it, for the panel and
// the agent alike; headless mode (no dialog) never grants.
func TestComputerGrantRequiresNativeConfirmation(t *testing.T) {
	app := computer.App{ID: "1:1", PID: 1, Name: "Fixture", BundleID: "test.fixture", Path: "/fixture", Started: "1"}
	scope := computer.Scope{SessionID: "ses_one", Directory: "/repo"}
	ctx := context.Background()

	g := &Gateway{computer: computer.NewWithBackend(grantDesktop{app})}
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
