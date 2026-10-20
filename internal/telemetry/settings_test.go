package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryDefaultsEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_DISABLE_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	if state := Current(); !state.Enabled || state.EnvOverride {
		t.Fatalf("expected enabled default, got %+v", state)
	}
	if !Enabled() {
		t.Fatal("Enabled must follow Current")
	}
}

func TestTelemetryOptOutPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
	if err := SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !Current().Enabled {
		t.Fatal("expected re-enabled state")
	}
}

func TestTelemetryEnvOverrideWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
}

func TestTelemetryMalformedStateFailsOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".hoxi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Current().Enabled {
		t.Fatal("malformed state must not disable telemetry")
	}
}
