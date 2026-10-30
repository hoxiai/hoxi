//go:build darwin && cgo

package updater

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/hoxiai/hoxi/internal/subproc"
)

var (
	nativeReady        atomic.Bool
	darwinSparkleReady atomic.Bool

	darwinDMGMu      sync.Mutex
	darwinCanInstall func(force bool) bool
	darwinRelease    func()
	darwinAppBundle  string
	darwinInstalling bool
)

const darwinSwapAndRestartSh = `
target_pid="$HOXI_UPD_PID"
staged_app="$HOXI_UPD_STAGED_APP"
target_app="$HOXI_UPD_TARGET_APP"
stage_dir="$HOXI_UPD_STAGE_DIR"

i=0
while [ "$i" -lt 60 ]; do
  if ! kill -0 "$target_pid" 2>/dev/null; then
    break
  fi
  sleep 0.5
  i=$((i + 1))
done

backup_app="${target_app}.backup.$$"
rm -rf "$backup_app"
if [ -d "$target_app" ]; then
  mv "$target_app" "$backup_app" 2>/dev/null || rm -rf "$target_app"
fi
if ditto "$staged_app" "$target_app"; then
  rm -rf "$backup_app"
  xattr -dr com.apple.quarantine "$target_app" 2>/dev/null || true
elif [ -d "$backup_app" ]; then
  rm -rf "$target_app"
  mv "$backup_app" "$target_app" 2>/dev/null || true
fi
rm -rf "$stage_dir"
open "$target_app"
`

var nativeCallbacks = struct {
	sync.Mutex
	next    uint64
	entries map[uint64]*nativeAdmission
}{entries: make(map[uint64]*nativeAdmission)}

type nativeAdmission struct {
	sync.Mutex
	canInstall func(force bool) bool
	release    func()
	touched    bool
	staged     bool
	stopped    bool
}

// StartNative starts Sparkle in a configured .app after the Cocoa main loop starts.
// Calls are marshaled synchronously to the main thread. Both callbacks must be
// bounded, must not call this API or wait for the main thread, and must tolerate
// repeated admission checks within one cycle. canInstall(force) must fail
// closed on timeout and retain its draining admission until release is called;
// force is only set after the user confirmed interrupting active work.
//
// Automatic checks are enabled; background downloads are deliberately disabled.
// The bundle must set SUAllowsAutomaticUpdates=false to prevent Sparkle's UI
// from enabling automatic download/install. Sparkle has no install-on-quit veto:
// after a user starts staging an update, quitting can still install it. The
// supplied admission callback is not a guarantee against every termination path.
// Once extraction/installer preparation starts, admission is conservatively held
// until shutdown, even after cancellation or error: Sparkle may still install on
// quit. The host UI must treat this as draining/restart-required, not idle, and
// reject new work while its gateway admission is held. No UI wiring is done here.
func StartNative(canInstall func(force bool) bool, release func()) error {
	if canInstall == nil || release == nil {
		return errors.New("native updater requires admission and release callbacks")
	}
	nativeCallbacks.Lock()
	nativeCallbacks.next++
	token := nativeCallbacks.next
	nativeCallbacks.entries[token] = &nativeAdmission{canInstall: canInstall, release: release}
	nativeCallbacks.Unlock()
	if message := C.hoxi_native_start(C.ulonglong(token)); message != nil {
		sparkleErr := C.GoString(message)
		C.free(unsafe.Pointer(message))
		hoxiNativeRelease(C.ulonglong(token), 1)

		exe, err := os.Executable()
		if err != nil || strings.TrimSpace(exe) == "" {
			return errors.New(sparkleErr)
		}
		if real, err := filepath.EvalSymlinks(exe); err == nil && real != "" {
			exe = real
		}
		appBundle, err := resolveDarwinAppBundlePath(exe)
		if err != nil {
			return errors.New(sparkleErr)
		}
		darwinDMGMu.Lock()
		darwinCanInstall = canInstall
		darwinRelease = release
		darwinAppBundle = appBundle
		darwinInstalling = false
		darwinDMGMu.Unlock()
		darwinSparkleReady.Store(false)
		nativeReady.Store(true)
		return nil
	}
	darwinSparkleReady.Store(true)
	nativeReady.Store(true)
	return nil
}

