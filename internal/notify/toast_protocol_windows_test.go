//go:build windows

package notify

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsToastProtocolTargetsOnlyThisInstallation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Hoxi with spaces")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "Hoxi.exe")
	other := filepath.Join(t.TempDir(), "Hoxi.exe")
	for _, path := range []string{app, other} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, command string
		want          bool
	}{
		{"installer quoting", `"` + app + `" "%1"`, true},
		{"old unquoted path", app + ` "%1"`, false},
		{"other installation", windows.ComposeCommandLine([]string{other, "%1"}), false},
		{"extra arguments", windows.ComposeCommandLine([]string{app, "%1", "--flag"}), false},
		{"missing URI placeholder", windows.ComposeCommandLine([]string{app, "hoxi://session/ses_test"}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowsToastProtocolTargetsExecutable(tc.command, app); got != tc.want {
				t.Errorf("protocol command %q: got %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}
