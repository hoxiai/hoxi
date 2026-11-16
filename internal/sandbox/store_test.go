package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerRoundTrip(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(filepath.Join(root, "policy"))
	workspace := t.TempDir()

	snapshot, err := manager.Set("ses_test_123", workspace, true, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Enabled || snapshot.Network != NetworkDeny {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if _, err := os.Stat(snapshot.ProfilePath); err != nil {
		t.Fatalf("profile file missing: %v", err)
	}
	loaded, enabled, err := manager.Get("ses_test_123")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled || loaded.ProfilePath != snapshot.ProfilePath {
		t.Fatalf("unexpected loaded snapshot: %#v", loaded)
	}
	if filepath.Base(loaded.ProfilePath) != hash("ses_test_123")+".sb" {
		t.Fatalf("profile path must be derived from the session hash: %s", loaded.ProfilePath)
	}
	if err := manager.Remove("ses_test_123"); err != nil {
		t.Fatal(err)
	}
	if _, enabled, _ := manager.Get("ses_test_123"); enabled {
		t.Fatal("removed session must be disabled")
	}
	if _, err := os.Stat(snapshot.ProfilePath); !os.IsNotExist(err) {
		t.Fatalf("profile file should be removed: %v", err)
	}
}

func TestManagerDisableIsIdempotent(t *testing.T) {
	manager := NewManager(t.TempDir())
	if _, err := manager.Set("ses_missing", "", false, NetworkDeny); err != nil {
		t.Fatalf("disabling an unknown session should not fail: %v", err)
	}
	if _, err := manager.Set("", "", true, NetworkDeny); err == nil {
		t.Fatal("expected a session id to be required")
	}
}

func TestManagerRejectsUnknownNetworkMode(t *testing.T) {
	manager := NewManager(t.TempDir())
	if _, err := manager.Set("ses_bad", t.TempDir(), true, "proxy"); err == nil {
		t.Fatal("expected an unsupported network mode to fail")
	}
}