// NativeAvailable reports whether StartNative has successfully started Sparkle or the DMG self-updater.
func NativeAvailable() bool { return nativeReady.Load() }

// CheckNative opens Sparkle's standard UI when Sparkle is embedded, or runs the
// SHA-256 verified DMG self-updater when running from a non-Sparkle .app bundle.
func CheckNative() error {
	if !NativeAvailable() {
		return errors.New("native updater is unavailable")
	}
	if !darwinSparkleReady.Load() {
		return checkDarwinDMGSelfUpdate()
	}
	if message := C.hoxi_native_check(); message != nil {
		defer C.free(unsafe.Pointer(message))
		return errors.New(C.GoString(message))
	}
	return nil
}

// StopNative disables scheduling, invalidates bridge callbacks and releases
// admission, including staged admission. Only call this during final app shutdown,
// after new work is permanently disabled. Sparkle has no public stop/cancel API
// for an already staged installer; StopNative is not an update cancellation API.
func StopNative() {
	if nativeReady.Swap(false) {
		if darwinSparkleReady.Swap(false) {
			C.hoxi_native_stop()
			return
		}
		darwinDMGMu.Lock()
		release := darwinRelease
		darwinCanInstall = nil
		darwinRelease = nil
		darwinAppBundle = ""
		darwinInstalling = false
		darwinDMGMu.Unlock()
		if release != nil {
			release()
		}
	}
}

func stageDarwinAppFromDMG(ctx context.Context, dmgPath, expectedVersion, stageDir string) (string, error) {
	mountPoint := filepath.Join(stageDir, "mnt")
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		return "", err
	}
	attachCmd := subproc.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noverify", "-mountpoint", mountPoint, dmgPath)
	if out, err := attachCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("mount update DMG: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	detached := false
	detach := func() {
		if detached {
			return
		}
		detached = true
		_ = subproc.Command("hdiutil", "detach", mountPoint, "-force").Run()
	}
	defer detach()

	mountedApp := filepath.Join(mountPoint, "Hoxi.app")
	mountedExe := filepath.Join(mountedApp, "Contents", "MacOS", "Hoxi")
	if info, err := os.Stat(mountedExe); err != nil || info.IsDir() {
		return "", errors.New("mounted DMG does not contain a valid Hoxi.app")
	}

	verifyCmd := subproc.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", mountedApp)
	if out, err := verifyCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("verify update code signature: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	wantVer := strings.TrimPrefix(strings.TrimSpace(expectedVersion), "v")
	if wantVer != "" {
		plistPath := filepath.Join(mountedApp, "Contents", "Info.plist")
		out, err := subproc.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleShortVersionString", plistPath).Output()
		gotVer := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
		if err != nil || gotVer != wantVer {
			return "", fmt.Errorf("staged Hoxi.app version mismatch: got %q, want %q", gotVer, wantVer)
		}
	}

	stagedApp := filepath.Join(stageDir, "Hoxi.app")
	dittoCmd := subproc.CommandContext(ctx, "ditto", mountedApp, stagedApp)
	if out, err := dittoCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stage updated Hoxi.app: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	detach()
	return stagedApp, nil
}

func checkDarwinDMGSelfUpdate() (installErr error) {
	darwinDMGMu.Lock()
	if darwinInstalling {
		darwinDMGMu.Unlock()
		return errors.New("update installation is already in progress")
	}
	canInstall := darwinCanInstall
	release := darwinRelease
	appBundle := darwinAppBundle
	darwinInstalling = true
	darwinDMGMu.Unlock()
	setInstallPhase("preparing")
	defer func() {
		finishInstallProgress(installErr)
		if installErr != nil {
			darwinDMGMu.Lock()
			darwinInstalling = false
			darwinDMGMu.Unlock()
		}
	}()

	cleanupAbort := func() {
		if release != nil {
			release()
		}
	}

	if canInstall == nil || strings.TrimSpace(appBundle) == "" {
		cleanupAbort()
		return errors.New("native updater is not configured")
	}
	admitted := canInstall(false)
	if !admitted && C.hoxi_native_confirm_busy() != 0 {
		admitted = canInstall(true)
	}
	if !admitted {
		cleanupAbort()
		return errors.New("cannot install update while agent sessions are active")
	}

	checker := activeChecker.Load()
	if checker == nil {
		cleanupAbort()
		return errors.New("update checker is not initialized")
	}
	res := checker.LastResult()
	if res == nil || !res.NeedUpdate {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		checked, err := checker.Check(ctx)
		cancel()
		if err != nil {
			cleanupAbort()
			return err
		}
		res = checked
	}
	if res == nil || !res.NeedUpdate {
		cleanupAbort()
		return errors.New("application is already up to date")
	}

	dmgURL := resolveDarwinDMGURL(res)
	if dmgURL == "" {
		cleanupAbort()
		return errors.New("update download URL is missing")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	stageDir, err := os.MkdirTemp("", "hoxi-macos-update-*")
	if err != nil {
		cleanupAbort()
		return fmt.Errorf("create staging directory: %w", err)
	}
	dmgPath := filepath.Join(stageDir, "update.dmg")
	setInstallPhase("downloading")
	if err := downloadAndVerifyDMG(ctx, nil, dmgURL, res.SHA256, dmgPath, func(p InstallProgress) {
		installProgress.Store(&p)
	}); err != nil {
		_ = os.RemoveAll(stageDir)
		cleanupAbort()
		return err
	}
	setInstallPhase("staging")
	stagedApp, err := stageDarwinAppFromDMG(ctx, dmgPath, res.LatestVersion, stageDir)
	_ = os.Remove(dmgPath)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		cleanupAbort()
		return err
	}

	cmd := subproc.Command("/bin/sh", "-c", darwinSwapAndRestartSh)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(),
		"HOXI_UPD_PID="+strconv.Itoa(os.Getpid()),
		"HOXI_UPD_STAGED_APP="+stagedApp,
		"HOXI_UPD_TARGET_APP="+appBundle,
		"HOXI_UPD_STAGE_DIR="+stageDir,
	)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(stageDir)
		cleanupAbort()
		return fmt.Errorf("launch update installer: %w", err)
	}
	_ = cmd.Process.Release()
	setInstallPhase("restarting")
	go func() {
		time.Sleep(250 * time.Millisecond)
		os.Exit(0)
	}()

	return nil
}

