package hoxipaths

import (
	"path/filepath"
	"testing"
)

func TestDBPathPriority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", "")
	if got, want := DBPath(), filepath.Join(home, ".hoxi", "hoxi.db"); got != want {
		t.Fatalf("default DBPath() = %s, want %s", got, want)
	}
	t.Setenv("HOXI_DB_PATH", filepath.Join(home, "custom", "..", "hoxi-custom.db"))
	if got, want := DBPath(), filepath.Join(home, "hoxi-custom.db"); got != want {
		t.Fatalf("HOXI_DB_PATH DBPath() = %s, want %s", got, want)
	}
	t.Setenv("OPENCODE_DB", filepath.Join(home, "engine.db"))
	if got, want := DBPath(), filepath.Join(home, "engine.db"); got != want {
		t.Fatalf("OPENCODE_DB must win, got %s want %s", got, want)
	}
	if files := DBFiles(); len(files) != 4 || files[1] != filepath.Join(home, "engine.db-wal") {
		t.Fatalf("unexpected DBFiles() = %v", files)
	}
}

func TestEngineDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_ENGINE_DATA_DIR", "")
	if got, want := EngineDataDir(), filepath.Join(home, ".hoxi", "engine-data"); got != want {
		t.Fatalf("EngineDataDir() = %s, want %s", got, want)
	}
	if got, want := EngineStateDir(), filepath.Join(home, ".hoxi", "engine-state"); got != want {
		t.Fatalf("EngineStateDir() = %s, want %s", got, want)
	}
	custom := filepath.Join(home, "engine")
	t.Setenv("HOXI_ENGINE_DATA_DIR", custom)
	if got := EngineDataDir(); got != custom {
		t.Fatalf("EngineDataDir() = %s, want %s", got, custom)
	}
	if got, want := EngineStateDir(), filepath.Join(custom, "state"); got != want {
		t.Fatalf("EngineStateDir() = %s, want %s", got, want)
	}
	if got, want := EngineAuthPath(), filepath.Join(custom, "opencode", "auth.json"); got != want {
		t.Fatalf("EngineAuthPath() = %s, want %s", got, want)
	}
}
