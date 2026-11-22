package app

import (
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// usesFramelessWindow reports whether Hoxi integrates its custom header bar
// directly into the OS window chrome via Wails frameless mode.
// macOS uses native TitleBarHiddenInset instead; Windows uses frameless mode
// with native DWM decorations (rounded corners & Aero shadow) so the header
// matches macOS without stacking a second system title bar above it.
func usesFramelessWindow(goos string) bool {
	return goos == "windows"
}

func hideWindowOnClose(goos string) bool {
	return goos == "darwin" || goos == "windows"
}

func buildWindowsAppOptions() *windows.Options {
	return &windows.Options{
		WebviewIsTransparent:              false,
		WindowIsTranslucent:               false,
		DisableFramelessWindowDecorations: false,
		Theme:                             windows.SystemDefault,
	}
}

// closeWindow performs the same user-initiated close semantics as the native
// window close button: persist latest window geometry first, then hide to the
// system tray on platforms with HideWindowOnClose enabled (or quit otherwise).
func (a *DesktopApp) closeWindow() {
	if a == nil {
		return
	}
	a.persistWindowGeometry()
	ctx := a.desktopContext()
	if ctx == nil || ctx.Value("frontend") == nil {
		return
	}
	if hideWindowOnClose(runtime.GOOS) {
		wailsruntime.WindowHide(ctx)
		return
	}
	wailsruntime.Quit(ctx)
}