//export hoxiNativeCanInstall
func hoxiNativeCanInstall(token C.ulonglong, force C.int) (allowed C.int) {
	nativeCallbacks.Lock()
	entry := nativeCallbacks.entries[uint64(token)]
	nativeCallbacks.Unlock()
	if entry == nil {
		return 0
	}
	entry.Lock()
	defer entry.Unlock()
	defer func() {
		if recover() != nil {
			allowed = 0
		}
	}()
	if entry.stopped {
		return 0
	}
	entry.touched = true
	if entry.canInstall(force != 0) {
		return 1
	}
	return 0
}

//export hoxiNativeRelease
func hoxiNativeRelease(token C.ulonglong, stop C.int) {
	nativeCallbacks.Lock()
	entry := nativeCallbacks.entries[uint64(token)]
	if stop != 0 {
		delete(nativeCallbacks.entries, uint64(token))
	}
	nativeCallbacks.Unlock()
	if entry == nil {
		return
	}
	entry.Lock()
	defer entry.Unlock()
	defer func() { _ = recover() }()
	if stop != 0 {
		entry.stopped = true
	}
	if entry.staged && stop == 0 {
		return
	}
	if entry.touched {
		entry.touched = false
		entry.release()
	}
}

//export hoxiNativeStaged
func hoxiNativeStaged(token C.ulonglong) {
	nativeCallbacks.Lock()
	entry := nativeCallbacks.entries[uint64(token)]
	nativeCallbacks.Unlock()
	if entry == nil {
		return
	}
	entry.Lock()
	defer entry.Unlock()
	if !entry.stopped {
		entry.staged = true
	}
}
