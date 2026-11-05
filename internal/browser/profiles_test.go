package browser

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProfileFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func newProfileService(t *testing.T) (*Service, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "browser-profile")
	s := newService(time.Now, time.Hour)
	t.Cleanup(s.Close)
	s.SetProfileRoot(root)
	return s, root
}

func TestForgetSessionRemovesOnlyThatSessionsProfiles(t *testing.T) {
	s, root := newProfileService(t)
	const id = "ses_hoxi_1_abc"
	removed := []string{
		filepath.Join(root+"-sessions", id),
		filepath.Join(root+"-sessions", id+"-2"),
		filepath.Join(root+"-stores", "store-a-sessions", id),
	}
	kept := []string{
		root, // shared profile
		filepath.Join(root+"-stores", "store-a"),
		filepath.Join(root+"-sessions", id+"x"),
		filepath.Join(root+"-sessions", id+"-2b"),
		filepath.Join(root+"-sessions", "ses_hoxi_2_other"),
		filepath.Join(root+"-sessions", "default"),
	}
	for _, dir := range append(append([]string{}, removed...), kept...) {
		writeProfileFile(t, filepath.Join(dir, "Default", "Cookies"), 1024)
	}
	// A symlink with a matching name must not lead deletion outside the root.
	outside := filepath.Join(t.TempDir(), "outside")
	writeProfileFile(t, filepath.Join(outside, "keep.txt"), 10)
	link := filepath.Join(root+"-sessions", id+"-3")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if got := s.SessionProfileBytes(id); got != 3*1024 {
		t.Fatalf("SessionProfileBytes = %d, want %d", got, 3*1024)
	}
	if freed := s.ForgetSession(id); freed != 3*1024 {
		t.Fatalf("freed = %d, want %d", freed, 3*1024)
	}
	for _, dir := range removed {
		if exists(dir) {
			t.Fatalf("%s should be removed", dir)
		}
	}
	for _, dir := range kept {
		if !exists(filepath.Join(dir, "Default", "Cookies")) {
			t.Fatalf("%s must be kept", dir)
		}
	}
	if !exists(filepath.Join(outside, "keep.txt")) || !exists(link) {
		t.Fatal("a symlinked profile must not be followed or removed")
	}
}

func TestForgetSessionRejectsUnsafeIDs(t *testing.T) {
	s, root := newProfileService(t)
	writeProfileFile(t, filepath.Join(root+"-sessions", "default", "Default", "Cookies"), 8)
	writeProfileFile(t, filepath.Join(root, "Default", "Cookies"), 8)
	for _, id := range []string{"", "default", "..", "../browser-profile", "a/b", "-x"} {
		if freed := s.ForgetSession(id); freed != 0 {
			t.Fatalf("ForgetSession(%q) freed %d bytes", id, freed)
		}
	}
	if !exists(filepath.Join(root+"-sessions", "default", "Default", "Cookies")) || !exists(filepath.Join(root, "Default", "Cookies")) {
		t.Fatal("shared and default profiles must survive")
	}
}

func TestClearProfileCachesKeepsSignInsAndRunningProfiles(t *testing.T) {
	s, root := newProfileService(t)
	idle := filepath.Join(root+"-sessions", "ses_idle")
	running := filepath.Join(root+"-sessions", "ses_running")
	for _, p := range []string{root, idle, running} {
		writeProfileFile(t, filepath.Join(p, "Default", "Cache", "data_1"), 4096)
		writeProfileFile(t, filepath.Join(p, "Default", "Code Cache", "js", "x"), 2048)
		writeProfileFile(t, filepath.Join(p, "GrShaderCache", "y"), 1024)
		writeProfileFile(t, filepath.Join(p, "Default", "Cookies"), 512)
		writeProfileFile(t, filepath.Join(p, "Default", "Login Data"), 512)
	}
	if err := os.Symlink("hostname-123", filepath.Join(running, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if total := s.ProfileStorageBytes(); total != 3*(4096+2048+1024+512+512) {
		t.Fatalf("ProfileStorageBytes = %d", total)
	}

	freed, skipped := s.ClearProfileCaches()
	if freed != 2*(4096+2048+1024) || skipped != 1 {
		t.Fatalf("freed=%d skipped=%d", freed, skipped)
	}
	for _, p := range []string{root, idle} {
		if exists(filepath.Join(p, "Default", "Cache")) || exists(filepath.Join(p, "GrShaderCache")) {
			t.Fatalf("caches of %s should be cleared", p)
		}
	}
	for _, p := range []string{root, idle, running} {
		if !exists(filepath.Join(p, "Default", "Cookies")) || !exists(filepath.Join(p, "Default", "Login Data")) {
			t.Fatalf("sign-ins of %s must be kept", p)
		}
	}
	if !exists(filepath.Join(running, "Default", "Cache", "data_1")) {
		t.Fatal("a running profile's cache must not be touched")
	}
}

func TestProfileHelpersWithoutRoot(t *testing.T) {
	s := newService(time.Now, time.Hour)
	defer s.Close()
	if s.ForgetSession("ses_x") != 0 || s.ProfileStorageBytes() != 0 {
		t.Fatal("no profile root means nothing to measure or remove")
	}
	if freed, skipped := s.ClearProfileCaches(); freed != 0 || skipped != 0 {
		t.Fatalf("freed=%d skipped=%d", freed, skipped)
	}
}
