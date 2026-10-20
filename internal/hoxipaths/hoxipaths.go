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

// Home returns ~/.hoxi, or "" when the user home directory is unknown.
func Home() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi")
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
