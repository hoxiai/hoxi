package telemetry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func TestTelemetryDefaultsDisabledInSourceBuildsAndEnabledInRelease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_DISABLE_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("HOXI_OFFLINE", "")
	SetDefaultEnabled(false)
	t.Cleanup(func() { SetDefaultEnabled(false) })

	if state := Current(); state.Enabled || state.EnvOverride || state.Offline {
		t.Fatalf("expected source/dev build default disabled, got %+v", state)
	}
	if Enabled() {
		t.Fatal("Enabled must be false by default in source/dev builds")
	}

	SetDefaultEnabled(true)
	if state := Current(); !state.Enabled || state.EnvOverride {
		t.Fatalf("expected release build default enabled, got %+v", state)
	}
	if !Enabled() {
		t.Fatal("Enabled must follow release default when unset")
	}
}

func TestTelemetryOptOutAndOfflinePersistIndependently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_DISABLE_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("HOXI_OFFLINE", "")
	SetDefaultEnabled(true)
	t.Cleanup(func() { SetDefaultEnabled(false) })

	if err := SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if state := Current(); state.Enabled {
		t.Fatalf("expected disabled, got %+v", state)
	}
	info, err := os.Stat(filepath.Join(home, ".hoxi", settingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
	if err := SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !Current().Enabled {
		t.Fatal("expected re-enabled state")
	}

	// Enabling offline mode suppresses Enabled() and AuxiliaryNetworkDisabled()
	// without overwriting the saved telemetry preference.
	if err := SetOffline(true); err != nil {
		t.Fatal(err)
	}
	if !Current().Offline || !hoxipaths.AuxiliaryNetworkDisabled() {
		t.Fatal("expected offline mode active")
	}
	if Enabled() {
		t.Fatal("Enabled() must be false while offline mode is active")
	}
	if err := SetOffline(false); err != nil {
		t.Fatal(err)
	}
	if Current().Offline || hoxipaths.AuxiliaryNetworkDisabled() {
		t.Fatal("expected offline mode cleared")
	}
	if !Enabled() {
		t.Fatal("expected preserved telemetry enabled preference after clearing offline mode")
	}
}

func TestTelemetryAndOfflineEnvOverridesWin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	SetDefaultEnabled(true)
	t.Cleanup(func() { SetDefaultEnabled(false) })

	if err := SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DO_NOT_TRACK", "1")
	state := Current()
	if state.Enabled || !state.EnvOverride {
		t.Fatalf("expected environment opt-out, got %+v", state)
	}
	if err := SetEnabled(true); err != ErrEnvOverride {
		t.Fatalf("expected ErrEnvOverride, got %v", err)
	}

	t.Setenv("HOXI_OFFLINE", "1")
	state = Current()
	if !state.Offline || !state.OfflineEnvOverride || !hoxipaths.AuxiliaryNetworkDisabled() {
		t.Fatalf("expected HOXI_OFFLINE override, got %+v", state)
	}
	if err := SetOffline(false); err != ErrOfflineEnvOverride {
		t.Fatalf("expected ErrOfflineEnvOverride, got %v", err)
	}
}

func TestTelemetryMalformedStateFallsBackToVariantDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_DISABLE_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("HOXI_OFFLINE", "")
	dir := filepath.Join(home, ".hoxi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	SetDefaultEnabled(false)
	if Current().Enabled {
		t.Fatal("malformed state in source/dev build must remain disabled")
	}

	SetDefaultEnabled(true)
	t.Cleanup(func() { SetDefaultEnabled(false) })
	if !Current().Enabled {
		t.Fatal("malformed state in release build must fall back to release default")
	}
}
