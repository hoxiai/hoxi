// Package telemetry reports anonymous install and launch events to hoxi.ai.
// This file persists the local opt-out and never blocks startup when it fails.
package telemetry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const settingsFile = "telemetry.json"

// ErrEnvOverride is returned when the environment disables telemetry and the
// stored preference cannot take effect.
var ErrEnvOverride = errors.New("telemetry disabled by environment")

// State is the effective preference plus whether an environment override wins.
type State struct {
	Enabled     bool `json:"enabled"`
	EnvOverride bool `json:"envOverride"`
}

var settingsMu sync.Mutex

func envDisabled() bool {
	for _, name := range []string{"HOXI_DISABLE_TELEMETRY", "DO_NOT_TRACK"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func settingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", settingsFile)
}

// Enabled reports whether a launch event may be sent. Missing, unreadable or
// malformed state keeps the current default (enabled) so reporting is never
// silently lost, while an explicit opt-out is always respected.
func Enabled() bool {
	return Current().Enabled
}

// Current returns the effective telemetry preference.
func Current() State {
	if envDisabled() {
		return State{EnvOverride: true}
	}
	path := settingsPath()
	if path == "" {
		return State{Enabled: true}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return State{Enabled: true}
	}
	var stored struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(data, &stored) != nil || stored.Enabled == nil {
		return State{Enabled: true}
	}
	return State{Enabled: *stored.Enabled}
}

// SetEnabled persists an explicit opt-in or opt-out.
func SetEnabled(enabled bool) error {
	if envDisabled() {
		return ErrEnvOverride
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	path := settingsPath()
	if path == "" {
		return errors.New("telemetry settings path unavailable")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".telemetry-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(State{Enabled: enabled}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
