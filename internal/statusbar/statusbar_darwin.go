//go:build darwin && cgo

package statusbar

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa

void hoxi_statusbar_start(void);
void hoxi_statusbar_update(int attention, int running, int queued, int available);
void hoxi_statusbar_stop(void);
*/
import "C"

import (
	"context"
	"sync"

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
	C.hoxi_statusbar_update(C.int(summary.Attention), C.int(summary.Running), C.int(summary.Queued), available)
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
	if ctx == nil {
		return
	}
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
}

//export HoxiQuitApplication
func HoxiQuitApplication() {
	ctx := activeStatusBarCtx()
	if ctx == nil {
		return
	}
	wailsruntime.Quit(ctx)
}
