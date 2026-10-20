// Package hoxipaths is the single source of truth for Hoxi's per-user state
// locations under ~/.hoxi and the private OpenCode engine directories.
//
// It is a foundation package: it depends only on the standard library and
// must never import a business domain package.
//
// Environment overrides, highest priority first:
//
//	Engine database (hoxi.db): OPENCODE_DB > HOXI_DB_PATH > ~/.hoxi/hoxi.db
//	Engine data dir:           HOXI_ENGINE_DATA_DIR > ~/.hoxi/engine-data
//	Engine state dir:          HOXI_ENGINE_DATA_DIR/state > ~/.hoxi/engine-state
//
// OPENCODE_DB wins because the supervised `opencode serve` child inherits it
// verbatim; Hoxi only injects its own path when the variable is unset.
package hoxipaths

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultServerBaseURL is the canonical Hoxi cloud endpoint when HOXI_SERVER_URL is unset.
	DefaultServerBaseURL = "https://hoxi.ai"
	// DefaultReleaseBridgeAddr is the default loopback gateway address for release builds.
	DefaultReleaseBridgeAddr = "127.0.0.1:5410"
	// DefaultDevBridgeAddr is the default loopback gateway address for dev builds.
	DefaultDevBridgeAddr = "127.0.0.1:5420"
)

var protectedStateFiles = []string{
	"models.json",
	"auto-pools.json",
	"workspaces.json",
	"auth.json",
	"device-id",
	"telemetry.json",
}

// Home returns ~/.hoxi (or $HOXI_HOME when set), or "" when the user home directory is unknown.
func Home() string {
	if custom := env("HOXI_HOME"); custom != "" {
		return filepath.Clean(custom)
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi")
}

// Join returns filepath.Join(Home(), elem...), or "" when Home() is unknown.
func Join(elem ...string) string {
	return join(Home(), elem...)
}

// ServerBaseURL returns the configured Hoxi server base URL without a trailing slash.
func ServerBaseURL() string {
	if custom := strings.TrimRight(env("HOXI_SERVER_URL"), "/"); custom != "" {
		return custom
	}
	return DefaultServerBaseURL
}

// NormalizedPlatform maps runtime.GOOS values to Hoxi API platform names.
func NormalizedPlatform(goos string) string {
	if goos == "darwin" {
		return "mac"
	}
	return goos
}

// NormalizedArch maps runtime.GOARCH values to Hoxi API architecture names.
func NormalizedArch(goarch string) string {
	if goarch == "amd64" {
		return "x64"
	}
	return goarch
}

// ProtectedStateFiles returns the filenames under ~/.hoxi that sandboxed child
// processes and untrusted file readers must not overwrite or exfiltrate.
func ProtectedStateFiles() []string {
	out := make([]string, len(protectedStateFiles))
	copy(out, protectedStateFiles)
	return out
}

// IsProtectedStateFile reports whether baseName is a protected ~/.hoxi state file.
func IsProtectedStateFile(baseName string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseName))
	for _, f := range protectedStateFiles {
		if lower == f {
			return true
		}
	}
	return false
}

// BinDir returns ~/.hoxi/bin, where Hoxi installs its managed binaries.
func BinDir() string {
	return join(Home(), "bin")
}

// DBPath returns the SQLite database the managed OpenCode engine writes to.
func DBPath() string {
	if custom := env("OPENCODE_DB"); custom != "" {
		return filepath.Clean(custom)
	}
	if custom := env("HOXI_DB_PATH"); custom != "" {
		return filepath.Clean(custom)
	}
	return join(Home(), "hoxi.db")
}

// DBFiles returns the database path plus its SQLite sidecar files.
func DBFiles() []string {
	db := DBPath()
	if db == "" {
		return nil
	}
	return []string{db, db + "-wal", db + "-shm", db + "-journal"}
}

// EngineDataDir returns the XDG data home Hoxi hands to the managed engine.
func EngineDataDir() string {
	if custom := env("HOXI_ENGINE_DATA_DIR"); custom != "" {
		return filepath.Clean(custom)
	}
	return join(Home(), "engine-data")
}

// EngineStateDir returns the XDG state home Hoxi hands to the managed engine.
func EngineStateDir() string {
	if custom := env("HOXI_ENGINE_DATA_DIR"); custom != "" {
		return filepath.Join(filepath.Clean(custom), "state")
	}
	return join(Home(), "engine-state")
}

// EngineOpenCodeDir returns the engine's OpenCode data directory, which holds
// the engine-scoped auth.json.
func EngineOpenCodeDir() string {
	return join(EngineDataDir(), "opencode")
}

// EngineAuthPath returns the engine-scoped OpenCode auth.json.
func EngineAuthPath() string {
	return join(EngineOpenCodeDir(), "auth.json")
}

// EngineLegacyDBPath is the engine database from before hoxi.db existed.
func EngineLegacyDBPath() string {
	return join(EngineOpenCodeDir(), "opencode.db")
}

func env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func join(base string, elem ...string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, elem...)...)
}
