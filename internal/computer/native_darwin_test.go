//go:build darwin && cgo

package computer

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/fixture_darwin.m
var desktopFixtureSource string

// Opt in because this test creates and controls its own foreground test app.
// It never requests OS permissions and never targets an existing user app.
func TestNativeComputerFixture(t *testing.T) {
	if os.Getenv("HOXI_COMPUTER_NATIVE_TEST") != "1" {
		t.Skip("set HOXI_COMPUTER_NATIVE_TEST=1 to control the test fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend := nativeBackend{}
	permission, err := backend.Permissions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !permission.Accessibility || !permission.ScreenRecording {
		t.Skip("system permissions not granted; never prompt from tests")
	}
	root := t.TempDir()
	contents := filepath.Join(root, "Computer Fixture.app", "Contents")
	bin := filepath.Join(contents, "MacOS", "ComputerFixture")
	if err = os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.hoxi.computer.fixture</string><key>CFBundleExecutable</key><string>ComputerFixture</string><key>CFBundleName</key><string>Computer Fixture</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "fixture.m")
	if err = os.WriteFile(source, []byte(desktopFixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(ctx, "clang", "-fobjc-arc", "-framework", "Cocoa", source, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("fixture compile: %v: %s", err, output)
	}
	statePath := filepath.Join(root, "state.json")
	command := exec.CommandContext(ctx, bin, statePath)
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	service := newService(backend)
	defer service.Stop()
	var target App
	for i := 0; i < 80; i++ {
		apps, err := backend.Apps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range apps {
			if app.PID == command.Process.Pid {
				target = app
			}
		}
		if target.ID != "" {
			if _, err := os.Stat(statePath); err == nil {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if target.PID != command.Process.Pid {
		t.Fatal("owned fixture app not discovered")
	}
	scope := Scope{SessionID: "ses_native_fixture", Directory: root}
	if _, err := service.Authorize(ctx, scope, target.ID); err != nil {
		t.Fatal(err)
	}
	observe := func() Snapshot {
		t.Helper()
		snapshot, err := service.Observe(ctx, scope, "", true)
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.StdEncoding.DecodeString(snapshot.Image)
		if err != nil {
			t.Fatal(err)
		}
		image, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || image.Width != snapshot.Width || image.Height != snapshot.Height {
			t.Fatal("native capture is not the declared JPEG", err)
		}
		return snapshot
	}
	run := func(snapshot Snapshot, action Action) {
		t.Helper()
		action.ObservationID, action.WindowID = snapshot.ObservationID, snapshot.WindowID
		if err := service.Execute(ctx, scope, action); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	clickElement := func(snapshot Snapshot, role, name string) {
		t.Helper()
		var element *Element
		for i := range snapshot.Elements {
			row := &snapshot.Elements[i]
			if row.Role == role && (name == "" || strings.Contains(row.Name, name)) {
				element = row
				break
			}
		}
		if element == nil {
			t.Fatal("fixture element not found", role)
		}
		bounds := snapshot.Windows[0].Bounds
		run(snapshot, Action{Action: "click", X: element.Bounds.X + element.Bounds.Width/2 - bounds.X, Y: element.Bounds.Y + element.Bounds.Height/2 - bounds.Y})
	}
	clickElement(observe(), "AXButton", "Fixture click")
	clickElement(observe(), "AXTextField", "")
	run(observe(), Action{Action: "key", Key: "Meta+a"})
	text := "Hoxi fixture \u4e2d\u6587"
	run(observe(), Action{Action: "type", Text: text})
	var state struct {
		Clicks int    `json:"clicks"`
		Text   string `json:"text"`
	}
	for i := 0; i < 30; i++ {
		raw, err := os.ReadFile(statePath)
		if err == nil {
			_ = json.Unmarshal(raw, &state)
		}
		if state.Clicks == 1 && state.Text == text {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state.Clicks != 1 || state.Text != text {
		t.Fatalf("fixture input did not complete: clicks=%d text=%q", state.Clicks, state.Text)
	}
	t.Log("owned fixture: single-window JPEG, accessibility tree, click, Command+A and Unicode typing verified")
}
