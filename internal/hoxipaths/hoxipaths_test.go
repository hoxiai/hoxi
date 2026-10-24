package hoxipaths

import (
	"os"
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

func TestHomeOverrideServerURLAndProtectedStateFiles(t *testing.T) {
	customHome := t.TempDir()
	t.Setenv("HOXI_HOME", customHome)
	if got := Home(); got != customHome {
		t.Fatalf("Home() with HOXI_HOME = %s, want %s", got, customHome)
	}
	if got, want := Join("models.json"), filepath.Join(customHome, "models.json"); got != want {
		t.Fatalf("Join(models.json) = %s, want %s", got, want)
	}

	t.Setenv("HOXI_SERVER_URL", "https://mirror.hoxi.ai/")
	if got := ServerBaseURL(); got != "https://mirror.hoxi.ai" {
		t.Fatalf("ServerBaseURL() = %s, want https://mirror.hoxi.ai", got)
	}

	for _, name := range []string{"models.json", "auth.json", "auto-pools.json", "workspaces.json", "device-id", "telemetry.json", "relay-keys.json", "work-apps.json"} {
		if !IsProtectedStateFile(name) {
			t.Fatalf("expected %s to be protected state file", name)
		}
	}
	if IsProtectedStateFile("notes.txt") {
		t.Fatalf("expected notes.txt not to be protected state file")
	}
}

func TestProjectMemoryPaths(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo", "demo")
	if got, want := ProjectMemoryDir(root), filepath.Join(root, ".hoxi", "memory"); got != want {
		t.Fatalf("ProjectMemoryDir() = %s, want %s", got, want)
	}
	if got, want := LegacyProjectMemoryDir(root), filepath.Join(root, ".opencode", "memory"); got != want {
		t.Fatalf("LegacyProjectMemoryDir() = %s, want %s", got, want)
	}
	if got, want := ProjectMemoryFile(root), filepath.Join(root, ".hoxi", "memory", "project.md"); got != want {
		t.Fatalf("ProjectMemoryFile() = %s, want %s", got, want)
	}
	if got, want := ProjectSessionMemoryFile(root, "ses_1"), filepath.Join(root, ".hoxi", "memory", "sessions", "ses_1.md"); got != want {
		t.Fatalf("ProjectSessionMemoryFile() = %s, want %s", got, want)
	}
	if got, want := LegacyProjectSessionMemoryFile(root, "ses_1"), filepath.Join(root, ".opencode", "memory", "sessions", "ses_1.md"); got != want {
		t.Fatalf("LegacyProjectSessionMemoryFile() = %s, want %s", got, want)
	}
	if ProjectMemoryDir("  ") != "" {
		t.Fatal("blank project root must yield no memory dir")
	}
	if dirs := ProjectMemoryDirs(root); len(dirs) != 2 || dirs[0] != ProjectMemoryDir(root) || dirs[1] != LegacyProjectMemoryDir(root) {
		t.Fatalf("ProjectMemoryDirs() = %v", dirs)
	}
	if ProjectMemoryRelPath() != filepath.Join(".hoxi", "memory") {
		t.Fatalf("ProjectMemoryRelPath() = %s", ProjectMemoryRelPath())
	}
}

func TestProjectRootForWorktree(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo", "demo")
	cases := map[string]string{
		filepath.Join(root, ".hoxi", "worktrees", "ses_1"):       root,
		filepath.Join(root, ".hoxi", "worktrees", "ses_1", "in"): "",
		filepath.Join(root, ".hoxi", "worktrees"):                "",
		root: "",
		"":   "",
		filepath.Join(root, ".opencode", "memory"): "",
	}
	for dir, want := range cases {
		if got := ProjectRootForWorktree(dir); got != want {
			t.Fatalf("ProjectRootForWorktree(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestDefaultWorkspaceDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	want := filepath.Join(home, "workspaces", "default")
	if got := DefaultWorkspaceDir(); got != want {
		t.Fatalf("DefaultWorkspaceDir() = %q, want %q", got, want)
	}
	if got := EnsureDefaultWorkspaceDir(); got != want {
		t.Fatalf("EnsureDefaultWorkspaceDir() = %q, want %q", got, want)
	}
	for _, entry := range []string{"docs", "exports", "campaigns", "reports", "README.md"} {
		if _, err := os.Stat(filepath.Join(want, entry)); err != nil {
			t.Fatalf("expected default workspace entry %s to exist: %v", entry, err)
		}
	}
}
