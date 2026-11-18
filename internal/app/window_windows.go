//go:build windows

package app

import (
	"context"
	"unsafe"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

const (
	monitorDefaultToPrimary = 0x00000001
	mdtEffectiveDPI         = 0
	spiGetWorkArea          = 0x0030
	logPixelsX              = 88
	logPixelsY              = 90
	windowsDefaultDPI       = 96
)

var (
	modWindowUser32 = windows.NewLazySystemDLL("user32.dll")
	modWindowShcore = windows.NewLazySystemDLL("shcore.dll")
	modWindowGdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procMonitorFromWindow     = modWindowUser32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW       = modWindowUser32.NewProc("GetMonitorInfoW")
	procSystemParametersInfoW = modWindowUser32.NewProc("SystemParametersInfoW")
	procWindowGetDC           = modWindowUser32.NewProc("GetDC")
	procWindowReleaseDC       = modWindowUser32.NewProc("ReleaseDC")
	procGetDpiForMonitor      = modWindowShcore.NewProc("GetDpiForMonitor")
	procWindowGetDeviceCaps   = modWindowGdi32.NewProc("GetDeviceCaps")
)

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type winMonitorInfo struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
}

func primaryScreenWorkArea() screenWorkArea {
	var work winRect
	var dpiX, dpiY uint32

	if procMonitorFromWindow.Find() == nil && procGetMonitorInfoW.Find() == nil {
		hMon, _, _ := procMonitorFromWindow.Call(0, monitorDefaultToPrimary)
		if hMon != 0 {
			var mi winMonitorInfo
			mi.CbSize = uint32(unsafe.Sizeof(mi))
			ok, _, _ := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi)))
			if ok != 0 {
				work = mi.RcWork
			}
			if procGetDpiForMonitor.Find() == nil {
				var dx, dy uint32
				hr, _, _ := procGetDpiForMonitor.Call(
					hMon,
					mdtEffectiveDPI,
					uintptr(unsafe.Pointer(&dx)),
					uintptr(unsafe.Pointer(&dy)),
				)
				if hr == 0 && dx > 0 && dy > 0 {
					dpiX, dpiY = dx, dy
				}
			}
		}
	}

	if (work.Right-work.Left <= 0 || work.Bottom-work.Top <= 0) && procSystemParametersInfoW.Find() == nil {
		var fallback winRect
		ok, _, _ := procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&fallback)), 0)
		if ok != 0 {
			work = fallback
		}
	}

	physW := int(work.Right - work.Left)
	physH := int(work.Bottom - work.Top)
	if physW <= 0 || physH <= 0 {
		return screenWorkArea{}
	}

	if dpiX == 0 || dpiY == 0 {
		if procWindowGetDC.Find() == nil && procWindowGetDeviceCaps.Find() == nil && procWindowReleaseDC.Find() == nil {
			hdc, _, _ := procWindowGetDC.Call(0)
			if hdc != 0 {
				dx, _, _ := procWindowGetDeviceCaps.Call(hdc, logPixelsX)
				dy, _, _ := procWindowGetDeviceCaps.Call(hdc, logPixelsY)
				_, _, _ = procWindowReleaseDC.Call(0, hdc)
				if dx > 0 && dy > 0 {
					dpiX, dpiY = uint32(dx), uint32(dy)
				}
			}
		}
	}
	if dpiX == 0 {
		dpiX = windowsDefaultDPI
	}
	if dpiY == 0 {
		dpiY = windowsDefaultDPI
	}

	// Match Wails's ScaleToDefaultDPI conversion ((pixels * 96) / dpi).
	return screenWorkArea{
		Width:  (physW * windowsDefaultDPI) / int(dpiX),
		Height: (physH * windowsDefaultDPI) / int(dpiY),
	}
}

func startPlatformWindowObserver(_ context.Context, _ *windowGeometryTracker) {}

func stopPlatformWindowObserver() {}

func capturePlatformWindowState(ctx context.Context, tracker *windowGeometryTracker) {
	if ctx == nil || tracker == nil || ctx.Value("frontend") == nil {
		return
	}
	if wailsruntime.WindowIsMinimised(ctx) || wailsruntime.WindowIsFullscreen(ctx) {
		return
	}
	if wailsruntime.WindowIsMaximised(ctx) {
		tracker.RecordWindowSnapshot(0, 0, false, true)
		return
	}
	if wailsruntime.WindowIsNormal(ctx) {
		w, h := wailsruntime.WindowGetSize(ctx)
		tracker.RecordWindowSnapshot(w, h, true, false)
	}
}
