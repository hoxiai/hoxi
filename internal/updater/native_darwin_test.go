//go:build darwin && cgo

package updater

import (
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNativeUserChoiceAdmissionBridge(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "native-choice")
	compile := exec.Command("clang", "-fblocks", "-framework", "Cocoa", "testdata/native_choice.m", "-o", binary)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile choice bridge: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("choice bridge: %v\n%s", err, out)
	}
}

func TestNativeAdmissionLifecycle(t *testing.T) {
	var checks, releases atomic.Int32
	nativeCallbacks.Lock()
	nativeCallbacks.entries[0] = &nativeAdmission{
		canInstall: func() bool { return checks.Add(1) <= 2 },
		release:    func() { releases.Add(1) },
	}
	nativeCallbacks.Unlock()
	t.Cleanup(func() { hoxiNativeRelease(0, 1) })
	if hoxiNativeCanInstall(0) != 1 || hoxiNativeCanInstall(0) != 1 {
		t.Fatal("selection and relaunch must each recheck admission")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); hoxiNativeRelease(0, 0) }()
	}
	wg.Wait()
	if releases.Load() != 1 {
		t.Fatal("abort and finish must not double release")
	}
	if hoxiNativeCanInstall(0) != 0 {
		t.Fatal("busy callback must deny admission")
	}
	hoxiNativeRelease(0, 1)
	if releases.Load() != 2 {
		t.Fatal("stop must release even a denied admission attempt")
	}
	if hoxiNativeCanInstall(0) != 0 {
		t.Fatal("retired native token must fail closed")
	}
}

func TestNativeAdmissionPanicFailsClosed(t *testing.T) {
	var released bool
	nativeCallbacks.Lock()
	nativeCallbacks.entries[0] = &nativeAdmission{
		canInstall: func() bool { panic("probe failed") },
		release:    func() { released = true },
	}
	nativeCallbacks.Unlock()
	t.Cleanup(func() { hoxiNativeRelease(0, 1) })
	if hoxiNativeCanInstall(0) != 0 {
		t.Fatal("panic must not cross cgo boundary")
	}
	hoxiNativeRelease(0, 1)
	if !released {
		t.Fatal("failed probe must still release admission")
	}
}

func TestNativeStagedAdmissionHeldUntilShutdown(t *testing.T) {
	var releases atomic.Int32
	allow := true
	nativeCallbacks.Lock()
	nativeCallbacks.entries[0] = &nativeAdmission{
		canInstall: func() bool { return allow },
		release:    func() { releases.Add(1) },
	}
	nativeCallbacks.Unlock()
	t.Cleanup(func() { hoxiNativeRelease(0, 1) })
	if hoxiNativeCanInstall(0) != 1 {
		t.Fatal("selection must acquire admission")
	}
	hoxiNativeStaged(0) // Manual extraction or install-on-quit notification.
	hoxiNativeStaged(0) // Repeated staging notifications are harmless.
	allow = false
	if hoxiNativeCanInstall(0) != 0 {
		t.Fatal("busy relaunch must still be denied")
	}
	for range 4 { // Denial, cancellation, abort, and cycle completion.
		hoxiNativeRelease(0, 0)
	}
	if releases.Load() != 0 {
		t.Fatal("staged admission must remain held after all non-shutdown paths")
	}
	hoxiNativeRelease(0, 1)
	hoxiNativeStaged(0)
	hoxiNativeRelease(0, 1)
	if releases.Load() != 1 {
		t.Fatal("final shutdown must force release exactly once")
	}
}
