package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestSingletonLockLivenessAndIsolation(t *testing.T) {
	dir := t.TempDir()
	if got := singletonLockCheck(dir, "devbox.local", func(int) bool { return true }); got != singletonUnlocked {
		t.Fatalf("unlocked dir state = %v, want singletonUnlocked", got)
	}

	regularLock := filepath.Join(t.TempDir(), "regular")
	writeProfileFile(t, filepath.Join(regularLock, "SingletonLock"), 4)
	if got := singletonLockCheck(regularLock, "devbox.local", func(int) bool { return false }); got != singletonActive {
		t.Fatalf("non-symlink SingletonLock must fail closed as active, got %v", got)
	}

	if runtime.GOOS == "windows" {
		return
	}

	for _, tc := range []struct {
		name        string
		target      string
		currentHost string
		alive       bool
		want        singletonState
	}{
		{"dead local exact host", "devbox.local-4321", "devbox.local", false, singletonStale},
		{"dead local hyphenated host", "my-macbook-pro.local-4321", "my-macbook-pro.local", false, singletonStale},
		{"dead local mDNS suffix shift", "devbox.lan-4321", "devbox.local", false, singletonStale},
		{"dead local short vs .local", "devbox-4321", "devbox.local", false, singletonStale},
		{"live local process", "devbox.local-4321", "devbox.local", true, singletonActive},
		{"foreign hostname", "otherhost.local-4321", "devbox.local", false, singletonActive},
		{"distinct corporate domains", "node1.prod.example.com-4321", "node1.stage.example.com-4321", false, singletonActive},
		{"malformed no hyphen", "devbox", "devbox", false, singletonActive},
		{"malformed non-numeric pid", "devbox-abc", "devbox", false, singletonActive},
		{"malformed non-positive pid", "devbox-0", "devbox", false, singletonActive},
	} {
		prof := filepath.Join(t.TempDir(), "prof")
		if err := os.MkdirAll(prof, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(tc.target, filepath.Join(prof, "SingletonLock")); err != nil {
			t.Fatal(err)
		}
		got := singletonLockCheck(prof, tc.currentHost, func(int) bool { return tc.alive })
		if got != tc.want {
			t.Fatalf("%s: singletonLockCheck(%q, %q, alive=%v) = %v, want %v", tc.name, tc.target, tc.currentHost, tc.alive, got, tc.want)
		}
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("hostname unavailable")
	}

	// Live external lock on primary root must never be deleted and must isolate to -sessions.
	s, root := newProfileService(t)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	liveTarget := fmt.Sprintf("%s-%d", host, os.Getpid())
	if err := os.Symlink(liveTarget, filepath.Join(root, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("cookie", filepath.Join(root, "SingletonCookie")); err != nil {
		t.Fatal(err)
	}
	cleanStaleSingletonLocks(root)
	if !exists(filepath.Join(root, "SingletonLock")) || !exists(filepath.Join(root, "SingletonCookie")) {
		t.Fatal("cleanStaleSingletonLocks deleted a live process's SingletonLock")
	}

	sc1 := &scope{service: s, sessionID: "workstore_default", directory: t.TempDir(), window: true}
	got1, err := s.acquirePersistentProfile(sc1)
	if err != nil {
		t.Fatal(err)
	}
	wantFallback1 := filepath.Join(root+"-sessions", "workstore_default")
	if got1 != wantFallback1 {
		t.Fatalf("acquirePersistentProfile with live root lock = %q, want %q", got1, wantFallback1)
	}
	if !exists(filepath.Join(root, "SingletonLock")) {
		t.Fatal("acquirePersistentProfile deleted live root SingletonLock")
	}

	// If the first fallback profile is also locked by another live process, advance to -2.
	if err := os.Symlink(liveTarget, filepath.Join(wantFallback1, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	sc2 := &scope{service: s, sessionID: "workstore_default", directory: t.TempDir(), window: true}
	got2, err := s.acquirePersistentProfile(sc2)
	if err != nil {
		t.Fatal(err)
	}
	wantFallback2 := filepath.Join(root+"-sessions", "workstore_default-2")
	if got2 != wantFallback2 {
		t.Fatalf("acquirePersistentProfile with live fallback lock = %q, want %q", got2, wantFallback2)
	}
	if !exists(filepath.Join(wantFallback1, "SingletonLock")) {
		t.Fatal("acquirePersistentProfile deleted live fallback SingletonLock")
	}

	// Stale lock from an exited local process is cleaned up and primary root is reused.
	sStale, staleRoot := newProfileService(t)
	if err := os.MkdirAll(staleRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	shortCmd := exec.Command("true")
	if err := shortCmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := shortCmd.Process.Pid
	if !isLocalPIDAlive(deadPID) {
		if err := os.Symlink(fmt.Sprintf("%s-%d", host, deadPID), filepath.Join(staleRoot, "SingletonLock")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("stale-cookie", filepath.Join(staleRoot, "SingletonCookie")); err != nil {
			t.Fatal(err)
		}
		scStale := &scope{service: sStale, sessionID: "workstore_default", directory: t.TempDir(), window: true}
		gotStale, err := sStale.acquirePersistentProfile(scStale)
		if err != nil {
			t.Fatal(err)
		}
		if gotStale != staleRoot {
			t.Fatalf("expected stale root %q to be reclaimed, got %q", staleRoot, gotStale)
		}
		if exists(filepath.Join(staleRoot, "SingletonLock")) || exists(filepath.Join(staleRoot, "SingletonCookie")) {
			t.Fatal("expected stale SingletonLock and SingletonCookie to be removed")
		}
	}
}

func TestForgetSessionCleansSharedWindowCallerBindings(t *testing.T) {
	s, _ := newProfileService(t)
	s.SetDefaultWindowMode(true)
	key := scopeKey{t.TempDir(), "workstore_default"}
	sc, err := s.acquire(context.Background(), key, true)
	if err != nil {
		t.Fatal(err)
	}
	sc.browser = context.Background()
	sc.bindCallerTab("ses_deleted", "tab_1")
	sc.bindCallerTab("ses_kept", "tab_2")
	<-sc.gate

	s.ForgetSession("ses_deleted")
	if liveScope(s, key) != sc || sc.closed {
		t.Fatal("ForgetSession closed shared workstore_default scope")
	}
	if sc.workflowTab("ses_deleted") != "" {
		t.Fatalf("expected ses_deleted tab binding cleared, got %q", sc.workflowTab("ses_deleted"))
	}
	if sc.workflowTab("ses_kept") != "tab_2" {
		t.Fatalf("expected ses_kept tab binding preserved, got %q", sc.workflowTab("ses_kept"))
	}
	if sc.tabOwnedByOtherSession("tab_1", "ses_new") {
		t.Fatal("expected tab_1 ownership released after ses_deleted was forgotten")
	}
}
