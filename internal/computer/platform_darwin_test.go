//go:build darwin && cgo

package computer

import (
	"context"
	"testing"
)

// Probe the actual native bridge without requesting system permissions or
// reading any window contents. GUI input tests need an explicitly granted app.
func TestNativeComputerBridgeProbe(t *testing.T) {
	backend := nativeBackend{}
	permission, err := backend.Permissions(context.Background(), "")
	if err != nil || !permission.Supported || permission.Platform != "darwin" || !permission.ForegroundOnly {
		t.Fatalf("native permission probe failed: %v", err)
	}
	t.Logf("native permissions: accessibility=%v screenRecording=%v", permission.Accessibility, permission.ScreenRecording)
	apps, err := backend.Apps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.PID < 1 || app.ID == "" || app.Started == "" || app.Path == "" {
			t.Fatal("native app identity is incomplete")
		}
	}
}
