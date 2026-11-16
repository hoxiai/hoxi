//go:build windows

package sqlitecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBundledSQLiteWindows(t *testing.T) {
	if os.Getenv("HOXI_SQLITE_SIDECAR_TEST") != "1" {
		t.Skip("requires a verified sqlite3.exe beside the test executable")
	}
	bin := Executable()
	if bin == "sqlite3" {
		t.Fatal("bundled sqlite3.exe was not found")
	}
	db := filepath.Join(t.TempDir(), "test.db")
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, bin, args...).CombinedOutput()
	}
	if out, err := run(db, "CREATE TABLE items(name TEXT); INSERT INTO items VALUES ('Hoxi');"); err != nil {
		t.Fatalf("create database: %s: %v", out, err)
	}
	out, err := run("-readonly", "-cmd", ".timeout 3000", "-json", db, "SELECT name FROM items;")
	if err != nil {
		t.Fatalf("read database: %s: %v", out, err)
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) != 1 || rows[0].Name != "Hoxi" {
		t.Fatalf("unexpected SQLite JSON: %q (%v)", out, err)
	}
	if out, err := run("-readonly", db, "DELETE FROM items;"); err == nil {
		t.Fatalf("read-only database unexpectedly accepted a write: %s", out)
	}
}
