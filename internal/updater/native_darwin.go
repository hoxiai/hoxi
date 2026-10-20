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
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"
)

var nativeReady atomic.Bool

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
		defer C.free(unsafe.Pointer(message))
		hoxiNativeRelease(C.ulonglong(token), 1)
		return errors.New(C.GoString(message))
	}
	nativeReady.Store(true)
	return nil
}

// NativeAvailable reports whether StartNative has successfully started Sparkle.
func NativeAvailable() bool { return nativeReady.Load() }

// CheckNative opens Sparkle's standard UI. A denied install is not resumed
// automatically; the user must retry. Success only means the check was accepted.
func CheckNative() error {
	if !NativeAvailable() {
		return errors.New("native updater is unavailable")
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
		C.hoxi_native_stop()
	}
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
