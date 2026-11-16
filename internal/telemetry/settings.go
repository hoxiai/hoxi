// Package telemetry reports anonymous install and launch events to hoxi.ai,
// and manages the local silent-offline preference for auxiliary cloud requests.
// Source/development builds default telemetry to disabled; official release
// bundles opt in via SetDefaultEnabled(true) unless the user opts out.
package telemetry

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const settingsFile = "telemetry.json"

var (
	// ErrEnvOverride is returned when the environment disables telemetry and the
	// stored preference cannot take effect.
	ErrEnvOverride = errors.New("telemetry disabled by environment")
	// ErrOfflineEnvOverride is returned when HOXI_OFFLINE locks silent offline mode on.
	ErrOfflineEnvOverride = errors.New("offline mode enabled by environment")

	defaultEnabled atomic.Bool
	settingsMu     sync.Mutex
)

// State is the effective preference plus whether environment overrides win.
type State struct {
	Enabled            bool `json:"enabled"`
	EnvOverride        bool `json:"envOverride"`
	Offline            bool `json:"offline"`
	OfflineEnvOverride bool `json:"offlineEnvOverride"`
}

type persistedSettings struct {
	Enabled *bool `json:"enabled,omitempty"`
	Offline *bool `json:"offline,omitempty"`
}

// SetDefaultEnabled configures the fallback telemetry state when no explicit
// preference exists in ~/.hoxi/telemetry.json. Source builds keep this false;
// official packaged release builds set it to true at startup.
func SetDefaultEnabled(enabled bool) {
	defaultEnabled.Store(enabled)
}

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
	return hoxipaths.Join(settingsFile)
}

func readPersistedLocked(path string) persistedSettings {
	if path == "" {
		return persistedSettings{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedSettings{}
	}
	var stored persistedSettings
	if json.Unmarshal(data, &stored) != nil {
		return persistedSettings{}
	}
	return stored
}

// Enabled reports whether a launch event may be sent right now.
func Enabled() bool {
	s := Current()
	return s.Enabled && !s.Offline
}

// Current returns the effective telemetry and silent-offline preferences.
func Current() State {
	telEnv := envDisabled()
	offEnv := hoxipaths.AuxiliaryOfflineEnv()

	var stored persistedSettings
	if path := settingsPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &stored)
		}
	}

	enabled := defaultEnabled.Load()
	if stored.Enabled != nil {
		enabled = *stored.Enabled
	}
	if telEnv {
		enabled = false
	}

	offline := false
	if stored.Offline != nil {
		offline = *stored.Offline
	}
	if offEnv {
		offline = true
	}

	return State{
		Enabled:            enabled,
		EnvOverride:        telEnv,
		Offline:            offline,
		OfflineEnvOverride: offEnv,
	}
}

// SetEnabled persists an explicit telemetry opt-in or opt-out while preserving offline state.
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
	stored := readPersistedLocked(path)
	stored.Enabled = &enabled
	return hoxipaths.WriteJSONAtomic(path, stored, 0o600)
}

// SetOffline persists the silent offline preference while preserving telemetry state.
func SetOffline(offline bool) error {
	if hoxipaths.AuxiliaryOfflineEnv() {
		return ErrOfflineEnvOverride
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	path := settingsPath()
	if path == "" {
		return errors.New("telemetry settings path unavailable")
	}
	stored := readPersistedLocked(path)
	stored.Offline = &offline
	return hoxipaths.WriteJSONAtomic(path, stored, 0o600)
}
