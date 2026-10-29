package computer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDesktop struct {
	mu         sync.Mutex
	app        App
	resized    bool
	inputs     int
	lastActions []Action
	entered    chan struct{}
	wait       bool
	secure     bool
}

func (f *fakeDesktop) Permissions(context.Context, string) (Permissions, error) {
	return Permissions{Supported: true, Accessibility: true, ScreenRecording: true}, nil
}
func (f *fakeDesktop) Apps(context.Context) ([]App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []App{f.app}, nil
}
func (f *fakeDesktop) Inspect(_ context.Context, app App, window string, image bool) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	width := float64(800)
	if f.resized {
		width = 600
	}
	return Snapshot{
		WindowID: "one",
		Windows:  []Window{{ID: "one", Bounds: Rect{X: 100, Y: 50, Width: width, Height: 600}}},
		Elements: []Element{
			{ID: 1, Role: "AXTextField", Name: "private", Value: "password", Bounds: Rect{X: 140, Y: 90, Width: 200, Height: 30}, Secure: f.secure},
			{ID: 2, Role: "AXButton", Name: "Submit", Bounds: Rect{X: 200, Y: 150, Width: 100, Height: 40}},
		},
	}, nil
}
func (f *fakeDesktop) Input(ctx context.Context, _ App, _ Window, a Action) error {
	f.mu.Lock()
	f.inputs++
	f.lastActions = append(f.lastActions, a)
	wait := f.wait
	entered := f.entered
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func desktopFixture(t *testing.T) (*Service, *fakeDesktop, Scope) {
	t.Helper()
	fake := &fakeDesktop{app: App{ID: "target", PID: 1234, Name: "Fixture", BundleID: "test.fixture", Started: "one", Path: "/fixture"}}
	service := newService(fake)
	scope := Scope{SessionID: "ses_one", Directory: "/repo"}
	if _, err := service.Authorize(context.Background(), scope, "target"); err != nil {
		t.Fatal(err)
	}
	return service, fake, scope
}
func observeAction(t *testing.T, s *Service, scope Scope) Action {
	t.Helper()
	state, err := s.Observe(context.Background(), scope, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return Action{Action: "click", ObservationID: state.ObservationID, WindowID: "one", X: 10, Y: 20}
}
func TestDesktopGrantOwnershipAndReplay(t *testing.T) {
	s, f, scope := desktopFixture(t)
	action := observeAction(t, s, scope)
	if _, err := s.Authorize(context.Background(), Scope{SessionID: "ses_other", Directory: "/repo"}, "target"); err == nil {
		t.Fatal("app shared by concurrent sessions")
	}
	if err := s.Execute(context.Background(), Scope{SessionID: "ses_other", Directory: "/repo"}, action); err == nil {
		t.Fatal("cross-session action accepted")
	}
	if _, err := s.Observe(context.Background(), scope, "", false); err != nil {
		t.Fatal(err)
	} // UI polling must not invalidate the model's observation.
	if err := s.Execute(context.Background(), scope, action); err != nil {
		t.Fatal(err)
	}
	if err := s.Execute(context.Background(), scope, action); err == nil {
		t.Fatal("input replay accepted")
	}
	if f.inputs != 1 {
		t.Fatal("unexpected input count", f.inputs)
	}
}
func TestDesktopRevocationPauseResizeAndProcessReuse(t *testing.T) {
	for _, change := range []string{"revoke", "pause", "resize", "process", "path", "expired"} {
		t.Run(change, func(t *testing.T) {
			s, f, scope := desktopFixture(t)
			action := observeAction(t, s, scope)
			switch change {
			case "revoke":
				s.Revoke(scope)
			case "pause":
				s.Pause(scope, true)
			case "resize":
				f.resized = true
			case "process":
				f.app.Started = "two"
			case "path":
				f.app.Path = "/different"
			case "expired":
				s.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
			}
			if err := s.Execute(context.Background(), scope, action); err == nil {
				t.Fatal("stale input accepted")
			}
			if f.inputs != 0 {
				t.Fatal("unsafe native input executed")
			}
		})
	}
}
func TestDesktopSecureValuesAndBounds(t *testing.T) {
	s, f, scope := desktopFixture(t)
	f.secure = true
	snapshot, err := s.Observe(context.Background(), scope, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Elements[0].Value != "" || snapshot.Elements[0].Name != "" {
		t.Fatal("password accessibility value leaked")
	}
	action := observeAction(t, s, scope)
	action.X = 800
	if s.Execute(context.Background(), scope, action) == nil {
		t.Fatal("off-window coordinates accepted")
	}
	if f.inputs != 0 {
		t.Fatal("out-of-bounds input dispatched")
	}
	for _, app := range []App{{Name: "Hoxi"}, {BundleID: "ai.hoxi.desktop"}, {BundleID: "com.apple.systempreferences"}, {Path: `C:\\Windows\\System32\\consent.exe`, BundleID: "com.apple.securityagent"}} {
		if !BlockedApp(app) {
			t.Fatal("protected app allowed", app)
		}
	}
}
func TestDesktopEmergencyStopCancelsInFlightInput(t *testing.T) {
	s, f, scope := desktopFixture(t)
	f.wait = true
	f.entered = make(chan struct{})
	action := observeAction(t, s, scope)
	done := make(chan error, 1)
	go func() { done <- s.Execute(context.Background(), scope, action) }()
	<-f.entered
	s.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("emergency stop did not cancel input")
	}
	if s.Current(scope) != nil {
		t.Fatal("stop kept app grants")
	}
}

type delayedDesktop struct {
	*fakeDesktop
	permission func()
	inspection func()
}

func (f *delayedDesktop) Permissions(ctx context.Context, prompt string) (Permissions, error) {
	if f.permission != nil {
		f.permission()
	}
	return f.fakeDesktop.Permissions(ctx, prompt)
}
func (f *delayedDesktop) Inspect(ctx context.Context, app App, window string, image bool) (Snapshot, error) {
	if f.inspection != nil {
		f.inspection()
	}
	return f.fakeDesktop.Inspect(ctx, app, window, image)
}

func TestDesktopStopDuringConfirmationCannotRegrantApp(t *testing.T) {
	s, f, scope := desktopFixture(t)
	version := s.AuthorizationVersion()
	s.backend = &delayedDesktop{fakeDesktop: f, permission: func() { s.Stop() }}
	if _, err := s.AuthorizeConfirmed(context.Background(), scope, "target", version); err == nil {
		t.Fatal("confirmation restored app access after emergency stop")
	}
	if s.Current(scope) != nil {
		t.Fatal("stopped grant restored")
	}
}

func TestDesktopPauseAndResumeDiscardInFlightObservation(t *testing.T) {
	s, f, scope := desktopFixture(t)
	s.backend = &delayedDesktop{fakeDesktop: f, inspection: func() { s.Pause(scope, true); s.Pause(scope, false) }}
	if _, err := s.Observe(context.Background(), scope, "", true); err == nil {
		t.Fatal("old capture survived a pause and resume")
	}
	if len(s.grants[scope].observations) != 0 {
		t.Fatal("stale observation stored")
	}
}
func TestDesktopInputValidationAndGlobalShortcuts(t *testing.T) {
	for _, key := range []string{"Meta+Tab", "Meta+Space", "Meta+Alt+Escape", "Alt+Tab", "Alt+F4", "Meta+F3", "Meta+F11", "Control+Alt+Delete", "Control+Escape", "Control+Control+a", "Bogus+a"} {
		if _, err := ParseKey(key); err == nil {
			t.Fatal("unsafe shortcut accepted", key)
		}
	}
	for _, key := range []string{"Enter", "Control+a", "Meta+c", "Shift+ArrowLeft", "Alt+ArrowRight", "F1", "F5", "F12", "Shift+F10"} {
		if _, err := ParseKey(key); err != nil {
			t.Fatal(key, err)
		}
	}
	if ValidateAction(Action{Action: "type", ObservationID: strings.Repeat("a", 32), WindowID: "one", Text: strings.Repeat("a", 8193)}) == nil {
		t.Fatal("unbounded text accepted")
	}
	if ValidateAction(Action{Action: "execute", ObservationID: strings.Repeat("a", 32), WindowID: "one"}) == nil {
		t.Fatal("arbitrary desktop operation accepted")
	}
	if err := ValidateAction(Action{Action: "move", ObservationID: strings.Repeat("a", 32), WindowID: "one", X: 25, Y: 35}); err != nil {
		t.Fatal("move action rejected", err)
	}
}

func TestDesktopElementTargetingWindowBoundsAndLastAction(t *testing.T) {
	s, f, scope := desktopFixture(t)
	snapshot, err := s.Observe(context.Background(), scope, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Elements[1].WindowBounds == nil || snapshot.Elements[1].WindowBounds.X != 100 || snapshot.Elements[1].WindowBounds.Y != 100 {
		t.Fatalf("unexpected windowBounds: %+v", snapshot.Elements[1].WindowBounds)
	}
	// Click element 2 by elementID (center screen = (250, 170), window-relative = (150, 120))
	err = s.Execute(context.Background(), scope, Action{
		Action:        "click",
		ObservationID: snapshot.ObservationID,
		WindowID:      "one",
		ElementID:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.lastActions) != 1 || f.lastActions[0].X != 150 || f.lastActions[0].Y != 120 {
		t.Fatalf("expected window-relative center (150, 120), got %+v", f.lastActions)
	}
	grant := s.Current(scope)
	if grant == nil || grant.LastAction != "click" || grant.LastActionAt == 0 {
		t.Fatalf("expected LastAction=click on grant, got %+v", grant)
	}

	// Type into element 1 by elementID: should click first to focus, then type
	snapshot2, err := s.Observe(context.Background(), scope, "", false)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Execute(context.Background(), scope, Action{
		Action:        "type",
		ObservationID: snapshot2.ObservationID,
		WindowID:      "one",
		ElementID:     1,
		Text:          "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.lastActions) != 3 || f.lastActions[1].Action != "click" || f.lastActions[2].Action != "type" {
		t.Fatalf("expected click+type sequence, got %+v", f.lastActions)
	}

	// Secure element should reject elementID targeting
	f.secure = true
	snapshot3, err := s.Observe(context.Background(), scope, "", false)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Execute(context.Background(), scope, Action{
		Action:        "click",
		ObservationID: snapshot3.ObservationID,
		WindowID:      "one",
		ElementID:     1,
	})
	if err == nil || err.Error() != "secure_field_requires_manual_input" {
		t.Fatalf("expected secure_field_requires_manual_input, got %v", err)
	}
}
