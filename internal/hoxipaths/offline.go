package hoxipaths

import (
	"encoding/json"
	"os"
	"strings"
)

const telemetrySettingsFile = "telemetry.json"

// AuxiliaryOfflineEnv reports whether HOXI_OFFLINE forces silent offline mode
// for auxiliary Hoxi cloud requests (telemetry, update checks, leaderboard/price
// sync, skills marketplace catalog, and background account verification).
func AuxiliaryOfflineEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HOXI_OFFLINE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// AuxiliaryNetworkDisabled reports whether auxiliary background requests to the
// Hoxi server are disabled via HOXI_OFFLINE or ~/.hoxi/telemetry.json.
// Direct user-configured AI provider calls and explicit sign-in actions are unaffected.
func AuxiliaryNetworkDisabled() bool {
	if AuxiliaryOfflineEnv() {
		return true
	}
	path := Join(telemetrySettingsFile)
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var stored struct {
		Offline *bool `json:"offline"`
	}
	if json.Unmarshal(data, &stored) != nil || stored.Offline == nil {
		return false
	}
	return *stored.Offline
}
