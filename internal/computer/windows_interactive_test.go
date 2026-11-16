//go:build windows

package computer

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// This test creates its own window in an unlocked interactive desktop. It is
// opt-in because hosted CI runners may not have a visible Windows desktop.
func TestWindowsInteractiveWindowInspection(t *testing.T) {
	if os.Getenv("HOXI_WINDOWS_INTERACTIVE_TEST") != "1" {
		t.Skip("requires an unlocked interactive Windows desktop")
	}
	const script = `Add-Type -AssemblyName System.Windows.Forms
$form = New-Object System.Windows.Forms.Form
$form.Text = 'Hoxi Windows inspection test'
$form.Width = 500; $form.Height = 300
$button = New-Object System.Windows.Forms.Button
$button.Text = 'Hoxi test button'; $button.Width = 180; $button.Height = 40
$form.Controls.Add($button)
[void]$form.ShowDialog()`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	var target App
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		apps, err := (nativeBackend{}).Apps(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range apps {
			if app.PID == cmd.Process.Pid {
				target = app
				break
			}
		}
		if target.PID != 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if target.PID == 0 {
		t.Fatal("test window did not appear on the interactive desktop")
	}
	snapshot, err := (nativeBackend{}).Inspect(context.Background(), target, "", true)
	if err != nil {
		t.Fatalf("inspect and capture test window: %v", err)
	}
	if len(snapshot.Windows) == 0 || len(snapshot.Elements) == 0 || snapshot.Image == "" || snapshot.Width < 1 || snapshot.Height < 1 {
		t.Fatalf("incomplete interactive snapshot: windows=%d elements=%d image=%t size=%dx%d", len(snapshot.Windows), len(snapshot.Elements), snapshot.Image != "", snapshot.Width, snapshot.Height)
	}
}
