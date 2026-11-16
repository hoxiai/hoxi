//go:build darwin && cgo

package statusbar

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>

void hoxi_statusbar_start(void);
void hoxi_statusbar_update(
	int attention,
	int running,
	int queued,
	int available,
	int keepAwakeEnabled,
	int keepAwakeActive,
	const char *badge,
	const char *pendingItems,
	const char *runningItems
);
void hoxi_statusbar_stop(void);
*/
import "C"

import (
	"context"
	"sync"
	"unsafe"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	statusBarCtxMu sync.RWMutex
	statusBarCtx   context.Context
)

func Start(ctx context.Context) {
	statusBarCtxMu.Lock()
	statusBarCtx = ctx
	statusBarCtxMu.Unlock()
	C.hoxi_statusbar_start()
}

func Update(summary Summary) {
	available := C.int(0)
	if summary.Available {
		available = 1
	}
	keepAwakeEnabled := C.int(0)
	if summary.KeepAwakeEnabled {
		keepAwakeEnabled = 1
	}
	keepAwakeActive := C.int(0)
	if summary.KeepAwakeActive {
		keepAwakeActive = 1
	}
	badge := C.CString(summary.DockBadgeLabel())
	defer C.free(unsafe.Pointer(badge))
	pendingItems := C.CString(encodeMenuTargets(summary.PendingTargets))
	defer C.free(unsafe.Pointer(pendingItems))
	runningItems := C.CString(encodeMenuTargets(summary.RunningTargets))
	defer C.free(unsafe.Pointer(runningItems))
	C.hoxi_statusbar_update(
		C.int(summary.Attention),
		C.int(summary.Running),
		C.int(summary.Queued),
		available,
		keepAwakeEnabled,
		keepAwakeActive,
		badge,
		pendingItems,
		runningItems,
	)
}

func Stop() {
	C.hoxi_statusbar_stop()
	statusBarCtxMu.Lock()
	statusBarCtx = nil
	statusBarCtxMu.Unlock()
}

func activeStatusBarCtx() context.Context {
	statusBarCtxMu.RLock()
	defer statusBarCtxMu.RUnlock()
	return statusBarCtx
}

//export HoxiShowMainWindow
func HoxiShowMainWindow() {
	ctx := activeStatusBarCtx()
	fn := activeActionHandler()
	if ctx == nil && fn == nil {
		return
	}
	go func() {
		if ctx != nil {
			wailsruntime.WindowShow(ctx)
			wailsruntime.WindowUnminimise(ctx)
		}
		invokeActionHandler(fn, MenuActionWorkspace, "")
	}()
}

//export HoxiTriggerMenuAction
func HoxiTriggerMenuAction(kind *C.char, sessionID *C.char) {
	var k, sid string
	if kind != nil {
		k = C.GoString(kind)
	}
	if sessionID != nil {
		sid = C.GoString(sessionID)
	}
	ctx := activeStatusBarCtx()
	fn := activeActionHandler()
	if ctx == nil && fn == nil {
		return
	}
	go func() {
		if k != MenuActionToggleKeepAwake && ctx != nil {
			wailsruntime.WindowShow(ctx)
			wailsruntime.WindowUnminimise(ctx)
		}
		invokeActionHandler(fn, k, sid)
	}()
}

//export HoxiQuitApplication
func HoxiQuitApplication() {
	ctx := activeStatusBarCtx()
	if ctx == nil {
		return
	}
	go wailsruntime.Quit(ctx)
}
