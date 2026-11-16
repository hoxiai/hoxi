package sqlitecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBundledSQLiteOrPathFallback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Hoxi.exe")
	if got := resolve("windows", exe); got != "sqlite3" {
		t.Fatalf("missing sidecar: %q", got)
	}
	sidecar := filepath.Join(dir, "sqlite3.exe")
	if err := os.WriteFile(sidecar, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolve("windows", exe); got != sidecar {
		t.Fatalf("bundled sidecar: %q", got)
	}
	if got := resolve("darwin", exe); got != "sqlite3" {
		t.Fatalf("non-Windows fallback: %q", got)
	}
}
