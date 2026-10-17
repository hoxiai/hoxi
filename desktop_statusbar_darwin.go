//go:build darwin

package main

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa

void hoxi_statusbar_start(void);
void hoxi_statusbar_update(int attention, int running, int queued, int available);
void hoxi_statusbar_stop(void);
*/
import "C"

import (
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	statusBarAppMu sync.RWMutex
	statusBarApp   *desktopApp
)

func startDesktopStatusBar(app *desktopApp) {
	statusBarAppMu.Lock()
	statusBarApp = app
	statusBarAppMu.Unlock()
	C.hoxi_statusbar_start()
}

func updateDesktopStatusBar(summary desktopCodeSummary) {
	available := C.int(0)
	if summary.Available {
		available = 1
	}
	C.hoxi_statusbar_update(C.int(summary.Attention), C.int(summary.Running), C.int(summary.Queued), available)
}

func stopDesktopStatusBar() {
	C.hoxi_statusbar_stop()
	statusBarAppMu.Lock()
	statusBarApp = nil
	statusBarAppMu.Unlock()
}

func activeStatusBarApp() *desktopApp {
	statusBarAppMu.RLock()
	defer statusBarAppMu.RUnlock()
	return statusBarApp
}

//export HoxiShowMainWindow
func HoxiShowMainWindow() {
	app := activeStatusBarApp()
	if app == nil || app.context == nil {
		return
	}
	wailsruntime.WindowShow(app.context)
	wailsruntime.WindowUnminimise(app.context)
}

//export HoxiQuitApplication
func HoxiQuitApplication() {
	app := activeStatusBarApp()
	if app == nil || app.context == nil {
		return
	}
	wailsruntime.Quit(app.context)
}
